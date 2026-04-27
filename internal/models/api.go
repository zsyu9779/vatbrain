package models

import (
	"time"

	"github.com/google/uuid"
)

// ── Write API (Section 5.1) ────────────────────────────────────────────────

// WriteRequest is the payload for POST /api/v0/memories/episodic.
// It submits an episodic event through the significance gate + compound write pipeline.
type WriteRequest struct {
	ProjectID     string       `json:"project_id"`
	Language      string       `json:"language"`
	TaskType      TaskType     `json:"task_type"`
	Content       WriteContent `json:"content"`
	UserConfirmed bool         `json:"user_confirmed"`
	IsCorrection  bool         `json:"is_correction"`
}

// WriteContent is the inner content payload of a WriteRequest.
type WriteContent struct {
	Summary  string `json:"summary"`
	EntityID string `json:"entity_id"`
	Context  any    `json:"context,omitempty"`
}

// WriteResponse is returned after a successful write through the significance gate.
type WriteResponse struct {
	MemoryID    uuid.UUID   `json:"memory_id"`
	Persisted   bool        `json:"persisted"`
	GateReason  string      `json:"gate_reason"`
	MergeAction MergeAction `json:"merge_action"`
	Weight      float64     `json:"weight"`
}

// ── Search / Retrieval API (Section 5.2) ────────────────────────────────────

// SearchRequest is the payload for POST /api/v0/memories/search.
// It triggers the two-stage retrieval pipeline: Contextual Gating → Semantic Ranking.
type SearchRequest struct {
	Query          string        `json:"query"`
	Context        SearchContext `json:"context"`
	TopK           int           `json:"top_k"`
	IncludeDormant bool          `json:"include_dormant"`
}

// SearchResultItem is a single result in a search response.
type SearchResultItem struct {
	MemoryID       uuid.UUID   `json:"memory_id"`
	Type           string      `json:"type"` // "episodic" | "semantic"
	Content        string      `json:"content"`
	TrustLevel     TrustLevel  `json:"trust_level"`
	Weight         float64     `json:"weight"`
	RelevanceScore float64     `json:"relevance_score"`
	SourceIDs      []uuid.UUID `json:"source_ids"`
}

// ContextFilterStats reports metrics from Stage 1 (Contextual Gating).
type ContextFilterStats struct {
	TotalCandidates int   `json:"total_candidates"`
	AfterFilter     int   `json:"after_filter"`
	FilterTimeMs    int64 `json:"filter_time_ms"`
}

// SearchResponse is returned from POST /api/v0/memories/search.
type SearchResponse struct {
	Results            []SearchResultItem `json:"results"`
	ContextFilterStats ContextFilterStats  `json:"context_filter_stats"`
	SemanticRankTimeMs int64              `json:"semantic_rank_time_ms"`
}

// ── Feedback API (Section 5.3) ──────────────────────────────────────────────

// FeedbackRequest is the payload for POST /api/v0/memories/{memory_id}/feedback.
// It records user behavior after a retrieval, which drives attribution-based weight updates.
type FeedbackRequest struct {
	Action           SearchAction      `json:"action"`
	SessionID        string            `json:"session_id"`
	CorrectionDetail *CorrectionDetail `json:"correction_detail,omitempty"`
}

// CorrectionDetail captures what was corrected when a user disagrees with a result.
type CorrectionDetail struct {
	Original    string `json:"original"`
	CorrectedTo string `json:"corrected_to"`
}

// ── Consolidation API (Section 5.4) ─────────────────────────────────────────

// ConsolidationTriggerResponse is returned from POST /api/v0/consolidation/trigger.
type ConsolidationTriggerResponse struct {
	RunID   uuid.UUID `json:"run_id"`
	Status  string    `json:"status"` // "started" | "skipped"
	Message string    `json:"message"`
}

// ConsolidationRunResult is the detailed output of a single consolidation run.
// Returned by GET /api/v0/consolidation/runs/{run_id}.
type ConsolidationRunResult struct {
	RunID              uuid.UUID  `json:"run_id"`
	StartedAt          time.Time  `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at"`
	EpisodicsScanned   int        `json:"episodics_scanned"`
	CandidateRulesFound int       `json:"candidate_rules_found"`
	RulesPersisted     int        `json:"rules_persisted"`
	AverageAccuracy    float64    `json:"average_accuracy"`
}

// ── Weight / Touch API (Section 5.5) ────────────────────────────────────────

// TouchRequest is the payload for POST /api/v0/memories/{memory_id}/touch.
// It records a retrieval hit, updating effective_frequency and weight.
type TouchRequest struct {
	SessionID string `json:"session_id"`
}

// TouchResponse is returned after a touch updates the memory weight.
type TouchResponse struct {
	NewWeight float64 `json:"new_weight"`
}

// WeightDetailResponse is returned from GET /api/v0/memories/{memory_id}/weight.
// It exposes the full weight calculation for transparency.
type WeightDetailResponse struct {
	MemoryID           uuid.UUID `json:"memory_id"`
	Weight             float64   `json:"weight"`
	EffectiveFrequency float64   `json:"effective_frequency"`
	ExperienceDecay    float64   `json:"experience_decay"`
	ActivityDecay      float64   `json:"activity_decay"`
}

// ── Health ──────────────────────────────────────────────────────────────────

// HealthResponse is the shared health-check response used by all services.
type HealthResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
