# VatBrain v0.2 — Pitfall 聚类策略

> 所属 Phase：Phase 2 — Pitfall 提取引擎
>
> 前置阅读：`../00-design.md`（v0.2 设计草案）、`../../DESIGN_PRINCIPLES.md`
>
> 本文档定义 Pitfall 提取的聚类策略——从 debug 类型情境记忆中，如何将散乱的错误记录组织为可供 LLM 提炼的结构化候选集。

---

## 1. 问题定义

睡眠整合扫描到的 Episodic Memory 中，`task_type=debug` 的记忆是 Pitfall 提取的原料。但这些记忆是**扁平列表**——同一个 entity 的不同 bug、不同 entity 的相似 bug 混在一起。直接全量喂给 LLM 不可行：

- **Token 上限**：200 条 debug 记忆全部放入 prompt 远超上下文窗口
- **跨 entity 混淆**：`func:NewRedisPool` 的连接池问题和 `func:HandleRequest` 的超时问题不应在同一个 LLM 调用中分析
- **同 entity 多 bug**：同一个 entity 可能有多种不同的 bug，需区分后再分别提炼

聚类策略解决的核心问题：**将扁平列表转换为按 entity 分组、组内按错误模式子聚类、每个子簇恰好描述一种 bug 的结构化候选集。**

---

## 2. 前置依赖

### 2.1 EpisodicScanItem 扩展

当前 `EpisodicScanItem`（`internal/store/memory_store.go:46`）缺少 `EntityID` 字段。必须扩展：

```go
type EpisodicScanItem struct {
    ID           uuid.UUID
    Summary      string
    TaskType     models.TaskType
    ProjectID    string
    Language     string
    EntityGroup  string
    EntityID     string       // v0.2 新增：关联的代码实体标识
    Weight       float64
    LastAccessed time.Time
}
```

**影响范围**：三个 Store 后端的 `ScanRecent` 实现均需在 SELECT / MATCH 投影中补充 `entity_id` 字段。

### 2.2 Embedder 就绪

子聚类依赖 `signature` 的向量表示。Phase 0 的 ClaudeEmbedder 必须在聚类逻辑开发前完成——至少需要 `Embed(ctx, text) ([]float64, error)` 可用。StubEmbedder（返回零向量）会导致所有 Pitfall 被判定为相似并合并，不可用于开发和测试。

---

## 3. 聚类流水线

```
EpisodicMemory[]  (all task types)
    │
    ▼
[Filter]  → 仅保留 task_type=debug + entity_id 非空
    │
    ▼
[Stage 1: Entity Grouping]  → 按 (project_id, entity_id) 分组
    │
    ▼
[Stage 2: Intra-Entity Sub-Clustering]  → 每组内按 signature embedding 相似度子聚类
    │
    ▼
PitfallCandidate[]  → 每个子簇 = 一个候选 Pitfall
```

---

## 4. Stage 0：过滤

```go
func filterDebugEpisodics(all []store.EpisodicScanItem) []store.EpisodicScanItem {
    var result []store.EpisodicScanItem
    for _, ep := range all {
        if ep.TaskType == models.TaskTypeDebug && ep.EntityID != "" {
            result = append(result, ep)
        }
    }
    return result
}
```

**过滤条件**：
- `task_type == "debug"` — Pitfall 仅从调试记录中提取
- `entity_id != ""` — 没有 entity 锚点的记忆无法聚类（Pitfall 要求强绑定到实体）

这两条任意一条不满足则该记忆不参与 Pitfall 提取（但仍参与语义规则线）。

**数据量预估**：
- 总扫描量 ~1000 条/24h
- 调试记忆通常占 20-30% = 200-300 条
- 含有 entity_id 的比例 ~80% = 160-240 条有效候选

---

## 5. Stage 1：Entity 级聚类

### 5.1 分组键

```go
type EntityGroup struct {
    ProjectID string
    EntityID  string
    Episodics []store.EpisodicScanItem
}

func groupByEntity(episodics []store.EpisodicScanItem) map[string]*EntityGroup {
    groups := make(map[string]*EntityGroup)
    for _, ep := range episodics {
        key := ep.ProjectID + "|" + ep.EntityID
        if _, ok := groups[key]; !ok {
            groups[key] = &EntityGroup{
                ProjectID: ep.ProjectID,
                EntityID:  ep.EntityID,
            }
        }
        groups[key].Episodics = append(groups[key].Episodics, ep)
    }
    return groups
}
```

### 5.2 最小簇大小过滤

```go
const MinEpisodicsPerEntity = 3  // 同 entity 至少 3 条 debug 记忆才触发提取
```

一个 entity 只有 >= 3 条 debug 记录的才进入 Stage 2 子聚类。这个阈值可配置（与语义规则的 `MinClusterSize` 共用或独立配置）。

### 5.3 硬上限

同一 entity 的 debug 记忆超过 `MaxEpisodicsPerEntity`（默认 50）时，取 `weight` 最高的 50 条。避免某个热点 entity 的调试记录撑爆 LLM context。

---

## 6. Stage 2：Entity 内子聚类

这是整个聚类策略中最复杂的部分。同一个 entity（如 `func:NewRedisPool`）上可能发生过多种不同的 bug：

- "连接池耗尽（MaxOpenConns 配置不足）"
- "连接超时（网络抖动触发 ConnectionTimeout）"
- "连接泄漏（defer Close 缺失）"

这三种 bug 的 signature embedding 应分布在向量空间的不同区域。Stage 2 的目标是将它们分开。

### 6.1 子聚类算法

**选用方案：基于 signature embedding 的层次凝聚聚类（HAC）**，而非 K-Means 或 DBSCAN。

理由：
- **不需要预设 K**：无法预知一个 entity 上有几种 bug
- **距离阈值控制合并粒度**：通过相似度阈值控制何时停止合并，可控性好
- **数据集小**：单个 entity 通常 3-50 条记忆，HAC O(n²) 完全可接受

```go
// SubCluster represents a sub-cluster within an entity.
type SubCluster struct {
    Episodics  []store.EpisodicScanItem
    Centroid   []float64  // 成员 embedding 的平均值
}

// subClusterBySignature performs HAC on one entity's debug episodics
// using signature embedding similarity.
func subClusterBySignature(
    ctx context.Context,
    group *EntityGroup,
    emb embedder.Embedder,
    mergeThreshold float64,   // 默认 0.85 — 相似度高于此值认为同一种 bug
) ([]SubCluster, error) {
    if len(group.Episodics) <= 1 {
        // 只有一条记忆 → 单成员簇
        emb_, _ := emb.Embed(ctx, group.Episodics[0].Summary)
        return []SubCluster{{Episodics: group.Episodics, Centroid: emb_}}, nil
    }

    // 1. 为每条 summary 生成 embedding
    embeddings := make([][]float64, len(group.Episodics))
    for i, ep := range group.Episodics {
        emb_, err := emb.Embed(ctx, ep.Summary)
        if err != nil {
            return nil, fmt.Errorf("embed ep %s: %w", ep.ID, err)
        }
        embeddings[i] = emb_
    }

    // 2. HAC：每个元素初始为独立簇
    clusters := make([]SubCluster, len(group.Episodics))
    for i, ep := range group.Episodics {
        clusters[i] = SubCluster{
            Episodics: []store.EpisodicScanItem{ep},
            Centroid:  embeddings[i],
        }
    }

    // 3. 迭代合并最相似的簇对
    for len(clusters) > 1 {
        // 找最近的两个簇
        bestI, bestJ, bestSim := 0, 1, cosineSimilarity(clusters[0].Centroid, clusters[1].Centroid)
        for i := 0; i < len(clusters); i++ {
            for j := i + 1; j < len(clusters); j++ {
                sim := cosineSimilarity(clusters[i].Centroid, clusters[j].Centroid)
                if sim > bestSim {
                    bestI, bestJ, bestSim = i, j, sim
                }
            }
        }

        if bestSim < mergeThreshold {
            break // 没有足够相似的簇 → 停止合并
        }

        // 合并 bestJ 到 bestI
        clusters[bestI] = mergeClusters(clusters[bestI], clusters[bestJ])
        // 删除 bestJ
        clusters = append(clusters[:bestJ], clusters[bestJ+1:]...)
    }

    // 4. 过滤：只保留 >= 3 个成员的子簇（单/双成员的可能是噪声）
    var result []SubCluster
    for _, c := range clusters {
        if len(c.Episodics) >= 3 {
            result = append(result, c)
        }
    }
    return result, nil
}
```

### 6.2 关键参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `mergeThreshold` | 0.85 | cosine 相似度高于此值认为两个子簇描述同一种 bug，触发合并 |
| `minSubClusterSize` | 3 | 子簇最少成员数。单/双成员的簇可能是噪声，不触发 LLM 提取 |
| `maxEpisodicsPerEntity` | 50 | 单个 entity 最多取前 N 条记忆（按 weight 排序）参与子聚类 |

### 6.3 合并阈值 0.85 的选择依据

- 0.95 → 过于保守，只有几乎相同的描述才合并 → 同一 bug 的多种表述可能被错误分裂
- 0.75 → 过于激进，不同 bug 可能被错误合并 → 连接池问题和超时问题被混为一谈
- 0.85 → 基准值，来自 v0.1 的 PatternSeparation 阈值（Section 4.3）。后续基于实际数据调优

### 6.4 LLM 参与的混合策略（可选增强）

HAC 只能基于语义相似度，无法判断"都是连接池问题但根因不同"。如果同一个 entity 上 HAC 产出一个很大的子簇（>= 10 条），发送给 LLM 做二次拆分：

```
System: You are analyzing debug sessions for entity "{entity_id}".
These {N} sessions are semantically similar but may describe
different bugs. Split them into groups, where each group
represents a SINGLE distinct bug pattern.

Return JSON: {"groups": [{"indices": [0, 3, 7], "description": "..."}]}
```

此增强为 **可选路径**，v0.2 初版实现使用纯 HAC，若出现"明显不同的 bug 被合并"的案例再启用。

---

## 7. 输出：PitfallCandidate

```go
// PitfallCandidate 是聚类产物，每个子簇对应一个候选 Pitfall。
// 它是 Pitfall Extractor 和 LLM 之间的数据桥梁。
type PitfallCandidate struct {
    EntityID    string       // 锚点实体
    ProjectID   string       // 所属项目
    Language    string       // 语言/框架
    EpisodicIDs []uuid.UUID  // 源情境记忆 ID 列表（溯源链）
    Summaries   []string     // 源情境记忆摘要列表（喂给 LLM 的素材）
    ClusterSize int          // 簇大小（用于排序优先级）
    AvgWeight   float64      // 簇内平均权重（高权重簇优先提取）
}
```

按 `(AvgWeight * ClusterSize)` 降序排列，优先处理高价值簇。

---

## 8. 边界情况处理

| 场景 | 处理策略 |
|------|---------|
| debug 记忆 < 10 条 | 整个 Pitfall 线跳过，返回空结果 |
| 单个 entity 聚集了 80% 的 debug 记忆 | 截断到 maxEpisodicsPerEntity=50，其他 entity 不受影响 |
| 某个 entity 的所有 debug 记忆都在一个子簇内 | 产出 1 个 PitfallCandidate，LLM 提取时可能产出多个 Pitfall（如果 prompt 判断有多个 distinct bug） |
| 所有 entity 的子簇都不满足 minSubClusterSize=3 | 返回空，本次整合不产出 Pitfall |
| Embedder 不可用或返回错误 | 跳过子聚类阶段，所有 entity 记忆直接按 entity 聚合成单簇（降级策略），日志记录 |

---

## 9. 测试策略

### 单元测试

| 场景 | 输入 | 期望输出 |
|------|------|---------|
| 空 debug 集 | `[]` | 0 个 EntityGroup |
| 单 entity 单条记录 | 1 条 debug 记忆 | 0 个子簇（< minSubClusterSize） |
| 单 entity 同一 bug 描述 5 次 | 5 条相似 summary | 1 个子簇，含 5 条记忆 |
| 单 entity 三种不同 bug 各 3 条 | 9 条 summary | 3 个子簇，每个 >= 3 条 |
| 多 entity 混合 | 3 个 entity 各 4 条 | 3 个 EntityGroup，每个含 1 子簇 |
| 超过 maxEpisodicsPerEntity | 同一 entity 80 条 | 截断到 50 条 |
| Embedder 返回错误 | — | 降级为单 entity 单簇 |

### 集成测试

- 写入 30 条 `task_type=debug` 记忆（3 个 entity，各 10 条，含 2-3 种 bug 描述）→ 触发整合 → 验证子聚类结果 entity 分组正确、子簇不混合不同 bug

---

## 10. 性能目标

| 指标 | 目标 | 备注 |
|------|------|------|
| Stage 0 过滤 | < 1ms | 纯内存操作 |
| Stage 1 Entity 分组 | < 2ms | map 聚合 |
| Stage 2 子聚类（含 embedding） | < 5s（10 个 entity） | 瓶颈在 Embedder 批量调用 |
| Claude Embedding API | < 500ms/条 | 预期批量 10-50 条 |

---

*本文档与 `02b-pitfall-llm-extraction.md` 和 `02c-consolidation-parallel.md` 共同构成 Phase 2 的完整设计。*
