package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// handleSearch implements POST /api/v0/memories/search.
//
// Pipeline: embed query → Store.SearchEpisodic → Contextual Gating →
// merge with semantic results → inject pitfalls → respond.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req models.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Query == "" {
		respondError(w, http.StatusBadRequest, "query is required")
		return
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}

	ctx := r.Context()

	// Generate query embedding.
	queryEmb, err := s.Embedder.Embed(ctx, req.Query)
	if err != nil {
		slog.Error("embed query", "err", err)
		respondError(w, http.StatusInternalServerError, "embedding failed")
		return
	}

	// Search episodic memories via Store — hard constraints + semantic ranking in one call.
	episodics, err := s.Store.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID:       req.Context.ProjectID,
		Language:        req.Context.Language,
		Embedding:       vector.Float32To64(queryEmb),
		Limit:           s.RetrievalEngine.MaxCandidates,
		IncludeObsolete: req.IncludeDormant,
	})
	if err != nil {
		slog.Error("store search episodics", "err", err)
		respondError(w, http.StatusInternalServerError, "fetch candidates failed")
		return
	}

	// Stage 1: Contextual Gating.
	gating := &core.ContextualGating{}
	filtered, stats := gating.FilterAndRank(episodics, req.Context,
		s.WeightDecay.CoolingThreshold, s.RetrievalEngine.MaxCandidates)

	// Build id → episodic map for lookup.
	epByID := make(map[string]models.EpisodicMemory, len(episodics))
	for _, ep := range episodics {
		epByID[ep.ID.String()] = ep
	}

	// Build results from gated candidates.
	var results []models.SearchResultItem
	for _, f := range filtered {
		ep, ok := epByID[f.MemoryID]
		if !ok {
			continue
		}
		score := ep.Weight
		results = append(results, models.SearchResultItem{
			MemoryID:       ep.ID,
			Type:           "episodic",
			Content:        ep.Summary,
			TrustLevel:     ep.TrustLevel,
			Weight:         ep.Weight,
			RelevanceScore: score,
		})
	}

	// Fetch and filter semantic candidates.
	semantics, semErr := s.Store.SearchSemantic(ctx, store.SemanticSearchRequest{
		Limit: 200,
	})
	if semErr != nil {
		slog.Warn("store search semantics", "err", semErr)
	}

	for _, sem := range semantics {
		if core.TokenOverlap(req.Query, sem.Content) {
			results = append(results, models.SearchResultItem{
				MemoryID:       sem.ID,
				Type:           "semantic",
				Content:        sem.Content,
				TrustLevel:     sem.TrustLevel,
				Weight:         sem.Weight,
				RelevanceScore: 0.5,
			})
		}
	}

	// v0.2: Error-aware search — inject pitfall matches for the entity anchor.
	if req.Context.EntityID != "" && req.Context.ProjectID != "" {
		pitfalls, pfErr := s.Store.SearchPitfallByEntity(ctx, req.Context.EntityID, req.Context.ProjectID)
		if pfErr != nil {
			slog.Warn("store search pitfalls", "entity", req.Context.EntityID, "err", pfErr)
		} else {
			// Sort: user-corrected first, then occurrence_count desc, then weight desc.
			sort.Slice(pitfalls, func(i, j int) bool {
				if pitfalls[i].WasUserCorrected != pitfalls[j].WasUserCorrected {
					return pitfalls[i].WasUserCorrected
				}
				if pitfalls[i].OccurrenceCount != pitfalls[j].OccurrenceCount {
					return pitfalls[i].OccurrenceCount > pitfalls[j].OccurrenceCount
				}
				return pitfalls[i].Weight > pitfalls[j].Weight
			})

			// Inject at most 3 pitfalls (7±2 principle, design doc D4).
			pitfallLimit := 3
			if len(pitfalls) < pitfallLimit {
				pitfallLimit = len(pitfalls)
			}
			for _, p := range pitfalls[:pitfallLimit] {
				results = append(results, models.SearchResultItem{
					MemoryID:          p.ID,
					Type:              "pitfall",
					Content:           p.Signature,
					TrustLevel:        p.TrustLevel,
					Weight:            p.Weight,
					RelevanceScore:    0.6, // moderate relevance for entity-anchored pitfalls
					RootCauseCategory: string(p.RootCauseCategory),
					FixStrategy:       p.FixStrategy,
					WasUserCorrected:  p.WasUserCorrected,
				})
			}
		}
	}

	// Sort by relevance descending, cap at TopK.
	sort.Slice(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})
	if len(results) > req.TopK {
		results = results[:req.TopK]
	}

	respondJSON(w, http.StatusOK, models.SearchResponse{
		Results:            results,
		ContextFilterStats:  stats,
		SemanticRankTimeMs: 0,
	})
}
