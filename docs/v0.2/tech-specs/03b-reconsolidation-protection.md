# VatBrain v0.2 — 记忆保护级别与反馈管线

> 所属 Phase：Phase 3 — 记忆再巩固
>
> 前置阅读：`../00-design.md`（第 5、6 节）、`03a-reconsolidation-traceback.md`
>
> 本文档定义记忆保护级别系统——当一条记忆被反复纠正后，如何提升其保护级别使其在衰减曲线上获得更长的半衰期；以及从 API 到核心引擎的完整 Feedback 管线。

---

## 1. 保护级别系统

### 1.1 设计动机

一条被纠正过的记忆，其价值远高于从未被回访的记忆——它已被实践证明存在缺陷，保留它有助于防止未来重复犯错。但并非所有被纠正的记忆都应无限期保留：被纠正一次 vs 被纠正三次，信息价值不同。

保护级别系统提供**分级保护**：被纠正次数越多 → 衰减越慢 → 更难被冷却。

### 1.2 ProtectionLevel 定义

```go
// ProtectionLevel escalates the protection of a memory based on correction history.
type ProtectionLevel int

const (
    ProtectionNormal     ProtectionLevel = 0 // 默认：标准衰减
    ProtectionCorrected  ProtectionLevel = 1 // 被纠正过 ≥ 1 次：衰减减速 50%
    ProtectionCorrSource ProtectionLevel = 2 // 被纠正过 ≥ CorrectionSourceThreshold 次：
                                              // 衰减减速 80%，免冷却（不下沉到冷存储）
)
```

### 1.3 衰减参数调整

```go
// GetDecayParams returns α/β decay rates adjusted for protection level.
func GetDecayParams(base WeightDecayEngine, level ProtectionLevel) (alpha, beta float64) {
    switch level {
    case ProtectionNormal:
        return base.AlphaExperience, base.BetaActivity
    case ProtectionCorrected:
        return base.AlphaExperience * 0.5, base.BetaActivity * 0.5  // 衰减减半
    case ProtectionCorrSource:
        return base.AlphaExperience * 0.2, base.BetaActivity * 0.2  // 衰减降为 1/5
    default:
        return base.AlphaExperience, base.BetaActivity
    }
}
```

### 1.4 CorrectionSource 标记

当同一条源记忆被纠正 >= `CorrectionSourceThreshold`（默认 2）次时，标记为 `correction_source`：

```go
// CorrectionTracker tracks how many times each source memory has been corrected.
type CorrectionTracker struct {
    mu            sync.RWMutex
    correctionMap map[uuid.UUID]int // memory_id → correction_count
}

func (ct *CorrectionTracker) Increment(ctx context.Context, s store.MemoryStore, id uuid.UUID, threshold int) (bool, error) {
    ct.mu.Lock()
    ct.correctionMap[id]++
    count := ct.correctionMap[id]
    ct.mu.Unlock()

    if count >= threshold {
        // 标记为 correction_source → 提升保护级别
        // 需要在 EpisodicMemory 模型中增加 protection_level 字段
        // 或通过 entity_group / tag 机制标记
        return true, nil
    }
    return false, nil
}
```

**注意**：CorrectionTracker 在单进程内使用 `sync.Map` 或带锁 map。由于 v0.2 是单服务部署（非分布式），内存计数器足够。重启后计数器清零——这不影响功能，因为标记本身就是多次纠正的累积效果，重启后前几次纠正已体现在 weight 中。

### 1.5 模型扩展

```go
// EpisodicMemory 和 SemanticMemory 新增字段
type EpisodicMemory struct {
    // ... 现有字段 ...
    ProtectionLevel  ProtectionLevel `json:"protection_level"`   // 保护级别（默认 0）
    CorrectionCount  int             `json:"correction_count"`   // 被纠正次数
}
```

若不在模型中新增字段，可通过以下间接方式实现：
- `ProtectionNormal` ↔ `TrustLevel <= 3` 且 `Weight < 2.0`
- `ProtectionCorrected` ↔ `TrustLevel >= 4` 或 `Weight >= 2.0`
- `ProtectionCorrSource` ↔ 在 `entity_group` 中标记 `"correction_source:"` 前缀

推荐新增显式字段（更清晰），但需同步更新三个 Store 后端的 DDL 和读写逻辑。

---

## 2. Feedback 管线

### 2.1 完整流程

```
POST /memories/{id}/feedback
    │
    ▼
FeedbackHandler.Handle(w, r)
    │
    ├─→ 1. 解析 FeedbackRequest { action, correction_detail }
    │
    ├─→ 2. 查找记忆（GetEpisodic / GetSemantic / GetPitfall）
    │
    ├─→ 3. ApplyFeedback(currentWeight, effFreq, action, isUserCorrected)
    │      → 更新被反馈记忆自身的 weight
    │      → 调用 UpdateWeight 持久化
    │
    ├─→ 4. 若 action == "corrected" → ReconsolidationEngine.Process(...)
    │      → 溯源链反向传播
    │      → 更新源记忆 weight
    │      → 检查 CorrectionSource 阈值
    │
    └─→ 5. 返回 FeedbackResponse { new_weight, reconsolidation }
```

### 2.2 Feedback Handler 实现

```go
// internal/api/feedback_handler.go（增强）

func (h *FeedbackHandler) Handle(w http.ResponseWriter, r *http.Request) {
    memoryID := chi.URLParam(r, "memory_id")
    id, err := uuid.Parse(memoryID)
    if err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid memory_id"})
        return
    }

    var req models.FeedbackRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
        return
    }

    if !req.Action.IsValid() {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid action"})
        return
    }

    ctx := r.Context()

    // 1. 查找记忆（尝试 episodic → semantic → pitfall）
    var memType string
    var currentWeight, currentEffFreq float64
    var isUserCorrected bool

    ep, err := h.store.GetEpisodic(ctx, id)
    if err == nil {
        memType = "episodic"
        currentWeight = ep.Weight
        currentEffFreq = ep.EffectiveFrequency
    } else {
        sem, err := h.store.GetSemantic(ctx, id)
        if err == nil {
            memType = "semantic"
            currentWeight = sem.Weight
            currentEffFreq = sem.EffectiveFrequency
        } else {
            // 尝试 pitfall（v0.2 新增）
            pit, err := h.store.GetPitfall(ctx, id)
            if err == nil {
                memType = "pitfall"
                currentWeight = pit.Weight
                currentEffFreq = 0 // Pitfall 无 effective_frequency
            } else {
                writeJSON(w, http.StatusNotFound, map[string]string{"error": "memory not found"})
                return
            }
        }
    }

    // 2. 行为归因权重更新
    if req.CorrectionDetail != nil {
        isUserCorrected = true // 有 correction_detail 说明是用户显式纠正
    }
    newWeight, newEffFreq := core.ApplyFeedback(
        currentWeight, currentEffFreq, req.Action, isUserCorrected,
    )

    // 3. 持久化权重
    switch memType {
    case "episodic":
        h.store.UpdateEpisodicWeight(ctx, id, newWeight, newEffFreq)
    case "semantic":
        // Semantic 当前无 UpdateWeight 方法，通过重写实现（见 2.3 节）
        h.store.UpdateSemanticWeight(ctx, id, newWeight, newEffFreq)
    case "pitfall":
        h.store.UpdatePitfallWeight(ctx, id, newWeight)
    }

    // 4. 再巩固（仅纠正操作）
    var reconsolidation *ReconsolidationResult
    if req.Action == models.SearchActionCorrected && req.CorrectionDetail != nil {
        reconsolidation, _ = h.reconsolidation.Process(
            ctx, h.store, id, memType, *req.CorrectionDetail, isUserCorrected,
        )
    }

    // 5. 响应
    resp := FeedbackResponse{
        NewWeight:      newWeight,
        Reconsolidation: reconsolidation,
    }
    writeJSON(w, http.StatusOK, resp)
}

type FeedbackResponse struct {
    NewWeight       float64                 `json:"new_weight"`
    Reconsolidation *ReconsolidationResult  `json:"reconsolidation,omitempty"`
}
```

### 2.3 Semantic UpdateWeight 方法

当前 `MemoryStore` 接口有 `UpdateEpisodicWeight` 和 `UpdatePitfallWeight`（v0.2 新增），但没有 `UpdateSemanticWeight`。Phase 3 需新增：

```go
// MemoryStore 接口新增
UpdateSemanticWeight(ctx context.Context, id uuid.UUID, weight, effFreq float64) error
```

三个后端的实现：
- **SQLite**：`UPDATE semantic_memories SET weight=?, effective_frequency=? WHERE id=?`
- **Neo4j+pgvector**：`MATCH (s:SemanticMemory {id: $id}) SET s.weight=$w, s.effective_frequency=$ef`
- **In-Memory**：`s.semantic[id].Weight = weight`

---

## 3. 与 Weight Decay 的交互

保护级别影响衰减函数的 α/β 参数。计算 weight 时需根据 protection_level 选择参数：

```go
// WeightDecayEngine 新增方法
func (e *WeightDecayEngine) WeightWithProtection(
    effectiveFrequency float64,
    createdAt time.Time,
    lastAccessedAt time.Time,
    now time.Time,
    level ProtectionLevel,
) float64 {
    alpha, beta := GetDecayParams(e, level)
    experienceDecay := math.Exp(-alpha * daysBetween(createdAt, now))
    activityDecay := math.Exp(-beta * daysBetween(lastAccessedAt, now))
    return effectiveFrequency * experienceDecay * activityDecay
}
```

**免冷却保护**：`ProtectionCorrSource` 级别的记忆，即使 weight 低于 `CoolingThreshold`，也不进入冷存储：

```go
func (e *WeightDecayEngine) IsCooled(weight float64, level ProtectionLevel) bool {
    if level == ProtectionCorrSource {
        return false // 永远不冷却
    }
    return weight < e.CoolingThreshold
}
```

---

## 4. Append-Only 纠正记录

### 4.1 原则

纠正不覆盖原始 summary，而是追加。保留历史使纠正可审计、可回溯。

```go
// appendCorrection adds a correction record to the memory summary.
// Format:
//
//	[ORIGINAL] <original summary>
//	[CORRECTED at 2026-05-05T10:30:00Z] <corrected_to>
func appendCorrection(originalSummary string, detail models.CorrectionDetail) string {
    now := time.Now().UTC().Format(time.RFC3339)
    return fmt.Sprintf("%s\n[CORRECTED at %s] %s",
        originalSummary, now, detail.CorrectedTo)
}
```

### 4.2 多次纠正

```
[ORIGINAL] Redis MaxOpenConns 建议设为 50
[CORRECTED at 2026-05-01T...] Redis MaxOpenConns 生产环境必须 >= 100
[CORRECTED at 2026-05-05T...] Redis MaxOpenConns >= 200 for high-traffic services
```

检索时优先展示最新的纠正内容（取最后一段 `[CORRECTED at ...]`）。

---

## 5. 测试策略

### 5.1 单元测试

| 场景 | 验证点 |
|------|--------|
| `GetDecayParams` ProtectionNormal | α/β 不变 |
| `GetDecayParams` ProtectionCorrected | α/β 减半 |
| `GetDecayParams` ProtectionCorrSource | α/β 降为 1/5 |
| `IsCooled` with ProtectionCorrSource | 始终 false |
| `CorrectionTracker.Increment` 首次 | count=1, 不触发 |
| `CorrectionTracker.Increment` 第 2 次（threshold=2） | 触发标记 |
| `appendCorrection` 空原始 | 仅含纠正记录 |
| `appendCorrection` 已有原始 | 追加到末尾 |

### 5.2 集成测试

```
测试: 完整 Feedback 管线
  1. 写入 3 条 EpisodicMemory (src1, src2, src3)
  2. ConsolidationEngine.Run → 产生 1 条 SemanticMemory + 3 条 DERIVED_FROM 边
  3. POST /memories/{sem_id}/feedback {action: "corrected", correction_detail: ...}
  4. 验证:
     - SemanticMemory weight *= 1.5 ✓
     - src1, src2, src3 weight 各 *= 1.5 ✓
     - src1, src2, src3 的 summary 追加了 [CORRECTED at ...] ✓
     - 响应含 reconsolidation.updated_ids = [src1, src2, src3] ✓

测试: 重复纠正触发 ProtectionCorrSource
  1. 对同一条 src1 纠正 2 次
  2. 验证: src1 的保护级别升级，IsCooled 返回 false
```

---

## 6. 复杂度与风险

### 6.1 实现复杂度

| 组件 | 复杂度 | 理由 |
|------|--------|------|
| `ReconsolidationEngine` | 中 | 纯 logic，无外部依赖，主要复杂度在图遍历 |
| `FeedbackHandler` 增强 | 低 | 在现有 handler 中增加再巩固调用 |
| `CorrectionTracker` | 低 | 内存 map + 简单计数器 |
| `ProtectionLevel` 集成 | 中 | 需要更新三个 Store 后端的 DDL + 读写，新增字段 |

### 6.2 风险

| 风险 | 缓解 |
|------|------|
| 再巩固批量更新源记忆时部分失败 | best-effort：成功多少算多少，失败记录在 `FailedIDs` |
| 多次纠正导致 summary 膨胀 | 保留最后 3 次纠正记录，更早的截断（保留首次原始 + 最后 2 次纠正） |
| 恶意 feedback 反复纠正导致权重异常高 | 单条记忆 weight 硬上限 = 10.0（约 10 次纠正后触顶） |

---

*本文档与 `03a-reconsolidation-traceback.md` 共同构成 Phase 3 的完整设计。*
