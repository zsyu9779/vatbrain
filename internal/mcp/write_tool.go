package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

func writeMemoryTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("write_memory",
		mcp.WithDescription("Write an episodic memory through the significance gate, "+
			"embedding, pattern separation, and persistence pipeline."),
		mcp.WithString("project_id", mcp.Required(),
			mcp.Description("Project identifier — hard constraint for retrieval")),
		mcp.WithString("summary", mcp.Required(),
			mcp.Description("Summary of the memory to store")),
		mcp.WithString("language",
			mcp.Description("Programming language or framework context")),
		mcp.WithString("task_type",
			mcp.Description("Task type: debug, feature, refactor, or review"),
			mcp.Enum("debug", "feature", "refactor", "review")),
		mcp.WithString("entity_id",
			mcp.Description("Entity identifier for pattern separation (e.g. func:NewRedisPool)")),
		mcp.WithBoolean("user_confirmed",
			mcp.Description("Whether the user explicitly confirmed this memory")),
		mcp.WithBoolean("is_correction",
			mcp.Description("Whether this is a correction of previous information")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			projectID, err := req.RequireString("project_id")
			if err != nil {
				return mcp.NewToolResultError("project_id is required"), nil
			}
			summary, err := req.RequireString("summary")
			if err != nil {
				return mcp.NewToolResultError("summary is required"), nil
			}
			language := req.GetString("language", "")
			taskType := req.GetString("task_type", "")
			entityID := req.GetString("entity_id", "")
			userConfirmed := req.GetBool("user_confirmed", false)
			isCorrection := req.GetBool("is_correction", false)

			// Fetch working-memory cycles from Redis.
			cyclesKey := fmt.Sprintf("working_memory:%s", projectID)
			summaries, rErr := a.Redis.LRange(ctx, cyclesKey, 0, -1)
			if rErr != nil && rErr.Error() != "redis: nil" {
				slog.Warn("redis lrange working memory", "err", rErr)
			}

			workingMemory := make([]core.WorkingMemoryCycle, len(summaries))
			for i, s := range summaries {
				workingMemory[i] = core.WorkingMemoryCycle{Summary: s}
			}

			// Evaluate significance gate.
			event := core.WriteEvent{
				Summary:       summary,
				UserConfirmed: userConfirmed,
				IsCorrection:  isCorrection,
			}
			gateResult := a.SignificanceGate.Evaluate(event, workingMemory)

			if !gateResult.ShouldPersist {
				resp, jErr := mcp.NewToolResultJSON(writeMemoryOutput{
					Persisted:  false,
					GateReason: gateResult.Reason,
				})
				if jErr != nil {
					return mcp.NewToolResultError(jErr.Error()), nil
				}
				return resp, nil
			}

			// Generate embedding.
			embedding, embErr := a.Embedder.Embed(ctx, summary)
			if embErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("embedding failed: %v", embErr)), nil
			}

			// Search for similar existing memories.
			candidates, simErr := a.Pgvector.SimilaritySearch(ctx, embedding, 5, nil)
			if simErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("similarity search failed: %v", simErr)), nil
			}

			newCtx := core.SeparationContext{
				ProjectID: projectID,
				Language:  language,
				EntityID:  entityID,
			}

			// Check each similar candidate for merge.
			for _, candidate := range candidates {
				candidateEmb, cEmbErr := a.Pgvector.GetEmbedding(ctx, candidate.MemoryID)
				if cEmbErr != nil {
					slog.Warn("pgvector get embedding", "memory_id", candidate.MemoryID, "err", cEmbErr)
					continue
				}

				candidateProjectID, _ := stringFromMeta(candidate.Metadata, "project_id")
				candidateLang, _ := stringFromMeta(candidate.Metadata, "language")
				candidateEntity, _ := stringFromMeta(candidate.Metadata, "entity_id")

				candidateCtx := core.SeparationContext{
					ProjectID: candidateProjectID,
					Language:  candidateLang,
					EntityID:  candidateEntity,
				}

				sepResult := a.PatternSeparation.Check(embedding, candidateEmb, newCtx, candidateCtx)
				if !sepResult.ShouldMerge {
					continue
				}

				// Merge: update existing memory.
				parsedID, pErr := uuid.Parse(candidate.MemoryID)
				if pErr != nil {
					continue
				}

				now := time.Now()
				newWeight := clampWeight(candidate.Similarity + 0.1)

				_, uErr := a.Neo4j.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
					_, err := tx.Run(ctx, `
					MATCH (e:EpisodicMemory {id: $id})
					SET e.weight = $weight,
					    e.last_accessed_at = $now,
					    e.summary = e.summary + '\n' + $newSummary
					RETURN e.id
				`, map[string]any{
						"id":         candidate.MemoryID,
						"weight":     newWeight,
						"now":        now,
						"newSummary": summary,
					})
					return nil, err
				})
				if uErr != nil {
					return mcp.NewToolResultError(fmt.Sprintf("merge update failed: %v", uErr)), nil
				}

				resp, jErr := mcp.NewToolResultJSON(writeMemoryOutput{
					MemoryID:    parsedID,
					Persisted:   true,
					GateReason:  gateResult.Reason,
					MergeAction: string(models.MergeActionUpdatedExisting),
					Weight:      newWeight,
				})
				if jErr != nil {
					return mcp.NewToolResultError(jErr.Error()), nil
				}
				return resp, nil
			}

			// No merge — create new episodic memory.
			memoryID := uuid.New()
			now := time.Now()
			effFreq, weight := a.WeightDecay.ComputeFull([]time.Time{now}, now, now)

			_, cErr := a.Neo4j.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
				_, err := tx.Run(ctx, `
				CREATE (e:EpisodicMemory {
					id: $id,
					project_id: $projectID,
					language: $language,
					task_type: $taskType,
					summary: $summary,
					source_type: $sourceType,
					trust_level: $trustLevel,
					weight: $weight,
					effective_frequency: $effFreq,
					created_at: $createdAt,
					entity_group: $entityGroup,
					embedding_id: $embeddingID,
					full_snapshot_uri: ''
				})
				RETURN e.id
			`, map[string]any{
					"id":          memoryID.String(),
					"projectID":   projectID,
					"language":    language,
					"taskType":    taskType,
					"summary":     summary,
					"sourceType":  string(models.SourceTypeLLM),
					"trustLevel":  int(models.DefaultTrustLevel),
					"weight":      weight,
					"effFreq":     effFreq,
					"createdAt":   now,
					"entityGroup": entityID,
					"embeddingID": memoryID.String(),
				})
				return nil, err
			})
			if cErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("create memory failed: %v", cErr)), nil
			}

			// Insert embedding into pgvector.
			if insErr := a.Pgvector.InsertEmbedding(ctx, memoryID.String(), embedding,
				summary, projectID, language, taskType,
				map[string]any{"entity_id": entityID}); insErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("embedding insert failed: %v", insErr)), nil
			}

			// Push to working-memory cycles.
			if pushErr := a.Redis.LPush(ctx, cyclesKey, summary); pushErr != nil {
				slog.Warn("redis lpush working memory", "err", pushErr)
			}
			if trimErr := a.Redis.LTrim(ctx, cyclesKey, 0, 19); trimErr != nil {
				slog.Warn("redis ltrim working memory", "err", trimErr)
			}

			resp, jErr := mcp.NewToolResultJSON(writeMemoryOutput{
				MemoryID:    memoryID,
				Persisted:   true,
				GateReason:  gateResult.Reason,
				MergeAction: string(models.MergeActionCreatedNew),
				Weight:      weight,
			})
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}

type writeMemoryOutput struct {
	MemoryID    uuid.UUID `json:"memory_id"`
	Persisted   bool      `json:"persisted"`
	GateReason  string    `json:"gate_reason"`
	MergeAction string    `json:"merge_action"`
	Weight      float64   `json:"weight"`
}
