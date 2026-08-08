package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// PitfallMemory represents an independent error pattern node. It answers the
// question "how did this break?" rather than "how should this work?" (which
// is the domain of SemanticMemory).
type PitfallMemory struct {
	ID                   uuid.UUID  `json:"id"`
	EntityID             string     `json:"entity_id"`
	EntityType           EntityType `json:"entity_type"`
	ProjectID            string     `json:"project_id"`
	Language             string     `json:"language"`
	Signature            string     `json:"signature"`
	SignatureEmbeddingID string     `json:"signature_embedding_id"`
	RootCauseCategory    RootCause  `json:"root_cause_category"`
	FixStrategy          string     `json:"fix_strategy"`
	WasUserCorrected     bool       `json:"was_user_corrected"`
	OccurrenceCount      int        `json:"occurrence_count"`
	LastOccurredAt       *time.Time `json:"last_occurred_at"`
	SourceType           SourceType `json:"source_type"`
	TrustLevel           TrustLevel `json:"trust_level"`
	Weight               float64    `json:"weight"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	ObsoletedAt          *time.Time `json:"obsoleted_at"`
	SourceEpisodicIDs    []uuid.UUID `json:"source_episodic_ids"`
	// Status is the Pitfall Workbench state machine (v0.2.2). Defaults to
	// proposed when empty. confirmed/高置信 proposed 可主动注入；suppressed
	// 是逃生阀（不再注入）；obsolete 对应实体已重构/修复，降权。
	Status PitfallStatus `json:"status"`
	// TimesShown counts how often active injection surfaced this pitfall;
	// TimesSuppressed counts user-suppress decisions. InterferenceRate =
	// TimesSuppressed / max(1, TimesShown) (EVOLUTION_PLAN v0.2.2).
	TimesShown       int `json:"times_shown"`
	TimesSuppressed  int `json:"times_suppressed"`
}

// PitfallStatus is the Pitfall Workbench lifecycle state.
type PitfallStatus string

const (
	PitfallProposed   PitfallStatus = "proposed"   // extracted, not yet confirmed
	PitfallConfirmed  PitfallStatus = "confirmed"  // user-approved or auto-promoted
	PitfallSuppressed PitfallStatus = "suppressed" // user invalidated — no injection
	PitfallObsolete   PitfallStatus = "obsolete"   // entity refactored/fixed — decayed
)

// Normalize maps an empty status to proposed; unknown statuses become proposed.
func (p PitfallStatus) Normalize() PitfallStatus {
	switch p {
	case PitfallProposed, PitfallConfirmed, PitfallSuppressed, PitfallObsolete:
		return p
	default:
		return PitfallProposed
	}
}

// Injectable reports whether active injection may surface this pitfall
// (confirmed or high-confidence proposed; never suppressed/obsolete).
func (p PitfallMemory) Injectable() bool {
	switch p.Status.Normalize() {
	case PitfallConfirmed:
		return true
	case PitfallProposed:
		// 高置信 proposed：多命中 + 高权重作为置信度代理。
		return p.OccurrenceCount >= 2 && p.Weight >= 0.6
	default:
		return false
	}
}

// InterferenceRate returns the share of injections the user suppressed.
func (p PitfallMemory) InterferenceRate() float64 {
	if p.TimesShown <= 0 {
		return 0
	}
	r := float64(p.TimesSuppressed) / float64(p.TimesShown)
	if r > 1 {
		return 1
	}
	return r
}

// EntityType classifies the code entity a Pitfall anchors on.
type EntityType string

const (
	EntityTypeFunction EntityType = "FUNCTION"
	EntityTypeModule   EntityType = "MODULE"
	EntityTypeAPI      EntityType = "API"
	EntityTypeConfig   EntityType = "CONFIG"
	EntityTypeQuery    EntityType = "QUERY"
)

// IsValid reports whether the EntityType is a known value.
func (et EntityType) IsValid() bool {
	switch et {
	case EntityTypeFunction, EntityTypeModule, EntityTypeAPI, EntityTypeConfig, EntityTypeQuery:
		return true
	}
	return false
}

// RootCause categorizes the root cause of a Pitfall.
type RootCause string

const (
	RootCauseConcurrency       RootCause = "CONCURRENCY"
	RootCauseResourceExhaustion RootCause = "RESOURCE_EXHAUSTION"
	RootCauseConfig            RootCause = "CONFIG"
	RootCauseContractViolation RootCause = "CONTRACT_VIOLATION"
	RootCauseLogicError        RootCause = "LOGIC_ERROR"
	RootCauseUnknown           RootCause = "UNKNOWN"
)

// IsValid reports whether the RootCause is a known value.
func (rc RootCause) IsValid() bool {
	switch rc {
	case RootCauseConcurrency, RootCauseResourceExhaustion, RootCauseConfig,
		RootCauseContractViolation, RootCauseLogicError, RootCauseUnknown:
		return true
	}
	return false
}

// Pitfall error sentinels.
var (
	ErrPitfallNotFound  = errors.New("pitfall not found")
	ErrPitfallDuplicate = errors.New("pitfall duplicate: same entity_id and signature")
)

// Pitfall edge types — properties structs for the relationships between
// PitfallMemory nodes and other memory types.
type PitfallDerivedFromEdge struct {
	RunID string `json:"run_id"`
}

type PitfallResolvedByEdge struct {
	Confidence float64 `json:"confidence"`
}

type PitfallCausesEdge struct {
	Confidence float64 `json:"confidence"`
}

type TriggeredPitfallEdge struct {
	Similarity float64 `json:"similarity"`
}

type HasPitfallEdge struct {
	Relevance float64 `json:"relevance"`
}
