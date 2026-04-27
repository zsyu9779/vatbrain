package models

import (
	"time"

	"github.com/google/uuid"
)

// EpisodicMemory represents an (:EpisodicMemory) node in Neo4j.
// It stores "what happened when" — contextualized events, debug sessions, and
// interactions that pass through the significance gate into long-term memory.
type EpisodicMemory struct {
	ID                 uuid.UUID  `json:"id"`
	ProjectID          string     `json:"project_id"`
	Language           string     `json:"language"`
	TaskType           TaskType   `json:"task_type"`
	Summary            string     `json:"summary"`
	SourceType         SourceType `json:"source_type"`
	TrustLevel         TrustLevel `json:"trust_level"`
	Weight             float64    `json:"weight"`
	EffectiveFrequency float64    `json:"effective_frequency"`
	CreatedAt          time.Time  `json:"created_at"`
	LastAccessedAt     *time.Time `json:"last_accessed_at"`
	ObsoletedAt        *time.Time `json:"obsoleted_at"`
	EntityGroup        string     `json:"entity_group"`
	EmbeddingID        string     `json:"embedding_id"`
	ContextVector      []float32  `json:"context_vector,omitempty"`
	FullSnapshotURI    string     `json:"full_snapshot_uri"`
}

// PrecedesEdge represents a [:PRECEDES] relationship between episodic memories.
// It captures temporal ordering.
type PrecedesEdge struct {
	Strength float64 `json:"strength"`
}

// CausedByEdge represents a [:CAUSED_BY] relationship between episodic memories.
// It captures causal links (e.g. a refactor that was caused by a prior debug session).
type CausedByEdge struct {
	Confidence float64 `json:"confidence"`
}

// RelatesToEdge represents a [:RELATES_TO] relationship between episodic memories.
// Created during Link-on-Write to connect semantically or temporally related episodes.
type RelatesToEdge struct {
	Strength  float64           `json:"strength"`
	Dimension RelationDimension `json:"dimension"`
	CreatedAt time.Time         `json:"created_at"`
}

// InstantiatedEdge represents an [:INSTANTIATES] relationship from an episodic
// memory to a semantic memory. It has no properties — its presence is the signal.
type InstantiatedEdge struct{}
