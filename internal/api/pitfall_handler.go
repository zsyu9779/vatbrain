package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// pitfallSearchRequest is the payload for POST /api/v0/pitfalls/search.
type pitfallSearchRequest struct {
	EntityID          string `json:"entity_id"`
	ProjectID         string `json:"project_id"`
	Language          string `json:"language"`
	RootCauseCategory string `json:"root_cause_category"`
	Query             string `json:"query"` // optional: embed for signature similarity
	TopK              int    `json:"top_k"`
}

// pitfallSearchResponse is the response for POST /api/v0/pitfalls/search.
type pitfallSearchResponse struct {
	Results []models.SearchResultItem `json:"results"`
}

// handlePitfallSearch implements POST /api/v0/pitfalls/search.
//
// Dual-key matching: when both entity_id and query are provided, results are
// filtered by entity_id exact match AND signature embedding similarity.
func (s *Server) handlePitfallSearch(w http.ResponseWriter, r *http.Request) {
	var req pitfallSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}

	ctx := r.Context()

	var pitfalls []models.PitfallMemory
	var err error

	// If entity_id is set, use entity-anchored search (fast path).
	if req.EntityID != "" {
		pitfalls, err = s.Store.SearchPitfallByEntity(ctx, req.EntityID, req.ProjectID)
		if err != nil {
			slog.Error("pitfall search by entity", "entity_id", req.EntityID, "err", err)
			respondError(w, http.StatusInternalServerError, "pitfall search failed")
			return
		}
	} else {
		// Full search with optional embedding.
		sr := store.PitfallSearchRequest{
			ProjectID:        req.ProjectID,
			Language:         req.Language,
			RootCauseCategory: models.RootCause(req.RootCauseCategory),
			Limit:            req.TopK,
		}
		// Embed query for signature similarity if provided.
		if req.Query != "" {
			emb, embErr := s.Embedder.Embed(ctx, req.Query)
			if embErr != nil {
				slog.Warn("pitfall search embed", "err", embErr)
			} else {
				sr.Embedding = vector.Float32To64(emb)
			}
		}
		pitfalls, err = s.Store.SearchPitfall(ctx, sr)
		if err != nil {
			slog.Error("pitfall search", "err", err)
			respondError(w, http.StatusInternalServerError, "pitfall search failed")
			return
		}
	}

	results := make([]models.SearchResultItem, 0, len(pitfalls))
	for _, p := range pitfalls {
		results = append(results, models.SearchResultItem{
			MemoryID:          p.ID,
			Type:              "pitfall",
			Content:           p.Signature,
			TrustLevel:        p.TrustLevel,
			Weight:            p.Weight,
			RelevanceScore:    p.Weight,
			RootCauseCategory: string(p.RootCauseCategory),
			FixStrategy:       p.FixStrategy,
			WasUserCorrected:  p.WasUserCorrected,
		})
	}

	if len(results) > req.TopK {
		results = results[:req.TopK]
	}

	respondJSON(w, http.StatusOK, pitfallSearchResponse{Results: results})
}
