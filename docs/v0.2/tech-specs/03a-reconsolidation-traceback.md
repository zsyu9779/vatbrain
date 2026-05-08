# VatBrain v0.2 — 记忆再巩固：溯源链反向传播

> 所属 Phase：Phase 3 — 记忆再巩固
>
> 前置阅读：`../00-design.md`（第 5 节）、`../../DESIGN_PRINCIPLES.md`（第 4.2 节）
>
> 本文档定义记忆再巩固的核心机制——当一条被检索的记忆被纠正时，纠正信号如何沿溯源链反向传播到源记忆。

---

## 1. 理论背景

DESIGN_PRINCIPLES 第 4.2 节的核心论断：

> 每次回忆都不是只读操作。记忆被提取时会重新进入不稳定状态，可以被修改。

工程推论：**检索不应无副作用。** 当用户（或 Agent）纠正了检索结果中的某条记忆，纠正信号应反向传播到产生该记忆的源数据上。

这与 v0.1 的 Feedback API 不同——v0.1 的 feedback 只更新被纠正的那条记忆本身的 weight，不追溯源记忆。v0.2 实现完整的溯源链反向传播。

---

## 2. 触发路径

```
Agent 检索 → 获得结果 → Agent 使用了某条结果后行为出错
  → 用户纠正 Agent
    → POST /memories/{id}/feedback {
        action: "corrected",
        correction_detail: { original: "...", corrected_to: "..." }
      }
      → Feedback Handler
        → WeightDecayEngine (更新被纠正记忆的 weight)
        → ReconsolidationEngine.Process (反向传播到源记忆)  ← v0.2 新增
```

---

## 3. 溯源链拓扑

### 3.1 图结构

```
        SemanticMemory (被纠正)
              │
              │ DERIVED_FROM (run_id)
              ▼
    ┌─────────────────────────┐
    │ EpisodicMemory (源 1)   │
    │ EpisodicMemory (源 2)   │
    │ EpisodicMemory (源 3)   │
    └─────────────────────────┘

    也可能链路更短：
        EpisodicMemory (被纠正)
            ← 无 DERIVED_FROM 入边
            → 本身就是源 → 直接更新
```

### 3.2 DERIVED_FROM 边的语义

- 方向：`SemanticMemory -[DERIVED_FROM {run_id}]-> EpisodicMemory`
- 含义："这条语义规则是从哪些情境记忆中提炼出来的"
- `run_id` 属性：是哪次整合运行产生的
- 多对多：一条语义规则可以 DERIVED_FROM 多条情境记忆，一条情境记忆可以被多条规则引用

### 3.3 支持的纠正路径

| 被纠正的记忆类型 | 溯源路径 | 更新目标 |
|----------------|---------|---------|
| SemanticMemory | 沿 DERIVED_FROM 入边 → 找到 source_episodic_ids | 所有源 EpisodicMemory 的 weight + content |
| EpisodicMemory | 自身就是源 | 直接更新自身的 weight + content |
| PitfallMemory | 沿 DERIVED_FROM 入边 → 找到 source_episodic_ids | 所有源 EpisodicMemory 的 weight |

---

## 4. ReconsolidationEngine

### 4.1 核心结构

```go
// internal/core/reconsolidation_engine.go（新文件）

// ReconsolidationEngine handles back-propagation of correction signals
// through DERIVED_FROM traceability chains.
type ReconsolidationEngine struct {
    // 权重更新参数
    CorrectionBoost      float64 // 源记忆被纠正时的 weight 增量倍数（默认 1.5）
    MaxTracebackHops     int     // 最大回溯跳数（默认 2，防止无限循环）
    CorrectionSourceThreshold int // 被纠正多少次后标记为 correction_source（默认 2）
}
```

### 4.2 Process 方法

```go
// Process handles a correction feedback event.
//
// Flow:
//  1. Look up the corrected memory.
//  2. If SemanticMemory/PitfallMemory → trace DERIVED_FROM edges to source episodics.
//  3. If EpisodicMemory → update directly.
//  4. Update source memories' weight (multiply by CorrectionBoost).
//  5. Append correction record to source memories' summary.
//  6. If source has been corrected >= CorrectionSourceThreshold times → mark as
//     "correction_source", elevating protection level.
func (re *ReconsolidationEngine) Process(
    ctx context.Context,
    s store.MemoryStore,
    correctedID uuid.UUID,
    correctedType string,           // "episodic" | "semantic" | "pitfall"
    detail models.CorrectionDetail, // { original, corrected_to }
    isUserCorrected bool,           // true = 用户显式纠正（高可信），false = LLM 推断
) (*ReconsolidationResult, error) {

    result := &ReconsolidationResult{
        CorrectedID: correctedID,
    }

    switch correctedType {
    case "episodic":
        // EpisodicMemory 自身就是源 → 直接更新
        return re.processEpisodic(ctx, s, correctedID, detail, isUserCorrected)

    case "semantic":
        // SemanticMemory → 溯源到 source episodics
        return re.processSemantic(ctx, s, correctedID, detail, isUserCorrected)

    case "pitfall":
        // PitfallMemory → 溯源到 source episodics
        return re.processPitfall(ctx, s, correctedID, detail, isUserCorrected)

    default:
        return nil, fmt.Errorf("reconsolidation: unknown corrected type %q", correctedType)
    }
}
```

### 4.3 语义记忆溯源

```go
func (re *ReconsolidationEngine) processSemantic(
    ctx context.Context,
    s store.MemoryStore,
    semanticID uuid.UUID,
    detail models.CorrectionDetail,
    isUserCorrected bool,
) (*ReconsolidationResult, error) {

    // 1. 查找 DERIVED_FROM 入边
    edges, err := s.GetEdges(ctx, semanticID, "DERIVED_FROM", "incoming")
    if err != nil {
        return nil, fmt.Errorf("trace DERIVED_FROM for %s: %w", semanticID, err)
    }

    result := &ReconsolidationResult{CorrectedID: semanticID}

    if len(edges) == 0 {
        // 无溯源边 → 可能是手工创建的语义记忆
        // 记录但不传播（没有源可以更新）
        return result, nil
    }

    // 2. 对每个源 EpisodicMemory 更新
    for _, edge := range edges {
        ep, err := s.GetEpisodic(ctx, edge.FromID) // DERIVED_FROM 入边：From 是 source
        if err != nil {
            // best-effort：某个源可能已被删除 → 跳过
            result.SkippedIDs = append(result.SkippedIDs, edge.FromID)
            continue
        }

        // 3. 更新 weight
        newWeight := ep.Weight * re.CorrectionBoost

        // 4. 更新 content（append correction record）
        newSummary := appendCorrection(ep.Summary, detail)

        // 5. 持久化
        if err := s.UpdateEpisodicWeight(ctx, ep.ID, newWeight, ep.EffectiveFrequency); err != nil {
            result.FailedIDs = append(result.FailedIDs, ep.ID)
            continue
        }

        // 注意：v0.1 的 MemoryStore 没有 UpdateSummary 方法
        // 需要通过 WriteEpisodic 重新写入完整对象，或新增 UpdateEpisodicSummary
        // 此处暂时假设有 TouchEpisodic 可用（至少更新 last_accessed_at）

        // 6. 检查是否需要标记为 correction_source
        // （见 03b 保护级别文档）

        result.UpdatedIDs = append(result.UpdatedIDs, ep.ID)
        result.TotalWeightDelta += newWeight - ep.Weight
    }

    return result, nil
}
```

### 4.4 情境记忆直接更新

```go
func (re *ReconsolidationEngine) processEpisodic(
    ctx context.Context,
    s store.MemoryStore,
    episodicID uuid.UUID,
    detail models.CorrectionDetail,
    isUserCorrected bool,
) (*ReconsolidationResult, error) {

    ep, err := s.GetEpisodic(ctx, episodicID)
    if err != nil {
        return nil, fmt.Errorf("reconsolidation get episodic %s: %w", episodicID, err)
    }

    newWeight := ep.Weight * re.CorrectionBoost
    if err := s.UpdateEpisodicWeight(ctx, ep.ID, newWeight, ep.EffectiveFrequency); err != nil {
        return nil, fmt.Errorf("update source weight: %w", err)
    }

    return &ReconsolidationResult{
        CorrectedID:      episodicID,
        UpdatedIDs:       []uuid.UUID{episodicID},
        TotalWeightDelta: newWeight - ep.Weight,
    }, nil
}
```

---

## 5. GetEdges 的方向语义

当前 `MemoryStore.GetEdges` 签名：

```go
GetEdges(ctx context.Context, nodeID uuid.UUID, edgeType string, direction string) ([]Edge, error)
```

`direction` 参数值与溯源链的对应关系：

| direction | 含义 | 溯源场景 |
|-----------|------|---------|
| `"outgoing"` | nodeID → other | 查询语义规则关联了哪些源记忆 |
| `"incoming"` | other → nodeID | 查询哪些语义规则是从这些源记忆产生的 |

对于再巩固的语义记忆溯源——被纠正的是 SemanticMemory，需要找它的源 EpisodicMemory。`DERIVED_FROM` 边的方向是 `SemanticMemory → EpisodicMemory`，因此查询 `GetEdges(semanticID, "DERIVED_FROM", "outgoing")`。

若边方向是 `EpisodicMemory ← SemanticMemory`（即 DERIVED_FROM 的创建方向与箭头方向一致），则用 `"outgoing"`。这个方向需要在 Phase 1 实现时明确并统一。

---

## 6. 多跳溯源与循环防护

### 6.1 多跳场景

当前设计中 DERIVED_FROM 只有一层：`Semantic → Episodic`。没有 `Episodic → Episodic` 或 `Semantic → Semantic → Episodic` 的多跳链路。因此 `MaxTracebackHops = 2` 实际上支持：

- 跳 0：被纠正的 EpisodicMemory 自身
- 跳 1：Semantic → Episodic（DERIVED_FROM 边）

### 6.2 循环检测

虽然当前不会出现循环（DERIVED_FROM 方向固定），但为防御未来可能的复杂溯源链，在 `ReconsolidationEngine` 中加入访问集：

```go
func (re *ReconsolidationEngine) Process(ctx context.Context, ...) {
    visited := make(map[uuid.UUID]bool)
    // 在每次更新前检查
    if visited[targetID] {
        continue // 防止循环
    }
    visited[targetID] = true
}
```

---

## 7. ReconsolidationResult

```go
type ReconsolidationResult struct {
    CorrectedID      uuid.UUID   `json:"corrected_id"`
    UpdatedIDs       []uuid.UUID `json:"updated_ids"`       // 成功更新的源记忆
    SkippedIDs       []uuid.UUID `json:"skipped_ids"`       // 跳过的源记忆（已删除等）
    FailedIDs        []uuid.UUID `json:"failed_ids"`        // 更新失败的源记忆
    TotalWeightDelta float64     `json:"total_weight_delta"` // 累计权重变化
}
```

---

## 8. 权重更新公式

### 8.1 纠正信号（最强学习信号）

```go
// 被纠正的记忆自身
newWeight_self = currentWeight * 1.5  // CorrectionBoost

// 源情境记忆（溯源链上所有节点）
for each sourceEpisodic:
    newWeight_source = sourceWeight * 1.5
    newEffFreq = effectiveFrequency + 0.5  // 纠正的 effectiveFrequency 增量等同于多次访问
```

### 8.2 Trust Level 提升

仅当纠正来自用户显式 feedback（`isUserCorrected = true`）时：

```go
if isUserCorrected && sourceEpisodic.TrustLevel < 5 {
    sourceEpisodic.TrustLevel += 1
}
```

LLM 推断的纠正（`isUserCorrected = false`）不提升 trust_level，仅更新 weight。

---

## 9. 测试策略

### 单元测试

```go
func TestReconsolidation_SemanticToEpisodic(t *testing.T) {
    // 写入 3 条 EpisodicMemory
    // 写入 1 条 SemanticMemory + 3 条 DERIVED_FROM 边
    // 纠正 SemanticMemory → ReconsolidationEngine.Process
    // 验证：3 条 EpisodicMemory 的 weight 各 × 1.5
}

func TestReconsolidation_EpisodicDirect(t *testing.T) {
    // 纠正 EpisodicMemory → 直接更新自身
}

func TestReconsolidation_NoTracebackChain(t *testing.T) {
    // 纠正无 DERIVED_FROM 边的 SemanticMemory → 返回空结果
}

func TestReconsolidation_PartialSourceFailure(t *testing.T) {
    // 3 条源记忆中 1 条已被删除 → SkippedIDs 含该 ID
}

func TestReconsolidation_UserCorrectedTrustBoost(t *testing.T) {
    // isUserCorrected=true → trust_level +1
    // isUserCorrected=false → trust_level 不变
}

func TestReconsolidation_CycleDetection(t *testing.T) {
    // 构造人工循环 → 不无限循环，正确终止
}
```

---

*本文档与 `03b-reconsolidation-protection.md` 共同构成 Phase 3 的完整设计。*
