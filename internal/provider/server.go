package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

// Method names on the vatbrain-provider stdio JSON-RPC interface. hermes
// spawns this binary and drives it line-by-line (one JSON-RPC 2.0 request
// per line, no Content-Length framing).
const (
	MethodInitialize = "initialize"
	MethodSyncTurn   = "sync_turn"
	MethodPing       = "ping"
	MethodShutdown   = "shutdown"
)

// sessionState tracks the per-session binding established by initialize.
type sessionState struct {
	projectID    string // agent_identity (profile) or "hermes"
	agentContext string // "primary" | "subagent" | "cron" | "flush"
}

// Server is the stdio JSON-RPC handler. It is single-threaded by design:
// hermes already serialises sync_turn calls on its own FIFO worker
// (memory_manager.py DaemonThreadPoolExecutor max_workers=1), so a request
// arriving here is processed to completion before the next is read.
type Server struct {
	deps        core.WriteDeps
	deriver     func(userContent, assistantContent string) core.WriteEvent
	sessions    map[string]*sessionState
	mu          sync.Mutex
	shutdownC   chan struct{}
	writeTimeout time.Duration // per-sync_turn deadline; 0 disables
}

// NewServer creates a JSON-RPC server bound to the given write-pipeline deps.
func NewServer(deps core.WriteDeps) *Server {
	return &Server{
		deps:         deps,
		deriver:      DeriveWriteEvent,
		sessions:     make(map[string]*sessionState),
		shutdownC:    make(chan struct{}),
		writeTimeout: 30 * time.Second,
	}
}

// ShutdownSignal returns a channel closed when a shutdown request is handled.
func (s *Server) ShutdownSignal() <-chan struct{} {
	return s.shutdownC
}

// Serve reads line-delimited JSON-RPC requests from r and writes responses
// to w until EOF or a shutdown request. maxReqBytes bounds a single request
// line. Returns the shutdown error if the server was told to exit.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer, maxReqBytes int) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxReqBytes)
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		req, err := parseRequest(line)
		if err != nil {
			if encErr := enc.Encode(newErrorResponse(nil, -32700, err.Error())); encErr != nil {
				return encErr
			}
			continue
		}

		resp := s.handle(ctx, req)
		if encErr := enc.Encode(resp); encErr != nil {
			return encErr
		}

		select {
		case <-s.shutdownC:
			return nil
		default:
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("provider: read stdin: %w", err)
	}
	return nil
}

// rpcRequest is a line-delimited JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcResponse is a JSON-RPC 2.0 response. ID mirrors the request (null for
// notifications, which the daemon treats as requests for simplicity).
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func parseRequest(line []byte) (rpcRequest, error) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return req, fmt.Errorf("parse error: %v", err)
	}
	if req.Method == "" {
		return req, errors.New("invalid request: missing method")
	}
	return req, nil
}

func newResponse(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func newErrorResponse(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

// handle dispatches a single request. It runs synchronously; a slow method
// blocks the FIFO by design (hermes calls are already serialised).
func (s *Server) handle(ctx context.Context, req rpcRequest) rpcResponse {
	switch req.Method {
	case MethodInitialize:
		return s.handleInitialize(req)
	case MethodSyncTurn:
		return s.handleSyncTurn(ctx, req)
	case MethodPing:
		return newResponse(req.ID, map[string]bool{"pong": true})
	case MethodShutdown:
		close(s.shutdownC)
		return newResponse(req.ID, map[string]bool{"shutdown": true})
	default:
		return newErrorResponse(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// initializeParams mirrors the kwargs hermes passes to MemoryProvider.initialize.
type initializeParams struct {
	SessionID     string `json:"session_id"`
	HermesHome    string `json:"hermes_home"`
	Platform      string `json:"platform"`
	AgentContext  string `json:"agent_context"`
	AgentIdentity string `json:"agent_identity"`
}

type initializeResult struct {
	Provider      string `json:"provider"`
	StoreBackend  string `json:"store_backend"`
	ReadOnlyMode  bool   `json:"read_only_mode"`
	ProjectID     string `json:"project_id"`
}

func (s *Server) handleInitialize(req rpcRequest) rpcResponse {
	var p initializeParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid initialize params: %v", err))
	}

	projectID := strings.TrimSpace(p.AgentIdentity)
	if projectID == "" {
		projectID = "hermes"
	}
	// Non-primary contexts (subagent/cron/flush) must not write — their
	// system prompts would corrupt user representations (hermes contract).
	readOnly := p.AgentContext != "" && p.AgentContext != "primary"

	s.mu.Lock()
	s.sessions[p.SessionID] = &sessionState{
		projectID:    projectID,
		agentContext: p.AgentContext,
	}
	s.mu.Unlock()

	return newResponse(req.ID, initializeResult{
		Provider:     "vatbrain",
		StoreBackend: "sqlite",
		ReadOnlyMode: readOnly,
		ProjectID:    projectID,
	})
}

// syncTurnParams carries the turn text plus the session it belongs to.
type syncTurnParams struct {
	SessionID        string `json:"session_id"`
	UserContent      string `json:"user_content"`
	AssistantContent string `json:"assistant_content"`
	AgentContext     string `json:"agent_context"`
}

type syncTurnResult struct {
	Persisted   bool   `json:"persisted"`
	MemoryID    string `json:"memory_id,omitempty"`
	GateReason  string `json:"gate_reason,omitempty"`
	IsCorrection bool  `json:"is_correction"`
	MergeAction string `json:"merge_action,omitempty"`
	Weight      float64 `json:"weight,omitempty"`
}

func (s *Server) handleSyncTurn(ctx context.Context, req rpcRequest) rpcResponse {
	var p syncTurnParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid sync_turn params: %v", err))
	}

	session, ok := s.sessionFor(p.SessionID)
	if !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}

	// Non-primary contexts are read-only (hermes contract).
	if p.AgentContext != "" && p.AgentContext != "primary" {
		return newResponse(req.ID, syncTurnResult{Persisted: false, GateReason: "read_only_context"})
	}
	if session.agentContext != "" && session.agentContext != "primary" {
		return newResponse(req.ID, syncTurnResult{Persisted: false, GateReason: "read_only_context"})
	}

	event := s.deriver(p.UserContent, p.AssistantContent)
	if strings.TrimSpace(event.Summary) == "" {
		return newResponse(req.ID, syncTurnResult{Persisted: false, GateReason: "empty_summary"})
	}

	writeCtx := ctx
	if s.writeTimeout > 0 {
		var cancel context.CancelFunc
		writeCtx, cancel = context.WithTimeout(ctx, s.writeTimeout)
		defer cancel()
	}

	res, err := core.WriteMemory(writeCtx, s.deps, event,
		session.projectID, "", "", models.TaskTypeFeature)
	if err != nil {
		slog.Warn("provider: sync_turn write failed", "err", err)
		return newErrorResponse(req.ID, -32000, fmt.Sprintf("write failed: %v", err))
	}

	out := syncTurnResult{
		Persisted:   res.Persisted,
		MemoryID:    res.MemoryID.String(),
		GateReason:  res.GateReason,
		IsCorrection: event.IsCorrection,
		MergeAction: string(res.MergeAction),
		Weight:      res.Weight,
	}
	if !res.Persisted {
		out.MemoryID = ""
	}
	return newResponse(req.ID, out)
}

func (s *Server) sessionFor(sessionID string) (*sessionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[sessionID]
	return st, ok
}
