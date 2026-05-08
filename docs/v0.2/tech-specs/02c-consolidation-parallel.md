# VatBrain v0.2 — ConsolidationEngine 并行化

> 所属 Phase：Phase 2 — Pitfall 提取引擎
>
> 前置阅读：`../00-design.md`（第 4 节）、`02a-pitfall-clustering.md`、`02b-pitfall-llm-extraction.md`
>
> 本文档定义 ConsolidationEngine.Run 如何从单一规则线扩展为语义规则 + Pitfall 并行执行。

---

## 1. Current State（v0.1）

当前 `ConsolidationEngine.Run`（`internal/core/consolidation_engine.go`）：

```go
func (e *ConsolidationEngine) Run(
    ctx context.Context,
    s store.MemoryStore,
    emb embedder.Embedder,
) (models.ConsolidationRunResult, error)
```

流程：

```
scan → cluster (by project_id + task_type) → extract → backtest → persist
                                                         ↓
                                               ConsolidationRunResult
```

特点：
- 单线执行，串行
- 聚类维度单一（project_id + task_type）
- 只产出 SemanticMemory
- `ConsolidationRunResult` 无 Pitfall 统计

---

## 2. Target State（v0.2）

### 2.1 扩展流程

```
scan (all episodics, shared)
    │
    ├─→ filter: debug + entity_id 非空    ← 新增过滤
    │       │
    │       ▼
    │   cluster_by_entity → sub_cluster  ← 新增（见 02a）
    │       │
    │       ▼
    │   extract_pitfalls (LLM) → dedup   ← 新增（见 02b）
    │       │
    │       ▼
    │   persist pitfalls + DERIVED_FROM edges
    │
    └─→ cluster_by_pattern (existing: project_id + task_type)
            │
            ▼
        extract_rules → backtest (existing)
            │
            ▼
        persist semantic rules + DERIVED_FROM edges

                    ↓
    merged ConsolidationRunResult (rules + pitfalls)
```

两条线共享同一批 scan 结果，**并行执行**，互不阻断。

### 2.2 新的 Run 签名

```go
func (e *ConsolidationEngine) Run(
    ctx context.Context,
    s store.MemoryStore,
    emb embedder.Embedder,
    llm LLMClient,           // v0.2 新增：LLM 客户端
) (models.ConsolidationRunResult, error)
```

新增 `llm LLMClient` 参数。当前 v0.1 的 `extractRule` 是纯字符串拼接（不调 LLM），因此 v0.1 必须同步完成 Phase 0 的 LLM 提炼才兼容此签名。短期过渡：传入 nil 时退化为 v0.1 行为（纯规则线，Pitfall 线跳过）。

---

## 3. 并行执行模式

### 3.1 实现

```go
func (e *ConsolidationEngine) Run(
    ctx context.Context,
    s store.MemoryStore,
    emb embedder.Embedder,
    llm LLMClient,
) (models.ConsolidationRunResult, error) {
    runID := uuid.New()
    result := models.ConsolidationRunResult{
        RunID:     runID,
        StartedAt: time.Now().UTC(),
    }

    since := result.StartedAt.Add(-time.Duration(e.HoursToScan * float64(time.Hour)))

    // === SHARED: Scan ===
    episodics, err := s.ScanRecent(ctx, since, 1000)
    if err != nil {
        return result, fmt.Errorf("consolidation scan: %w", err)
    }
    result.EpisodicsScanned = len(episodics)
    if len(episodics) == 0 {
        now := time.Now()
        result.CompletedAt = &now
        return result, nil
    }

    // === PARALLEL: Rules + Pitfalls ===
    var wg sync.WaitGroup
    wg.Add(2)

    // Goroutine 1: Semantic Rules (existing logic)
    go func() {
        defer wg.Done()
        rulesResult := e.runRuleExtraction(ctx, s, emb, llm, episodics, runID)
        mu.Lock()
        result.RulesPersisted = rulesResult.RulesPersisted
        result.CandidateRulesFound = rulesResult.CandidateRulesFound
        result.AverageAccuracy = rulesResult.AverageAccuracy
        if rulesResult.Error != nil {
            result.RulesError = rulesResult.Error.Error()
        }
        mu.Unlock()
    }()

    // Goroutine 2: Pitfall Extraction (new logic)
    go func() {
        defer wg.Done()
        pitfallResult := e.runPitfallExtraction(ctx, s, emb, llm, episodics)
        mu.Lock()
        result.PitfallsExtracted = pitfallResult.Extracted
        result.PitfallsMerged = pitfallResult.Merged
        result.PitfallsPersisted = pitfallResult.Persisted
        if pitfallResult.Error != nil {
            result.PitfallError = pitfallResult.Error.Error()
        }
        mu.Unlock()
    }()

    wg.Wait()

    now := time.Now()
    result.CompletedAt = &now
    return result, nil
}
```

### 3.2 子方法拆分

```go
// ruleExtractionResult holds the output of the semantic rule extraction line.
type ruleExtractionResult struct {
    RulesPersisted     int
    CandidateRulesFound int
    AverageAccuracy    float64
    Error              error
}

func (e *ConsolidationEngine) runRuleExtraction(
    ctx context.Context,
    s store.MemoryStore,
    emb embedder.Embedder,
    llm LLMClient,
    episodics []store.EpisodicScanItem,
    runID uuid.UUID,
) ruleExtractionResult {
    // v0.1 现有逻辑移入此处（clusterByPattern → extractRule → backtest → WriteSemantic）
    // v0.2 若 llm != nil，extractRule 改为调 LLM
}

// pitfallExtractionResult holds the output of the pitfall extraction line.
type pitfallExtractionResult struct {
    Extracted int  // 提取的 Pitfall 候选数（去重前）
    Merged    int  // 去重合并的 Pitfall 数
    Persisted int  // 最终写入的 Pitfall 数
    Error     error
}

func (e *ConsolidationEngine) runPitfallExtraction(
    ctx context.Context,
    s store.MemoryStore,
    emb embedder.Embedder,
    llm LLMClient,
    episodics []store.EpisodicScanItem,
) pitfallExtractionResult {
    // 1. Filter debug + entity_id
    debugEps := filterDebugEpisodics(episodics)

    // 2. 数据量不足则跳过
    if len(debugEps) < 10 {
        return pitfallExtractionResult{}
    }

    // 3. PitfallExtractor.Extract（见 02b）
    extractor := &PitfallExtractor{
        Embedder:       emb,
        LLMClient:      llm,
        MinClusterSize: 3,
        MergeThreshold: 0.85,
        DedupThreshold: 0.9,
        MaxConcurrency: 5,
    }
    pitfalls, candidatesFound, merged, err := extractor.Extract(ctx, debugEps)
    if err != nil {
        return pitfallExtractionResult{Error: err, Extracted: candidatesFound, Merged: merged}
    }

    // 4. Persist
    persisted := 0
    for _, p := range pitfalls {
        if err := s.WritePitfall(ctx, &p); err != nil {
            // best-effort: log + continue
            log.Printf("consolidation: write pitfall %s: %v", p.ID, err)
            continue
        }
        // Create DERIVED_FROM edges
        // (source_episodic_ids 需从 PitfallMemory 模型获取，此处需扩展模型)
        persisted++
    }

    return pitfallExtractionResult{
        Extracted: candidatesFound,
        Merged:    merged,
        Persisted: persisted,
    }
}
```

---

## 4. 数据模型调整

### 4.1 ConsolidationRunResult 扩展

```go
// internal/models/api.go

type ConsolidationRunResult struct {
    RunID              uuid.UUID  `json:"run_id"`
    StartedAt          time.Time  `json:"started_at"`
    CompletedAt        *time.Time `json:"completed_at"`

    // Rules line
    EpisodicsScanned    int     `json:"episodics_scanned"`
    CandidateRulesFound int     `json:"candidate_rules_found"`
    RulesPersisted      int     `json:"rules_persisted"`
    AverageAccuracy     float64 `json:"average_accuracy"`

    // Pitfall line (v0.2 新增)
    PitfallsExtracted int    `json:"pitfalls_extracted"`
    PitfallsMerged    int    `json:"pitfalls_merged"`
    PitfallsPersisted int    `json:"pitfalls_persisted"`

    // Error isolation (v0.2 新增)
    RulesError   string `json:"rules_error,omitempty"`
    PitfallError string `json:"pitfall_error,omitempty"`
}
```

### 4.2 PitfallMemory 模型补充

当前设计草案的 PitfallMemory 无 `SourceEpisodicIDs` 字段。为支持 DERIVED_FROM 溯源链，需补充：

```go
type PitfallMemory struct {
    // ... 现有字段 ...
    SourceEpisodicIDs []uuid.UUID `json:"source_episodic_ids"` // 溯源：产生此 Pitfall 的源情境记忆
}
```

此字段在 LLM 提取阶段由 `matched_indices` 映射填充，持久化时用于创建 DERIVED_FROM 边。

---

## 5. Context 传播与超时控制

```go
// ConsolidationEngine.Run 内部：
// 从父 ctx 派生两个子 ctx，各自有独立的超时时间

// 规则线 ctx：沿用 v0.1 行为，无独立超时
ruleCtx := ctx

// Pitfall 线 ctx：独立超时（LLM 调用可能慢）
pitfallCtx, pitfallCancel := context.WithTimeout(ctx, 120*time.Second)
defer pitfallCancel()
```

Pitfall 线独立超时 120 秒的理由：
- 最多 10 个 entity group × 5 并发 LLM 调用 × 每调用 ~3 秒 = ~6 秒纯 LLM 耗时
- 加上 embedding 批量调用 ~5 秒
- 120 秒提供充足的余量

若 Pitfall 线超时，仅 Pitfall 线返回 `context.DeadlineExceeded` 并写入 `result.PitfallError`，规则线不受影响。

---

## 6. LLMClient 接口定义

```go
// internal/llm/client.go（新文件）

// LLMClient abstracts LLM calls for both rule extraction and pitfall extraction.
type LLMClient interface {
    Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
```

规则线和 Pitfall 线共用同一接口，各传不同的 system prompt。

---

## 7. 错误隔离策略

| 场景 | 规则线行为 | Pitfall 线行为 | 最终 Run 返回值 |
|------|-----------|---------------|---------------|
| 两条线都成功 | 正常返回 | 正常返回 | result, nil |
| 规则线成功，Pitfall 线部分 LLM 失败 | 正常返回 | 返回已成功的 Pitfall | result, nil（PitfallError 为空） |
| 规则线成功，Pitfall 线全部失败 | 正常返回 | 返回 error | result, nil（PitfallError 有值） |
| 规则线失败，Pitfall 线成功 | 返回 error | 正常返回 | result, nil（RulesError 有值） |
| 两条线都失败 | 返回 error | 返回 error | result, rulesErr（优先返回规则线 error） |
| Scan 失败 | — | — | result, err（不执行任何线） |

核心原则：**Run 返回的 error 仅用于基础设施级失败（scan 失败、DB 连接断开）。业务线失败写入 result 的 Error 字段，不通过 error 返回值传播。**

---

## 8. 单元测试

### 8.1 并行执行测试

```go
func TestRun_ParallelExecution(t *testing.T) {
    // Mock MemoryStore + Embedder + LLMClient
    // 验证：
    // 1. 两条线都被调用
    // 2. RulesPersisted 和 PitfallsPersisted 都有值
    // 3. 两条线抛出错误时，各自 Error 字段正确记录
}
```

### 8.2 错误隔离测试

```go
func TestRun_PitfallFailsRulesContinue(t *testing.T) {
    // LLMClient 对 Pitfall prompt 返回 error，对 Rules prompt 正常
    // 验证：RulesPersisted > 0 && PitfallError != ""
}

func TestRun_RulesFailPitfallContinue(t *testing.T) {
    // 反之
}
```

### 8.3 空数据测试

```go
func TestRun_NoDebugEpisodics(t *testing.T) {
    // 所有 episodics 都是 task_type=feature
    // 验证：PitfallsPersisted = 0，PitfallError 为空
}
```

---

## 9. 实现路线（ConsolidationEngine 部分）

1. **扩展 EpisodicScanItem + EntityID**（前置，见 02a Section 2.1）
2. **扩展 ConsolidationRunResult**：新增 Pitfall 统计字段 + Error 字段
3. **重构 Run 方法**：抽取 `runRuleExtraction` + `runPitfallExtraction` 子方法
4. **实现并行 goroutine**：sync.WaitGroup + context 派生
5. **测试**：并行执行 + 错误隔离 + 空数据

---

*本文档与 `02a-pitfall-clustering.md` 和 `02b-pitfall-llm-extraction.md` 共同构成 Phase 2 的完整设计。*
