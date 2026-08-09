package adapters

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

// CodexProvider scans OpenAI Codex CLI session transcripts.
//
// Storage layout: the Codex CLI persists each session as a JSONL transcript
// under its data root. Layouts have varied by version — from
// ~/.codex/sessions/<project>/<session>/session.jsonl to hashed project
// directories — so this adapter walks the sessions root and treats every
// *.jsonl file as a transcript, making it robust to layout drift. Each line is
// a message with a role and a content field that is either a string or an
// array of {type, text} blocks.
//
// Default root:
//   - unix:   ~/.codex/sessions/
//   - Windows: %USERPROFILE%\.codex\sessions\
//
// Override with the watch path (VATBRAIN_CODEX_SESSIONS_PATH).
type CodexProvider struct {
	watchPath string
	mu        sync.Mutex
	status    watcher.ProviderStatus
}

// NewCodexProvider creates a Codex transcript adapter. If watchPath is empty,
// the platform default sessions root is used.
func NewCodexProvider(watchPath string) *CodexProvider {
	if watchPath == "" {
		watchPath = defaultCodexSessionsDir()
	}

	healthy := true
	if _, err := os.Stat(watchPath); os.IsNotExist(err) {
		healthy = false
	}

	return &CodexProvider{
		watchPath: watchPath,
		status: watcher.ProviderStatus{
			Name:        "codex",
			Description: "Watches OpenAI Codex CLI session transcripts (~/.codex/sessions)",
			Healthy:     healthy,
			WatchPath:   watchPath,
			Config: map[string]string{
				"watch_path": watchPath,
				"format":     "jsonl",
			},
		},
	}
}

// defaultCodexSessionsDir resolves the platform default Codex sessions root.
func defaultCodexSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

func (p *CodexProvider) Name() string        { return "codex" }
func (p *CodexProvider) Description() string { return "Watches OpenAI Codex CLI session transcripts" }

// codexMsg is a single line of a Codex transcript. content may be a plain
// string or a list of content blocks.
type codexMsg struct {
	Role      string `json:"role"`
	Content   any    `json:"content"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
}

// text extracts the human-readable text from a message's content, handling
// both the plain-string and content-block forms.
func (m *codexMsg) text() string {
	switch c := m.Content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, block := range c {
			bm, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := bm["text"].(string); ok && t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Scan walks the Codex sessions root for *.jsonl transcripts and emits one
// RawMemory per transcript, joined across user + assistant messages.
func (p *CodexProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.watchPath == "" {
		p.status.Healthy = false
		p.status.LastScanAt = time.Now()
		return nil, nil
	}
	if _, err := os.Stat(p.watchPath); os.IsNotExist(err) {
		p.status.Healthy = false
		p.status.LastScanAt = time.Now()
		return nil, nil
	}

	var raws []watcher.RawMemory
	err := filepath.WalkDir(p.watchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			slog.Debug("codex: walk", "path", path, "err", err)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return nil
		}

		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		raw, perr := p.parseJSONL(path, info)
		if perr != nil {
			slog.Warn("codex: parse transcript", "path", path, "err", perr)
			return nil
		}
		if raw != nil {
			raws = append(raws, *raw)
		}
		return nil
	})
	if err != nil {
		p.status.Healthy = false
		p.status.LastError = err.Error()
		return nil, err
	}

	sort.Slice(raws, func(i, j int) bool {
		return raws[i].ModifiedAt.After(raws[j].ModifiedAt)
	})

	p.status.Healthy = true
	p.status.LastScanAt = time.Now()
	p.status.TotalSeen += len(raws)
	return raws, nil
}

// parseJSONL reads one Codex transcript into a RawMemory.
func (p *CodexProvider) parseJSONL(path string, info os.FileInfo) (*watcher.RawMemory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var userTexts, assistantTexts []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg codexMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		text := msg.text()
		if text == "" {
			continue
		}
		switch msg.Role {
		case "user":
			userTexts = append(userTexts, text)
		case "assistant":
			assistantTexts = append(assistantTexts, text)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(userTexts) == 0 && len(assistantTexts) == 0 {
		return nil, nil
	}

	content := strings.Join(append(userTexts, assistantTexts...), "\n")
	raw := &watcher.RawMemory{
		ProviderName: "codex",
		SourceURI:    path,
		Content:      content,
		ModifiedAt:   info.ModTime(),
		ProjectID:    projectIDFromPath(p.watchPath, path),
		Metadata: map[string]string{
			"user_msg_count": itoa(len(userTexts)),
			"asst_msg_count": itoa(len(assistantTexts)),
		},
	}
	raw.HashContent()
	return raw, nil
}

func (p *CodexProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}
