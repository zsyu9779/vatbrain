package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/vatbrain/vatbrain/internal/models"
)

// handleTouch implements POST /api/v0/memories/{memory_id}/touch.
//
// Records a retrieval hit, recomputes full weight via the WeightDecayEngine,
// and updates the Neo4j node.
func (s *Server) handleTouch(w http.ResponseWriter, r *http.Request) {
	memoryID, err := uuid.Parse(chi.URLParam(r, "memory_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid memory_id format")
		return
	}

	var req models.TouchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	now := time.Now()

	// Fetch current memory state.
	var createdAt time.Time
	var lastAccessedAt *time.Time

	found, err := s.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		records, err := tx.Run(ctx, `
			MATCH (e:EpisodicMemory {id: $id})
			RETURN e.created_at, e.last_accessed_at
		`, map[string]any{"id": memoryID.String()})
		if err != nil {
			return false, err
		}
		if !records.Next(ctx) {
			return false, records.Err()
		}
		r := records.Record()
		createdAt, _, _ = neo4j.GetRecordValue[time.Time](r, "e.created_at")
		la, laIsNil, _ := neo4j.GetRecordValue[time.Time](r, "e.last_accessed_at")
		if !laIsNil {
			lastAccessedAt = &la
		}
		return true, records.Err()
	})
	if err != nil {
		slog.Error("neo4j read for touch", "err", err)
		respondError(w, http.StatusInternalServerError, "read memory failed")
		return
	}
	if found != true {
		respondError(w, http.StatusNotFound, "memory not found")
		return
	}

	// Build access timestamps: creation + last access (if any) + now.
	accessTimestamps := []time.Time{createdAt, now}
	newWeight := s.WeightDecay.Weight(1.0, createdAt, now, now)

	// Update Neo4j.
	_, err = s.Neo4j.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MATCH (e:EpisodicMemory {id: $id})
			SET e.last_accessed_at = $now,
			    e.weight = $newWeight,
			    e.effective_frequency = e.effective_frequency + 1
		`, map[string]any{
			"id":        memoryID.String(),
			"now":       now,
			"newWeight": newWeight,
		})
		return nil, err
	})
	if err != nil {
		slog.Error("neo4j touch update", "err", err)
		respondError(w, http.StatusInternalServerError, "touch update failed")
		return
	}

	_ = accessTimestamps
	_ = lastAccessedAt

	respondJSON(w, http.StatusOK, models.TouchResponse{
		NewWeight: newWeight,
	})
}

// handleWeightDetail implements GET /api/v0/memories/{memory_id}/weight.
//
// Returns the full weight calculation breakdown for a given memory.
func (s *Server) handleWeightDetail(w http.ResponseWriter, r *http.Request) {
	memoryID, err := uuid.Parse(chi.URLParam(r, "memory_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid memory_id format")
		return
	}

	ctx := r.Context()
	now := time.Now()

	var createdAt time.Time
	var lastAccessedAt time.Time
	var hasLastAccess bool
	var effFreq, weight float64

	found, err := s.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		records, err := tx.Run(ctx, `
			MATCH (e:EpisodicMemory {id: $id})
			RETURN e.created_at, e.last_accessed_at, e.effective_frequency, e.weight
		`, map[string]any{"id": memoryID.String()})
		if err != nil {
			return false, err
		}
		if !records.Next(ctx) {
			return false, records.Err()
		}
		r := records.Record()
		createdAt, _, _ = neo4j.GetRecordValue[time.Time](r, "e.created_at")
		lastAccessedAt, hasLastAccess, _ = neo4j.GetRecordValue[time.Time](r, "e.last_accessed_at")
		effFreq, _, _ = neo4j.GetRecordValue[float64](r, "e.effective_frequency")
		weight, _, _ = neo4j.GetRecordValue[float64](r, "e.weight")
		return true, records.Err()
	})
	if err != nil {
		slog.Error("neo4j read for weight detail", "err", err)
		respondError(w, http.StatusInternalServerError, "read memory failed")
		return
	}
	if found != true {
		respondError(w, http.StatusNotFound, "memory not found")
		return
	}

	// Compute decay components.
	experienceDecay := s.WeightDecay.Weight(effFreq, createdAt, createdAt, now)
	activityDecay := 0.0
	if hasLastAccess {
		activityDecay = s.WeightDecay.Weight(effFreq, createdAt, lastAccessedAt, now)
	}

	respondJSON(w, http.StatusOK, models.WeightDetailResponse{
		MemoryID:           memoryID,
		Weight:             weight,
		EffectiveFrequency: effFreq,
		ExperienceDecay:    experienceDecay,
		ActivityDecay:      activityDecay,
	})
}
