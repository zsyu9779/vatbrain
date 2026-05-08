package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
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

			// Fetch working-memory cycles from in-process buffer.
			summaries := a.WorkingMemory.GetAll(projectID)
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

			// Search for similar existing memories via Store.
			candidates, simErr := a.Store.SearchEpisodic(ctx, store.EpisodicSearchRequest{
				ProjectID: projectID,
				Embedding: vector.Float32To64(embedding),
				Limit:     5,
			})
			if simErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("similarity search failed: %v", simErr)), nil
			}

			newCtx := core.SeparationContext{
				ProjectID: projectID,
				Language:  language,
				EntityID:  entityID,
			}

			emb64 := vector.Float32To64(embedding)

			// Check each similar candidate for merge.
			for _, candidate := range candidates {
				if len(candidate.ContextVector) == 0 {
					continue
				}

				candEmb := vector.Float32To64(candidate.ContextVector)

				candidateCtx := core.SeparationContext{
					ProjectID: candidate.ProjectID,
					Language:  candidate.Language,
					EntityID:  candidate.EntityGroup,
				}

				sepResult := a.PatternSeparation.Check(embedding, candidate.ContextVector, newCtx, candidateCtx)
				if !sepResult.ShouldMerge {
					continue
				}

				// Merge: update existing memory.
				existing, gErr := a.Store.GetEpisodic(ctx, candidate.ID)
				if gErr != nil {
					continue
				}

				now := time.Now()
				sim := vector.CosineSimilarity(emb64, candEmb)
				newWeight := clampWeight(sim + 0.1)

				existing.Summary = existing.Summary + "\n" + summary
				existing.Weight = newWeight
				existing.LastAccessedAt = &now

				if uErr := a.Store.WriteEpisodic(ctx, existing); uErr != nil {
					return mcp.NewToolResultError(fmt.Sprintf("merge update failed: %v", uErr)), nil
				}

				resp, jErr := mcp.NewToolResultJSON(writeMemoryOutput{
					MemoryID:    candidate.ID,
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

			mem := &models.EpisodicMemory{
				ID:                 memoryID,
				ProjectID:          projectID,
				Language:           language,
				TaskType:           models.TaskType(taskType),
				Summary:            summary,
				SourceType:         models.SourceTypeLLM,
				TrustLevel:         models.DefaultTrustLevel,
				Weight:             weight,
				EffectiveFrequency: effFreq,
				CreatedAt:          now,
				EntityGroup:        entityID,
				ContextVector:      embedding,
			}

			if cErr := a.Store.WriteEpisodic(ctx, mem); cErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("create memory failed: %v", cErr)), nil
			}

			// Link to related memories.
			core.LinkOnWrite(ctx, a.Store, memoryID, summary, projectID, entityID, models.TaskType(taskType))

			// Push to working-memory cycles.
			a.WorkingMemory.Push(projectID, summary)

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
