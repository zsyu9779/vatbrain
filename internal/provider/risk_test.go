package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// TestServe_PrepareEditContext_ReturnsRisk is the v0.3 acceptance at the
// daemon level: given files + goal, returns relevant memory + top pitfall +
// risk score, and records the pitfall as shown (interference numerator).
func TestServe_PrepareEditContext_ReturnsRisk(t *testing.T) {
	deps, st := testDeps(t)
	s := NewServer(deps)

	ctx := context.Background()
	now := time.Now().UTC()

	// 已确认的中文 pitfall + 一条相关记忆
	require.NoError(t, st.WritePitfall(ctx, &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "clawfeed-push-v3.py",
		EntityType:        models.EntityTypeModule,
		ProjectID:         "coder",
		Signature:         "ClawFeed 推送必须用 v3.py，旧脚本以错误身份发送",
		RootCauseCategory: models.RootCauseConfig,
		FixStrategy:       "使用 clawfeed-push-v3.py --as bot",
		OccurrenceCount:   5,
		LastOccurredAt:    &now,
		TrustLevel:        models.TrustLevelMax,
		Weight:            1.0,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            models.PitfallConfirmed,
	}))
	require.NoError(t, st.WriteEpisodic(ctx, &models.EpisodicMemory{
		ID:         uuid.New(),
		ProjectID:  "coder",
		Summary:    "ClawFeed 推送身份问题排查",
		SourceType: models.SourceTypeUSER,
		TrustLevel: models.TrustLevelMax,
		CreatedAt:  now,
	}))

	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-risk", "agent_context": "primary", "agent_identity": "coder",
	}))

	resp := w.send(t, requestLine(t, 2, MethodPrepareEditContext, map[string]any{
		"session_id": "sess-risk",
		"files":      []string{"scripts/clawfeed-push-v3.py", "config/feedpush.conf"},
		"task_type":  "refactor",
		"user_goal":  "修复 ClawFeed 播报推送身份问题",
	}))
	require.Equal(t, "", resp.errorMessage())

	var out prepareEditContextResult
	require.NoError(t, json.Unmarshal(resp.Result, &out))
	assert.True(t, out.RiskScore > 0, "risk_score 应 > 0")
	assert.Contains(t, out.ReasonCodes, "recent_error")
	require.NotEmpty(t, out.Pitfalls)
	assert.Equal(t, "clawfeed-push-v3.py", out.Pitfalls[0].EntityID)
	require.NotEmpty(t, out.Memories)

	// 干扰率分子：pitfall 计入 shown
	pitfalls, err := st.SearchPitfall(ctx, store.PitfallSearchRequest{ProjectID: "coder"})
	require.NoError(t, err)
	require.Len(t, pitfalls, 1)
	assert.Equal(t, 1, pitfalls[0].TimesShown)

	w.send(t, requestLine(t, 3, MethodShutdown, map[string]any{}))
	<-done
}
