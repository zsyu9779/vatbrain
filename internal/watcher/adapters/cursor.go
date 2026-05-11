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

// CursorProvider scans Cursor agent-transcripts JSONL files.
// Storage layout: ~/.cursor/projects/{project-id}/agent-transcripts/*/*.jsonl
type CursorProvider struct {
	homeDir string
	mu      sync.Mutex
	status  watcher.ProviderStatus
}

// NewCursorProvider creates a Cursor transcript adapter. If homeDir is empty,
// os.UserHomeDir() is used.
func NewCursorProvider(homeDir string) *CursorProvider {
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	root := filepath.Join(homeDir, ".cursor", "projects")
	healthy := true
	if _, err := os.Stat(root); os.IsNotExist(err) {
		healthy = false
	}

	return &CursorProvider{
		homeDir: homeDir,
		status: watcher.ProviderStatus{
			Name:        "cursor",
			Description: "Watches Cursor agent-transcript JSONL files",
			Healthy:     healthy,
			WatchPath:   root,
			Config: map[string]string{
				"home_dir": homeDir,
				"format":   "jsonl",
			},
		},
	}
}

func (p *CursorProvider) Name() string        { return "cursor" }
func (p *CursorProvider) Description() string  { return "Watches Cursor agent-transcript JSONL files" }

// cursorMsg is a single line in a Cursor JSONL transcript file.
type cursorMsg struct {
	Role    string `json:"role"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func (m *cursorMsg) textContent() string {
	var parts []string
	for _, c := range m.Message.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (p *CursorProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	root := filepath.Join(p.homeDir, ".cursor", "projects")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		p.status.Healthy = false
		p.status.LastScanAt = time.Now()
		return nil, nil
	}

	var raws []watcher.RawMemory

	projectEntries, err := os.ReadDir(root)
	if err != nil {
		p.status.Healthy = false
		p.status.LastError = err.Error()
		return nil, err
	}

	for _, projectEntry := range projectEntries {
		if !projectEntry.IsDir() {
			continue
		}
		projectID := projectEntry.Name()
		transcriptsDir := filepath.Join(root, projectID, "agent-transcripts")

		transcriptDirs, err := os.ReadDir(transcriptsDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			slog.Warn("cursor: read transcripts", "dir", transcriptsDir, "err", err)
			continue
		}

		for _, td := range transcriptDirs {
			if !td.IsDir() {
				continue
			}
			transcriptID := td.Name()
			jsonlPath := filepath.Join(transcriptsDir, transcriptID, transcriptID+".jsonl")

			info, err := os.Stat(jsonlPath)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				continue
			}

			raw, err := p.parseJSONL(jsonlPath, projectID, info.ModTime())
			if err != nil {
				slog.Warn("cursor: parse jsonl", "path", jsonlPath, "err", err)
				continue
			}
			if raw != nil {
				raws = append(raws, *raw)
			}
		}
	}

	// Sort by modification time (most recent first).
	sort.Slice(raws, func(i, j int) bool {
		return raws[i].ModifiedAt.After(raws[j].ModifiedAt)
	})

	p.status.Healthy = true
	p.status.LastScanAt = time.Now()
	p.status.TotalSeen += len(raws)

	return raws, nil
}

func (p *CursorProvider) parseJSONL(path, projectID string, modTime time.Time) (*watcher.RawMemory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var userTexts []string
	var assistantTexts []string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg cursorMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		text := msg.textContent()
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

	// Build content from user + assistant messages.
	content := strings.Join(append(userTexts, assistantTexts...), "\n")

	raw := &watcher.RawMemory{
		ProviderName: "cursor",
		SourceURI:    path,
		Content:      content,
		ModifiedAt:   modTime,
		ProjectID:    cursorProjectToReadable(projectID),
		Metadata: map[string]string{
			"raw_project_id":  projectID,
			"user_msg_count":  itoa(len(userTexts)),
			"asst_msg_count":  itoa(len(assistantTexts)),
		},
	}
	raw.HashContent()
	return raw, nil
}

func (p *CursorProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}

// cursorProjectToReadable converts a Cursor workspace ID to a readable name.
func cursorProjectToReadable(workspaceID string) string {
	parts := strings.Split(workspaceID, "-")
	if len(parts) >= 3 && parts[0] == "Users" {
		rest := parts[2:]
		if len(rest) > 0 {
			return strings.Join(rest, "-")
		}
	}
	return workspaceID
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
