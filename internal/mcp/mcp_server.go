// Package mcp implements the MCP (Model Context Protocol) server that exposes
// VatBrain's memory operations as tools for AI agents. It wraps the existing
// engine and database layer behind the MCP tool interface.
package mcp

import (
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
)

// NewMCPServer creates an MCP server with all VatBrain tools registered.
// It uses stdio transport — the caller should invoke server.ServeStdio(s).
func NewMCPServer(a *app.App) *server.MCPServer {
	s := server.NewMCPServer("vatbrain", "0.1.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	for _, st := range RegisteredTools(a) {
		s.AddTool(st.Tool, st.Handler)
	}
	return s
}

// RegisteredTools returns all VatBrain MCP tools with their handlers.
// This is exported for testing — use mcptest.NewServer with these tools.
func RegisteredTools(a *app.App) []server.ServerTool {
	return []server.ServerTool{
		writeMemoryTool(a),
		searchMemoriesTool(a),
		triggerConsolidationTool(a),
		getMemoryWeightTool(a),
		touchMemoryTool(a),
		healthCheckTool(a),
	}
}
