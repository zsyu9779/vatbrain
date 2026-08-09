package embedder

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeywordEmbedder_Deterministic_NonZero(t *testing.T) {
	k := NewKeywordEmbedder(64)
	v1, err := k.Embed(context.Background(), "软路由 OpenClash 调试记录")
	require.NoError(t, err)
	v2, err := k.Embed(context.Background(), "软路由 OpenClash 调试记录")
	require.NoError(t, err)
	assert.Len(t, v1, 64)
	// 非零向量（与 stub 的零向量不同——含语义信号）
	assert.True(t, anyNonZero(v1), "keyword embedder 必须产生非零向量")
	// 确定性
	assert.Equal(t, v1, v2)
}

func TestKeywordEmbedder_SimilarText_SimilarVector(t *testing.T) {
	k := NewKeywordEmbedder(256)
	ctx := context.Background()
	a, _ := k.Embed(ctx, "OpenClash 覆写脚本用 Ruby YAML 解析")
	b, _ := k.Embed(ctx, "OpenClash 覆写脚本用 Ruby YAML 解析")
	c, _ := k.Embed(ctx, "量子计算 引力波 黑洞")
	simAB := cosine(a, b)
	simAC := cosine(a, c)
	assert.Greater(t, simAB, simAC, "相似文本余弦应高于无关文本")
}

func TestKeywordEmbedder_Empty(t *testing.T) {
	k := NewKeywordEmbedder(8)
	v, err := k.Embed(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, v, 8)
	assert.False(t, anyNonZero(v), "空文本 → 零向量")
}

func TestDualChannel_FallsBackToKeyword(t *testing.T) {
	// 语义通道失败（nil provider → local 回退）→ keyword 向量
	d := NewDualChannelEmbedder(nil)
	v, err := d.Embed(context.Background(), "中文记忆内容")
	require.NoError(t, err)
	assert.True(t, anyNonZero(v), "语义失败时应回退到非零 keyword 向量")
}

func TestNewEmbedderFromConfig_None_UsesKeyword(t *testing.T) {
	e := NewEmbedderFromConfig(DefaultEmbedderConfig())
	k, ok := e.(*KeywordEmbedder)
	require.True(t, ok, "semantic=none 应返回 keyword embedder")
	v, err := k.Embed(context.Background(), "测试")
	require.NoError(t, err)
	assert.True(t, anyNonZero(v))
}

func TestNewEmbedderFromConfig_OpenAI_ReturnsDual(t *testing.T) {
	cfg := DefaultEmbedderConfig()
	cfg.SemanticProvider = SemanticProviderOpenAI
	cfg.SemanticAPIKey = "sk-test"
	e := NewEmbedderFromConfig(cfg)
	d, ok := e.(*DualChannelEmbedder)
	require.True(t, ok, "openai 应返回 dual-channel embedder")
	_, ok = d.Semantic.(*OpenAIProvider)
	require.True(t, ok)
}

// cosine returns the cosine similarity between two vectors.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func anyNonZero(v []float32) bool {
	for _, c := range v {
		if c != 0 {
			return true
		}
	}
	return false
}
