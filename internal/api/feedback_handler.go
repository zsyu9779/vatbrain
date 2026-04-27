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

// handleFeedback implements POST /api/v0/memories/{memory_id}/feedback.
//
// Records user behavior after a retrieval. Updates weight via simple delta
// based on the action: used (+0.15), confirmed (+0.20), ignored (-0.05),
// corrected (+0.30).
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	memoryID, err := uuid.Parse(chi.URLParam(r, "memory_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid memory_id format")
		return
	}

	var req models.FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !req.Action.IsValid() {
		respondError(w, http.StatusBadRequest, "invalid action")
		return
	}

	ctx := r.Context()

	// Fetch current weight from Neo4j.
	var currentWeight float64
	_, err = s.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		records, err := tx.Run(ctx, `
			MATCH (e:EpisodicMemory {id: $id})
			RETURN e.weight, e.effective_frequency, e.created_at
		`, map[string]any{"id": memoryID.String()})
		if err != nil {
			return nil, err
		}
		if !records.Next(ctx) {
			return nil, nil
		}
		r := records.Record()
		w, _, _ := neo4j.GetRecordValue[float64](r, "e.weight")
		currentWeight = w
		return nil, records.Err()
	})
	if err != nil {
		slog.Error("neo4j read for feedback", "err", err)
		respondError(w, http.StatusInternalServerError, "read memory failed")
		return
	}

	// Check if memory was found — if currentWeight is 0 and no error, it might
	// not exist. Do a dedicated existence check.
	if currentWeight == 0 {
		exists, _ := s.Neo4j.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			records, err := tx.Run(ctx,
				`MATCH (e:EpisodicMemory {id: $id}) RETURN count(e) AS c`,
				map[string]any{"id": memoryID.String()})
			if err != nil {
				return false, err
			}
			if records.Next(ctx) {
				r := records.Record()
				count, _, _ := neo4j.GetRecordValue[int64](r, "c")
				return count > 0, records.Err()
			}
			return false, records.Err()
		})
		if exists != true {
			respondError(w, http.StatusNotFound, "memory not found")
			return
		}
	}

	// Compute weight delta.
	delta := feedbackDelta(req.Action)
	newWeight := clampWeight(currentWeight + delta)
	now := time.Now()

	// Update Neo4j.
	_, err = s.Neo4j.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MATCH (e:EpisodicMemory {id: $id})
			SET e.weight = $newWeight, e.last_accessed_at = $now
		`, map[string]any{
			"id":        memoryID.String(),
			"newWeight": newWeight,
			"now":       now,
		})
		return nil, err
	})
	if err != nil {
		slog.Error("neo4j feedback update", "err", err)
		respondError(w, http.StatusInternalServerError, "weight update failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"memory_id":  memoryID,
		"new_weight": newWeight,
	})
}

// feedbackDelta returns the weight change for a given user action.
func feedbackDelta(action models.SearchAction) float64 {
	switch action {
	case models.SearchActionUsed:
		return 0.15
	case models.SearchActionConfirmed:
		return 0.20
	case models.SearchActionIgnored:
		return -0.05
	case models.SearchActionCorrected:
		return 0.30
	default:
		return 0
	}
}
