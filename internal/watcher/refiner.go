package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/embedder"
	"github.com/vatbrain/vatbrain/internal/llm"
	"github.com/vatbrain/vatbrain/internal/models"
)

const defaultRefineSystemPrompt = `You are a memory refinement engine for an AI agent memory system.
Your task is to analyze a raw memory entry and extract structured metadata.

Input fields:
- provider: the agent that produced this memory
- project: project identifier
- memory_type: type from the agent's own classification (user, feedback, project, reference)
- content: the raw memory text

Output a JSON object with these fields:
{
  "summary": "One concise paragraph describing what this memory is about (≤500 chars)",
  "language": "go | typescript | python | rust | proto | zh | unknown",
  "task_type": "debug | feature | refactor | review | unknown",
  "entity_id": "a code entity reference like func:Name, pkg:name, file:path/to/file.go, or empty string",
  "project_id": "normalized project identifier",
  "key_entities": ["list", "of", "key", "entity", "names"],
  "confidence": 0.0-1.0
}

Only respond with the JSON object, no other text.`

// Refiner converts RawMemory into EpisodicMemory via LLM extraction with
// automatic heuristic fallback.
type Refiner struct {
	LLMClient    llm.Client
	Embedder     embedder.Embedder
	SystemPrompt string
}

// NewRefiner creates a Refiner. If systemPrompt is empty, the default prompt is used.
func NewRefiner(llmClient llm.Client, emb embedder.Embedder, systemPrompt string) *Refiner {
	if systemPrompt == "" {
		systemPrompt = defaultRefineSystemPrompt
	}
	return &Refiner{
		LLMClient:    llmClient,
		Embedder:     emb,
		SystemPrompt: systemPrompt,
	}
}

// refineOutput is the expected JSON structure from the LLM.
type refineOutput struct {
	Summary     string   `json:"summary"`
	Language    string   `json:"language"`
	TaskType    string   `json:"task_type"`
	EntityID    string   `json:"entity_id"`
	ProjectID   string   `json:"project_id"`
	KeyEntities []string `json:"key_entities"`
	Confidence  float64  `json:"confidence"`
}

// Refine converts a RawMemory into an EpisodicMemory. Returns nil if
// confidence is below the threshold or the raw memory should be skipped.
func (r *Refiner) Refine(ctx context.Context, raw RawMemory) (*models.EpisodicMemory, error) {
	var out refineOutput

	if r.LLMClient != nil {
		llmOut, err := r.refineWithLLM(ctx, raw)
		if err != nil {
			slog.Warn("refiner: LLM extraction failed, falling back to heuristic",
				"err", err, "source", raw.SourceURI)
			out = r.refineHeuristic(raw)
		} else {
			out = llmOut
		}
	} else {
		out = r.refineHeuristic(raw)
	}

	if out.Confidence < 0.5 {
		return nil, nil
	}

	taskType := models.TaskType(out.TaskType)
	switch taskType {
	case models.TaskTypeDebug, models.TaskTypeFeature,
		models.TaskTypeRefactor, models.TaskTypeReview:
		// valid
	default:
		taskType = models.TaskTypeFeature
	}

	summary := out.Summary
	if summary == "" {
		summary = truncateRunes(raw.Content, 500)
	}

	projectID := out.ProjectID
	if projectID == "" {
		projectID = raw.ProjectID
	}

	createdAt := raw.ModifiedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	mem := &models.EpisodicMemory{
		ID:                 uuid.New(),
		ProjectID:          projectID,
		Language:           out.Language,
		TaskType:           taskType,
		Summary:            summary,
		SourceType:         models.SourceTypeLLM,
		TrustLevel:         models.DefaultTrustLevel,
		Weight:             models.DefaultWeight,
		EffectiveFrequency: 1.0,
		CreatedAt:          createdAt,
		EntityGroup:        out.EntityID,
	}

	// Embed the summary for vector search.
	if r.Embedder != nil {
		vec, err := r.Embedder.Embed(ctx, summary)
		if err != nil {
			slog.Warn("refiner: embed failed", "err", err)
		} else {
			mem.ContextVector = vec
		}
	}

	return mem, nil
}

// refineWithLLM sends the raw memory to the LLM for structured extraction.
func (r *Refiner) refineWithLLM(ctx context.Context, raw RawMemory) (refineOutput, error) {
	userPrompt := fmt.Sprintf(
		"provider: %s\nproject: %s\nmemory_type: %s\ncontent:\n%s",
		raw.ProviderName,
		raw.ProjectID,
		raw.Metadata["memory_type"],
		raw.Content,
	)

	resp, err := r.LLMClient.Chat(ctx, r.SystemPrompt, userPrompt)
	if err != nil {
		return refineOutput{}, fmt.Errorf("llm chat: %w", err)
	}

	// Strip markdown code fences if present.
	resp = strings.TrimSpace(resp)
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	var out refineOutput
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return refineOutput{}, fmt.Errorf("parse llm response: %w (raw: %.200s)", err, resp)
	}

	return out, nil
}

// refineHeuristic performs keyword-based extraction when LLM is unavailable.
func (r *Refiner) refineHeuristic(raw RawMemory) refineOutput {
	content := raw.Content
	if content == "" {
		for _, v := range raw.FrontMatter {
			content += v + " "
		}
	}

	language := inferLanguage(raw.ProjectID, content)
	taskType := inferTaskType(content)
	entityID := extractEntityID(content)
	summary := buildSummary(content, raw.FrontMatter)

	confidence := 0.6 // heuristic always has moderate confidence
	if content == "" {
		confidence = 0.3
	}

	return refineOutput{
		Summary:    summary,
		Language:   language,
		TaskType:   string(taskType),
		EntityID:   entityID,
		ProjectID:  raw.ProjectID,
		Confidence: confidence,
	}
}

// --- Heuristic helpers (mirrors import_cursor.go logic) ---

var entityRe = regexp.MustCompile(`@[\w./-]+\.(go|proto|ts|tsx|js|py|java|rs)`)

func extractEntityID(content string) string {
	if m := entityRe.FindString(content); m != "" {
		return strings.TrimPrefix(m, "@")
	}
	return ""
}

func inferLanguage(projectID, content string) string {
	allText := strings.ToLower(projectID + " " + content)
	indicators := map[string]string{
		"go": "go", "golang": "go",
		"proto": "proto", "protobuf": "proto",
		"typescript": "typescript", "ts": "typescript", "tsx": "typescript",
		"python": "python", "py": "python",
		"rust": "rust", "rs": "rust",
		"java": "java",
		"javascript": "javascript", "js": "javascript",
	}
	for keyword, lang := range indicators {
		if strings.Contains(allText, keyword) {
			return lang
		}
	}
	return "zh"
}

func inferTaskType(content string) models.TaskType {
	lower := strings.ToLower(content)

	debugWords := []string{"bug", "错误", "报错", "error", "panic", "debug", "修复", "fix", "crash", "崩溃"}
	for _, w := range debugWords {
		if strings.Contains(lower, w) {
			return models.TaskTypeDebug
		}
	}

	refactorWords := []string{"重构", "refactor", "拆分", "extract", "rename", "重命名"}
	for _, w := range refactorWords {
		if strings.Contains(lower, w) {
			return models.TaskTypeRefactor
		}
	}

	reviewWords := []string{"review", "审核", "检查", "审计", "audit"}
	for _, w := range reviewWords {
		if strings.Contains(lower, w) {
			return models.TaskTypeReview
		}
	}

	return models.TaskTypeFeature
}

func buildSummary(content string, fm map[string]string) string {
	if name, ok := fm["name"]; ok && name != "" {
		if desc, ok := fm["description"]; ok && desc != "" {
			return name + ": " + desc
		}
		return name
	}
	return truncateRunes(content, 500)
}

func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
