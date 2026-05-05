package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ClaudeClient implements Client using the Claude Messages API.
type ClaudeClient struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxRetries int
	HTTPClient *http.Client
}

// NewClaudeClient returns a ClaudeClient with sensible defaults.
func NewClaudeClient(apiKey, baseURL, model string) *ClaudeClient {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if model == "" {
		model = "claude-sonnet-4-6-20250501"
	}
	return &ClaudeClient{
		APIKey:     apiKey,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Model:      model,
		MaxRetries: 3,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Chat sends a system + user prompt to the Claude Messages API and returns the
// text response. It retries on transient failures up to MaxRetries times.
func (c *ClaudeClient) Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	url := c.BaseURL + "/v1/messages"

	reqBody := messagesRequest{
		Model:     c.Model,
		MaxTokens: 4096,
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: userPrompt},
		},
	}

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		resp, err := c.doRequest(ctx, url, reqBody)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}
	return "", fmt.Errorf("claude chat: max retries exceeded: %w", lastErr)
}

func (c *ClaudeClient) doRequest(ctx context.Context, url string, body messagesRequest) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("claude api %d: %s", resp.StatusCode, string(respBody))
	}

	var mr messagesResponse
	if err := json.Unmarshal(respBody, &mr); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if mr.Error != nil {
		return "", fmt.Errorf("claude api error: %s", mr.Error.Message)
	}

	var texts []string
	for _, block := range mr.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}
