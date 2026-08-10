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
	errWritePipelineNoGate      = errors.New("write pipeline: significance gate is nil")
	errWritePipelineNoStore     = errors.New("write pipeline: store is nil")
	errWritePipelineEmbed       = errors.New("write pipeline: embedding failed")
	errWritePipelineSearch      = errors.New("write pipeline: similarity search failed")
	errWritePipelinePersist     = errors.New("write pipeline: persistence failed")
	errWritePipelinePatternSep  = errors.New("write pipeline: pattern separation engine is nil")
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
	// SkipLinkOnWrite disables RELATES_TO edge creation. The OmniMemEval bench
	// entrypoint sets it: edges are not needed for benchmark scoring, and
	// LinkOnWrite re-embeds the new summary against up to 20 candidates, so
	// skipping it avoids up to ~40 embedding API calls per write when a paid
	// semantic embedder is configured.
	SkipLinkOnWrite bool
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

	prepared, err := prepareWriteEvent(ctx, deps, event, projectID)
	if err != nil {
		return WriteResult{}, err
	}
	if !prepared.gate.ShouldPersist {
		return WriteResult{
			Persisted:  false,
			GateReason: prepared.gate.Reason,
		}, nil
	}

	// Generate embedding.
	embedding, err := deps.Embedder.Embed(ctx, event.Summary)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %v", errWritePipelineEmbed, err)
	}

	return writeMemoryPersist(ctx, deps, event, prepared, embedding,
		projectID, language, entityID, taskType)
}

// WriteMemoryWithEmbedding runs the write pipeline with a caller-supplied
// embedding for event.Summary, skipping the internal Embed call. The
// vatbrain-bench entrypoint uses it to embed an entire /v1/add batch
// concurrently and then persist messages sequentially (SQLite is a single
// writer) — the gate, search, merge, and persistence order is identical to
// WriteMemory. The embedding must be the vector for the exact summary text.
func WriteMemoryWithEmbedding(ctx context.Context, deps WriteDeps, event WriteEvent, embedding []float32,
	projectID, language, entityID string, taskType models.TaskType) (WriteResult, error) {

	prepared, err := prepareWriteEvent(ctx, deps, event, projectID)
	if err != nil {
		return WriteResult{}, err
	}
	if !prepared.gate.ShouldPersist {
		return WriteResult{
			Persisted:  false,
			GateReason: prepared.gate.Reason,
		}, nil
	}
	if len(embedding) == 0 {
		return WriteResult{}, fmt.Errorf("%w: empty embedding", errWritePipelineEmbed)
	}

	return writeMemoryPersist(ctx, deps, event, prepared, embedding,
		projectID, language, entityID, taskType)
}

// preparedWrite carries the pre-embedding half of the write pipeline: the
// validated dependency check, the significance-gate verdict, and the
// prediction-error score.
type preparedWrite struct {
	gate     GateResult
	surprise float64
}

// prepareWriteEvent validates the pipeline dependencies, fetches the
// project's working-memory cycles, evaluates the significance gate, and
// scores the prediction-error signal. It is shared by WriteMemory and
// WriteMemoryWithEmbedding so the gate → surprise → persist order never
// drifts between the sequential and precomputed-embedding paths.
func prepareWriteEvent(ctx context.Context, deps WriteDeps, event WriteEvent, projectID string) (preparedWrite, error) {
	if deps.Gate == nil {
		return preparedWrite{}, errWritePipelineNoGate
	}
	if deps.Store == nil {
		return preparedWrite{}, errWritePipelineNoStore
	}
	if deps.PatternSep == nil {
		return preparedWrite{}, errWritePipelinePatternSep
	}
	if deps.WeightDecay == nil {
		return preparedWrite{}, errWritePipelineWeightDecay
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

	// Score the prediction-error signal (independent of the gate decision).
	scorer := deps.Surprise
	if scorer == nil {
		scorer = DefaultSurpriseScorer()
	}

	return preparedWrite{gate: gateResult, surprise: scorer.Score(event)}, nil
}

// writeMemoryPersist runs the post-embedding half of the write pipeline:
// similarity search, pattern-separation merge, persistence, link-on-write,
// and working-memory push.
func writeMemoryPersist(ctx context.Context, deps WriteDeps, event WriteEvent, prepared preparedWrite,
	embedding []float32, projectID, language, entityID string, taskType models.TaskType) (WriteResult, error) {

	surprise := prepared.surprise

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
		// OccurredAt keeps the story anchor: on a pattern-separation append
		// the memory retains its original event time (the appended summary is
		// usually a restatement of the same event) — unless the incoming
		// event explicitly predates it, in which case the anchor moves
		// earlier so temporal reasoning still sees the story's start.
		if !event.OccurredAt.IsZero() && event.OccurredAt.Before(existing.EffectiveOccurredAt()) {
			existing.OccurredAt = event.OccurredAt
		}
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
			GateReason:  prepared.gate.Reason,
			MergeAction: models.MergeActionUpdatedExisting,
			Weight:      newWeight,
		}, nil
	}

	// No merge — create new episodic memory.
	memoryID := uuid.New()
	now := time.Now()
	effFreq, weight := deps.WeightDecay.ComputeFull([]time.Time{now}, now, now)

	// Temporal attribute: carry the event's explicit time through; when the
	// write path had none (zero), fall back to the write time.
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = now
	}

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
		OccurredAt:         occurredAt,
	}

	if err := deps.Store.WriteEpisodic(ctx, mem); err != nil {
		return WriteResult{}, fmt.Errorf("%w: %v", errWritePipelinePersist, err)
	}

	// Link to related memories (best-effort). Skippable for batch-import paths
	// (e.g. the benchmark entrypoint) where RELATES_TO edges are not consumed.
	if !deps.SkipLinkOnWrite {
		LinkOnWrite(ctx, deps.Embedder, deps.Store, memoryID, event.Summary,
			projectID, entityID, taskType)
	}

	// Push to working-memory cycles for cross-cycle persistence.
	if deps.WorkingMem != nil {
		deps.WorkingMem.Push(projectID, event.Summary)
	}

	return WriteResult{
		MemoryID:    memoryID,
		Persisted:   true,
		GateReason:  prepared.gate.Reason,
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
