package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEntityType_IsValid(t *testing.T) {
	assert.True(t, EntityTypeFunction.IsValid())
	assert.True(t, EntityTypeModule.IsValid())
	assert.True(t, EntityTypeAPI.IsValid())
	assert.True(t, EntityTypeConfig.IsValid())
	assert.True(t, EntityTypeQuery.IsValid())
	assert.False(t, EntityType("").IsValid())
	assert.False(t, EntityType("CLASS").IsValid())
	assert.False(t, EntityType("unknown").IsValid())
}

func TestRootCause_IsValid(t *testing.T) {
	assert.True(t, RootCauseConcurrency.IsValid())
	assert.True(t, RootCauseResourceExhaustion.IsValid())
	assert.True(t, RootCauseConfig.IsValid())
	assert.True(t, RootCauseContractViolation.IsValid())
	assert.True(t, RootCauseLogicError.IsValid())
	assert.True(t, RootCauseUnknown.IsValid())
	assert.False(t, RootCause("").IsValid())
	assert.False(t, RootCause("MEMORY_LEAK").IsValid())
	assert.False(t, RootCause("random").IsValid())
}

func TestPitfallErrorSentinels(t *testing.T) {
	assert.EqualError(t, ErrPitfallNotFound, "pitfall not found")
	assert.EqualError(t, ErrPitfallDuplicate, "pitfall duplicate: same entity_id and signature")
	assert.NotEqual(t, ErrPitfallNotFound, ErrPitfallDuplicate)
}
