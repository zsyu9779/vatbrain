# VatBrain v0.2 — Pitfall LLM 提取引擎

> 所属 Phase：Phase 2 — Pitfall 提取引擎
>
> 前置阅读：`../00-design.md`、`02a-pitfall-clustering.md`
>
> 本文档定义从聚类后的 PitfallCandidate 到结构化 PitfallMemory 节点的 LLM 提取全流程。

---

## 1. 职责边界

```
02a 产出：PitfallCandidate[]  (聚类后的候选集)
        │
        ▼
02b 本文档：LLM 提取 + 回测去重 + 持久化
        │
        ▼
    产出：PitfallMemory[]  (写入 Store)
```

本文档不涉及聚类策略（见 02a）、不涉及 ConsolidationEngine 并行化（见 02c）。

---

## 2. LLM 提取 Prompt

### 2.1 System Prompt

```
You are an error pattern analyst for a code memory system. Given a cluster
of debug sessions about the SAME code entity, identify distinct error
patterns. Each debug session is summarized by a developer or AI agent.

CRITICAL RULES:
- The same entity can have MULTIPLE distinct bugs. Analyze carefully.
- Output each distinct bug as a SEPARATE object in the JSON array.
- If all sessions describe the same bug, output a single-object array.
- If there is insufficient information to identify any pattern, output [].
- Each signature must be reusable — not a specific traceback, but a
  pattern description another developer could recognize.
- fix_strategy must be ACTIONABLE: "Increase MaxOpenConns from default
  50 to >= 200 for production environments" — NOT "fix the connection pool".

OUTPUT FORMAT (JSON array):
[
  {
    "signature": "one-line error pattern description (≤ 200 chars)",
    "root_cause_category": "CONCURRENCY | RESOURCE_EXHAUSTION | CONFIG | CONTRACT_VIOLATION | LOGIC_ERROR | UNKNOWN",
    "fix_strategy": "≤ 500 chars: concrete, actionable fix strategy",
    "confidence": 0.0-1.0,
    "matched_indices": [0, 2, 5]  // which summaries this pitfall covers
  }
]

ROOT CAUSE CATEGORY GUIDE:
- CONCURRENCY: race conditions, deadlocks, goroutine leaks, lock contention
- RESOURCE_EXHAUSTION: connection pool exhaustion, OOM, file descriptor limits,
  disk full
- CONFIG: wrong config values, missing env vars, incorrect defaults
- CONTRACT_VIOLATION: API contract mismatch, type errors, nil pointer from
  unexpected input shape
- LOGIC_ERROR: incorrect algorithm, wrong conditional, off-by-one
- UNKNOWN: use ONLY when genuinely cannot determine — avoid as default

CONFIDENCE GUIDE:
- 0.9-1.0: clear pattern, explicit mention of root cause in summaries
- 0.7-0.8: reasonable inference from consistent symptoms
- 0.5-0.6: plausible but speculative
- below 0.5: discard — don't output
```

### 2.2 User Prompt 模板

```
Entity: {entity_id} ({entity_type})
Project: {project_id} | Language: {language}
Total debug sessions for this entity: {count}

Debug session summaries:
{for i, summary in summaries}
[{i}] {summary}
{end}

Analyze these {count} debug sessions and extract structured error patterns.
Remember: the same entity can have multiple distinct bugs.
```

### 2.3 Prompt 组装规则

```go
// buildPitfallExtractionPrompt builds the user prompt for a single entity group.
// It handles truncation and formatting.
func buildPitfallExtractionPrompt(candidate PitfallCandidate) string {
    var b strings.Builder
    b.WriteString(fmt.Sprintf("Entity: %s\n", candidate.EntityID))
    b.WriteString(fmt.Sprintf("Project: %s | Language: %s\n", candidate.ProjectID, candidate.Language))
    b.WriteString(fmt.Sprintf("Total debug sessions: %d\n\n", len(candidate.Summaries)))
    b.WriteString("Debug session summaries:\n")

    for i, s := range candidate.Summaries {
        // 单条 summary 截断到 500 字符
        truncated := s
        if len(s) > 500 {
            truncated = s[:497] + "..."
        }
        b.WriteString(fmt.Sprintf("[%d] %s\n", i, truncated))
    }
    return b.String()
}
```

**汇总 token 预算**：
- System prompt ~700 tokens
- User prompt：每条 summary 平均 100 tokens × 最多 50 条 = 5000 tokens
- 总计每 LLM 调用 ~6000 tokens 输入，输出 ~200-500 tokens
- 按 10 个 entity group 计算 = 10 次 LLM 调用，总计 ~60K input tokens

---

## 3. LLM 输出解析

### 3.1 响应结构

```go
// PitfallLLMOutput is the parsed JSON from one LLM extraction call.
type PitfallLLMOutput struct {
    Signature        string  `json:"signature"`
    RootCauseCategory string `json:"root_cause_category"`
    FixStrategy      string  `json:"fix_strategy"`
    Confidence       float64 `json:"confidence"`
    MatchedIndices   []int   `json:"matched_indices"`
}
```

### 3.2 解析与校验

```go
func parsePitfallResponse(raw string, candidate PitfallCandidate) ([]models.PitfallMemory, error) {
    var outputs []PitfallLLMOutput
    if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
        // 尝试修复常见 JSON 错误
        outputs = recoverJSON(raw)
        if outputs == nil {
            return nil, fmt.Errorf("pitfall llm parse: %w", err)
        }
    }

    var results []models.PitfallMemory
    for _, o := range outputs {
        // 校验
        if o.Confidence < 0.5 {
            continue // 低置信度丢弃
        }
        if !isValidRootCause(o.RootCauseCategory) {
            o.RootCauseCategory = "UNKNOWN"
        }
        if len(o.Signature) > 200 {
            o.Signature = o.Signature[:197] + "..."
        }
        if len(o.FixStrategy) > 500 {
            o.FixStrategy = o.FixStrategy[:497] + "..."
        }

        // 收集 matched_indices 对应的 episodic IDs
        var sourceIDs []uuid.UUID
        for _, idx := range o.MatchedIndices {
            if idx >= 0 && idx < len(candidate.EpisodicIDs) {
                sourceIDs = append(sourceIDs, candidate.EpisodicIDs[idx])
            }
        }

        now := time.Now().UTC()
        pm := models.PitfallMemory{
            ID:                uuid.New(),
            EntityID:          candidate.EntityID,
            ProjectID:         candidate.ProjectID,
            Language:          candidate.Language,
            Signature:         o.Signature,
            RootCauseCategory: models.RootCause(o.RootCauseCategory),
            FixStrategy:       o.FixStrategy,
            Confidence:        o.Confidence,
            SourceType:        models.SourceTypeLLM,
            TrustLevel:        models.DefaultTrustLevel,
            Weight:            1.0,
            OccurrenceCount:   len(o.MatchedIndices),
            LastOccurredAt:    &now,
            CreatedAt:         now,
            UpdatedAt:         now,
        }
        results = append(results, pm)
    }
    return results, nil
}
```

### 3.3 JSON 恢复策略

LLM 偶尔输出不符合 schema 的 JSON（多了逗号、少了引号、用 markdown 包裹等）：

```go
func recoverJSON(raw string) []PitfallLLMOutput {
    // 1. 去掉 ```json ... ``` 包裹
    raw = strings.TrimSpace(raw)
    raw = strings.TrimPrefix(raw, "```json")
    raw = strings.TrimPrefix(raw, "```")
    raw = strings.TrimSuffix(raw, "```")
    raw = strings.TrimSpace(raw)

    // 2. 尝试找到第一个 '[' 和最后一个 ']'
    start := strings.Index(raw, "[")
    end := strings.LastIndex(raw, "]")
    if start >= 0 && end > start {
        raw = raw[start : end+1]
    }

    // 3. 重新解析
    var outputs []PitfallLLMOutput
    if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
        return nil
    }
    return outputs
}
```

### 3.4 RootCause 校验

```go
var validRootCauses = map[string]bool{
    "CONCURRENCY":         true,
    "RESOURCE_EXHAUSTION": true,
    "CONFIG":              true,
    "CONTRACT_VIOLATION":  true,
    "LOGIC_ERROR":         true,
    "UNKNOWN":             true,
}

func isValidRootCause(s string) bool {
    return validRootCauses[strings.ToUpper(strings.TrimSpace(s))]
}
```

---

## 4. 回测：去重与排他性验证

### 4.1 去重检查

LLM 提取完成后，跨所有 entity 检查是否存在重复 Pitfall（不同 entity 上相同的签名 + root cause）：

```go
func deduplicatePitfalls(
    ctx context.Context,
    pitfalls []models.PitfallMemory,
    emb embedder.Embedder,
    threshold float64, // 默认 0.9
) ([]models.PitfallMemory, int, error) {
    // 1. 为每个 pitfall 的 signature 生成 embedding
    embeddings := make([][]float64, len(pitfalls))
    for i, p := range pitfalls {
        // embedding 输入 = root_cause + "|" + signature + "|" + fix_strategy
        input := fmt.Sprintf("%s|%s|%s", p.RootCauseCategory, p.Signature, p.FixStrategy)
        emb_, err := emb.Embed(ctx, input)
        if err != nil {
            return nil, 0, fmt.Errorf("embed pitfall %s: %w", p.ID, err)
        }
        embeddings[i] = emb_
    }

    // 2. 两两比较，相似度 > threshold 的合并
    merged := 0
    for i := 0; i < len(pitfalls); i++ {
        if pitfalls[i].ObsoletedAt != nil {
            continue // 已被合并
        }
        for j := i + 1; j < len(pitfalls); j++ {
            if pitfalls[j].ObsoletedAt != nil {
                continue
            }
            if cosineSimilarity(embeddings[i], embeddings[j]) > threshold {
                // 合并 j 到 i（详见 00-design.md 4.4 节合并策略）
                mergePitfall(&pitfalls[i], &pitfalls[j])
                merged++
            }
        }
    }

    // 3. 过滤掉被合并的
    var result []models.PitfallMemory
    for _, p := range pitfalls {
        if p.ObsoletedAt == nil {
            result = append(result, p)
        }
    }
    return result, merged, nil
}
```

### 4.2 合并执行

```go
func mergePitfall(keep, discard *models.PitfallMemory) {
    // occurrence_count 累加
    keep.OccurrenceCount += discard.OccurrenceCount
    // fix_strategy 保留更长的
    if len(discard.FixStrategy) > len(keep.FixStrategy) {
        keep.FixStrategy = discard.FixStrategy
    }
    // last_occurred_at 取较新的
    if discard.LastOccurredAt != nil &&
        (keep.LastOccurredAt == nil || discard.LastOccurredAt.After(*keep.LastOccurredAt)) {
        keep.LastOccurredAt = discard.LastOccurredAt
    }
    // was_user_corrected：任一为 true = true
    if discard.WasUserCorrected {
        keep.WasUserCorrected = true
    }
    // 被合并方标记为废弃
    now := time.Now().UTC()
    discard.ObsoletedAt = &now
    discard.Weight *= 0.5
}
```

---

## 5. Pitfall 提取全流程

```go
// PitfallExtractor runs the full extraction pipeline for one consolidation run.
type PitfallExtractor struct {
    Embedder        embedder.Embedder
    LLMClient       LLMClient
    MinClusterSize  int     // 默认 3
    MergeThreshold  float64 // 默认 0.85（聚类用）
    DedupThreshold  float64 // 默认 0.9（去重用）
    MaxConcurrency  int     // 默认 5，LLM 调用并发数
}

func (pe *PitfallExtractor) Extract(
    ctx context.Context,
    debugEpisodics []store.EpisodicScanItem,
) (pitfalls []models.PitfallMemory, candidatesFound int, merged int, err error) {

    // Step 1: 聚类
    entityGroups := groupByEntity(debugEpisodics)
    var candidates []PitfallCandidate
    for _, eg := range entityGroups {
        if len(eg.Episodics) < pe.MinClusterSize {
            continue
        }
        subClusters := subClusterBySignature(ctx, eg, pe.Embedder, pe.MergeThreshold)
        for _, sc := range subClusters {
            var ids []uuid.UUID
            var summaries []string
            var totalWeight float64
            for _, ep := range sc.Episodics {
                ids = append(ids, ep.ID)
                summaries = append(summaries, ep.Summary)
                totalWeight += ep.Weight
            }
            candidates = append(candidates, PitfallCandidate{
                EntityID:    eg.EntityID,
                ProjectID:   eg.ProjectID,
                Language:    eg.Language,
                EpisodicIDs: ids,
                Summaries:   summaries,
                ClusterSize: len(ids),
                AvgWeight:   totalWeight / float64(len(ids)),
            })
        }
    }
    candidatesFound = len(candidates)
    if len(candidates) == 0 {
        return nil, 0, 0, nil
    }

    // Step 2: LLM 提取（并发）
    var mu sync.Mutex
    sem := make(chan struct{}, pe.MaxConcurrency)
    var wg sync.WaitGroup
    var allPitfalls []models.PitfallMemory
    var firstErr error

    for _, c := range candidates {
        wg.Add(1)
        go func(cand PitfallCandidate) {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()

            prompt := pe.buildPrompt(cand)
            raw, err := pe.LLMClient.Chat(ctx, systemPromptPitfall, prompt)
            if err != nil {
                mu.Lock()
                if firstErr == nil {
                    firstErr = err
                }
                mu.Unlock()
                return
            }

            parsed, err := parsePitfallResponse(raw, cand)
            if err != nil {
                mu.Lock()
                if firstErr == nil {
                    firstErr = err
                }
                mu.Unlock()
                return
            }

            mu.Lock()
            allPitfalls = append(allPitfalls, parsed...)
            mu.Unlock()
        }(c)
    }
    wg.Wait()

    // 容忍部分 LLM 调用失败——返回已成功的
    if len(allPitfalls) == 0 && firstErr != nil {
        return nil, candidatesFound, 0, firstErr
    }

    // Step 3: 去重
    pitfalls, merged, err = deduplicatePitfalls(ctx, allPitfalls, pe.Embedder, pe.DedupThreshold)
    if err != nil {
        // 去重失败不阻断——返回未去重的结果
        return allPitfalls, candidatesFound, 0, nil
    }

    return pitfalls, candidatesFound, merged, nil
}
```

---

## 6. LLM Client 接口

```go
// LLMClient 抽象 LLM 调用，便于测试 mock。
// 与 semantic rule 提取共用同一接口。
type LLMClient interface {
    Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
```

Claude API 实现（`internal/llm/claude_client.go`）：

```go
type ClaudeClient struct {
    APIKey     string
    BaseURL    string
    Model      string // 默认 "claude-sonnet-4-6"（性价比最高的模型用于结构化提取）
    MaxRetries int    // 默认 3
    HTTPClient *http.Client
}
```

**模型选择**：Pitfall 提取是结构化 JSON 输出任务，不需要最强大的推理能力。用 Sonnet 4.6 而非 Opus 4.7，控制成本。

---

## 7. 错误处理矩阵

| 错误类型 | 处理策略 | 是否阻断 |
|---------|---------|---------|
| LLM 调用超时 | 重试 2 次，仍失败则跳过该 candidate | 否（部分成功） |
| LLM 返回非 JSON | `recoverJSON` 尝试修复 → 仍失败则跳过 | 否 |
| JSON 解析成功但字段缺失 | `signature` / `root_cause_category` 为空 → 跳过该对象 | 否（丢弃单条） |
| `matched_indices` 越界 | 忽略越界索引，只用合法的 | 否 |
| Embedder 调用失败 | 该 candidate 跳过 | 否（部分成功） |
| 全部 candidate 失败 | 返回 error | 是（整个 Pitfall 线失败） |

核心原则：**Pitfall 线失败不阻断规则线**。ConsolidationEngine.Run 捕获 Pitfall 错误并写入 `result.PitfallError`，规则线独立完成。

---

## 8. 测试策略

### 单元测试

| 场景 | 验证点 |
|------|--------|
| `parsePitfallResponse` 正常 JSON 单对象 | 1 个 Pitfall，字段正确 |
| `parsePitfallResponse` 正常 JSON 多对象 | N 个 Pitfall，matched_indices 不同 |
| `parsePitfallResponse` JSON 被 markdown 包裹 | recoverJSON 成功去掉 ``` |
| `parsePitfallResponse` 完全畸形 | 返回 error |
| `parsePitfallResponse` confidence < 0.5 | 被过滤掉 |
| `parsePitfallResponse` invalid root_cause | 自动修正为 UNKNOWN |
| `parsePitfallResponse` signature > 200 chars | 截断 |
| `deduplicatePitfalls` 两个相同 Pitfall | 合并为 1 个，occurrence_count 累加 |
| `deduplicatePitfalls` 两个不同 Pitfall | 保持独立 |
| `buildPrompt` 50 条 summaries | prompt 完整，无截断丢失 |

### Mock LLM Client

```go
type MockLLMClient struct {
    Response string
    Err      error
}

func (m *MockLLMClient) Chat(ctx context.Context, sys, user string) (string, error) {
    return m.Response, m.Err
}
```

---

*本文档与 `02a-pitfall-clustering.md` 和 `02c-consolidation-parallel.md` 共同构成 Phase 2 的完整设计。*
