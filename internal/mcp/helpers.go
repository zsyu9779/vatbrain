package mcp

import "github.com/vatbrain/vatbrain/internal/core"

// ClampWeight ensures the weight stays in [0, 1]. Re-exported from core so
// existing MCP callers keep compiling; the canonical implementation lives in
// the shared write pipeline (core.WriteMemory).
func ClampWeight(w float64) float64 {
	return core.ClampWeight(w)
}
