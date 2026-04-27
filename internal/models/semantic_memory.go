package models

import (
	"time"

	"github.com/google/uuid"
)

// SemanticMemory represents a (:SemanticMemory) node in Neo4j.
// It stores distilled knowledge: rules, facts, patterns, and constraints
// extracted from episodic memories by the sleep consolidation engine.
type SemanticMemory struct {
	ID                 uuid.UUID   `json:"id"`
	Type               MemoryType  `json:"type"`
	Content            string      `json:"content"`
	SourceType         SourceType  `json:"source_type"`
	TrustLevel         TrustLevel  `json:"trust_level"`
	Weight             float64     `json:"weight"`
	EffectiveFrequency float64     `json:"effective_frequency"`
	CreatedAt          time.Time   `json:"created_at"`
	LastAccessedAt     *time.Time  `json:"last_accessed_at"`
	ObsoletedAt        *time.Time  `json:"obsoleted_at"`
	EntityGroup        string      `json:"entity_group"`
	ConsolidationRunID string      `json:"consolidation_run_id"`
	BacktestAccuracy   float64     `json:"backtest_accuracy"`
	SourceEpisodicIDs  []uuid.UUID `json:"source_episodic_ids"`
}

// DependsOnEdge represents a [:DEPENDS_ON] relationship between semantic memories.
// It captures knowledge dependency (e.g. a rule that relies on another rule being true).
type DependsOnEdge struct {
	RelationType string `json:"relation_type"`
}

// ConflictsWithEdge represents a [:CONFLICTS_WITH] relationship between semantic memories.
// Created when different consolidation runs produce contradictory rules.
type ConflictsWithEdge struct {
	Resolution string `json:"resolution"`
}

// SupersededEdge represents a [:SUPERSEDED] relationship between semantic memories.
// Marks that a newer version of a rule replaced an older one within the same entity_group.
type SupersededEdge struct {
	At time.Time `json:"at"`
}

// DerivedFromEdge represents a [:DERIVED_FROM] relationship from a semantic memory
// back to the episodic memories it was extracted from.
// This traceability chain is the safety valve — every semantic rule must be traceable
// to its sources so corrections can propagate back.
type DerivedFromEdge struct {
	RunID string `json:"run_id"`
}
