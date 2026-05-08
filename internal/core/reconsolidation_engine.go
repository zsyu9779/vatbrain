package core

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// ReconsolidationEngine handles back-propagation of correction signals through
// DERIVED_FROM traceability chains. When a user corrects a retrieved memory,
// the correction signal flows backward to the source episodic memories that
// produced it — implementing the principle that "every retrieval is a potential
// write" (DESIGN_PRINCIPLES.md §4.2).
type ReconsolidationEngine struct {
	// CorrectionBoost is the weight multiplier applied to source memories when
	// a derived memory is corrected. Default 1.5.
	CorrectionBoost float64
	// MaxTracebackHops limits DERIVED_FROM traceback depth. A value < 1
	// disables traceback entirely. Default 2. v0.2 topologies are 1-hop
	// (semantic/pitfall → episodic); the field is reserved for future
	// multi-hop chains.
	MaxTracebackHops int
	// CorrectionSourceThreshold is how many corrections a source must receive
	// before being flagged as a recurring error source. Default 2.
	CorrectionSourceThreshold int
}

// DefaultReconsolidationEngine returns a ReconsolidationEngine with tuned defaults.
func DefaultReconsolidationEngine() *ReconsolidationEngine {
	return &ReconsolidationEngine{
		CorrectionBoost:            1.5,
		MaxTracebackHops:           2,
		CorrectionSourceThreshold:  2,
	}
}

// ReconsolidationResult records the outcome of a reconsolidation pass.
type ReconsolidationResult struct {
	CorrectedID      uuid.UUID   `json:"corrected_id"`
	UpdatedIDs       []uuid.UUID `json:"updated_ids"`
	SkippedIDs       []uuid.UUID `json:"skipped_ids"`
	FailedIDs        []uuid.UUID `json:"failed_ids"`
	TotalWeightDelta float64     `json:"total_weight_delta"`
}

// Process handles a correction feedback event by tracing DERIVED_FROM edges
// from the corrected memory back to its source episodics, updating their
// weights and content.
//
// Flow:
//  1. Look up the corrected memory.
//  2. Semantic / Pitfall → trace DERIVED_FROM to source episodics.
//  3. Episodic → update directly (no traceback needed).
//  4. Multiply source weights by CorrectionBoost.
//  5. If user-corrected → bump trust_level.
func (re *ReconsolidationEngine) Process(
	ctx context.Context,
	s store.MemoryStore,
	correctedID uuid.UUID,
	correctedType string, // "episodic" | "semantic" | "pitfall"
	detail models.CorrectionDetail,
	isUserCorrected bool,
) (*ReconsolidationResult, error) {

	visited := make(map[uuid.UUID]bool)
	visited[correctedID] = true // protect against self-loop

	switch correctedType {
	case "episodic":
		return re.processEpisodic(ctx, s, correctedID, detail, isUserCorrected)
	case "semantic":
		return re.processSemantic(ctx, s, correctedID, detail, isUserCorrected, visited)
	case "pitfall":
		return re.processPitfall(ctx, s, correctedID, detail, isUserCorrected, visited)
	default:
		return nil, fmt.Errorf("reconsolidation: unknown corrected type %q", correctedType)
	}
}

// processEpisodic handles correction of an episodic memory by updating it directly.
func (re *ReconsolidationEngine) processEpisodic(
	ctx context.Context,
	s store.MemoryStore,
	episodicID uuid.UUID,
	detail models.CorrectionDetail,
	isUserCorrected bool,
) (*ReconsolidationResult, error) {
	ep, err := s.GetEpisodic(ctx, episodicID)
	if err != nil {
		return nil, fmt.Errorf("reconsolidation get episodic %s: %w", episodicID, err)
	}

	oldWeight := ep.Weight
	newWeight := ep.Weight * re.CorrectionBoost
	now := time.Now().UTC()

	if err := s.UpdateEpisodicWeight(ctx, ep.ID, newWeight, ep.EffectiveFrequency); err != nil {
		return nil, fmt.Errorf("update source weight: %w", err)
	}

	// Touch to update last_accessed_at.
	_ = s.TouchEpisodic(ctx, ep.ID, now)

	// Trust boost for user corrections.
	if isUserCorrected && ep.TrustLevel < 5 {
		ep.TrustLevel++
		ep.Weight = newWeight
		_ = s.WriteEpisodic(ctx, ep) // best-effort trust update
	}

	return &ReconsolidationResult{
		CorrectedID:      episodicID,
		UpdatedIDs:       []uuid.UUID{episodicID},
		TotalWeightDelta: newWeight - oldWeight,
	}, nil
}

// processSemantic traces DERIVED_FROM edges from the corrected semantic memory
// back to its source episodics and updates them.
func (re *ReconsolidationEngine) processSemantic(
	ctx context.Context,
	s store.MemoryStore,
	semanticID uuid.UUID,
	detail models.CorrectionDetail,
	isUserCorrected bool,
	visited map[uuid.UUID]bool,
) (*ReconsolidationResult, error) {
	// Semantics are derived FROM episodics, so edges go semantic → episodic.
	edges, err := s.GetEdges(ctx, semanticID, "DERIVED_FROM", "outgoing")
	if err != nil {
		return nil, fmt.Errorf("trace DERIVED_FROM for %s: %w", semanticID, err)
	}

	result := &ReconsolidationResult{CorrectedID: semanticID}
	if len(edges) == 0 || re.MaxTracebackHops < 1 {
		return result, nil
	}

	for _, edge := range edges {
		if visited[edge.ToID] {
			result.SkippedIDs = append(result.SkippedIDs, edge.ToID)
			continue
		}
		visited[edge.ToID] = true

		ep, getErr := s.GetEpisodic(ctx, edge.ToID)
		if getErr != nil {
			result.SkippedIDs = append(result.SkippedIDs, edge.ToID)
			continue
		}

		oldWeight := ep.Weight
		newWeight := ep.Weight * re.CorrectionBoost
		if upErr := s.UpdateEpisodicWeight(ctx, ep.ID, newWeight, ep.EffectiveFrequency); upErr != nil {
			result.FailedIDs = append(result.FailedIDs, ep.ID)
			continue
		}

		// Append correction detail to summary.
		ep.Summary = ep.Summary + "\n[corrected] " + detail.CorrectedTo
		ep.Weight = newWeight
		_ = s.WriteEpisodic(ctx, ep) // best-effort summary update

		if isUserCorrected && ep.TrustLevel < 5 {
			ep.TrustLevel++
			_ = s.WriteEpisodic(ctx, ep)
		}

		result.UpdatedIDs = append(result.UpdatedIDs, ep.ID)
		result.TotalWeightDelta += newWeight - oldWeight
	}

	return result, nil
}

// processPitfall traces DERIVED_FROM edges from the corrected pitfall back to
// source episodics. Pitfalls anchor on entities (entity_id), so the correction
// signal carries entity-specific debug context.
func (re *ReconsolidationEngine) processPitfall(
	ctx context.Context,
	s store.MemoryStore,
	pitfallID uuid.UUID,
	detail models.CorrectionDetail,
	isUserCorrected bool,
	visited map[uuid.UUID]bool,
) (*ReconsolidationResult, error) {
	edges, err := s.GetEdges(ctx, pitfallID, "DERIVED_FROM", "outgoing")
	if err != nil {
		return nil, fmt.Errorf("trace DERIVED_FROM for pitfall %s: %w", pitfallID, err)
	}

	result := &ReconsolidationResult{CorrectedID: pitfallID}
	if len(edges) == 0 || re.MaxTracebackHops < 1 {
		return result, nil
	}

	for _, edge := range edges {
		if visited[edge.ToID] {
			result.SkippedIDs = append(result.SkippedIDs, edge.ToID)
			continue
		}
		visited[edge.ToID] = true

		ep, getErr := s.GetEpisodic(ctx, edge.ToID)
		if getErr != nil {
			result.SkippedIDs = append(result.SkippedIDs, edge.ToID)
			continue
		}

		oldWeight := ep.Weight
		newWeight := ep.Weight * re.CorrectionBoost
		if upErr := s.UpdateEpisodicWeight(ctx, ep.ID, newWeight, ep.EffectiveFrequency); upErr != nil {
			result.FailedIDs = append(result.FailedIDs, ep.ID)
			continue
		}

		if isUserCorrected && ep.TrustLevel < 5 {
			ep.TrustLevel++
			ep.Weight = newWeight
			_ = s.WriteEpisodic(ctx, ep)
		}

		result.UpdatedIDs = append(result.UpdatedIDs, ep.ID)
		result.TotalWeightDelta += newWeight - oldWeight
	}

	return result, nil
}
