package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// Sentinel errors from WriteMemory so callers can distinguish configuration
// failures from transient I/O failures.
var (
	errWritePipelineNoGate   = errors.New("write pipeline: significance gate is nil")
	errWritePipelineNoStore  = errors.New("write pipeline: store is nil")
	errWritePipelineEmbed    = errors.New("write pipeline: embedding failed")
	errWritePipelineSearch   = errors.New("write pipeline: similarity search failed")
	errWritePipelinePersist  = errors.New("write pipeline: persistence failed")
	errWritePipelinePatternSep = errors.New("write pipeline: pattern separation engine is nil")
	errWritePipelineWeightDecay = errors.New("write pipeline: weight decay engine is nil")
)

// WriteDeps carries the components the shared write pipeline needs. Both the
// MCP write_memory tool and the vatbrain-provider daemon (hermes sync_turn)
// route through WriteMemory so the significance gate → pattern separation →
// persistence → link-on-write order never drifts between callers.
type WriteDeps struct {
	Store       store.MemoryStore
	Gate        *SignificanceGate
	PatternSep  *PatternSeparation
	WeightDecay *WeightDecayEngine
	Embedder    embedder.Embedder
	// Surprise scores the prediction-error signal (§12) for persisted events.
	// May be nil, in which case a DefaultSurpriseScorer is used. The score is
	// stored on the memory so surprise-aware decay/ranking can read it later.
	Surprise *SurpriseScorer
	// WorkingMem accumulates accepted summaries for cross-cycle persistence.
	// May be nil, in which case cross-cycle gating sees an empty buffer.
	WorkingMem *store.WorkingMemoryBuffer
}

// WriteResult summarises the outcome of WriteMemory.
type WriteResult struct {
	MemoryID    uuid.UUID
	Persisted   bool
	GateReason  string
	MergeAction models.MergeAction
	Weight      float64
}

// WriteMemory runs the full write pipeline for an event: significance gate,
// embedding, pattern-separation merge, persistence, and link-on-write. It is
// shared by every entry point that writes episodic memories from a
// WriteEvent.
func WriteMemory(ctx context.Context, deps WriteDeps, event WriteEvent,
	projectID, language, entityID string, taskType models.TaskType) (WriteResult, error) {

	if deps.Gate == nil {
		return WriteResult{}, errWritePipelineNoGate
	}
	if deps.Store == nil {
		return WriteResult{}, errWritePipelineNoStore
	}
	if deps.PatternSep == nil {
		return WriteResult{}, errWritePipelinePatternSep
	}
	if deps.WeightDecay == nil {
		return WriteResult{}, errWritePipelineWeightDecay
	}

	// Fetch working-memory cycles accumulated for this project.
	var workingMemory []WorkingMemoryCycle
	if deps.WorkingMem != nil {
		for _, s := range deps.WorkingMem.GetAll(projectID) {
			workingMemory = append(workingMemory, WorkingMemoryCycle{Summary: s})
		}
	}

	// Evaluate significance gate (four conditions, all triggerable).
	gateResult := deps.Gate.Evaluate(ctx, event, workingMemory)
	if !gateResult.ShouldPersist {
		return WriteResult{
			Persisted:  false,
			GateReason: gateResult.Reason,
		}, nil
	}

	// Score the prediction-error signal (independent of the gate decision).
	scorer := deps.Surprise
	if scorer == nil {
		scorer = DefaultSurpriseScorer()
	}
	surprise := scorer.Score(event)

	// Generate embedding.
	embedding, err := deps.Embedder.Embed(ctx, event.Summary)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %v", errWritePipelineEmbed, err)
	}

	// Search for similar existing memories to check pattern-separation merge.
	candidates, err := deps.Store.SearchEpisodic(ctx, store.EpisodicSearchRequest{
		ProjectID: projectID,
		Embedding: vector.Float32To64(embedding),
		Limit:     5,
	})
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %v", errWritePipelineSearch, err)
	}

	newCtx := SeparationContext{
		ProjectID: projectID,
		Language:  language,
		EntityID:  entityID,
	}

	// Check each similar candidate for merge.
	for _, candidate := range candidates {
		if len(candidate.ContextVector) == 0 {
			continue
		}

		candidateCtx := SeparationContext{
			ProjectID: candidate.ProjectID,
			Language:  candidate.Language,
			EntityID:  candidate.EntityGroup,
		}

		sepResult := deps.PatternSep.Check(embedding, candidate.ContextVector, newCtx, candidateCtx)
		if !sepResult.ShouldMerge {
			continue
		}

		// Merge: update existing memory.
		existing, gErr := deps.Store.GetEpisodic(ctx, candidate.ID)
		if gErr != nil {
			continue
		}

		now := time.Now()
		sim := vector.CosineSimilarity(vector.Float32To64(embedding),
			vector.Float32To64(candidate.ContextVector))
		newWeight := ClampWeight(sim + 0.1)

		existing.Summary = existing.Summary + "\n" + event.Summary
		existing.Weight = newWeight
		existing.LastAccessedAt = &now
		if event.IsCorrection {
			existing.IsCorrection = true
		}
		if surprise > existing.SurpriseScore {
			existing.SurpriseScore = surprise
		}

		if uErr := deps.Store.WriteEpisodic(ctx, existing); uErr != nil {
			return WriteResult{}, fmt.Errorf("%w: %v", errWritePipelinePersist, uErr)
		}

		return WriteResult{
			MemoryID:    existing.ID,
			Persisted:   true,
			GateReason:  gateResult.Reason,
			MergeAction: models.MergeActionUpdatedExisting,
			Weight:      newWeight,
		}, nil
	}

	// No merge — create new episodic memory.
	memoryID := uuid.New()
	now := time.Now()
	effFreq, weight := deps.WeightDecay.ComputeFull([]time.Time{now}, now, now)

	mem := &models.EpisodicMemory{
		ID:                 memoryID,
		ProjectID:          projectID,
		Language:           language,
		TaskType:           taskType,
		Summary:            event.Summary,
		SourceType:         models.SourceTypeLLM,
		TrustLevel:         models.DefaultTrustLevel,
		Weight:             weight,
		EffectiveFrequency: effFreq,
		CreatedAt:          now,
		EntityGroup:        entityID,
		ContextVector:      embedding,
		IsCorrection:       event.IsCorrection,
		SurpriseScore:      surprise,
	}

	if err := deps.Store.WriteEpisodic(ctx, mem); err != nil {
		return WriteResult{}, fmt.Errorf("%w: %v", errWritePipelinePersist, err)
	}

	// Link to related memories (best-effort).
	LinkOnWrite(ctx, deps.Embedder, deps.Store, memoryID, event.Summary,
		projectID, entityID, taskType)

	// Push to working-memory cycles for cross-cycle persistence.
	if deps.WorkingMem != nil {
		deps.WorkingMem.Push(projectID, event.Summary)
	}

	return WriteResult{
		MemoryID:    memoryID,
		Persisted:   true,
		GateReason:  gateResult.Reason,
		MergeAction: models.MergeActionCreatedNew,
		Weight:      weight,
	}, nil
}

// ClampWeight ensures the weight stays in [0, 1].
func ClampWeight(w float64) float64 {
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}
