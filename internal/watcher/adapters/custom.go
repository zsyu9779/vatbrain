package adapters

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vatbrain/vatbrain/internal/watcher"
)

// CustomProviderConfig defines an agent's memory format via YAML configuration.
// It is the deserialised form of a custom adapter config file.
type CustomProviderConfig struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Enabled     bool         `yaml:"enabled"`
	Watch       WatchConfig  `yaml:"watch"`
	Format      FormatConfig `yaml:"format"`
}

type WatchConfig struct {
	Paths           []string `yaml:"paths"`
	ExcludePatterns []string `yaml:"exclude_patterns"`
	PollInterval    string   `yaml:"poll_interval"` // e.g. "10s"
}

type FormatConfig struct {
	Type          string            `yaml:"type"` // yaml_frontmatter | json_lines | raw_text
	FieldMappings FieldMappings     `yaml:"field_mappings"`
	Metadata      map[string]string `yaml:"metadata"`
}

type FieldMappings struct {
	Content   string `yaml:"content"`    // source field for RawMemory.Content (required)
	ProjectID string `yaml:"project_id"` // (optional)
	SessionID string `yaml:"session_id"` // (optional)
}

// CustomProvider implements MemoryProvider for user-defined agent memory formats.
type CustomProvider struct {
	cfg    CustomProviderConfig
	mu     sync.Mutex
	status watcher.ProviderStatus
}

// NewCustomProvider creates a provider from a YAML config file path.
func NewCustomProvider(configPath string) (*CustomProvider, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("custom adapter: read config: %w", err)
	}

	var cfg CustomProviderConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("custom adapter: parse config: %w", err)
	}

	if cfg.Format.FieldMappings.Content == "" {
		return nil, fmt.Errorf("custom adapter: format.field_mappings.content is required")
	}

	watchPath := ""
	if len(cfg.Watch.Paths) > 0 {
		watchPath = cfg.Watch.Paths[0]
		// Expand ~ to home directory.
		if strings.HasPrefix(watchPath, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				watchPath = filepath.Join(home, watchPath[2:])
			}
		}
	}

	healthy := true
	if watchPath != "" {
		if _, err := os.Stat(watchPath); os.IsNotExist(err) {
			healthy = false
		}
	} else {
		healthy = false
	}

	return &CustomProvider{
		cfg: cfg,
		status: watcher.ProviderStatus{
			Name:        cfg.Name,
			Description: cfg.Description,
			Healthy:     healthy,
			WatchPath:   watchPath,
			Config: map[string]string{
				"config_path": configPath,
				"format":      cfg.Format.Type,
			},
		},
	}, nil
}

func (p *CustomProvider) Name() string        { return p.cfg.Name }
func (p *CustomProvider) Description() string  { return p.cfg.Description }

func (p *CustomProvider) Scan(ctx context.Context) ([]watcher.RawMemory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var allRaws []watcher.RawMemory

	for _, pattern := range p.cfg.Watch.Paths {
		expanded := pattern
		if strings.HasPrefix(expanded, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				expanded = filepath.Join(home, expanded[2:])
			}
		}

		// Handle glob patterns.
		matches, err := filepath.Glob(expanded)
		if err != nil {
			slog.Warn("custom: glob error", "pattern", expanded, "err", err)
			continue
		}

		for _, path := range matches {
			if p.isExcluded(path) {
				continue
			}

			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}

			raw, err := p.parseFile(path, info.ModTime())
			if err != nil {
				slog.Warn("custom: parse file", "path", path, "err", err)
				continue
			}
			if raw != nil {
				allRaws = append(allRaws, *raw)
			}
		}
	}

	p.status.Healthy = true
	p.status.LastScanAt = time.Now()
	p.status.TotalSeen += len(allRaws)

	return allRaws, nil
}

func (p *CustomProvider) isExcluded(path string) bool {
	for _, pat := range p.cfg.Watch.ExcludePatterns {
		if matched, _ := filepath.Match(pat, filepath.Base(path)); matched {
			return true
		}
	}
	return false
}

func (p *CustomProvider) parseFile(path string, modTime time.Time) (*watcher.RawMemory, error) {
	var content string
	var frontMatter map[string]string

	switch p.cfg.Format.Type {
	case "yaml_frontmatter":
		fm, body, err := parseYAMLFrontmatter(path)
		if err != nil {
			return nil, err
		}
		frontMatter = fm
		content = body
	case "json_lines":
		body, err := parseJSONLines(path)
		if err != nil {
			return nil, err
		}
		content = body
	default: // "raw_text"
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content = string(data)
	}

	if content == "" && len(frontMatter) == 0 {
		return nil, nil
	}

	raw := &watcher.RawMemory{
		ProviderName: p.cfg.Name,
		SourceURI:    path,
		Content:      content,
		FrontMatter:  frontMatter,
		ModifiedAt:   modTime,
		Metadata:     make(map[string]string),
	}

	// Apply field mappings.
	if p.cfg.Format.FieldMappings.ProjectID != "" {
		if v, ok := frontMatter[p.cfg.Format.FieldMappings.ProjectID]; ok {
			raw.ProjectID = v
		}
	}
	if p.cfg.Format.FieldMappings.SessionID != "" {
		if v, ok := frontMatter[p.cfg.Format.FieldMappings.SessionID]; ok {
			raw.AgentSessionID = v
		}
	}
	if raw.ProjectID == "" {
		raw.ProjectID = p.cfg.Name
	}

	// Copy configured metadata fields.
	for metaKey, srcField := range p.cfg.Format.Metadata {
		if v, ok := frontMatter[srcField]; ok {
			raw.Metadata[metaKey] = v
		}
	}

	raw.HashContent()
	return raw, nil
}

func (p *CustomProvider) Status() watcher.ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Watching = true
	return s
}

// parseYAMLFrontmatter reads a file with optional YAML frontmatter.
func parseYAMLFrontmatter(path string) (map[string]string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	var fmLines []string
	var bodyLines []string
	inFrontMatter := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	lineNum := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		if lineNum == 1 && strings.TrimSpace(line) == "---" {
			inFrontMatter = true
			continue
		}
		if inFrontMatter && strings.TrimSpace(line) == "---" {
			inFrontMatter = false
			continue
		}

		if inFrontMatter {
			fmLines = append(fmLines, line)
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, "", err
	}

	fm := make(map[string]string)
	if len(fmLines) > 0 {
		var yamlMap map[string]any
		yamlStr := strings.Join(fmLines, "\n")
		if err := yaml.Unmarshal([]byte(yamlStr), &yamlMap); err == nil {
			for k, v := range yamlMap {
				fm[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	return fm, strings.TrimSpace(strings.Join(bodyLines, "\n")), nil
}

// parseJSONLines reads a JSONL file and concatenates "text" fields.
func parseJSONLines(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var parts []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if t, ok := m["text"].(string); ok {
			parts = append(parts, t)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return strings.Join(parts, "\n"), nil
}
