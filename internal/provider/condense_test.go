package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatbrain/vatbrain/internal/models"
)

// --- EstimateTokens: 注入上下文的确定性 token 计量（效率轴测量点） ---

func TestEstimateTokens_Literals(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii short", "hello world", 3}, // 11 chars → ceil(11/4)
		{"ascii four", "aaaa", 1},         // 4 chars → 1 token
		{"ascii five", "aaaaa", 2},        // 5 chars → ceil(5/4)
		{"cjk", "你好世界", 4},                // 每 CJK 字符 1 token
		{"cjk punct", "。", 1},             // 全角标点在 CJK 区间
		{"mixed", "你好 hello", 4},          // 2 CJK + 6 other → 2 + ceil(6/4)
		{"latin single", "abc", 1},        // 3 chars → ceil(3/4)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, EstimateTokens(c.in))
		})
	}
}

// --- 近重复压制：rank 序保持（阈值 = 写路径 restatement 边界） ---

// 以下三条摘要的 Dice 手工核算：
//   - s1 与 s2（完全相同）→ Dice 1.0 → 抑制
//   - s1 与 s3（前缀扩展）→ s1 的 32 个 bigram 全部共享
//     Dice = 64/68 ≈ 0.94 → 抑制
//   - s1 与 s4（thinking vs text）→ 共享 23 bigram
//     Dice = 46/55 ≈ 0.84 < 0.9 → 保留
//     （细节不同的关键记忆绝不能被当作重复压制——
//     这正是"不损失 recall"的守门用例）
const (
	dupBase   = "use max_tokens 8000 for thinking"
	dupSame   = "use max_tokens 8000 for thinking"
	dupExtend = "use max_tokens 8000 for thinking now"
	dupDiff   = "use max_tokens 8000 for text"
	dupUnrltd = "the cafeteria closes at six on fridays"
)

func mkEp(summary string) models.EpisodicMemory {
	return models.EpisodicMemory{Summary: summary, Language: "en"}
}

func mkPit(entityID, signature string) models.PitfallMemory {
	now := time.Now()
	return models.PitfallMemory{
		EntityID:          entityID,
		Signature:         signature,
		RootCauseCategory: models.RootCauseConfig,
		TrustLevel:        4,
		LastOccurredAt:    &now,
	}
}

func TestCondenseEpisodes_SuppressesNearDuplicates(t *testing.T) {
	eps := []models.EpisodicMemory{
		mkEp(dupBase),
		mkEp(dupSame),
		mkEp(dupExtend),
		mkEp(dupDiff),
		mkEp(dupUnrltd),
	}
	opts := DefaultCondenseOptions()
	kept, suppressed := condenseEpisodes(eps, opts)
	require.Len(t, kept, 3)
	assert.Equal(t, 2, suppressed,
		"相同 + 前缀扩展各抑制一条，thinking/text 细节差异保留")
	assert.Equal(t, dupBase, kept[0].Summary, "最高序代表保留")
	assert.Equal(t, dupDiff, kept[1].Summary, "细节不同的记忆保留")
	assert.Equal(t, dupUnrltd, kept[2].Summary)
}

func TestCondenseEpisodes_OrderPreservedForDistinct(t *testing.T) {
	eps := []models.EpisodicMemory{
		mkEp("alpha service binds port 8080"),
		mkEp("beta worker drains queue every 30s"),
		mkEp("gamma scheduler retries with backoff"),
	}
	kept, suppressed := condenseEpisodes(eps, DefaultCondenseOptions())
	assert.Equal(t, 0, suppressed)
	require.Len(t, kept, 3)
	for i := range eps {
		assert.Equal(t, eps[i].Summary, kept[i].Summary, "rank 序不得被打乱")
	}
}

func TestCondensePitfalls_SuppressesNearDuplicates(t *testing.T) {
	base := "推送前未校验目标分支存在,merge 到不存在的分支报错"
	pits := []models.PitfallMemory{
		mkPit("clawfeed-push-v3.py", base),
		mkPit("clawfeed-push-v3.py", base+" 已在 v3 验证"),
		mkPit("clawfeed-push-v2.py", base), // 不同实体、相同签名 → 必须保留
		mkPit("other-tool.sh", "完全无关的另一个坑"),
	}
	kept, suppressed := condensePitfalls(pits, DefaultCondenseOptions())
	require.Len(t, kept, 3)
	assert.Equal(t, 1, suppressed)
	assert.Equal(t, base, kept[0].Signature)
	assert.Equal(t, "clawfeed-push-v2.py", kept[1].EntityID,
		"实体锚:不同实体的建议绝不因签名相同而折叠")
}

// --- FormatPrefetchCondensed：公共 seam ---

func TestFormatPrefetchCondensed_MatchesFormatPrefetchWithoutDuplicates(t *testing.T) {
	// 无重复 + 预算不触发 → 与旧 FormatPrefetch 逐字节一致（回归守卫：
	// 精简在常见路径上不改变注入内容）。
	now := time.Now()
	eps := []models.EpisodicMemory{
		mkEp("alpha service binds port 8080"),
		mkEp("beta worker drains queue"),
	}
	pits := []models.PitfallMemory{mkPit("clawfeed-push-v3.py", "必须用 v3.py 推送")}
	pits[0].RootCauseCategory = models.RootCauseConfig
	pits[0].FixStrategy = "用 --as bot"
	pits[0].TrustLevel = 4
	pits[0].LastOccurredAt = &now

	want := FormatPrefetch(eps, pits)
	got, stats := FormatPrefetchCondensed(eps, pits, DefaultCondenseOptions())
	assert.Equal(t, want, got)
	assert.Equal(t, 0, stats.SuppressedEpisodes)
	assert.Equal(t, 0, stats.SuppressedPitfalls)
	assert.False(t, stats.BudgetHit)
	assert.Equal(t, stats.InputTokens, stats.OutputTokens)
}

// controlledSummary 生成 40 ASCII 字符、内容彼此可区分的摘要
// （= 恰好 10 tokens，互扰 Dice ≈ 0.74 < 0.9 不会误伤抑制）
// ——预算测试的字面算术基础。
func controlledSummary(n int) string {
	tail := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}[n%6]
	pad := "shared prefix one two three "
	return pad + tail + " " + strings.Repeat("x", 40-len(pad)-len(tail)-1)
}

func TestFormatPrefetchCondensed_BudgetTrimsRankTail(t *testing.T) {
	// 5 条 episodes-only，字面算术（每条摘要 40 chars → 格式化行 52 chars → 13
	// tokens；表头 28 chars → 7 tokens；行间分隔 1 char → 1 token）：
	//   2 条 = 28 + 2×52 + 1 = 133 chars → 34 tokens ≤ 40
	//   3 条 = 28 + 3×52 + 2 = 186 chars → 47 tokens > 40
	// → 预算 40 恰好装下 rank 序前 2 条。
	eps := make([]models.EpisodicMemory, 5)
	for i := range eps {
		eps[i] = mkEp(controlledSummary(i))
	}

	opts := DefaultCondenseOptions()
	opts.MaxTokens = 40
	text, stats := FormatPrefetchCondensed(eps, nil, opts)

	assert.Equal(t, 5, stats.EpisodesIn)
	assert.Equal(t, 0, stats.SuppressedEpisodes, "不同内容不误伤")
	assert.Equal(t, 2, stats.EpisodesOut, "预算裁剪只丢 rank 尾部")
	assert.True(t, stats.BudgetHit)
	assert.Contains(t, text, eps[0].Summary, "top-1 保留")
	assert.Contains(t, text, eps[1].Summary)
	assert.NotContains(t, text, eps[2].Summary, "rank 尾部被裁")
}

func TestFormatPrefetchCondensed_BudgetKeepsTopOnePerList(t *testing.T) {
	// 极端预算下也不注入空上下文：每个非空列表至少保留 top-1
	// （软预算硬地板）。
	eps := []models.EpisodicMemory{mkEp(controlledSummary(0))}
	pits := []models.PitfallMemory{mkPit("entity-a.go", "pitfall signature one")}

	opts := DefaultCondenseOptions()
	opts.MaxTokens = 1
	text, stats := FormatPrefetchCondensed(eps, pits, opts)
	assert.True(t, stats.BudgetHit)
	assert.Equal(t, 1, stats.EpisodesOut)
	assert.Equal(t, 1, stats.PitfallsOut)
	assert.Contains(t, text, eps[0].Summary)
	assert.Contains(t, text, "entity-a.go")
}

// TestFormatPrefetchCondensed_Halving 是 ticket 08 的可复现减半证据：
// 评测场景中同一事实被反复写入（pattern-separation 追加路径保留
// restatement），检索 top-k 里满是近重复 → 注入前压制后 token 量减半以上，
// 同时所有独立信息点与风险提示全部保留。
func TestFormatPrefetchCondensed_Halving(t *testing.T) {
	// 每簇 6 个变体：代表 + 5 个 restatement
	// （同一事实被反复写入的评测形态）。
	clusterA := []string{
		"软路由 OpenClash 覆写脚本用 Ruby YAML 解析,不要用文本 gsub",
		"软路由 OpenClash 覆写脚本用 Ruby YAML 解析,不要用文本 gsub 已修复",
		"软路由 OpenClash 覆写脚本用 Ruby YAML 解析,不要用文本 gsub 备忘",
		"软路由 OpenClash 覆写脚本用 Ruby YAML 解析,不要用文本 gsub 确认",
		"软路由 OpenClash 覆写脚本用 Ruby YAML 解析,不要用文本 gsub 注意引擎版本",
		"软路由 OpenClash 覆写脚本用 Ruby YAML 解析,不要用文本 gsub 网关侧亦同",
	}
	bBase := "MiniMax M2.7 API 的 max_tokens 必须设到 8000 才能同时输出 thinking 和 text"
	clusterB := []string{
		bBase,
		bBase + " 否则截断",
		bBase + " 已验证",
		bBase + " 网关侧",
		bBase + " 注意请求体",
		bBase + " 生产确认",
	}
	clusterC := []string{
		"飞书机器人 cli_a9722632 是广播机器人,仅发播报",
		"飞书机器人 cli_a9722632 是广播机器人,仅发播报 不回复私聊",
		"飞书机器人 cli_a9722632 是广播机器人,仅发播报 需单独订阅",
		"飞书机器人 cli_a9722632 是广播机器人,仅发播报 已验证生产",
		"飞书机器人 cli_a9722632 是广播机器人,仅发播报 事件回调单独配置",
		"飞书机器人 cli_a9722632 是广播机器人,仅发播报 不处理私聊消息",
	}

	var eps []models.EpisodicMemory
	for _, s := range append(append(clusterA, clusterB...), clusterC...) {
		eps = append(eps, mkEp(s))
	}
	pits := []models.PitfallMemory{
		mkPit("clawfeed-push-v3.py",
			"推送前未校验目标分支存在,merge 到不存在的分支报错"),
		mkPit("openclash-config.yaml", "覆写脚本用文本 gsub 会破坏 YAML 结构"),
		mkPit("max-tokens", "max_tokens 低于 8000 时 thinking 与 text 只能二选一"),
	}

	text, stats := FormatPrefetchCondensed(eps, pits, DefaultCondenseOptions())

	require.Equal(t, 18, stats.EpisodesIn)
	assert.Equal(t, 3, stats.EpisodesOut, "每簇只注入最高序代表")
	assert.Equal(t, 15, stats.SuppressedEpisodes)
	assert.Equal(t, 3, stats.PitfallsOut)
	assert.False(t, stats.BudgetHit, "减半由近重复压制实现，不由预算裁剪实现")

	t.Logf("context condensation: in=%d tokens (%d eps + %d pits), "+
		"out=%d tokens (%d eps + %d pits), reduction=%.1f%%",
		stats.InputTokens, stats.EpisodesIn, stats.PitfallsIn,
		stats.OutputTokens, stats.EpisodesOut, stats.PitfallsOut,
		100*(1-float64(stats.OutputTokens)/float64(stats.InputTokens)))
	assert.Less(t, stats.OutputTokens, stats.InputTokens/2,
		"上下文 token 量必须减半以上：in=%d out=%d", stats.InputTokens, stats.OutputTokens)

	// 全部独立信息点都在：每簇代表 + 三个风险提示。
	assert.Contains(t, text, clusterA[0])
	assert.Contains(t, text, clusterB[0])
	assert.Contains(t, text, clusterC[0])
	assert.Contains(t, text, "clawfeed-push-v3.py")
	assert.Contains(t, text, "openclash-config.yaml")
	assert.Contains(t, text, "max-tokens")
	// 被抑制的 restatement 变体不再注入。
	assert.NotContains(t, text, "已修复")
	assert.NotContains(t, text, "网关侧")
	assert.NotContains(t, text, "已验证生产")
}

// TestFormatPrefetchCondensed_RrfOrderPreserved 是"更准 top-k"协同守卫：
// 精简只抑制/裁尾，绝不重排 RRF 融合后的顺序 —— top-1 恒在，
// 序列保持子序列不变。
func TestFormatPrefetchCondensed_RrfOrderPreserved(t *testing.T) {
	// 6 条独立信息，按 RRF 融合序（降序相关）给出。
	eps := []models.EpisodicMemory{
		mkEp("the alpha service binds port 8080 for http traffic"),
		mkEp("the beta worker drains its queue every thirty seconds"),
		mkEp("the gamma scheduler retries failed jobs with backoff"),
		mkEp("the delta proxy caches responses for five minutes"),
		mkEp("the epsilon logger rotates files at midnight each day"),
		mkEp("the zeta client times out after three seconds idle"),
	}
	pits := []models.PitfallMemory{mkPit("entity-a.go", "pitfall signature one")}

	// 无预算命中：顺序与输入完全一致。
	_, stats := FormatPrefetchCondensed(eps, pits, DefaultCondenseOptions())
	require.False(t, stats.BudgetHit)
	text, _ := FormatPrefetchCondensed(eps, pits, DefaultCondenseOptions())
	prev := -1
	for i, ep := range eps {
		idx := strings.Index(text, ep.Summary)
		require.GreaterOrEqual(t, idx, 0, "rank %d 的记忆必须注入", i)
		require.Greater(t, idx, prev, "注入顺序保持 RRF 序（子序列不变）")
		prev = idx
	}

	// 预算命中：保留的是输入前缀（rank 序头部），且 top-1 恒在。
	// 字面算术（摘要 ~49 chars → 行 61 chars → 16 tokens，表头 7）：
	// 3 条 = 57 ≤ 64，4 条 = 74 > 64 → 预算 64 恰好装下前 3 条。
	opts := DefaultCondenseOptions()
	opts.MaxTokens = 64
	text2, stats2 := FormatPrefetchCondensed(eps, nil, opts)
	assert.True(t, stats2.BudgetHit)
	assert.Equal(t, 3, stats2.EpisodesOut)
	assert.Contains(t, text2, eps[0].Summary, "top-1 恒在")
	assert.Contains(t, text2, eps[1].Summary)
	assert.Contains(t, text2, eps[2].Summary)
	assert.NotContains(t, text2, eps[3].Summary, "裁掉的只是 rank 尾部")
}

// TestServe_Prefetch_CondensesNearDuplicates 是接线级证据：两条相同的记忆在库
// （pattern-separation 追加路径允许 restatement 共存），prefetch 真实注入路径
// 上只出现一次 —— 精简不在测试缝里空转，而是在线生效。
func TestServe_Prefetch_CondensesNearDuplicates(t *testing.T) {
	deps, _ := testDeps(t)
	summary := "软路由 OpenClash 覆写脚本用 Ruby YAML 解析，不要用文本 gsub"
	seedEpisodic(t, deps, "coder", summary)
	seedEpisodic(t, deps, "coder", summary) // restatement → 第二条独立记忆

	s := NewServer(deps)
	r, w := pipeLines(t)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), r, w, 1<<20) }()

	w.send(t, requestLine(t, 1, MethodInitialize, map[string]any{
		"session_id": "sess-cond", "agent_context": "primary", "agent_identity": "coder",
	}))
	resp := w.send(t, requestLine(t, 2, MethodPrefetch, map[string]any{
		"session_id": "sess-cond", "query": "OpenClash 覆写脚本 Ruby YAML 怎么解析",
	}))
	require.Equal(t, "", resp.errorMessage())
	var out prefetchResult
	json.Unmarshal(resp.Result, &out)
	assert.Equal(t, 1, strings.Count(out.Context, "不要用文本 gsub"),
		"两条 restatement 只注入一条")
	assert.Contains(t, out.Context, "[vatbrain memory context]")

	// 无关查询 → 空上下文（既有行为不变）。
	resp = w.send(t, requestLine(t, 3, MethodPrefetch, map[string]any{
		"session_id": "sess-cond", "query": "量子计算 引力波 黑洞 弦论",
	}))
	json.Unmarshal(resp.Result, &out)
	assert.Equal(t, "", out.Context)

	w.send(t, requestLine(t, 4, MethodShutdown, map[string]any{}))
	<-done
}
