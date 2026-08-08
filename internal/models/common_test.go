package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceType_IsValid(t *testing.T) {
	assert.True(t, SourceTypeAST.IsValid())
	assert.True(t, SourceTypeLLM.IsValid())
	assert.True(t, SourceTypeUSER.IsValid())
	assert.True(t, SourceTypeDEBUG.IsValid())
	assert.True(t, SourceTypeINFERRED.IsValid())
	assert.True(t, SourceTypeUserDeclared.IsValid())
	assert.True(t, SourceTypeSummarized.IsValid())
	assert.False(t, SourceType("").IsValid())
	assert.False(t, SourceType("UNKNOWN").IsValid())
	assert.False(t, SourceType("random").IsValid())
}

func TestSourceType_IsEpisodicSource(t *testing.T) {
	assert.True(t, SourceTypeAST.IsEpisodicSource())
	assert.True(t, SourceTypeLLM.IsEpisodicSource())
	assert.True(t, SourceTypeUSER.IsEpisodicSource())
	assert.True(t, SourceTypeDEBUG.IsEpisodicSource())
	assert.False(t, SourceTypeINFERRED.IsEpisodicSource())
	assert.False(t, SourceTypeUserDeclared.IsEpisodicSource())
	assert.False(t, SourceTypeSummarized.IsEpisodicSource())
	assert.False(t, SourceType("").IsEpisodicSource())
}

func TestSourceType_IsSemanticSource(t *testing.T) {
	assert.True(t, SourceTypeINFERRED.IsSemanticSource())
	assert.True(t, SourceTypeUserDeclared.IsSemanticSource())
	assert.True(t, SourceTypeSummarized.IsSemanticSource())
	assert.False(t, SourceTypeAST.IsSemanticSource())
	assert.False(t, SourceTypeLLM.IsSemanticSource())
	assert.False(t, SourceTypeUSER.IsSemanticSource())
	assert.False(t, SourceTypeDEBUG.IsSemanticSource())
	assert.False(t, SourceType("").IsSemanticSource())
}

func TestTaskType_IsValid(t *testing.T) {
	assert.True(t, TaskTypeDebug.IsValid())
	assert.True(t, TaskTypeFeature.IsValid())
	assert.True(t, TaskTypeRefactor.IsValid())
	assert.True(t, TaskTypeReview.IsValid())
	assert.False(t, TaskType("").IsValid())
	assert.False(t, TaskType("unknown").IsValid())
}

func TestMemoryType_IsValid(t *testing.T) {
	assert.True(t, MemoryTypeRule.IsValid())
	assert.True(t, MemoryTypeFact.IsValid())
	assert.True(t, MemoryTypePattern.IsValid())
	assert.True(t, MemoryTypeConstraint.IsValid())
	assert.False(t, MemoryType("").IsValid())
	assert.False(t, MemoryType("UNKNOWN").IsValid())
}

func TestTrustLevel_IsValid(t *testing.T) {
	assert.True(t, TrustLevel(1).IsValid())
	assert.True(t, TrustLevel(3).IsValid())
	assert.True(t, TrustLevel(5).IsValid())
	assert.False(t, TrustLevel(0).IsValid())
	assert.False(t, TrustLevel(6).IsValid())
	assert.False(t, TrustLevel(-1).IsValid())
	assert.False(t, TrustLevel(100).IsValid())
}

func TestSearchAction_IsValid(t *testing.T) {
	assert.True(t, SearchActionUsed.IsValid())
	assert.True(t, SearchActionCorrected.IsValid())
	assert.True(t, SearchActionIgnored.IsValid())
	assert.True(t, SearchActionConfirmed.IsValid())
	assert.False(t, SearchAction("").IsValid())
	assert.False(t, SearchAction("clicked").IsValid())
}

func TestDefaultTrustLevelForSource(t *testing.T) {
	assert.Equal(t, TrustLevel(5), DefaultTrustLevelForSource(SourceTypeUSER))
	assert.Equal(t, TrustLevel(5), DefaultTrustLevelForSource(SourceTypeUserDeclared))
	assert.Equal(t, TrustLevel(4), DefaultTrustLevelForSource(SourceTypeAST))
	assert.Equal(t, TrustLevel(3), DefaultTrustLevelForSource(SourceTypeDEBUG))
	assert.Equal(t, TrustLevel(2), DefaultTrustLevelForSource(SourceTypeINFERRED))
	assert.Equal(t, TrustLevel(2), DefaultTrustLevelForSource(SourceTypeSummarized))
	assert.Equal(t, TrustLevel(1), DefaultTrustLevelForSource(SourceTypeLLM))
	assert.Equal(t, DefaultTrustLevel, DefaultTrustLevelForSource(SourceType("")))
	assert.Equal(t, DefaultTrustLevel, DefaultTrustLevelForSource(SourceType("UNKNOWN")))
}
