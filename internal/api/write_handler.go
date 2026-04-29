package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// handleWrite implements POST /api/v0/memories/episodic.
//
// Pipeline: Significance Gate → embed → Pattern Separation → persist via Store.
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

	// Fetch working-memory cycles from in-process buffer.
	summaries := s.WorkingMemory.GetAll(req.ProjectID)
	workingMemory := make([]core.WorkingMemoryCycle, len(summaries))
	for i, sum := range summaries {
		workingMemory[i] = core.WorkingMemoryCycle{Summary: sum}
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

	// Search for similar existing memories via Store.
	candidates, err := s.Store.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: req.ProjectID,
		Embedding: vector.Float32To64(embedding),
		Limit:     5,
	})
	if err != nil {
		slog.Error("store search episodics", "err", err)
		respondError(w, http.StatusInternalServerError, "similarity search failed")
		return
	}

	newCtx := core.SeparationContext{
		ProjectID: req.ProjectID,
		Language:  req.Language,
		EntityID:  req.Content.EntityID,
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

		sepResult := s.PatternSeparation.Check(embedding, candidate.ContextVector, newCtx, candidateCtx)
		if !sepResult.ShouldMerge {
			continue
		}

		// Merge: update existing memory.
		existing, err := s.Store.GetEpisodic(ctx, candidate.ID)
		if err != nil {
			slog.Warn("get episodic for merge", "memory_id", candidate.ID, "err", err)
			continue
		}

		now := time.Now()
		sim := vector.CosineSimilarity(emb64, candEmb)
		newWeight := clampWeight(sim + 0.1)

		existing.Summary = existing.Summary + "\n" + req.Content.Summary
		existing.Weight = newWeight
		existing.LastAccessedAt = &now

		if err := s.Store.WriteEpisodic(ctx, existing); err != nil {
			slog.Error("store merge update", "err", err)
			respondError(w, http.StatusInternalServerError, "merge update failed")
			return
		}

		respondJSON(w, http.StatusOK, models.WriteResponse{
			MemoryID:    candidate.ID,
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

	mem := &models.EpisodicMemory{
		ID:                 memoryID,
		ProjectID:          req.ProjectID,
		Language:           req.Language,
		TaskType:           req.TaskType,
		Summary:            req.Content.Summary,
		SourceType:         models.SourceTypeLLM,
		TrustLevel:         models.DefaultTrustLevel,
		Weight:             weight,
		EffectiveFrequency: effFreq,
		CreatedAt:          now,
		EntityGroup:        req.Content.EntityID,
		ContextVector:      embedding,
	}

	if err := s.Store.WriteEpisodic(ctx, mem); err != nil {
		slog.Error("store create episodic", "err", err)
		respondError(w, http.StatusInternalServerError, "create memory failed")
		return
	}

	// Link to related memories via RELATES_TO edges.
	core.LinkOnWrite(ctx, s.Store, memoryID, req.Content.Summary, req.ProjectID)

	// Push to working-memory cycles.
	s.WorkingMemory.Push(req.ProjectID, req.Content.Summary)

	respondJSON(w, http.StatusOK, models.WriteResponse{
		MemoryID:    memoryID,
		Persisted:   true,
		GateReason:  gateResult.Reason,
		MergeAction: models.MergeActionCreatedNew,
		Weight:      weight,
	})
}
