package core

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// LinkOnWrite finds memories related to the newly created episodic memory and
// creates RELATES_TO edges. If the memory is a debug session with an entity
// anchor, it also checks for existing Pitfall matches and creates
// TRIGGERED_PITFALL edges. All operations are best-effort — failures are
// logged but do not prevent the write from succeeding.
func LinkOnWrite(ctx context.Context, s store.MemoryStore, memoryID uuid.UUID, summary, projectID, entityID string, taskType models.TaskType) {
	// 1. Existing: RELATES_TO edges via token similarity.
	related, err := s.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: projectID,
		Limit:     20,
	})
	if err != nil {
		slog.Warn("link_on_write: fetch candidates", "err", err)
		return
	}

	for _, r := range related {
		if r.ID == memoryID {
			continue
		}
		strength := tokenSimilarity(summary, r.Summary)
		if strength < 0.15 {
			continue
		}
		err := s.CreateEdge(ctx, memoryID, r.ID, "RELATES_TO", map[string]any{
			"strength":   strength,
			"dimension":  "SEMANTIC",
			"created_at": time.Now().UTC(),
		})
		if err != nil {
			slog.Warn("link_on_write: create edge", "from", memoryID, "to", r.ID, "err", err)
		}
	}

	// 2. v0.2: Pitfall association for debug-type memories with entity anchor.
	if taskType == models.TaskTypeDebug && entityID != "" {
		pitfalls, err := s.SearchPitfallByEntity(ctx, entityID, projectID)
		if err != nil {
			slog.Warn("link_on_write: search pitfalls", "entity_id", entityID, "err", err)
			return
		}
		now := time.Now().UTC()
		for _, p := range pitfalls {
			if err := s.CreateEdge(ctx, memoryID, p.ID, "TRIGGERED_PITFALL", map[string]any{
				"similarity": 0.0, // exact entity match, no embedding comparison
			}); err != nil {
				slog.Warn("link_on_write: triggered_pitfall edge", "from", memoryID, "to", p.ID, "err", err)
				continue
			}
			// Touch the pitfall to increment occurrence_count.
			if err := s.TouchPitfall(ctx, p.ID, now); err != nil {
				slog.Warn("link_on_write: touch pitfall", "pitfall_id", p.ID, "err", err)
			}
		}
	}
}

// tokenSimilarity computes Jaccard similarity on token sets.
func tokenSimilarity(a, b string) float64 {
	aToks := Tokenize(a)
	bToks := Tokenize(b)
	if len(aToks) == 0 || len(bToks) == 0 {
		return 0
	}
	bSet := make(map[string]struct{}, len(bToks))
	for _, t := range bToks {
		bSet[t] = struct{}{}
	}
	intersection := 0
	for _, t := range aToks {
		if _, ok := bSet[t]; ok {
			intersection++
		}
	}
	union := len(aToks) + len(bToks) - intersection
	return float64(intersection) / float64(union)
}
