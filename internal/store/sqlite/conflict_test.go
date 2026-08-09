package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/models"
)

func TestRuleConflict_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	c := &models.RuleConflict{
		ID:          uuid.New(),
		RuleAID:     uuid.New(),
		RuleBID:     uuid.New(),
		EntityGroup: "proj",
		Basis:       models.ConflictBasisPolarity,
		Status:      models.ConflictPending,
		Reason:      "opposite directives on Redis MaxOpenConns",
		CreatedAt:   time.Now().UTC(),
	}
	require.NoError(t, s.SaveRuleConflict(ctx, c))

	got, err := s.ListRuleConflicts(ctx, "", 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, c.ID, got[0].ID)
	assert.Equal(t, c.RuleAID, got[0].RuleAID)
	assert.Equal(t, models.ConflictPending, got[0].Status)

	// Filter by status.
	empty, err := s.ListRuleConflicts(ctx, string(models.ConflictAutoResolved), 20)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestRuleConflict_Resolve(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ruleA := makeSemanticForConflict(t, s, "Redis MaxOpenConns 应该设为 100")
	ruleB := makeSemanticForConflict(t, s, "Redis MaxOpenConns 不要设为 100")
	c := &models.RuleConflict{
		ID:        uuid.New(),
		RuleAID:   ruleA.ID,
		RuleBID:   ruleB.ID,
		Status:    models.ConflictPending,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.SaveRuleConflict(ctx, c))

	now := time.Now().UTC()
	require.NoError(t, s.ResolveRuleConflict(ctx, c.ID, models.ConflictAutoResolved, ruleA.ID, "trust 5 > 1", now))

	// The losing rule is retired.
	require.NoError(t, s.MarkSemanticObsolete(ctx, ruleB.ID, now))
	rb, err := s.GetSemantic(ctx, ruleB.ID)
	require.NoError(t, err)
	require.NotNil(t, rb.ObsoletedAt)

	got, err := s.ListRuleConflicts(ctx, string(models.ConflictAutoResolved), 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, ruleA.ID.String(), got[0].Resolution)
	assert.Equal(t, models.ConflictAutoResolved, got[0].Status)
}

func makeSemanticForConflict(t *testing.T, s *Store, content string) *models.SemanticMemory {
	t.Helper()
	m := &models.SemanticMemory{
		ID:          uuid.New(),
		Type:        models.MemoryTypeRule,
		Content:     content,
		SourceType:  models.SourceTypeINFERRED,
		TrustLevel:  3,
		Weight:      1.0,
		EntityGroup: "proj",
		CreatedAt:   time.Now().UTC(),
	}
	require.NoError(t, s.WriteSemantic(context.Background(), m))
	return m
}
