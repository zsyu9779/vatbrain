package api

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
