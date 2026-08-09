package embedder

import (
	"github.com/vatbrain/vatbrain/internal/models"
)

// SemanticProviderName enumerates the supported semantic channels.
type SemanticProviderName string

const (
	SemanticProviderNone   SemanticProviderName = "none"   // 仅关键词通道
	SemanticProviderLocal  SemanticProviderName = "local"  // 本地启发式（零网络）
	SemanticProviderOpenAI SemanticProviderName = "openai" // OpenAI 兼容 /embeddings
	SemanticProviderVoyage SemanticProviderName = "voyage" // Voyage AI
)

// EmbedderConfig configures the dual-channel embedder (v0.1 tech-spec
// 01-embedder-architecture.md).
type EmbedderConfig struct {
	// SemanticProvider selects the semantic channel ("openai" | "voyage" |
	// "local" | "none"; default "none").
	SemanticProvider SemanticProviderName `yaml:"semantic_provider"`
	// SemanticAPIKey / SemanticBaseURL / SemanticModel configure the external
	// provider. For openai/voyage these are required; "local"/"none" need none.
	SemanticAPIKey  string `yaml:"semantic_api_key"`
	SemanticBaseURL string `yaml:"semantic_base_url"`
	SemanticModel   string `yaml:"semantic_model"`
	// KeywordDim is the keyword-channel dimension (default DefaultEmbeddingDim).
	KeywordDim int `yaml:"keyword_dim"`
}

// DefaultEmbedderConfig returns the zero-API-key config: keyword channel only.
func DefaultEmbedderConfig() EmbedderConfig {
	return EmbedderConfig{
		SemanticProvider: SemanticProviderNone,
		SemanticModel:    "",
		KeywordDim:       models.DefaultEmbeddingDim,
	}
}

// semanticProvider resolves the semantic channel, or nil when "none".
func (c EmbedderConfig) semanticProvider() SemanticProvider {
	switch c.SemanticProvider {
	case SemanticProviderLocal:
		return NewLocalProvider()
	case SemanticProviderOpenAI, SemanticProviderVoyage:
		baseURL := c.SemanticBaseURL
		if baseURL == "" {
			if c.SemanticProvider == SemanticProviderVoyage {
				baseURL = "https://api.voyageai.com/v1"
			} else {
				baseURL = "https://api.openai.com/v1"
			}
		}
		model := c.SemanticModel
		if model == "" {
			if c.SemanticProvider == SemanticProviderVoyage {
				model = "voyage-3"
			} else {
				model = "text-embedding-3-small"
			}
		}
		return NewOpenAIProvider(c.SemanticAPIKey, baseURL, model)
	default:
		return nil
	}
}

// NewEmbedderFromConfig builds the configured embedder. With no semantic
// provider it returns a keyword embedder (non-zero, CJK-safe) instead of the
// zero-vector stub — retrieval/clustering keep working without an API key.
func NewEmbedderFromConfig(cfg EmbedderConfig) Embedder {
	if cfg.SemanticProvider != SemanticProviderNone {
		return NewDualChannelEmbedder(cfg.semanticProvider())
	}
	dim := cfg.KeywordDim
	if dim <= 0 {
		dim = models.DefaultEmbeddingDim
	}
	return NewKeywordEmbedder(dim)
}

// defaultDim returns the default embedding dimension.
func defaultDim() int {
	return models.DefaultEmbeddingDim
}
