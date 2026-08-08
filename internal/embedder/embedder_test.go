package embedder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vatbrain/vatbrain/internal/models"
)

func TestStubEmbedder_New(t *testing.T) {
	s := NewStubEmbedder()
	assert.NotNil(t, s)
	assert.Equal(t, models.DefaultEmbeddingDim, s.Dim)
}

func TestStubEmbedder_Embed_ReturnsZeroVector(t *testing.T) {
	s := &StubEmbedder{Dim: 1536}
	vec, err := s.Embed(context.Background(), "any text")
	assert.NoError(t, err)
	assert.Len(t, vec, 1536)
	for _, v := range vec {
		assert.Equal(t, float32(0), v)
	}
}

func TestStubEmbedder_Embed_EmptyText(t *testing.T) {
	s := &StubEmbedder{Dim: 256}
	vec, err := s.Embed(context.Background(), "")
	assert.NoError(t, err)
	assert.Len(t, vec, 256)
}

func TestStubEmbedder_Embed_CustomDim(t *testing.T) {
	s := &StubEmbedder{Dim: 768}
	vec, err := s.Embed(context.Background(), "test")
	assert.NoError(t, err)
	assert.Len(t, vec, 768)
}

func TestNewClaudeEmbedder_Defaults(t *testing.T) {
	c := NewClaudeEmbedder("sk-key", "", "")
	assert.Equal(t, "sk-key", c.APIKey)
	assert.Equal(t, "https://api.anthropic.com", c.BaseURL)
	assert.Equal(t, "claude-text-embedding-3-small", c.Model)
	assert.NotNil(t, c.HTTPClient)
}

func TestNewClaudeEmbedder_CustomValues(t *testing.T) {
	c := NewClaudeEmbedder("sk-v2", "https://embed.example.com", "custom-model")
	assert.Equal(t, "sk-v2", c.APIKey)
	assert.Equal(t, "https://embed.example.com", c.BaseURL)
	assert.Equal(t, "custom-model", c.Model)
}

func TestEmbedder_Interface_StubImplements(t *testing.T) {
	var e Embedder = NewStubEmbedder()
	assert.NotNil(t, e)
	_, err := e.Embed(context.Background(), "hello")
	assert.NoError(t, err)
}

func TestClaudeEmbedder_Embed_InvalidURL(t *testing.T) {
	c := NewClaudeEmbedder("sk-test", "http://127.0.0.1:1", "test-model")
	_, err := c.Embed(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "claude embedder")
}

func TestClaudeEmbedder_Embed_ContextCancelled(t *testing.T) {
	c := NewClaudeEmbedder("sk-test", "http://127.0.0.1:1", "test-model")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Embed(ctx, "test")
	assert.Error(t, err)
}
