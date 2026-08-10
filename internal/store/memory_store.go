// Package store defines the MemoryStore abstraction over all VatBrain persistence.
// Each backend (SQLite, Neo4j+pgvector, in-memory) implements this interface.
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
)

// ── Request Types ───────────────────────────────────────────────────────────────

// EpisodicSearchRequest carries structured filters and an optional embedding for
// semantic similarity search. Backends that support vector search use Embedding;
// others fall back to structured filtering + in-process cosine similarity.
type EpisodicSearchRequest struct {
	ProjectID       string
	Language        string
	TaskType        models.TaskType
	MinWeight       float64
	Limit           int
	IncludeObsolete bool

	// Embedding is the query vector for semantic similarity search.
	// Set by the caller (engine/embedder). Nil means pure structured query.
	Embedding []float64

	// SurpriseBoost, when > 0, multiplies each candidate's ranking weight by
	// (1 + SurpriseBoost × surprise_score), promoting high-surprise memories
	// above otherwise-equal peers. 0 (default) leaves ranking unchanged, so
	// existing callers see identical behavior.
	SurpriseBoost float64

	// OccurredAfter, when non-zero, keeps only memories whose occurred_at is
	// >= this time (inclusive). Zero disables the lower bound.
	OccurredAfter time.Time
	// OccurredBefore, when non-zero, keeps only memories whose occurred_at is
	// <= this time (inclusive). Zero disables the upper bound.
	OccurredBefore time.Time
	// SortByOccurredAt, when true, orders results by occurred_at descending
	// (most recent first) instead of the default relevance ranking — the
	// "最近一次" (most recent) semantic. It also disables the hot cache:
	// time-ordered results are inherently freshness-sensitive.
	SortByOccurredAt bool

	// Query is the lexical-channel query text — typically the query-expanded
	// form of the question (embedder.ExpandQuery). When set together with
	// Embedding, candidates are ranked by both channels — semantic cosine
	// (Embedding) and lexical character-bigram overlap (Query vs summary) —
	// and the two rankings are fused with reciprocal rank fusion (RRF), so
	// exact-keyword facts the semantic channel ranks low still surface.
	// Backends that do not implement fusion ignore it, degrading to the
	// pure-semantic ranking. Without Embedding it is ignored entirely.
	Query string

	// RrfK is the RRF fusion constant K (fused score = Σ 1/(K + rank), ranks
	// 1-based); 0 uses the default (60). It only applies when Query and
	// Embedding are both set.
	RrfK int
}

// SemanticSearchRequest carries filters and an optional embedding for semantic
// similarity search over consolidated rules/facts.
type SemanticSearchRequest struct {
	ProjectID  string
	MemoryType models.MemoryType
	Limit      int

	// Embedding is the query vector for semantic similarity search.
	Embedding []float64
}

// PitfallSearchRequest carries filters and an optional signature embedding for
// PitfallMemory retrieval. Implements dual-key matching: when both EntityID and
// Embedding are set, the query requires entity_id exact match AND signature
// embedding similarity > 0.7 (AND semantics).
type PitfallSearchRequest struct {
	ProjectID        string
	Language         string
	EntityID         string
	RootCauseCategory models.RootCause
	MinWeight        float64
	Limit            int
	Embedding        []float64 // key2: signature embedding (AND with EntityID key1)
}

// ── Return Types ────────────────────────────────────────────────────────────────

// EpisodicScanItem is a lightweight projection returned by ScanRecent, owned by
// the store package to avoid circular dependencies with core.
type EpisodicScanItem struct {
	ID           uuid.UUID
	Summary      string
	TaskType     models.TaskType
	ProjectID    string
	Language     string
	EntityGroup  string
	EntityID     string // v0.2: code entity anchor for Pitfall clustering
	Weight       float64
	LastAccessed time.Time
	// OccurredAt is when the event happened (v0.4 temporal attribute),
	// falling back to CreatedAt for memories without an explicit time.
	OccurredAt time.Time
}

// Edge represents a directed relationship between two memory nodes.
type Edge struct {
	FromID     uuid.UUID
	ToID       uuid.UUID
	EdgeType   string
	Properties map[string]any
	CreatedAt  time.Time
}

// ── MemoryStore Interface ───────────────────────────────────────────────────────

// MemoryStore is the single abstraction over all VatBrain persistence.
// Each backend (SQLite, Neo4j+pgvector, in-memory) implements this interface.
type MemoryStore interface {
	// ── Episodic Memory ──────────────────────────────────────────────
	WriteEpisodic(ctx context.Context, mem *models.EpisodicMemory) error
	SearchEpisodic(ctx context.Context, req EpisodicSearchRequest) ([]models.EpisodicMemory, error)
	GetEpisodic(ctx context.Context, id uuid.UUID) (*models.EpisodicMemory, error)
	TouchEpisodic(ctx context.Context, id uuid.UUID, now time.Time) error
	UpdateEpisodicWeight(ctx context.Context, id uuid.UUID, weight, effFreq float64) error
	MarkObsolete(ctx context.Context, id uuid.UUID, at time.Time) error

	// ── Semantic Memory ─────────────────────────────────────────────
	WriteSemantic(ctx context.Context, mem *models.SemanticMemory) error
	SearchSemantic(ctx context.Context, req SemanticSearchRequest) ([]models.SemanticMemory, error)
	GetSemantic(ctx context.Context, id uuid.UUID) (*models.SemanticMemory, error)

	// ── Edges ───────────────────────────────────────────────────────
	CreateEdge(ctx context.Context, from, to uuid.UUID, edgeType string, props map[string]any) error
	GetEdges(ctx context.Context, nodeID uuid.UUID, edgeType string, direction string) ([]Edge, error)

	// ── Consolidation ───────────────────────────────────────────────
	// ScanRecent returns episodic memories modified since a given time.
	ScanRecent(ctx context.Context, since time.Time, limit int) ([]EpisodicScanItem, error)
	SaveConsolidationRun(ctx context.Context, run *models.ConsolidationRunResult) error
	GetConsolidationRun(ctx context.Context, runID uuid.UUID) (*models.ConsolidationRunResult, error)

	// ── Pitfall Memory ───────────────────────────────────────────
	WritePitfall(ctx context.Context, p *models.PitfallMemory) error
	SearchPitfall(ctx context.Context, req PitfallSearchRequest) ([]models.PitfallMemory, error)
	GetPitfall(ctx context.Context, id uuid.UUID) (*models.PitfallMemory, error)
	TouchPitfall(ctx context.Context, id uuid.UUID, now time.Time) error
	UpdatePitfallWeight(ctx context.Context, id uuid.UUID, weight float64) error
	UpdatePitfallStatus(ctx context.Context, id uuid.UUID, status models.PitfallStatus) error
	AddPitfallCounters(ctx context.Context, id uuid.UUID, shownDelta, suppressedDelta int) error
	ApplyPitfallFeedback(ctx context.Context, id uuid.UUID, action models.PitfallFeedbackAction, now time.Time) error
	MarkPitfallObsolete(ctx context.Context, id uuid.UUID, at time.Time) error

	// SearchPitfallByEntity finds all Pitfalls anchored on a specific entity.
	SearchPitfallByEntity(ctx context.Context, entityID, projectID string) ([]models.PitfallMemory, error)

	// UpdateSemanticWeight updates the weight and effective frequency of a semantic memory.
	UpdateSemanticWeight(ctx context.Context, id uuid.UUID, weight, effFreq float64) error

	// ── Lifecycle ───────────────────────────────────────────────────
	HealthCheck(ctx context.Context) error
	Close() error
}

// EpisodicDeleteStore is the optional capability to delete all episodic
// memories for a project — how the OmniMemEval benchmark entrypoint isolates
// per-user evaluation runs (docs/v0.3/tech-specs/03-omnimemeval-benchmark.md).
// SQLite and the in-memory store implement it; backends that do not (e.g. the
// deprecated Neo4j+pgvector store) omit the methods, and the benchmark
// entrypoint reports the backend as unsupported via a type assertion.
type EpisodicDeleteStore interface {
	// DeleteEpisodicByProject removes every episodic memory under projectID
	// and returns how many rows were deleted.
	DeleteEpisodicByProject(ctx context.Context, projectID string) (int, error)
}

// RuleConflictStore is the optional conflict-governance capability a backend
// may implement on top of MemoryStore (ROADMAP v1.0 冲突协调引擎). SQLite and
// the in-memory store implement it; backends that do not (e.g. the deprecated
// Neo4j+pgvector store) simply omit the methods, and conflict tools report the
// backend as unsupported via a type assertion.
type RuleConflictStore interface {
	// ListRuleConflicts returns conflict records, optionally filtered by status
	// ("" = all), most recent first, capped at limit (0 = default 20).
	ListRuleConflicts(ctx context.Context, status string, limit int) ([]models.RuleConflict, error)
	// SaveRuleConflict stores a newly detected conflict as pending.
	SaveRuleConflict(ctx context.Context, c *models.RuleConflict) error
	// ResolveRuleConflict records a resolution: the winning rule ID plus the
	// status transition and a free-form reason.
	ResolveRuleConflict(ctx context.Context, id uuid.UUID, status models.ConflictStatus, resolution uuid.UUID, reason string, at time.Time) error
	// MarkSemanticObsolete sets obsoleted_at on a semantic memory — how a
	// losing rule is retired after a conflict resolution.
	MarkSemanticObsolete(ctx context.Context, id uuid.UUID, at time.Time) error
}
