package embedder

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandQuery_Empty(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "  ")
	require.NoError(t, err)
	assert.Equal(t, "", out)
}

// TestExpandQuery_PreservesQuestion verifies the expanded text keeps the
// original question verbatim as its prefix — expansion only appends terms.
func TestExpandQuery_PreservesQuestion(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "软路由 OpenClash 覆写脚本")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "软路由 OpenClash 覆写脚本"))
}

// TestExpandQuery_LatinTokens verifies Latin word tokens are extracted and
// case-folded into the expansion.
func TestExpandQuery_LatinTokens(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "Refund Policy check")
	require.NoError(t, err)
	assert.Contains(t, out, "refund")
	assert.Contains(t, out, "policy")
	assert.Contains(t, out, "check")
}

// TestExpandQuery_EntityRef verifies code-entity references (file-like paths
// with a known extension) become explicit expansion terms.
func TestExpandQuery_EntityRef(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "clawfeed-push-v3.py 推送报错")
	require.NoError(t, err)
	assert.Contains(t, out, "clawfeed-push-v3.py")
}

// TestExpandQuery_EntityRefCaseFold verifies an uppercase entity reference is
// normalized to lowercase — the canonical form stored in memories.
func TestExpandQuery_EntityRefCaseFold(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "CLAWFEED-PUSH-V3.PY 推送")
	require.NoError(t, err)
	assert.Contains(t, out, "clawfeed-push-v3.py")
}

// TestExpandQuery_Numbers verifies numeric runs become expansion terms.
func TestExpandQuery_Numbers(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "超时 500 秒")
	require.NoError(t, err)
	assert.Contains(t, out, "500")
}

// TestExpandQuery_ChineseOnly verifies a CJK-only question is returned
// unchanged — Chinese needs no case folding or tokenization.
func TestExpandQuery_ChineseOnly(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "量子计算 引力波 黑洞")
	require.NoError(t, err)
	assert.Equal(t, "量子计算 引力波 黑洞", out)
}

// TestExpandQuery_DuplicateTerms verifies repeated terms are appended only
// once (question carries "pgvector" twice, expansion adds one more).
func TestExpandQuery_DuplicateTerms(t *testing.T) {
	out, err := ExpandQuery(context.Background(), "pgvector pgvector 报错")
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(out, "pgvector"))
}

// TestCharBigrams_CaseFold verifies ASCII case folding: Latin case variants
// produce the same bigram set, so "OpenClash" and "openclash" match.
func TestCharBigrams_CaseFold(t *testing.T) {
	assert.Equal(t, CharBigrams("OpenClash"), CharBigrams("openclash"))
}

// TestBigramOverlap_CaseInsensitive verifies the lexical scoring bridge for
// Latin case variants: an all-caps query token still overlaps a canonical
// summary.
func TestBigramOverlap_CaseInsensitive(t *testing.T) {
	assert.Greater(t, BigramOverlap("OPENCLASH 配置", "OpenClash 覆写脚本"), 0.0)
}

// TestBigramOverlap_Chinese covers the CJK Dice-coefficient properties:
// identical text → 1.0, unrelated → near 0, partial → strictly between.
func TestBigramOverlap_Chinese(t *testing.T) {
	assert.InDelta(t, 1.0, BigramOverlap("软路由调试", "软路由调试"), 1e-9)
	assert.Less(t, BigramOverlap("量子引力", "菜谱烹饪"), 0.1)
	s := BigramOverlap("OpenClash 调试", "OpenClash 配置")
	assert.True(t, s > 0 && s < 1, "partial overlap should be in (0,1), got %v", s)
}

// TestBigramOverlap_Empty verifies empty inputs score zero, never divide.
func TestBigramOverlap_Empty(t *testing.T) {
	assert.Equal(t, 0.0, BigramOverlap("", "anything"))
	assert.Equal(t, 0.0, BigramOverlap("anything", ""))
}
