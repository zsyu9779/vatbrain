// Package bench implements the HTTP evaluation entrypoint that exposes
// VatBrain's write + retrieval pipeline as an OmniMemEval memory backend
// (docs/v0.3/tech-specs/03-omnimemeval-benchmark.md).
//
// It reuses the shared core.WriteMemory write pipeline and provider retrieval
// so the benchmark measures the same production semantics, with one deliberate
// ablation knob: GateMode. In "off" mode every message persists (the memory
// kernel's storage + retrieval is what is benchmarked, matching how competing
// products ingest conversation content); in "on" mode the real significance
// gate runs, measuring VatBrain's "forgetting is default" system behavior.
package bench

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/provider"
	"github.com/vatbrain/vatbrain/internal/store"
)

// GateMode controls whether the significance gate filters writes.
type GateMode string

const (
	// GateModeOff persists every message — the benchmark's primary mode.
	GateModeOff GateMode = "off"
	// GateModeOn runs the real significance gate via provider.DeriveWriteEvent.
	GateModeOn GateMode = "on"
)

// maxRequestBodyBytes bounds a single /v1/* request (each message runs the
// full write pipeline, so an unbounded body is both a DoS and a cost vector).
const maxRequestBodyBytes = 32 << 20

// defaultMaxMessagesPerAdd caps the messages in one /v1/add request. Each
// message triggers embedding + similarity search + link-on-write, so a huge
// batch multiplies paid embedding calls when a semantic embedder is set.
const defaultMaxMessagesPerAdd = 2000

// Options configures the benchmark server.
type Options struct {
	GateMode GateMode
	// Language is stamped on every written memory (default "en"; the
	// benchmark data is English).
	Language string
	// TaskType is stamped on every written memory (default TaskTypeFeature).
	TaskType models.TaskType
	// Token, when non-empty, is required as Authorization: Bearer <token> on
	// every endpoint except /health. Set it whenever the server binds beyond
	// loopback — the API has no other authentication.
	Token string
	// MaxMessagesPerAdd caps the messages in one /v1/add request. 0 uses the
	// default (2000).
	MaxMessagesPerAdd int
	// IngestWorkers is the size of the parallel embedding worker pool for
	// /v1/add ingestion. 0 uses the default (32); values are clamped to
	// [1, 64] (docs/v0.3/05-agent-memory-handoff.md 【并发调研】).
	IngestWorkers int
	// EmbedBatchSize is the maximum texts per embedding request during
	// ingestion. 0 uses the default (64 — the provider hard limit).
	EmbedBatchSize int
}

// Server is the HTTP evaluation server. It holds the same WriteDeps the
// vatbrain-provider daemon uses, so write and search semantics do not drift
// between production and benchmark.
type Server struct {
	deps  core.WriteDeps
	opts  Options
	store store.MemoryStore
}

// NewServer creates the benchmark server. GateMode defaults to off and is
// validated (anything but "off"/"on" is an error, not a silent behavior
// change), Language to "en", TaskType to feature.
func NewServer(deps core.WriteDeps, opts Options) (*Server, error) {
	if opts.GateMode == "" {
		opts.GateMode = GateModeOff
	}
	switch opts.GateMode {
	case GateModeOff, GateModeOn:
	default:
		return nil, fmt.Errorf("bench: invalid gate mode %q (want %q|%q)",
			opts.GateMode, GateModeOff, GateModeOn)
	}
	if opts.Language == "" {
		opts.Language = "en"
	}
	if opts.TaskType == "" {
		opts.TaskType = models.TaskTypeFeature
	}
	if opts.MaxMessagesPerAdd <= 0 {
		opts.MaxMessagesPerAdd = defaultMaxMessagesPerAdd
	}
	// Wrap the embedder in a concurrent batch embedder so /v1/add can embed
	// all its messages in parallel (worker pool, 64 texts per request) and
	// then persist them sequentially — SQLite is a single writer. Embed
	// (single text) delegates unchanged; an embedder without batch support
	// makes handleAdd fall back to the sequential pipeline.
	deps.Embedder = embedder.NewBatchEmbedder(deps.Embedder, embedder.BatchOptions{
		Workers:   opts.IngestWorkers,
		BatchSize: opts.EmbedBatchSize,
	})
	return &Server{deps: deps, opts: opts, store: deps.Store}, nil
}

// ── Request / response types ────────────────────────────────────────────────

// AddMessage is one conversation message the harness wants stored.
type AddMessage struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	ChatTime string `json:"chat_time,omitempty"`
}

// AddRequest is POST /v1/add.
type AddRequest struct {
	UserID   string       `json:"user_id"`
	Messages []AddMessage `json:"messages"`
}

// AddResponse reports how many messages persisted vs were gated out.
// On a mid-batch failure the counts processed so far are still returned, plus
// the failing message index, so the harness can resume without re-writing.
type AddResponse struct {
	Persisted       int            `json:"persisted"`
	Skipped         int            `json:"skipped"`
	GateReasonCount map[string]int `json:"gate_reason_counts"`
	Error           string         `json:"error,omitempty"`
	FailedIndex     int            `json:"failed_index,omitempty"`
}

// SearchRequest is POST /v1/search.
type SearchRequest struct {
	UserID string `json:"user_id"`
	Query  string `json:"query"`
	TopK   int    `json:"top_k"`
}

// SearchResult is one retrieved memory in plain text.
type SearchResult struct {
	Content string  `json:"content"`
	Weight  float64 `json:"weight"`
}

// SearchResponse is the search result envelope.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

// DeleteRequest is POST /v1/delete.
type DeleteRequest struct {
	UserID string `json:"user_id"`
}

// DeleteResponse reports how many memories were removed.
type DeleteResponse struct {
	Deleted int `json:"deleted"`
}

// ── Routes ──────────────────────────────────────────────────────────────────

// Routes returns the HTTP handler. Go 1.22+ method+path patterns are used.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/add", s.handleAdd)
	mux.HandleFunc("POST /v1/search", s.handleSearch)
	mux.HandleFunc("POST /v1/delete", s.handleDelete)
	mux.HandleFunc("GET /health", s.handleHealth)

	var h http.Handler = s.logMiddleware(mux)
	if s.opts.Token != "" {
		h = s.authMiddleware(h)
	}
	return h
}

// authMiddleware requires Authorization: Bearer <token> on every endpoint
// except /health (kept open for liveness probes). /health leaks nothing.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	const prefix = "Bearer "
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("Authorization")
		if !strings.HasPrefix(got, prefix) ||
			subtle.ConstantTimeCompare(
				[]byte(strings.TrimSpace(strings.TrimPrefix(got, prefix))),
				[]byte(s.opts.Token),
			) != 1 {
			respondError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ListenAndServe serves until ctx is cancelled (then graceful shutdown).
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.Routes(),
		ReadTimeout:  30 * time.Second,
		// One /v1/add can legitimately take minutes: each message runs the
		// full write pipeline (embedding etc.). A 60s WriteTimeout would kill
		// large benchmark sessions mid-write.
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	slog.Info("vatbrain-bench listening",
		"addr", addr,
		"gate_mode", s.opts.GateMode,
		"language", s.opts.Language,
	)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

// ── Handlers ────────────────────────────────────────────────────────────────

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req AddRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		respondError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if len(req.Messages) == 0 {
		respondError(w, http.StatusBadRequest, "messages is required")
		return
	}
	if len(req.Messages) > s.opts.MaxMessagesPerAdd {
		respondError(w, http.StatusBadRequest, fmt.Sprintf(
			"too many messages: %d exceeds limit %d", len(req.Messages), s.opts.MaxMessagesPerAdd))
		return
	}

	persisted, skipped := 0, 0
	reasons := make(map[string]int)

	// Phase 1: build the write events and embed every non-empty summary in
	// parallel (worker pool, 64 texts per request, rate-limit retries). The
	// gate still runs per message in phase 2, so the significance-gate
	// semantics are unchanged — the only cost of pre-embedding is that
	// gate-on mode embeds messages that later get gated out.
	events := make([]core.WriteEvent, 0, len(req.Messages))
	indexes := make([]int, 0, len(req.Messages))
	for i, msg := range req.Messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			skipped++
			reasons["empty_message"]++
			continue
		}
		events = append(events, s.eventFor(content, msg.ChatTime))
		indexes = append(indexes, i)
	}

	var embeddings [][]float32
	if be, ok := s.deps.Embedder.(embedder.BatchCapable); ok {
		summaries := make([]string, len(events))
		for k, ev := range events {
			summaries[k] = ev.Summary
		}
		vecs, err := be.EmbedBatch(r.Context(), summaries)
		if err != nil && !errors.Is(err, embedder.ErrBatchNotSupported) {
			slog.Error("bench: parallel embedding failed", "messages", len(events), "err", err)
			respondJSON(w, http.StatusInternalServerError, AddResponse{
				Error:       "embedding failed",
				FailedIndex: 0,
			})
			return
		}
		if err == nil {
			embeddings = vecs
		}
	}

	// Phase 2: sequential writes in request order. The bench harness can
	// resume after a mid-batch failure (counts so far + failing index), so
	// earlier messages are never re-written on retry.
	for k, event := range events {
		res, err := s.writeOne(r.Context(), event, embeddings, k, req.UserID)
		if err != nil {
			slog.Error("bench: write failed", "index", indexes[k], "err", err)
			respondJSON(w, http.StatusInternalServerError, AddResponse{
				Persisted:       persisted,
				Skipped:         skipped,
				GateReasonCount: reasons,
				Error:           "write failed",
				FailedIndex:     indexes[k],
			})
			return
		}

		if res.Persisted {
			persisted++
		} else {
			skipped++
			reasons[res.GateReason]++
		}
	}

	respondJSON(w, http.StatusOK, AddResponse{
		Persisted:       persisted,
		Skipped:         skipped,
		GateReasonCount: reasons,
	})
}

// writeOne persists one event through the shared write pipeline. With
// precomputed embeddings available (the concurrent path) it uses
// WriteMemoryWithEmbedding so the batch's embedding work is not repeated;
// without them (sequential fallback) it uses WriteMemory, which embeds
// internally.
func (s *Server) writeOne(ctx context.Context, event core.WriteEvent, embeddings [][]float32, k int, userID string) (core.WriteResult, error) {
	if embeddings != nil {
		return core.WriteMemoryWithEmbedding(ctx, s.deps, event, embeddings[k],
			userID, s.opts.Language, "", s.opts.TaskType)
	}
	return core.WriteMemory(ctx, s.deps, event,
		userID, s.opts.Language, "", s.opts.TaskType)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		respondError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		respondError(w, http.StatusBadRequest, "query is required")
		return
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}

	episodes, err := provider.RetrieveEpisodic(r.Context(), s.deps, req.UserID, req.Query, topK)
	if err != nil {
		slog.Error("bench: search failed", "err", err)
		respondError(w, http.StatusInternalServerError, "search failed")
		return
	}

	results := make([]SearchResult, 0, len(episodes))
	for _, ep := range episodes {
		results = append(results, SearchResult{Content: ep.Summary, Weight: ep.Weight})
	}
	respondJSON(w, http.StatusOK, SearchResponse{Results: results})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req DeleteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		respondError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	del, ok := s.store.(store.EpisodicDeleteStore)
	if !ok {
		respondError(w, http.StatusNotImplemented,
			"store backend does not support episodic delete")
		return
	}

	n, err := del.DeleteEpisodicByProject(r.Context(), req.UserID)
	if err != nil {
		slog.Error("bench: delete failed", "err", err)
		respondError(w, http.StatusInternalServerError, "delete failed")
		return
	}

	// Drop the project's working-memory cycles too, so a reused user_id does
	// not leak stale summaries into cross-cycle gating in gate "on" mode.
	if s.deps.WorkingMem != nil {
		s.deps.WorkingMem.Clear(req.UserID)
	}

	respondJSON(w, http.StatusOK, DeleteResponse{Deleted: n})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := s.store.HealthCheck(ctx); err != nil {
		respondError(w, http.StatusServiceUnavailable, "store unhealthy")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// eventFor builds the WriteEvent for one message. When the message carries a
// parseable chat_time, the summary is prefixed with the event's date
// ("[2006-01-02] ") so retrieved context preserves temporal ordering. In
// GateModeOff the event is force-confirmed so the significance gate lets it
// through; in GateModeOn the production derivation
// (provider.DeriveWriteEvent) runs untouched.
func (s *Server) eventFor(content, chatTime string) core.WriteEvent {
	content = datePrefixedContent(content, chatTime)
	if s.opts.GateMode == GateModeOff {
		return core.WriteEvent{Summary: content, UserConfirmed: true}
	}
	return provider.DeriveWriteEvent(content, "")
}

// datePrefixedContent returns content prefixed with the message date as
// "[2006-01-02] " when chatTime parses under a common layout; an empty or
// unparseable chatTime leaves content unchanged (best-effort).
func datePrefixedContent(content, chatTime string) string {
	chatTime = strings.TrimSpace(chatTime)
	if chatTime == "" {
		return content
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	} {
		t, err := time.Parse(layout, chatTime)
		if err == nil {
			return "[" + t.Format("2006-01-02") + "] " + content
		}
	}
	return content
}

// ── Middleware + JSON helpers ───────────────────────────────────────────────

func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("bench: request",
			"method", r.Method,
			"path", r.URL.Path,
			"dur_ms", time.Since(start).Milliseconds(),
		)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return err
		}
		respondError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return err
	}
	// Reject trailing garbage after the single JSON value.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		respondError(w, http.StatusBadRequest, "request body must contain exactly one JSON value")
		return errors.New("trailing data after JSON body")
	}
	return nil
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("bench: encode response", "err", err)
	}
}

func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
