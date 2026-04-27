package mcp

// stringFromMeta extracts a string value from pgvector metadata.
func stringFromMeta(meta map[string]any, key string) (string, bool) {
	if meta == nil {
		return "", false
	}
	v, ok := meta[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// clampWeight ensures the weight stays in [0, 1].
func clampWeight(w float64) float64 {
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}
