package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store/memory"
)

func TestDetectPolarity(t *testing.T) {
	p, ok := DetectPolarity("Redis MaxOpenConns 不要设为 100")
	assert.True(t, ok)
	assert.Equal(t, PolarityProhibitive, p)

	p, ok = DetectPolarity("数据库连接池 应该按负载调整大小")
	assert.True(t, ok)
	assert.Equal(t, PolarityAffirmative, p)

	p, ok = DetectPolarity("并发问题通常出在锁粒度")
	assert.False(t, ok)
	assert.Equal(t, PolarityNeutral, p)

	// Prohibitive wins over affirmative inside the same rule.
	p, ok = DetectPolarity("不要使用文本 gsub 覆写脚本")
	assert.True(t, ok)
	assert.Equal(t, PolarityProhibitive, p)

	// Case-insensitive Latin.
	p, ok = DetectPolarity("Don't use global state")
	assert.True(t, ok)
	assert.Equal(t, PolarityProhibitive, p)
}

func testSemanticRule(id uuid.UUID, project, content string, trust models.TrustLevel) models.SemanticMemory {
	return models.SemanticMemory{
		ID:          id,
		Type:        models.MemoryTypeRule,
		Content:     content,
		SourceType:  models.SourceTypeINFERRED,
		TrustLevel:  trust,
		Weight:      1.0,
		EntityGroup: project,
		CreatedAt:   time.Now().UTC(),
	}
}

func TestConflictDetect_SameSubjectOppositePolarity(t *testing.T) {
	d := DefaultConflictDetector()
	rules := []models.SemanticMemory{
		testSemanticRule(uuid.New(), "proj", "Redis MaxOpenConns 应该设为 100", 3),
		testSemanticRule(uuid.New(), "proj", "Redis MaxOpenConns 不要设为 100", 3),
	}
	pairs := d.Detect(rules)
	require.Len(t, pairs, 1)
	assert.Contains(t, pairs[0].Reason, "trust")
}

func TestConflictDetect_NoConflictSamePolarity(t *testing.T) {
	d := DefaultConflictDetector()
	rules := []models.SemanticMemory{
		testSemanticRule(uuid.New(), "proj", "Redis MaxOpenConns 应该设为 100", 3),
		testSemanticRule(uuid.New(), "proj", "Redis MaxOpenConns 应该设为 200", 3), // same polarity
	}
	assert.Empty(t, d.Detect(rules))
}

func TestConflictDetect_NoConflictDifferentSubject(t *testing.T) {
	d := DefaultConflictDetector()
	rules := []models.SemanticMemory{
		testSemanticRule(uuid.New(), "proj", "Redis MaxOpenConns 应该设为 100", 3),
		testSemanticRule(uuid.New(), "proj", "MongoDB 索引不要建太多", 3), // prohibitive, different subject
	}
	assert.Empty(t, d.Detect(rules))
}

func TestConflictDetect_ObsoleteRulesExcluded(t *testing.T) {
	d := DefaultConflictDetector()
	now := time.Now()
	obsolete := testSemanticRule(uuid.New(), "proj", "Redis MaxOpenConns 不要设为 100", 3)
	obsolete.ObsoletedAt = &now
	rules := []models.SemanticMemory{
		testSemanticRule(uuid.New(), "proj", "Redis MaxOpenConns 应该设为 100", 3),
		obsolete,
	}
	assert.Empty(t, d.Detect(rules))
}

// TestRunRuleConflictDetection is the end-to-end acceptance: higher trust
// auto-resolves (loser retired), equal trust stays pending, and a second run
// is idempotent.
func TestRunRuleConflictDetection(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	project := "proj"

	high := testSemanticRule(uuid.New(), project, "Redis MaxOpenConns 应该设为 100", models.TrustLevelMax)
	low := testSemanticRule(uuid.New(), project, "Redis MaxOpenConns 不要设为 100", models.TrustLevelMin)
	equalA := testSemanticRule(uuid.New(), project, "日志级别 应该用 INFO", 3)
	equalB := testSemanticRule(uuid.New(), project, "日志级别 不要用 INFO", 3)
	for _, r := range []models.SemanticMemory{high, low, equalA, equalB} {
		require.NoError(t, s.WriteSemantic(ctx, &r))
	}

	res, err := RunRuleConflictDetection(ctx, s, project)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Detected)
	assert.Equal(t, 1, res.AutoResolved)
	assert.Equal(t, 1, res.Pending)

	// The low-trust rule was retired; the high-trust one survived.
	l, err := s.GetSemantic(ctx, low.ID)
	require.NoError(t, err)
	require.NotNil(t, l.ObsoletedAt, "low-trust losing rule must be retired")
	h, err := s.GetSemantic(ctx, high.ID)
	require.NoError(t, err)
	assert.Nil(t, h.ObsoletedAt, "high-trust winning rule survives")

	// Equal-trust pair remains active (pending adjudication).
	ea, err := s.GetSemantic(ctx, equalA.ID)
	require.NoError(t, err)
	assert.Nil(t, ea.ObsoletedAt)

	// Idempotency: a second run detects nothing new.
	res2, err := RunRuleConflictDetection(ctx, s, project)
	require.NoError(t, err)
	assert.Equal(t, 0, res2.Detected)
}

func TestResolveManually(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()

	a := testSemanticRule(uuid.New(), "proj", "日志级别 应该用 INFO", 3)
	b := testSemanticRule(uuid.New(), "proj", "日志级别 不要用 INFO", 3)
	require.NoError(t, s.WriteSemantic(ctx, &a))
	require.NoError(t, s.WriteSemantic(ctx, &b))

	conflict := &models.RuleConflict{
		ID:        uuid.New(),
		RuleAID:   a.ID,
		RuleBID:   b.ID,
		Status:    models.ConflictPending,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.SaveRuleConflict(ctx, conflict))

	// Manual adjudication: rule A wins → B retired.
	require.NoError(t, ResolveManually(ctx, s, conflict, a.ID, "用户明确说用 INFO"))

	badWinner := uuid.New()
	err := ResolveManually(ctx, s, conflict, badWinner, "")
	assert.Error(t, err, "a winner outside the pair must be rejected")
}
