package provider

import (
	"strings"

	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
)

// 注入侧 Context 精简（ticket 08，效率轴）。
//
// 检索注入的上下文从"10K+ tokens/题"减半以上：注入前压制近重复记忆
// （restatement）、按 token 预算裁剪 rank 尾部——与 ticket 04 的 RRF 融合
// 排序协同，不另起炉灶。精简只作用于注入面（FormatPrefetchCondensed），
// 不改检索 API、不动存储；被抑制的不是记忆（storage 语义不变），只是
// 本次注入中信息冗余的副本。
//
// 与既有写路径语义对齐（不冲突）：
//   - pattern-separation append 与 reconsolidation 在写侧保留 restatement
//     （RestatementSimilarity 0.9）——同样的边界在这里用于注入侧：
//     Dice ≥ 0.9 的记忆在召回时信息冗余，压制不损失任何独立信息点；
//   - update tracking 覆盖的旧记忆在检索时已被 obsoleted_at 排除，
//     到不了注入面；
//   - 细节差异（Dice < 0.9，如 "for thinking" vs "for text"）绝不压制
//     ——唯一的例外是写路径同款的 restatement 子串包含规则（长摘要包含
//     短摘要全文 = 信息严格冗余）。"不损失 recall 质量"的守门线。

// CondenseOptions tunes the injection-side context condensation.
type CondenseOptions struct {
	// MaxTokens is the injection token budget measured by EstimateTokens on
	// the formatted context. 0 or negative disables the budget. The budget is
	// a soft cap with a hard floor: the top-1 episode and top-1 pitfall of a
	// non-empty list always inject, so condensation never yields an empty
	// context for a non-empty retrieval.
	MaxTokens int
	// DuplicateSimilarity is the character-bigram Dice coefficient at or
	// above which two memories count as redundant restatements at recall —
	// the same boundary the write path uses to keep restatements apart
	// (core.UpdateTracker.RestatementSimilarity, default 0.9). Below it a
	// difference is treated as information, never suppressed. 0 uses the
	// default 0.9.
	DuplicateSimilarity float64
}

// DefaultCondenseOptions returns the tuned defaults: a 2048-token budget —
// generous enough to never bind on the default 5-episode + 3-pitfall
// injection (guaranteeing no common-path regression), hard enough to cap
// pathological contexts — and the 0.9 restatement boundary.
func DefaultCondenseOptions() CondenseOptions {
	return CondenseOptions{MaxTokens: 2048, DuplicateSimilarity: 0.9}
}

// PrefetchStats reports the measurable effect of one condensation pass — the
// in-repo measurement point for the efficiency axis (context tokens per
// question).
type PrefetchStats struct {
	EpisodesIn  int
	EpisodesOut int
	PitfallsIn  int
	PitfallsOut int
	// SuppressedEpisodes/Pitfalls count the redundant restatements that were
	// suppressed before injection.
	SuppressedEpisodes int
	SuppressedPitfalls int
	// InputTokens is the naive (pre-condensation) formatted context size;
	// OutputTokens the injected size. The efficiency metric is OutputTokens
	// per question; the halving claim is OutputTokens <= InputTokens/2.
	InputTokens  int
	OutputTokens int
	// BudgetHit reports whether the token budget actually trimmed the tail.
	BudgetHit bool
}

// EstimateTokens returns a deterministic estimate of the token count of s —
// the measurement the efficiency axis compares against. Each CJK rune
// (U+3000–U+9FFF: CJK punctuation, kana, han) counts 1 token, matching
// tokenizers where a single CJK char is a meaningful token; every 4 other
// chars (ASCII, Latin, digits, whitespace) count 1 token, with partial
// groups rounding up (5 chars → 2 tokens, 8 chars → 2). The estimate is
// monotonic in content length and independent of any external service, so
// the same context always measures the same.
func EstimateTokens(s string) int {
	var cjk, other int
	for _, r := range s {
		if r >= 0x3000 && r <= 0x9FFF {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// defaultDuplicateSimilarity is the Dice boundary above which two memories
// count as redundant restatements at recall — the same boundary the write
// path uses to keep restatements apart (core.UpdateTracker
// .RestatementSimilarity, default 0.9), kept as one named constant here so
// the alignment is explicit.
const defaultDuplicateSimilarity = 0.9

// duplicateThreshold resolves the CondenseOptions knob to the effective
// boundary (0 uses the write-path-aligned default).
func duplicateThreshold(opts CondenseOptions) float64 {
	if opts.DuplicateSimilarity <= 0 {
		return defaultDuplicateSimilarity
	}
	return opts.DuplicateSimilarity
}

// suppressNearDuplicates walks items in rank order and keeps each item unless
// it is a redundant restatement of an already-kept representative (same text,
// substring containment, or character-bigram Dice at or above threshold).
// When same is non-nil, only item pairs it accepts can be duplicates — the
// entity anchor of pitfalls, so two advisories on different entities never
// collapse. The output is a rank-ordered subsequence of the input —
// suppression never reorders.
func suppressNearDuplicates[T any](items []T, textOf func(T) string,
	threshold float64, same func(T, T) bool) (kept []T, suppressed int) {
	kept = make([]T, 0, len(items))
	for _, it := range items {
		t := oneLine(textOf(it))
		redundant := false
		for _, r := range kept {
			if same != nil && !same(it, r) {
				continue
			}
			if redundantRestatement(t, oneLine(textOf(r)), threshold) {
				redundant = true
				break
			}
		}
		if redundant {
			suppressed++
			continue
		}
		kept = append(kept, it)
	}
	return kept, suppressed
}

// redundantRestatement reports whether a and b carry the same information at
// recall: identical text, substring containment, or bigram Dice at or above
// threshold. Normalization to one line matches what actually gets injected.
func redundantRestatement(a, b string, threshold float64) bool {
	if a == b {
		return true
	}
	if len(a) < len(b) {
		a, b = b, a
	}
	if strings.Contains(a, b) {
		return true
	}
	return embedder.BigramOverlap(a, b) >= threshold
}

// condenseEpisodes suppresses redundant restatements among retrieved
// episodes, preserving rank order. Any two episodes may be restatements
// (the same fact written across sessions), so no anchor precondition applies.
func condenseEpisodes(episodes []models.EpisodicMemory,
	opts CondenseOptions) ([]models.EpisodicMemory, int) {
	return suppressNearDuplicates(episodes, func(m models.EpisodicMemory) string {
		return m.Summary
	}, duplicateThreshold(opts), nil)
}

// condensePitfalls suppresses redundant restatements among retrieved
// pitfalls, preserving rank order. Two pitfalls anchored on the same entity
// with near-identical signatures are one advisory, not two — and two pitfalls
// on different entities are never collapsed, even with identical signatures
// (each entity's advisory is distinct information).
func condensePitfalls(pitfalls []models.PitfallMemory,
	opts CondenseOptions) ([]models.PitfallMemory, int) {
	return suppressNearDuplicates(pitfalls, func(p models.PitfallMemory) string {
		return p.Signature
	}, duplicateThreshold(opts), func(a, b models.PitfallMemory) bool {
		return a.EntityID == b.EntityID
	})
}

// FormatPrefetchCondensed condenses episodes + pitfalls (near-duplicate
// suppression, then rank-tail trimming under the token budget) and formats
// them into the recall block. The returned stats make the condensation
// measurable: InputTokens is the naive context size, OutputTokens the
// injected size.
//
// Budget semantics: the budget is soft — after suppression, episodes are
// trimmed from the rank tail first, then pitfalls, and each non-empty input
// list always keeps its top-1 item, so condensation never injects nothing.
func FormatPrefetchCondensed(episodes []models.EpisodicMemory,
	pitfalls []models.PitfallMemory, opts CondenseOptions) (string, PrefetchStats) {
	eps, suppressedEps := condenseEpisodes(episodes, opts)
	pits, suppressedPits := condensePitfalls(pitfalls, opts)

	inputText := formatPrefetch(episodes, pitfalls)
	inputTokens := EstimateTokens(inputText)

	text := formatPrefetch(eps, pits)
	outTokens := EstimateTokens(text)
	budgetHit := false

	if opts.MaxTokens > 0 && outTokens > opts.MaxTokens {
		budgetHit = true
		for outTokens > opts.MaxTokens && len(eps) > 1 {
			eps = eps[:len(eps)-1]
			text = formatPrefetch(eps, pits)
			outTokens = EstimateTokens(text)
		}
		for outTokens > opts.MaxTokens && len(pits) > 1 {
			pits = pits[:len(pits)-1]
			text = formatPrefetch(eps, pits)
			outTokens = EstimateTokens(text)
		}
	}

	return text, PrefetchStats{
		EpisodesIn:         len(episodes),
		EpisodesOut:        len(eps),
		PitfallsIn:         len(pitfalls),
		PitfallsOut:        len(pits),
		SuppressedEpisodes: suppressedEps,
		SuppressedPitfalls: suppressedPits,
		InputTokens:        inputTokens,
		OutputTokens:       outTokens,
		BudgetHit:          budgetHit,
	}
}
