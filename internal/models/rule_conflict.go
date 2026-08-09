package models

import (
	"time"

	"github.com/google/uuid"
)

// ConflictStatus tracks the lifecycle of a rule-conflict record.
type ConflictStatus string

const (
	// ConflictPending means the two rules carry equal trust and a human must
	// adjudicate which one wins.
	ConflictPending ConflictStatus = "pending"
	// ConflictAutoResolved means the higher-trust rule overrode the lower one.
	ConflictAutoResolved ConflictStatus = "auto_resolved"
	// ConflictManualResolved means a human picked the winning rule.
	ConflictManualResolved ConflictStatus = "manual_resolved"
	// ConflictDismissed means the pair was judged not to be a real conflict.
	ConflictDismissed ConflictStatus = "dismissed"
)

// IsValid reports whether s is a known conflict status.
func (s ConflictStatus) IsValid() bool {
	switch s {
	case ConflictPending, ConflictAutoResolved, ConflictManualResolved, ConflictDismissed:
		return true
	}
	return false
}

// ConflictBasis records how a conflict was detected.
type ConflictBasis string

const (
	// ConflictBasisPolarity marks the deterministic lexical heuristic: two
	// active rules about the same subject carry opposite directives
	// (prohibitive vs affirmative).
	ConflictBasisPolarity ConflictBasis = "polarity"
	// ConflictBasisLLM is reserved for an LLM contradiction judge.
	ConflictBasisLLM ConflictBasis = "llm"
)

// RuleConflict records a detected contradiction between two semantic rules.
// Resolution follows §11 source grading: higher trust wins, equal trust is
// deferred to a human via the Workbench tools.
type RuleConflict struct {
	ID          uuid.UUID
	RuleAID     uuid.UUID
	RuleBID     uuid.UUID
	EntityGroup string
	Basis       ConflictBasis
	Status      ConflictStatus
	// Resolution holds the winning rule ID after resolution (or a free-form
	// note for dismissed conflicts).
	Resolution string
	// Reason is the human-readable explanation of the conflict.
	Reason     string
	CreatedAt  time.Time
	ResolvedAt *time.Time
}
