package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vatbrain/vatbrain/internal/models"
)

// Lifecycle method names on the provider daemon.
const (
	MethodOnSessionEnd    = "on_session_end"
	MethodOnMemoryWrite   = "on_memory_write"
	MethodOnSessionSwitch = "on_session_switch"
)

// consolidationTimeout bounds the sleep-integration run so a slow
// consolidation never hangs the daemon shutdown path.
const consolidationTimeout = 120 * time.Second

// memoryWriteEntry tracks one mirrored built-in memory write so replace and
// remove can locate the previous version (DERIVED_FROM provenance).
type memoryWriteEntry struct {
	contentHash string
	memoryID    uuid.UUID
}

// memoryWriteIndex maps a hermes memory target ("memory"/"user") to its
// written versions, newest last. In-memory only: a daemon restart loses the
// index, so replace/remove after a restart degrades to best-effort (the new
// write still lands, just without obsoleting the prior episode).
type memoryWriteIndex struct {
	mu       sync.Mutex
	byTarget map[string][]memoryWriteEntry
}

func newMemoryWriteIndex() *memoryWriteIndex {
	return &memoryWriteIndex{byTarget: make(map[string][]memoryWriteEntry)}
}

func (m *memoryWriteIndex) push(target string, e memoryWriteEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byTarget[target] = append(m.byTarget[target], e)
}

func (m *memoryWriteIndex) last(target string) (memoryWriteEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	es := m.byTarget[target]
	if len(es) == 0 {
		return memoryWriteEntry{}, false
	}
	return es[len(es)-1], true
}

// onSessionEndParams mirrors the hermes on_session_end hook.
type onSessionEndParams struct {
	SessionID string `json:"session_id"`
}

type onSessionEndResult struct {
	Started bool `json:"started"`
}

// handleOnSessionEnd triggers sleep integration (rule + pitfall extraction)
// in the background so the hermes shutdown path never blocks on it.
func (s *Server) handleOnSessionEnd(req rpcRequest) rpcResponse {
	var p onSessionEndParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid on_session_end params: %v", err))
	}
	session, ok := s.sessionFor(p.SessionID)
	if !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}
	if s.Consolidation == nil {
		return newErrorResponse(req.ID, -32003, "consolidation engine not configured")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), consolidationTimeout)
		defer cancel()
		result, err := s.Consolidation.Run(ctx, s.deps.Store, s.deps.Embedder)
		if err != nil {
			slog.Warn("provider: on_session_end consolidation failed",
				"project", session.projectID, "err", err)
			return
		}
		slog.Info("provider: session integration complete",
			"project", session.projectID,
			"scanned", result.EpisodicsScanned,
			"rules", result.RulesPersisted,
			"pitfalls", result.PitfallsPersisted)
	}()

	return newResponse(req.ID, onSessionEndResult{Started: true})
}

// onMemoryWriteParams mirrors the hermes on_memory_write hook.
type onMemoryWriteParams struct {
	Action    string            `json:"action"`
	Target    string            `json:"target"`
	Content   string            `json:"content"`
	SessionID string            `json:"session_id"`
	Metadata  map[string]string `json:"metadata"`
}

type onMemoryWriteResult struct {
	Persisted   bool   `json:"persisted"`
	MemoryID    string `json:"memory_id,omitempty"`
	Obsoleted   bool   `json:"obsoleted,omitempty"`
	EdgeCreated bool   `json:"edge_created,omitempty"`
}

// handleOnMemoryWrite mirrors a built-in hermes memory write into the graph
// as a user-explicit episodic (SourceType=USER, highest trust). add → new
// episode; replace → new episode + DERIVED_FROM edge + obsolete prior;
// remove → obsolete prior.
func (s *Server) handleOnMemoryWrite(ctx context.Context, req rpcRequest) rpcResponse {
	var p onMemoryWriteParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid on_memory_write params: %v", err))
	}
	session, ok := s.sessionFor(p.SessionID)
	if !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}
	if p.Action != "add" && p.Action != "replace" && p.Action != "remove" {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid action %q", p.Action))
	}

	target := p.Target
	if target == "" {
		target = "memory"
	}
	hash := entryHash(p.Content)

	switch p.Action {
	case "remove":
		prev, ok := s.memoryWrite.last(target)
		if !ok {
			return newResponse(req.ID, onMemoryWriteResult{Persisted: false})
		}
		now := time.Now()
		if err := s.deps.Store.MarkObsolete(ctx, prev.memoryID, now); err != nil {
			return newErrorResponse(req.ID, -32000, fmt.Sprintf("obsolete failed: %v", err))
		}
		return newResponse(req.ID, onMemoryWriteResult{Obsoleted: true, MemoryID: prev.memoryID.String()})

	case "add", "replace":
		id, err := s.writeMirroredEpisode(ctx, session.projectID, target, hash, p)
		if err != nil {
			return newErrorResponse(req.ID, -32000, fmt.Sprintf("mirror write failed: %v", err))
		}

		out := onMemoryWriteResult{Persisted: true, MemoryID: id.String()}
		if p.Action == "replace" {
			if prev, ok := s.memoryWrite.last(target); ok {
				now := time.Now()
				if oErr := s.deps.Store.MarkObsolete(ctx, prev.memoryID, now); oErr == nil {
					out.Obsoleted = true
				}
				if eErr := s.deps.Store.CreateEdge(ctx, prev.memoryID, id, "DERIVED_FROM", nil); eErr == nil {
					out.EdgeCreated = true
				}
			}
		}
		s.memoryWrite.push(target, memoryWriteEntry{contentHash: hash, memoryID: id})
		return newResponse(req.ID, out)
	}

	return newErrorResponse(req.ID, -32602, "unreachable")
}

// writeMirroredEpisode persists one built-in memory entry as a user-explicit
// episodic, embedding its content best-effort.
func (s *Server) writeMirroredEpisode(ctx context.Context, projectID, target, hash string,
	p onMemoryWriteParams) (uuid.UUID, error) {

	id := uuid.New()
	now := time.Now()

	// Provenance is encoded in the snapshot URI — the model has no metadata
	// map, so the FullSnapshotURI carries target/hash/action/source/origin.
	snapshot := fmt.Sprintf("hermes://memory-write/%s#%s?action=%s&source=user_explicit",
		target, hash, p.Action)
	if origin, ok := p.Metadata["write_origin"]; ok && origin != "" {
		snapshot += "&origin=" + origin
	}

	mem := &models.EpisodicMemory{
		ID:                 id,
		ProjectID:          projectID,
		Language:           "",
		TaskType:           models.TaskTypeFeature,
		Summary:            p.Content,
		SourceType:         models.SourceTypeUSER,
		TrustLevel:         models.TrustLevelMax,
		Weight:             1.0,
		EffectiveFrequency: 1.0,
		CreatedAt:          now,
		FullSnapshotURI:    snapshot,
	}

	if emb, err := s.deps.Embedder.Embed(ctx, p.Content); err == nil {
		mem.ContextVector = emb
	}

	if err := s.deps.Store.WriteEpisodic(ctx, mem); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// onSessionSwitchParams mirrors the hermes on_session_switch hook.
type onSessionSwitchParams struct {
	SessionID      string `json:"session_id"`
	NewSessionID   string `json:"new_session_id"`
	ParentSessionID string `json:"parent_session_id"`
	Reset          bool   `json:"reset"`
	Rewound        bool   `json:"rewound"`
}

type onSessionSwitchResult struct {
	Rebound bool `json:"rebound"`
}

// handleOnSessionSwitch rebinds a session after /new /reset /branch /resume
// /compression. reset=True clears the working-memory buffer (fresh
// conversation); rewound=True invalidates the prefetch cache (truncated
// transcript); otherwise the logical conversation continues under a new id.
func (s *Server) handleOnSessionSwitch(req rpcRequest) rpcResponse {
	var p onSessionSwitchParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, -32602, fmt.Sprintf("invalid on_session_switch params: %v", err))
	}
	if p.NewSessionID == "" {
		return newErrorResponse(req.ID, -32602, "new_session_id is required")
	}
	session, ok := s.sessionFor(p.SessionID)
	if !ok {
		return newErrorResponse(req.ID, -32002, "unknown session: call initialize first")
	}

	s.mu.Lock()
	delete(s.sessions, p.SessionID)
	s.sessions[p.NewSessionID] = &sessionState{
		projectID:    session.projectID,
		agentContext: session.agentContext,
	}
	s.mu.Unlock()

	if p.Reset && s.deps.WorkingMem != nil {
		s.deps.WorkingMem.Clear(session.projectID)
	}
	if p.Rewound {
		s.prefetchMu.Lock()
		delete(s.prefetchCache, p.SessionID)
		delete(s.prefetchCache, p.NewSessionID)
		s.prefetchMu.Unlock()
	}

	return newResponse(req.ID, onSessionSwitchResult{Rebound: true})
}
