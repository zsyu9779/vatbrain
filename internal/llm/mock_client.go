package llm

import "context"

// MockClient returns a canned response for testing.
type MockClient struct {
	Response string
	Err      error
}

// Chat returns the canned response or error.
func (m *MockClient) Chat(_ context.Context, _, _ string) (string, error) {
	return m.Response, m.Err
}
