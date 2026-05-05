// Package llm provides an abstraction over LLM API calls for rule extraction
// and Pitfall extraction during consolidation.
package llm

import "context"

// Client abstracts LLM chat calls for both rule extraction and pitfall extraction.
type Client interface {
	Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
