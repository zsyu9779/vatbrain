package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockClient_Chat_Success(t *testing.T) {
	m := &MockClient{Response: "hello world"}
	resp, err := m.Chat(context.Background(), "system", "user")
	assert.NoError(t, err)
	assert.Equal(t, "hello world", resp)
}

func TestMockClient_Chat_Error(t *testing.T) {
	m := &MockClient{Err: errors.New("rate limited")}
	resp, err := m.Chat(context.Background(), "system", "user")
	assert.EqualError(t, err, "rate limited")
	assert.Empty(t, resp)
}

func TestMockClient_Chat_BothSet(t *testing.T) {
	// When both are set, Err takes precedence (but both are returned).
	m := &MockClient{Response: "data", Err: errors.New("fail")}
	resp, err := m.Chat(context.Background(), "", "")
	assert.Error(t, err)
	// Response is still set but error takes precedence in caller's view.
	assert.Equal(t, "data", resp)
}

func TestNewClaudeClient_Defaults(t *testing.T) {
	c := NewClaudeClient("sk-test", "", "")
	assert.Equal(t, "sk-test", c.APIKey)
	assert.Equal(t, "https://api.anthropic.com", c.BaseURL)
	assert.Equal(t, "claude-sonnet-4-6-20250501", c.Model)
	assert.Equal(t, 3, c.MaxRetries)
	assert.NotNil(t, c.HTTPClient)
}

func TestNewClaudeClient_CustomValues(t *testing.T) {
	c := NewClaudeClient("sk-custom", "https://custom.api.com/v1/", "claude-opus-4-7")
	assert.Equal(t, "sk-custom", c.APIKey)
	assert.Equal(t, "https://custom.api.com/v1", c.BaseURL) // trimmed
	assert.Equal(t, "claude-opus-4-7", c.Model)
}

func TestNewClaudeClient_BaseURLTrailingSlash(t *testing.T) {
	c := NewClaudeClient("k", "https://api.example.com/", "")
	assert.Equal(t, "https://api.example.com", c.BaseURL)
}

func TestClaudeClient_Chat_InvalidURL(t *testing.T) {
	c := NewClaudeClient("sk-test", "http://127.0.0.1:1", "test-model")
	_, err := c.Chat(context.Background(), "system", "user msg")
	assert.Error(t, err)
}

func TestClaudeClient_Chat_ContextCancelled(t *testing.T) {
	c := NewClaudeClient("sk-test", "http://127.0.0.1:1", "test-model")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Chat(ctx, "sys", "user")
	assert.Error(t, err)
}

func TestMockClient_ImplementsClient(t *testing.T) {
	var client Client = &MockClient{Response: "ok"}
	resp, err := client.Chat(context.Background(), "sys", "user")
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp)
}
