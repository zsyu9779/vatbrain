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
