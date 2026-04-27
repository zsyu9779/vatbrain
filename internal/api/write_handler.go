package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

// handleWrite implements POST /api/v0/memories/episodic.
//
// Pipeline: Significance Gate → embed → Pattern Separation → persist (Neo4j + pgvector).
func (s *Server) handleWrite(w http.ResponseWriter, r *http.Request) {
	var req models.WriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ProjectID == "" {
		respondError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	if req.Content.Summary == "" {
		respondError(w, http.StatusBadRequest, "content.summary is required")
		return
	}

	ctx := r.Context()

	// Fetch working-memory cycles from Redis.
	cyclesKey := fmt.Sprintf("working_memory:%s", req.ProjectID)
	summaries, err := s.Redis.LRange(ctx, cyclesKey, 0, -1)
	if err != nil && err.Error() != "redis: nil" {
		slog.Warn("redis lrange working memory", "err", err)
	}

	workingMemory := make([]core.WorkingMemoryCycle, len(summaries))
	for i, s := range summaries {
		workingMemory[i] = core.WorkingMemoryCycle{Summary: s}
	}

	// Evaluate significance gate.
	event := core.WriteEvent{
		Summary:       req.Content.Summary,
		UserConfirmed: req.UserConfirmed,
		IsCorrection:  req.IsCorrection,
	}
	gateResult := s.SignificanceGate.Evaluate(event, workingMemory)

	if !gateResult.ShouldPersist {
		respondJSON(w, http.StatusOK, models.WriteResponse{
			Persisted:  false,
			GateReason: gateResult.Reason,
		})
		return
	}

	// Generate embedding.
	embedding, err := s.Embedder.Embed(ctx, req.Content.Summary)
	if err != nil {
		slog.Error("embed", "err", err)
		respondError(w, http.StatusInternalServerError, "embedding failed")
		return
	}

	// Search for similar existing memories.
	candidates, err := s.Pgvector.SimilaritySearch(ctx, embedding, 5, nil)
	if err != nil {
		slog.Error("pgvector similarity search", "err", err)
		respondError(w, http.StatusInternalServerError, "similarity search failed")
		return
	}

	newCtx := core.SeparationContext{
		ProjectID: req.ProjectID,
		Language:  req.Language,
		EntityID:  req.Content.EntityID,
	}

	// Check each similar candidate for merge.
	for _, candidate := range candidates {
		candidateEmb, err := s.Pgvector.GetEmbedding(ctx, candidate.MemoryID)
		if err != nil {
			slog.Warn("pgvector get embedding", "memory_id", candidate.MemoryID, "err", err)
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

		sepResult := s.PatternSeparation.Check(embedding, candidateEmb, newCtx, candidateCtx)
		if !sepResult.ShouldMerge {
			continue
		}

		// Merge: update existing memory.
		parsedID, err := uuid.Parse(candidate.MemoryID)
		if err != nil {
			continue
		}

		now := time.Now()
		newWeight := clampWeight(candidate.Similarity + 0.1)

		_, err = s.Neo4j.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
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
				"newSummary": req.Content.Summary,
			})
			return nil, err
		})
		if err != nil {
			slog.Error("neo4j merge update", "err", err)
			respondError(w, http.StatusInternalServerError, "merge update failed")
			return
		}

		respondJSON(w, http.StatusOK, models.WriteResponse{
			MemoryID:    parsedID,
			Persisted:   true,
			GateReason:  gateResult.Reason,
			MergeAction: models.MergeActionUpdatedExisting,
			Weight:      newWeight,
		})
		return
	}

	// No merge — create new episodic memory.
	memoryID := uuid.New()
	now := time.Now()
	effFreq, weight := s.WeightDecay.ComputeFull([]time.Time{now}, now, now)

	_, err = s.Neo4j.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
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
			"projectID":   req.ProjectID,
			"language":    req.Language,
			"taskType":    string(req.TaskType),
			"summary":     req.Content.Summary,
			"sourceType":  string(models.SourceTypeLLM),
			"trustLevel":  int(models.DefaultTrustLevel),
			"weight":      weight,
			"effFreq":     effFreq,
			"createdAt":   now,
			"entityGroup": req.Content.EntityID,
			"embeddingID": memoryID.String(),
		})
		return nil, err
	})
	if err != nil {
		slog.Error("neo4j create episodic", "err", err)
		respondError(w, http.StatusInternalServerError, "create memory failed")
		return
	}

	// Insert embedding into pgvector.
	err = s.Pgvector.InsertEmbedding(ctx, memoryID.String(), embedding,
		req.Content.Summary, req.ProjectID, req.Language, string(req.TaskType),
		map[string]any{
			"entity_id": req.Content.EntityID,
		})
	if err != nil {
		slog.Error("pgvector insert", "err", err)
		respondError(w, http.StatusInternalServerError, "embedding insert failed")
		return
	}

	// Push to working-memory cycles.
	if pushErr := s.Redis.LPush(ctx, cyclesKey, req.Content.Summary); pushErr != nil {
		slog.Warn("redis lpush working memory", "err", pushErr)
	}
	if trimErr := s.Redis.LTrim(ctx, cyclesKey, 0, 19); trimErr != nil {
		slog.Warn("redis ltrim working memory", "err", trimErr)
	}

	respondJSON(w, http.StatusOK, models.WriteResponse{
		MemoryID:    memoryID,
		Persisted:   true,
		GateReason:  gateResult.Reason,
		MergeAction: models.MergeActionCreatedNew,
		Weight:      weight,
	})
}

// clampWeight ensures the weight stays in [0, 1].
func clampWeight(w float64) float64 {
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

// stringFromMeta extracts a string value from pgvector metadata.
func stringFromMeta(meta map[string]any, key string) (string, bool) {
	if meta == nil {
		return "", false
	}
	v, ok := meta[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
