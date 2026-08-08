package provider

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

func seedEpisodic(t *testing.T, deps core.WriteDeps, projectID, summary string) {
	t.Helper()
	mem := &models.EpisodicMemory{
		ID:                 uuid.New(),
		ProjectID:          projectID,
		Language:           "zh",
		TaskType:           models.TaskTypeDebug,
		Summary:            summary,
		SourceType:         models.SourceTypeUSER,
		TrustLevel:         models.DefaultTrustLevel,
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		CreatedAt:          time.Now(),
	}
	require.NoError(t, deps.Store.WriteEpisodic(context.Background(), mem))
}

func TestRetrieveEpisodic_Lexical_Chinese(t *testing.T) {
	deps, _ := testDeps(t)
	seedEpisodic(t, deps, "coder",
		"软路由 OpenClash 覆写脚本用 Ruby YAML 解析，不要用文本 gsub")
	seedEpisodic(t, deps, "coder",
		"MiniMax M2.7 API：max_tokens 必须设到 8000 才能同时输出 thinking 和 text")
	seedEpisodic(t, deps, "other",
		"飞书机器人 cli_a9722632 是广播机器人，仅发播报")

	// 中文查询命中软路由条目（词法 bigram 重叠，stub embedder 零向量 → 走词法路径）
	res, err := RetrieveEpisodic(context.Background(), deps, "coder", "OpenClash 覆写脚本 Ruby YAML 解析", 5)
	require.NoError(t, err)
	require.NotEmpty(t, res)
	assert.Contains(t, res[0].Summary, "OpenClash")
}

func TestRetrieveEpisodic_ProjectScoped(t *testing.T) {
	deps, _ := testDeps(t)
	seedEpisodic(t, deps, "proj-a", "Alpha 项目的排障记录")
	seedEpisodic(t, deps, "proj-b", "Alpha 项目的排障记录")

	res, err := RetrieveEpisodic(context.Background(), deps, "proj-a", "Alpha 项目 排障 记录", 5)
	require.NoError(t, err)
	require.NotEmpty(t, res)
	for _, r := range res {
		assert.Equal(t, "proj-a", r.ProjectID)
	}
}

func TestRetrieveEpisodic_NoMatch(t *testing.T) {
	deps, _ := testDeps(t)
	seedEpisodic(t, deps, "coder", "完全无关的厨房菜谱内容")

	res, err := RetrieveEpisodic(context.Background(), deps, "coder", "量子计算 引力波 黑洞 弦论", 5)
	require.NoError(t, err)
	assert.Empty(t, res)
}

func TestRetrievePitfalls_EntityBoost(t *testing.T) {
	deps, st := testDeps(t)
	now := time.Now()
	require.NoError(t, st.WritePitfall(context.Background(), &models.PitfallMemory{
		ID:                uuid.New(),
		EntityID:          "clawfeed-push-v3.py",
		EntityType:        models.EntityTypeModule,
		ProjectID:         "coder",
		Signature:         "clawfeed 推送必须用 v3.py，旧脚本会发错身份",
		RootCauseCategory: models.RootCauseConfig,
		FixStrategy:       "使用 clawfeed-push-v3.py --as bot",
		TrustLevel:        models.DefaultTrustLevel,
		Weight:            1.0,
		OccurrenceCount:   3,
		LastOccurredAt:    &now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}))

	res, err := RetrievePitfalls(context.Background(), deps, "coder", "ClawFeed 推送应该用哪个脚本", 3)
	require.NoError(t, err)
	require.NotEmpty(t, res)
	assert.Equal(t, "clawfeed-push-v3.py", res[0].EntityID)
}

func TestFormatPrefetch_IncludesRiskAdvisory(t *testing.T) {
	now := time.Now()
	ep := models.EpisodicMemory{
		Summary: "软路由 OpenClash 调试记录\n跨行内容",
		Language: "zh",
	}
	pit := models.PitfallMemory{
		EntityID:          "clawfeed-push-v3.py",
		Signature:         "必须用 v3.py 推送",
		RootCauseCategory: models.RootCauseConfig,
		FixStrategy:       "用 --as bot",
		TrustLevel:        4,
		LastOccurredAt:    &now,
	}

	text := FormatPrefetch([]models.EpisodicMemory{ep}, []models.PitfallMemory{pit})
	assert.Contains(t, text, "[vatbrain memory context]")
	assert.Contains(t, text, "软路由 OpenClash 调试记录 跨行内容") // 单行化
	assert.Contains(t, text, "[Risk advisory — vatbrain]")
	assert.Contains(t, text, "clawfeed-push-v3.py")
	assert.Contains(t, text, "root cause: CONFIG")
	assert.Contains(t, text, "曾于 "+now.Format("2006-01-02")+" 发生")
	assert.Contains(t, text, "Fix: 用 --as bot")
}

func TestFormatPrefetch_Empty(t *testing.T) {
	assert.Empty(t, FormatPrefetch(nil, nil))
}

func TestBigramOverlap_Chinese(t *testing.T) {
	// 相同文本重叠 1.0
	assert.InDelta(t, 1.0, bigramOverlap("软路由调试", "软路由调试"), 1e-9)
	// 无关文本接近 0
	assert.Less(t, bigramOverlap("量子引力", "菜谱烹饪"), 0.1)
	// 部分重叠 0 < s < 1
	s := bigramOverlap("OpenClash 调试", "OpenClash 配置")
	assert.True(t, s > 0 && s < 1, "partial overlap should be in (0,1), got %v", s)
}
