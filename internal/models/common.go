// Package models defines data types for VatBrain's memory system.
//
// Types map 1:1 to Neo4j nodes/edges, pgvector tables, and the HTTP API surface.
// Every exported type has a Go doc comment per project conventions.
package models

// SourceType classifies the provenance of a memory.
type SourceType string

const (
	SourceTypeAST           SourceType = "AST"
	SourceTypeLLM           SourceType = "LLM"
	SourceTypeUSER          SourceType = "USER"
	SourceTypeDEBUG         SourceType = "DEBUG"
	SourceTypeINFERRED      SourceType = "INFERRED"
	SourceTypeUserDeclared  SourceType = "USER_DECLARED"
	SourceTypeSummarized    SourceType = "SUMMARIZED"
)

// IsValid reports whether s is a known source type.
func (s SourceType) IsValid() bool {
	switch s {
	case SourceTypeAST, SourceTypeLLM, SourceTypeUSER, SourceTypeDEBUG,
		SourceTypeINFERRED, SourceTypeUserDeclared, SourceTypeSummarized:
		return true
	}
	return false
}

// IsEpisodicSource reports whether s is used for episodic memory sources.
func (s SourceType) IsEpisodicSource() bool {
	switch s {
	case SourceTypeAST, SourceTypeLLM, SourceTypeUSER, SourceTypeDEBUG:
		return true
	}
	return false
}

// IsSemanticSource reports whether s is used for semantic memory sources.
func (s SourceType) IsSemanticSource() bool {
	switch s {
	case SourceTypeINFERRED, SourceTypeUserDeclared, SourceTypeSummarized:
		return true
	}
	return false
}

// TaskType classifies the engineering task that produced a memory.
type TaskType string

const (
	TaskTypeDebug    TaskType = "debug"
	TaskTypeFeature  TaskType = "feature"
	TaskTypeRefactor TaskType = "refactor"
	TaskTypeReview   TaskType = "review"
)

// IsValid reports whether t is a known task type.
func (t TaskType) IsValid() bool {
	switch t {
	case TaskTypeDebug, TaskTypeFeature, TaskTypeRefactor, TaskTypeReview:
		return true
	}
	return false
}

// MemoryType classifies a SemanticMemory node.
type MemoryType string

const (
	MemoryTypeRule       MemoryType = "RULE"
	MemoryTypeFact       MemoryType = "FACT"
	MemoryTypePattern    MemoryType = "PATTERN"
	MemoryTypeConstraint MemoryType = "CONSTRAINT"
)

// IsValid reports whether m is a known memory type.
func (m MemoryType) IsValid() bool {
	switch m {
	case MemoryTypeRule, MemoryTypeFact, MemoryTypePattern, MemoryTypeConstraint:
		return true
	}
	return false
}

// TrustLevel rates source credibility on a 1-5 scale.
type TrustLevel int

const (
	TrustLevelMin     TrustLevel = 1
	TrustLevelMax     TrustLevel = 5
	DefaultTrustLevel TrustLevel = 3
)

// IsValid reports whether t is within the valid trust range.
func (t TrustLevel) IsValid() bool {
	return t >= TrustLevelMin && t <= TrustLevelMax
}

// SearchAction records what the user did with a retrieved memory.
type SearchAction string

const (
	SearchActionUsed      SearchAction = "used"
	SearchActionCorrected SearchAction = "corrected"
	SearchActionIgnored   SearchAction = "ignored"
	SearchActionConfirmed SearchAction = "confirmed"
)

// IsValid reports whether a is a known search action.
func (a SearchAction) IsValid() bool {
	switch a {
	case SearchActionUsed, SearchActionCorrected, SearchActionIgnored, SearchActionConfirmed:
		return true
	}
	return false
}

// MergeAction describes the result of a write gate decision.
type MergeAction string

const (
	MergeActionCreatedNew      MergeAction = "created_new"
	MergeActionUpdatedExisting MergeAction = "updated_existing"
)

// GateReason records why the significance gate passed for a write.
type GateReason string

const (
	GateReasonUserConfirmed         GateReason = "user_confirmed"
	GateReasonCrossCyclePersistence GateReason = "cross_cycle_persistence"
	GateReasonPredictionError       GateReason = "prediction_error"
	GateReasonSubsequentReference   GateReason = "subsequent_reference"
)

// RelationDimension classifies a RELATES_TO edge between episodic memories.
type RelationDimension string

const (
	DimensionSemantic RelationDimension = "SEMANTIC"
	DimensionTemporal RelationDimension = "TEMPORAL"
	DimensionCausal   RelationDimension = "CAUSAL"
)

// Default weight and threshold constants.
const (
	DefaultWeight      float64 = 1.0
	CoolingThreshold   float64 = 0.01
	DefaultEmbeddingDim int    = 1536
)
