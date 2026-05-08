# VatBrain v0.2 — 设计草案

> 状态：**草案**
>
> 起草时间：2026-05-05
>
> 前置阅读：
> - `../DESIGN_PRINCIPLES.md`（设计基石）
> - `../ROADMAP.md`（版本路线图）
> - `../v0.1/00-design.md`（v0.1 设计定稿）
>
> v0.2 目标：让记忆系统从"被动记录"进化为"从错误中学习"。核心是 Pitfall Memory 独立建模和行为归因权重。

---

## 0. v0.1.x 已完成内容回顾

v0.1 已完成以下能力，v0.2 直接在此基础上增量构建：

### 0.1 存储层

| 能力 | 状态 | 说明 |
|------|------|------|
| MemoryStore 接口 | ✅ | 15 方法统一抽象，`internal/store/memory_store.go` |
| SQLite Store | ✅ | 零依赖快速启动，`internal/store/sqlite/` |
| Neo4j+pgvector Store | ✅ | 生产级后端，`internal/store/neo4jpg/` |
| In-Memory Store | ✅ | 测试用，`internal/store/memory/` |
| Store 工厂 | ✅ | `app.NewMemoryStore(cfg, nc, pc)` 按配置切换 |

### 0.2 核心引擎

| 引擎 | 状态 | 说明 |
|------|------|------|
| WeightDecayEngine | ✅ | RWF + 双参照衰减 + 冷却阈值 |
| SignificanceGate | ✅ | 四条件门控 |
| PatternSeparation | ✅ | 三阶段可分离性判别 |
| RetrievalEngine | ✅ | 两阶段：ContextualGating → SemanticRanking |
| ConsolidationEngine | ✅ | v1：scan → cluster → extract → backtest → persist |
| LinkOnWrite | ✅ | 写入时建立 RELATES_TO 边 |

### 0.3 数据模型

| 模型 | 状态 | 说明 |
|------|------|------|
| EpisodicMemory | ✅ | 完整字段，含 source_type / trust_level / entity_group |
| SemanticMemory | ✅ | 完整字段，含 backtest_accuracy / source_episodic_ids |
| 8 种边类型 | ✅ | PRECEDES / CAUSED_BY / RELATES_TO / INSTANTIATES / DEPENDS_ON / CONFLICTS_WITH / SUPERSEDED / DERIVED_FROM |
| API 模型 | ✅ | Write / Search / Feedback / Consolidation / Touch / Weight |

### 0.4 v0.1 待完成项（v0.2 前置或同期完成）

| 项 | 优先级 | 说明 |
|-----|--------|------|
| ClaudeEmbedder 真实实现 | P0 | 当前 StubEmbedder 返回零向量，Pitfall 提取和语义检索依赖真实 embedding |
| Consolidation LLM 提炼 | P0 | extractRule 目前是字符串拼接，需接入 LLM 做规则提炼 |
| 回测真实 LLM 验证 | P1 | backtest 目前是 `len(cluster) >= minSize` |
| **EpisodicScanItem 扩展** | **P0** | **当前 struct 无 EntityID 字段，Pitfall 提取按 entity_id 聚类依赖此字段。需在 ScanRecent 投影中补充 EntityID** |
| 后台定时调度 | P1 | 睡眠整合当前为手动触发 |
| MinIO 快照存储 | P2 | `full_snapshot_uri` 字段就位，写入链路未实现 |
| RELATES_TO 自动创建 | P1 | 写入时不创建关联边 |

---

## 1. v0.2 范围定义

### 1.1 纳入 v0.2

- **Pitfall 节点模型**：独立节点类型 `(:PitfallMemory)`（Neo4j）/ `pitfall_memories` 表（SQLite）/ 内存 map（In-Memory），包含 signature / root_cause_category / fix_strategy / was_user_corrected
- **Pitfall 提取引擎**：睡眠整合中新增 Pitfall 提取阶段——从 `task_type=debug` 的情境记忆聚类 → LLM 结构化 → 写入 Pitfall 节点，与语义规则提取并行运行
- **记忆再巩固**：检索命中 → 用户纠正 → 反向传播更新源记忆（沿 DERIVED_FROM 溯源链）的权重和内容
- **行为归因权重**：替换 v0.1 的静态初始化权重（weight=1.0）。权重增量由后续行为驱动：被检索命中 → +Δ，被纠正 → ++Δ，被确认 → +Δ
- **错误感知检索**：检索时对目标实体做 Pitfall 双键匹配（实体 ID + 情境签名），相关 Pitfall 注入检索结果
- **源监控增强**：source_type 和 trust_level 加入 SemanticRanker 排序权重，高可信源记忆排序优先

### 1.1.1 硬约束：v0.1.1 三后端 fallback

**所有 v0.2 新增的存储模型（PitfallMemory 及相关边）必须支持 v0.1.1 的三后端可插拔机制。** 即：

| 后端 | 配置值 | Pitfall 节点存储 | Pitfall 向量存储 | 边存储 |
|------|--------|-----------------|-----------------|--------|
| SQLite | `sqlite` | `pitfall_memories` 表 | 同表 `signature_embedding` BLOB 列 + 应用层 cosine | `pitfall_edges` 表 |
| Neo4j+pgvector | `neo4j+pgvector` | `(:PitfallMemory)` 节点 | pgvector `pitfall_embeddings` 表 + IVFFlat 索引 | Neo4j 原生边 |
| In-Memory | `memory` | `map[uuid.UUID]*PitfallMemory` | 内存 `[]float64` + 暴力 cosine | 内存 adjacency list |

**Pitfall 相关的 7 个接口方法**（见第 3 节）在三个后端中各有一份实现，通过 `app.NewMemoryStore(cfg)` 统一切换。新增的 `pitfall_embeddings` 表同样需要 SQLite 和 pgvector 两套 DDL，In-Memory 后端不需要。

### 1.2 纳入 v0.3+

- 风险预测引擎（Pitfall → 主动预警）
- 反事实推理（用户连续 N 次纠正同一模式 → 生成假设性规则）
- 预测误差信号（Surprise Score 独立维度）
- 压缩残差存储
- 自适应衰减参数
- 多租户/多用户

---

## 2. Pitfall Memory 独立建模

### 2.1 为什么 Pitfall 需要独立节点类型

DESIGN_PRINCIPLES.md 第 7 节已详细论证。关键工程推论：

| 维度 | SemanticMemory（语义规则） | PitfallMemory（错误记忆） |
|------|--------------------------|--------------------------|
| 回答的问题 | "怎么做" | "怎么炸的" |
| 检索触发 | 主动规划（"我要用 Redis"） | 异常/纠错（"Redis 刚才挂了"） |
| 与实体的关系 | 多对多（一条规则适用多个实体） | 强绑定（一个 Pitfall 锚定一个实体） |
| 衰减速度 | 慢（知识相对稳定） | 中等（修复后降低但不消失） |
| 信息密度 | 低（正常路径，熵小） | 高（异常路径，每个 bug 独特） |

两者在检索时机、触发条件、衰减曲线上完全不同。若复用 SemanticMemory 节点，需在查询时加 `type=PITFALL` 过滤，导致索引膨胀且无法独立优化衰减参数。

### 2.2 数据模型（多后端）

PitfallMemory 在所有三个后端中有统一的逻辑模型，物理存储由各 Store 实现负责映射。

#### Neo4j+pgvector 后端

```cypher
-- PitfallMemory 节点（Neo4j）
CREATE (p:PitfallMemory {
    id: UUID,
    entity_id: STRING,            -- 出问题的代码实体（"func:NewRedisPool"）
    entity_type: STRING,          -- FUNCTION | MODULE | API | CONFIG | QUERY
    project_id: STRING,           -- 硬约束
    language: STRING,             -- 硬约束
    signature: STRING,            -- 错误特征描述（人可读的 error pattern）
    signature_embedding_id: STRING,-- pgvector 中 signature 的向量 ID
    root_cause_category: STRING,  -- CONCURRENCY | RESOURCE_EXHAUSTION | CONFIG | CONTRACT_VIOLATION | LOGIC_ERROR | UNKNOWN
    fix_strategy: STRING,         -- 修复策略摘要（≤500 字）
    was_user_corrected: BOOLEAN,  -- 来源是否为用户直接纠正（true = 高可信）
    occurrence_count: INTEGER,    -- 发生次数（同一 Pitfall 被重复命中）
    last_occurred_at: DATETIME,
    source_type: STRING,          -- USER_CORRECTED | LLM_INFERRED | PATTERN_EXTRACTED
    trust_level: INTEGER,         -- 1-5
    weight: FLOAT,
    created_at: DATETIME,
    updated_at: DATETIME,
    obsoleted_at: DATETIME        -- 修复后可标记过时
})

-- Pitfall → Episodic：溯源链
(:PitfallMemory)-[:DERIVED_FROM {run_id: STRING}]->(:EpisodicMemory)

-- Pitfall → Semantic：修复方案引用了某条规则
(:PitfallMemory)-[:RESOLVED_BY {confidence: FLOAT}]->(:SemanticMemory)

-- Pitfall → Pitfall：因果链
(:PitfallMemory)-[:CAUSES {confidence: FLOAT}]->(:PitfallMemory)

-- Episodic → Pitfall：某次 debug 触发了已知 Pitfall
(:EpisodicMemory)-[:TRIGGERED_PITFALL {similarity: FLOAT}]->(:PitfallMemory)

-- Entity 引用边（任何节点类型都可以指向 Pitfall）
(:EpisodicMemory)-[:HAS_PITFALL {relevance: FLOAT}]->(:PitfallMemory)
(:SemanticMemory)-[:HAS_PITFALL {relevance: FLOAT}]->(:PitfallMemory)
```

```sql
-- pgvector pitfall_embeddings 表
CREATE TABLE pitfall_embeddings (
    id UUID PRIMARY KEY,
    pitfall_id UUID NOT NULL,
    embedding vector(1536),
    signature_text TEXT,
    project_id VARCHAR(255),
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX ON pitfall_embeddings
USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 50);
```

#### SQLite 后端

```sql
-- pitfall_memories 表（SQLite）
CREATE TABLE pitfall_memories (
    id TEXT PRIMARY KEY,
    entity_id TEXT NOT NULL,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('FUNCTION','MODULE','API','CONFIG','QUERY')),
    project_id TEXT NOT NULL,
    language TEXT NOT NULL,
    signature TEXT NOT NULL,
    signature_embedding BLOB,          -- float64 序列化为 []byte，应用层 cosine
    root_cause_category TEXT NOT NULL CHECK (root_cause_category IN ('CONCURRENCY','RESOURCE_EXHAUSTION','CONFIG','CONTRACT_VIOLATION','LOGIC_ERROR','UNKNOWN')),
    fix_strategy TEXT NOT NULL DEFAULT '',
    was_user_corrected INTEGER NOT NULL DEFAULT 0,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    last_occurred_at TEXT,
    source_type TEXT NOT NULL,
    trust_level INTEGER NOT NULL DEFAULT 3 CHECK (trust_level BETWEEN 1 AND 5),
    weight REAL NOT NULL DEFAULT 1.0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    obsoleted_at TEXT
);

CREATE INDEX idx_pitfall_entity ON pitfall_memories(entity_id, project_id);
CREATE INDEX idx_pitfall_project ON pitfall_memories(project_id, language);
CREATE INDEX idx_pitfall_weight ON pitfall_memories(weight DESC);

-- pitfall_edges 表（SQLite 模拟图边）
CREATE TABLE pitfall_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,  -- DERIVED_FROM | RESOLVED_BY | CAUSES | TRIGGERED_PITFALL | HAS_PITFALL
    properties TEXT,          -- JSON object
    created_at TEXT NOT NULL
);

CREATE INDEX idx_pitfall_edges_from ON pitfall_edges(from_id, edge_type);
CREATE INDEX idx_pitfall_edges_to ON pitfall_edges(to_id, edge_type);
```

#### In-Memory 后端

无 DDL。数据存储在 `map[uuid.UUID]*PitfallMemory` + adjacency list 中，向量做暴力 cosine 比较。

### 2.3 Go 模型

```go
// internal/models/pitfall_memory.go（新增）

// PitfallMemory represents a (:PitfallMemory) node.
type PitfallMemory struct {
    ID                   uuid.UUID  `json:"id"`
    EntityID             string     `json:"entity_id"`
    EntityType           EntityType `json:"entity_type"`
    ProjectID            string     `json:"project_id"`
    Language             string     `json:"language"`
    Signature            string     `json:"signature"`
    SignatureEmbeddingID string     `json:"signature_embedding_id"`
    RootCauseCategory    RootCause  `json:"root_cause_category"`
    FixStrategy          string     `json:"fix_strategy"`
    WasUserCorrected     bool       `json:"was_user_corrected"`
    OccurrenceCount      int        `json:"occurrence_count"`
    LastOccurredAt       *time.Time `json:"last_occurred_at"`
    SourceType           SourceType `json:"source_type"`
    TrustLevel           TrustLevel `json:"trust_level"`
    Weight               float64    `json:"weight"`
    CreatedAt            time.Time  `json:"created_at"`
    UpdatedAt            time.Time  `json:"updated_at"`
    ObsoletedAt          *time.Time `json:"obsoleted_at"`
}

// EntityType classifies the code entity that a Pitfall anchors on.
type EntityType string

const (
    EntityTypeFunction EntityType = "FUNCTION"
    EntityTypeModule   EntityType = "MODULE"
    EntityTypeAPI      EntityType = "API"
    EntityTypeConfig   EntityType = "CONFIG"
    EntityTypeQuery    EntityType = "QUERY"
)

// RootCause categorizes the root cause of a Pitfall.
type RootCause string

const (
    RootCauseConcurrency        RootCause = "CONCURRENCY"
    RootCauseResourceExhaustion RootCause = "RESOURCE_EXHAUSTION"
    RootCauseConfig             RootCause = "CONFIG"
    RootCauseContractViolation  RootCause = "CONTRACT_VIOLATION"
    RootCauseLogicError         RootCause = "LOGIC_ERROR"
    RootCauseUnknown            RootCause = "UNKNOWN"
)

// Pitfall-specific error sentinels (used by all three Store backends).
var (
    ErrPitfallNotFound  = errors.New("pitfall not found")
    ErrPitfallDuplicate = errors.New("pitfall duplicate: same entity_id and signature")
)
```

`ErrPitfallDuplicate` 在 `WritePitfall` 中用于触发 upsert 语义（相同 entity + signature → 增加 occurrence_count 而非创建新节点）。

### 2.4 签名 Embedding 策略

**Embedding 输入文本**：Pitfall 的签名 embedding 输入为以下字段的拼接（用 `|` 分隔）：

```
{root_cause_category}|{signature}|{fix_strategy}
```

- `root_cause_category` 确保同类别错误在向量空间中接近
- `signature` 提供错误特征的主体语义
- `fix_strategy` 补充修复方式的语义信息，用于区分"同一 root cause 不同修复路径"的 Pitfall

只用 `signature` 单字段做 embedding 会导致分类信息丢失，不同 root cause 的 Pitfall 仅因描述文字相似就被错误聚类。

**Embedding 维度**：1536（Claude text-embedding-3-small），与 episodic_embeddings 一致，复用同一 Embedder 接口。

**SQLite BLOB 格式**：
- 小端序 float64 序列化，每个 float64 占 8 字节
- BLOB 前 2 字节存储维度（uint16 little-endian），后接 `维度 × 8` 字节的 float64 数组
- 反序列化时先读维度，再读 float64 数组，避免跨版本维度变更导致解析失败

### 2.5 pgvector 扩展

```sql
-- pitfall_embeddings 表（新增）
-- embedding 输入为 "{root_cause_category}|{signature}|{fix_strategy}"
CREATE TABLE pitfall_embeddings (
    id UUID PRIMARY KEY,
    pitfall_id UUID NOT NULL,
    embedding vector(1536),
    signature_text TEXT,
    project_id VARCHAR(255),
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX ON pitfall_embeddings
USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 50);  -- Pitfall 量级小于情境记忆，lists 更小
```

### 2.6 Link on Write：Pitfall 关联检查

写入 Episodic Memory 时，若 `task_type=debug` 且 `entity_id` 非空，同步执行 Pitfall 关联检查：

```
WriteEpisodic (task_type=debug, entity_id="func:NewRedisPool")
  → SearchPitfallByEntity(ctx, "func:NewRedisPool", projectID)
    → 若命中已有 Pitfall：
      → 创建 TRIGGERED_PITFALL 边 (Episodic → Pitfall)，similarity 字段为 0（精确 entity 匹配）
      → 更新 Pitfall.last_occurred_at = now，occurrence_count += 1
    → 若未命中：正常完成写入，不创建边
```

这实现了"写入时自动关联已知错误"——Agent 在第 N 次踩同一个坑时，系统不等检索就自动建立关联，为后续的错误感知检索积累数据。

实现位置：`internal/core/link_on_write.go` 的 `LinkOnWrite` 函数中扩展。当前函数已处理 Episodic → Episodic 的 RELATES_TO 边创建，v0.2 新增对 Pitfall 的检查。

```go
// LinkOnWrite 扩展（internal/core/link_on_write.go）
func LinkOnWrite(ctx context.Context, s store.MemoryStore, mem *models.EpisodicMemory) error {
    // 1. 现有逻辑：向量定位 + RELATES_TO 边
    // ...

    // 2. v0.2 新增：若 task_type=debug，检查已有 Pitfall
    if mem.TaskType == models.TaskTypeDebug && mem.EntityID != "" {
        pitfalls, _ := s.SearchPitfallByEntity(ctx, mem.EntityID, mem.ProjectID)
        for _, p := range pitfalls {
            s.CreateEdge(ctx, mem.ID, p.ID, "TRIGGERED_PITFALL", map[string]any{
                "similarity": 0.0, // 精确 entity 匹配
            })
            // 更新 occurrence
            p.OccurrenceCount++
            p.LastOccurredAt = &now
            // TouchPitfall 更新 last_occurred_at + occurrence_count
        }
    }
    return nil
}
```

此检查同步完成（写入延迟预算中预留 < 50ms），不额外增加 LLM 调用。

---

## 3. MemoryStore 接口扩展

### 3.1 新增 Pitfall 方法

在 `internal/store/memory_store.go` 的 `MemoryStore` 接口中新增：

```go
// ── Pitfall Memory ───────────────────────────────────────────────
WritePitfall(ctx context.Context, p *models.PitfallMemory) error
SearchPitfall(ctx context.Context, req PitfallSearchRequest) ([]models.PitfallMemory, error)
GetPitfall(ctx context.Context, id uuid.UUID) (*models.PitfallMemory, error)
TouchPitfall(ctx context.Context, id uuid.UUID, now time.Time) error
UpdatePitfallWeight(ctx context.Context, id uuid.UUID, weight float64) error
MarkPitfallObsolete(ctx context.Context, id uuid.UUID, at time.Time) error

// SearchPitfallByEntity finds all Pitfalls anchored on a specific entity.
SearchPitfallByEntity(ctx context.Context, entityID, projectID string) ([]models.PitfallMemory, error)
```

### 3.2 PitfallSearchRequest

```go
// PitfallSearchRequest carries filters + optional signature embedding.
// 双键匹配语义：当 EntityID 和 Embedding 同时非零值时，
// 执行 "entity_id 精确匹配 AND signature embedding 相似度 > 0.7" 的复合查询。
// 仅 EntityID：精确匹配该实体所有 Pitfall（调用 SearchPitfallByEntity）。
// 仅 Embedding：全库 signature embedding 相似度搜索。
// 两者皆空：全表扫描（按 weight DESC + limit 截断）。
type PitfallSearchRequest struct {
    ProjectID        string
    Language         string
    EntityID         string        // 键1：实体精确匹配
    RootCauseCategory RootCause    // optional: filter by root cause
    MinWeight        float64
    Limit            int
    Embedding        []float64     // 键2：query signature embedding（非零时启用相似度过滤，阈值 0.7）
}
```

双键 AND 逻辑在各后端的实现：
- **Neo4j+pgvector**：Cypher MATCH by entity_id → 收集 pitfall_ids → pgvector `WHERE pitfall_id IN (...) ORDER BY embedding <=> $q LIMIT 3`
- **SQLite**：`SELECT * FROM pitfall_memories WHERE entity_id = ?` → 应用层对结果集做 cosine 过滤
- **In-Memory**：map 遍历 entity_id 匹配 → 暴力 cosine 过滤

### 3.3 三个 Store 后端的实现策略（硬约束）

**所有 7 个 Pitfall 方法必须在三个后端中各有一份完整实现。** 与 v0.1.1 现存 15 个方法的覆盖模式一致：

| 后端 | Pitfall 节点存储 | Pitfall 向量存储 | 边存储 | 实现文件 |
|------|-----------------|-----------------|--------|---------|
| SQLite | `pitfall_memories` 表 + BLOB embedding 列 | 应用层 cosine 相似度（读 embedding → `math.Cos`） | `pitfall_edges` 表 | `internal/store/sqlite/pitfall.go` |
| Neo4j+pgvector | `(:PitfallMemory)` Neo4j 节点 | pgvector `pitfall_embeddings` 表 + IVFFlat `<=>` 算子 | Neo4j 原生关系 | `internal/store/neo4jpg/pitfall.go` |
| In-Memory | `map[uuid.UUID]*models.PitfallMemory` | 内存 `[]float64` + 暴力 cosine | 内存 adjacency list | `internal/store/memory/pitfall.go` |

**Store 工厂不变**：`app.NewMemoryStore(cfg, nc, pc)` 返回 `store.MemoryStore` 接口，调用方不感知后端差异。Pitfall 方法与现有方法共享同一个 Store 实例，复用其内部连接（SQLite `*sql.DB` / Neo4j `neo4j.Session` / pgvector `*sql.DB`）。

**Neo4j+pgvector 双写事务边界**：`WritePitfall` 在 Neo4j+pgvector 后端需要同时写入 Neo4j 节点和 pgvector embedding。处理策略：

1. 先写 Neo4j `CREATE (p:PitfallMemory {...})` → 拿到 pitfall node
2. 再写 pgvector `INSERT INTO pitfall_embeddings` → 拿到 embedding_id
3. 回写 Neo4j `SET p.signature_embedding_id = $embedding_id`
4. 若步骤 2 失败 → Neo4j 节点已创建但无 embedding_id（可被后续修复：SearchPitfall 做 embedding 匹配时，无 embedding_id 的 Pitfall 仅在 entity_id 精确匹配时返回）
5. 若步骤 3 失败 → embedding 已写入但 Neo4j 节点未关联（best-effort，下次整合时可修复孤儿 embedding）

这是与 WriteEpisodic 一致的双写策略——不引入分布式事务，接受 best-effort 语义。

**数据库迁移**：v0.2 新增的表和索引在各后端的创建策略：
- **SQLite**：在 `schema.go` 的 `ensureSchema` 中新增 `CREATE TABLE IF NOT EXISTS` 语句，与现有 episodic/semantic 表同级。已有数据库首次启动 v0.2 时自动创建新表。
- **Neo4j+pgvector**：在 `store.go` 的 `NewNeo4jPGStore` 中新增 Pitfall 相关约束（`CREATE CONSTRAINT IF NOT EXISTS`）和 pgvector `CREATE TABLE IF NOT EXISTS`。已有数据库自动补充。
- 不在 v0.2 引入显式 migration 版本号系统——三后端均使用 `IF NOT EXISTS` 语义，保证幂等升级。
- 升级前建议备份（文档中注明 `pg_dump` / `neo4j-admin dump` 命令）。

**SQLite 后端的向量处理**：
- `signature_embedding` 存储为 BLOB（`[]float64` → `binary.Write` little-endian）
- 检索时从 SQLite 读出所有候选 embedding，应用层 `math.Cos` 计算相似度并排序
- 与现有 `SearchEpisodic` 在 SQLite 后端的处理方式一致（v0.1.1 已有实现）

---

## 4. Pitfall 提取引擎

### 4.0 Pitfall 衰减参数

Pitfall 独立使用一套衰减参数，不与 Episodic 或 Semantic 共享。根据 DESIGN_PRINCIPLES 第 7 节"中等衰减（修复后降低但不消失）"的原则：

```go
// PitfallDecayConfig 定义 Pitfall 专用衰减参数
type PitfallDecayConfig struct {
    LambdaDecay      float64 // 默认 0.15 — Pitfall 单次访问衰减快于 Episodic (0.1)
    AlphaExperience  float64 // 默认 0.008 — 经验衰减慢于 Episodic (0.005)，错误教训更持久
    BetaActivity     float64 // 默认 0.03 — 活跃度衰减慢于 Episodic (0.05)，修复后不快速冷却
    CoolingThreshold float64 // 默认 0.005 — 冷存储阈值低于 Episodic (0.01)，错误不应被轻易遗忘
}
```

与 Episodic 的关键差异：
- `LambdaDecay` 更高（0.15 vs 0.1）：单次访问价值衰减更快，需要更频繁触发才保持高权重
- `BetaActivity` 更低（0.03 vs 0.05）：修复后的 Pitfall 不应快速冷却—修复了不代表不会复现
- `CoolingThreshold` 减半（0.005 vs 0.01）：错误记忆应保留更久

`MarkPitfallObsolete` 不直接删除或冻结 Pitfall，而是将其 `weight` 乘以 0.5——修复后降低影响但不消除。`obsoleted_at` 时间戳参与检索排序（标记为已修复的 Pitfall 在检索结果中降权但不隐藏）。

### 4.1 在 ConsolidationEngine 中的位置

v0.1 的 ConsolidationEngine.Run 流程：

```
scan → cluster → extract → backtest → persist (semantic rules only)
```

v0.2 扩展为：

```
scan → cluster ─┬─→ extract_rules → backtest → persist (semantic)
                └─→ extract_pitfalls → cluster_by_entity → backtest_dedup → persist (pitfall)
```

两条线并行运行，共享同一批扫描结果，但聚类维度不同：
- **语义规则线**：按 `(project_id, task_type)` 聚类 → 提炼通用知识
- **Pitfall 线**：仅取 `task_type=debug` 的记忆，按 `entity_id` 聚类 → 提炼错误模式

**Run 方法签名变更**：

```go
// v0.1: Run(ctx, s, emb) (ConsolidationRunResult, error)
// v0.2: 返回值扩展为包含 Pitfall 统计
func (e *ConsolidationEngine) Run(
    ctx context.Context,
    s store.MemoryStore,
    emb embedder.Embedder,
) (ConsolidationRunResult, error) {
    // ... scan + cluster (shared) ...
    // 并行：rule extraction + pitfall extraction
    // 合并结果到 ConsolidationRunResult
}
```

**ConsolidationRunResult 扩展**（`internal/models/api.go`）：

```go
type ConsolidationRunResult struct {
    // ... v0.1 字段不变 ...
    
    // v0.2 新增 Pitfall 统计
    PitfallsExtracted int     `json:"pitfalls_extracted"`  // 本次提取的 Pitfall 数量
    PitfallsMerged    int     `json:"pitfalls_merged"`     // 因去重合并的 Pitfall 数量
    PitfallsPersisted int     `json:"pitfalls_persisted"`  // 最终写入的 Pitfall 数量
    PitfallError      string  `json:"pitfall_error,omitempty"` // Pitfall 线错误详情（非阻断）
    RulesError        string  `json:"rules_error,omitempty"`   // 规则线错误详情（非阻断）
}
```

两条线任一失败不阻断另一条——各自设置对应的 Error 字段，Run 总是尝试完成两条线。

### 4.2 PitfallExtractor（新增）

```go
// internal/core/pitfall_extractor.go（新增）

// PitfallExtractor extracts PitfallMemory nodes from debug-type episodic clusters.
// It runs as part of the consolidation sleep cycle, in parallel with rule extraction.
type PitfallExtractor struct {
    MinClusterSize        int     // minimum episodics to trigger extraction (default 3)
    Embedder              embedder.Embedder
    LLMClient             LLMClient  // for structured extraction
}

// PitfallCandidate is a provisional pitfall before LLM structuring.
type PitfallCandidate struct {
    EntityID    string
    EpisodicIDs []uuid.UUID
    Summaries   []string
}

// ExtractPitfalls runs the full pitfall extraction pipeline:
// 1. Filter episodic scan to task_type=debug only
// 2. Cluster by entity_id
// 3. For each cluster >= MinClusterSize, call LLM to extract structured pitfall
// 4. Return structured PitfallMemory nodes ready for persistence
func (pe *PitfallExtractor) ExtractPitfalls(
    ctx context.Context,
    episodics []store.EpisodicScanItem,
) ([]models.PitfallMemory, error)
```

### 4.3 LLM Prompt 设计（Pitfall 提取）

```
System: You are an error pattern analyst. Given a cluster of debug
sessions about the same code entity, extract a structured pitfall memory.

Output JSON:
{
  "signature": "one-line error pattern description",
  "root_cause_category": "CONCURRENCY|RESOURCE_EXHAUSTION|CONFIG|CONTRACT_VIOLATION|LOGIC_ERROR|UNKNOWN",
  "fix_strategy": "≤500 chars: how the issue was resolved",
  "confidence": 0.0-1.0
}

Rules:
- signature should be a reusable pattern, not a specific traceback
- If the debug sessions describe DIFFERENT bugs on the same entity → return
  multiple pitfall objects (one per bug pattern)
- If summaries are insufficient to determine root cause → category=UNKNOWN
- fix_strategy must be actionable ("increase timeout" not "fix the bug")
```

### 4.4 回测（Pitfall 线）

与语义规则的回测不同，Pitfall 的回测不验证"预测准确率"，而是验证"排他性"：
- 同一个 entity 上的 debug 记忆，聚出的 Pitfall 是否能区分不同 bug？
- 若两个 Pitfall 的 signature embedding 相似度 > 0.9 → 可能重复 → 合并

**合并策略**：
1. 保留 `occurrence_count` 更高的 Pitfall 作为主体
2. 被合并方的 `occurrence_count` 累加到主体
3. 被合并方的 `source_episodic_ids`（通过 DERIVED_FROM 边）全部重新指向主体
4. 主体的 `fix_strategy` 取两者中更长的（假设包含更多信息）
5. 主体的 `last_occurred_at` 取两者中较新的
6. 主体的 `was_user_corrected` = 两者任一为 true 即为 true
7. 被合并方标记 `obsoleted_at`，创建 SUPERSEDED 边指向主体
8. 删除被合并方的 pitfall_embeddings 行，避免检索重复

---

## 5. 记忆再巩固（Memory Reconsolidation）

### 5.1 触发路径

```
Agent 检索 → 获得结果 → 用户/Agent 纠正某条结果
  → POST /memories/{id}/feedback {action: "corrected", correction_detail: {...}}
    → ReconsolidationEngine.Process(ctx, feedback)
```

### 5.2 再巩固流程

```
1. 定位被纠正的记忆（episodic 或 semantic）
2. 沿溯源链反向传播：
   - 若被纠正的是 SemanticMemory → 沿 DERIVED_FROM 边找到 source_episodic_ids
   - 若被纠正的是 EpisodicMemory → 直接更新
3. 更新源记忆：
   - weight *= 1.5（纠正信号是最强学习信号）
   - 追加 correction 记录到 summary
4. 若同一条源记忆被纠正 >= 2 次 → 标记为 "correction_source"，保护级别提升
5. 若纠正来自用户显式 feedback（而非 LLM 推断）→ 源记忆 trust_level += 1
```

### 5.3 ReconsolidationEngine（新增）

```go
// internal/core/reconsolidation_engine.go（新增）

// ReconsolidationEngine handles the back-propagation of correction signals
// to source memories. Every retrieval is a potential write — when a user
// corrects a retrieved result, that correction flows backward through the
// DERIVED_FROM traceability chain.
type ReconsolidationEngine struct{}

// Process handles a correction feedback event:
// - Finds the corrected memory
// - Traces DERIVED_FROM edges to source episodics
// - Updates source weights and content
// - Re-evaluates trust_level
func (re *ReconsolidationEngine) Process(
    ctx context.Context,
    s store.MemoryStore,
    correctedID uuid.UUID,
    detail models.CorrectionDetail,
) error
```

### 5.4 Feedback API 增强

现有 `POST /memories/{memory_id}/feedback` 已接受 `CorrectionDetail`。v0.2 在 handler 中增加再巩固调用：

```go
// v0.1 的 feedback_handler.go 仅记录 feedback（未实现权重更新）
// v0.2 增强：
func (h *FeedbackHandler) Handle(w http.ResponseWriter, r *http.Request) {
    // 1. 解析 FeedbackRequest
    // 2. 调用 WeightDecayEngine.ApplyFeedback(memoryID, action)
    // 3. 若 action == "corrected" → 调用 ReconsolidationEngine.Process(...)
    // 4. 返回更新后的 weight
}
```

---

## 6. 行为归因权重

### 6.1 从静态评分到行为驱动

v0.1 的权重初始化：

```go
// v0.1: 所有新记忆 weight=1.0
Weight: 1.0,
EffectiveFrequency: 1.0,
```

问题：一条被反复验证正确的记忆和一条从未被检索的记忆拥有相同的初始权重。

v0.2 改为**行为驱动的权重增量模型**：

### 6.2 行为 → 权重映射

`ApplyFeedback` 是一个**纯函数**，不挂在 `WeightDecayEngine` 上——WeightDecayEngine 负责"时间→衰减"，行为归因负责"行为→权重增量"，是两个独立的维度。放在 `internal/core/attribution.go`（新文件）：

```go
// internal/core/attribution.go（新增）

// ApplyFeedback computes the weight delta from post-retrieval behavior.
// It is a pure function — no side effects, no I/O.
func ApplyFeedback(
    currentWeight float64,
    currentEffFreq float64,
    action models.SearchAction,
    isUserCorrected bool,
) (newWeight float64, newEffFreq float64) {

    const (
        boostUsed      = 0.1   // 被检索命中且被采用
        boostCorrected = 0.5   // 引出了用户纠正（最强信号）
        boostConfirmed = 0.2   // 被用户确认
        penaltyIgnored = -0.05 // 被检索但未使用（微衰减）

        // 用户纠正的额外信任加成
        trustBoostCorrected = 0.3 // was_user_corrected = true 时的额外加权
    )

    delta := 0.0
    switch action {
    case SearchActionUsed:
        delta = boostUsed
    case SearchActionCorrected:
        delta = boostCorrected
        if isUserCorrected {
            delta += trustBoostCorrected
        }
    case SearchActionConfirmed:
        delta = boostConfirmed
    case SearchActionIgnored:
        delta = penaltyIgnored
    }

    newWeight = currentWeight + delta
    if newWeight < 0 {
        newWeight = 0
    }
    newEffFreq = currentEffFreq + math.Abs(delta)
    return
}
```

放在 `core` 包而非挂在 `WeightDecayEngine` 上的理由：衰减引擎管理"时间→衰减"维度，行为归因管理"行为→增量"维度。两者参数独立演变（衰减参数由项目活跃度驱动调优，归因权重由反馈质量驱动调优），不应耦合在一个 struct 里。

### 6.3 Touch 与 Feedback 的职责分离

**Touch 和 ApplyFeedback 是两个独立操作**，不应在 Touch handler 中隐式调用 ApplyFeedback：

- `POST /memories/{id}/touch`：记录检索命中 → 更新 `last_accessed_at` 和 `effective_frequency`（纯时间/频率维度）
- `POST /memories/{id}/feedback`：记录检索后行为 → 调用 `ApplyFeedback` 更新 weight（行为归因维度）

两者的区别：一条记忆被检索命中（touch）不等于被采用（used）。将 `touch` 等同于 `ApplyFeedback(action=used)` 会系统性高估被检索但因不相关而被忽略的记忆权重。

**Touch handler v0.2 实现**：

```go
func (h *TouchHandler) Handle(w http.ResponseWriter, r *http.Request) {
    // 1. 解析 memoryID
    // 2. 更新 last_accessed_at = now
    // 3. 重新计算 effective_frequency（加入本次访问时间戳）
    // 4. 用 WeightDecayEngine 重新计算 weight
    // 5. 调用 UpdateEpisodicWeight / UpdatePitfallWeight 持久化
    // 6. 返回 TouchResponse{NewWeight: ...}
    // 注意：不调用 ApplyFeedback — 那属于 feedback handler 的职责
}
```

所有权重更新同步完成（不异步）：touch / feedback 请求返回时，权重已写入。目标延迟 < 50ms（单次 UPDATE 操作）。

---

## 7. 错误感知检索

### 7.1 检索流程扩展

v0.1 检索流程：

```
POST /memories/search
  → EpisodicSearch (pgvector)
  → SemanticSearch (pgvector)
  → merge & return
```

v0.2 检索流程：

```
POST /memories/search
  → EpisodicSearch (pgvector)
  → SemanticSearch (pgvector)
  → PitfallSearch: 对 query 中涉及的 entity_id 做双键匹配    ← 新增
      - 键1: entity_id 精确匹配
      - 键2: signature embedding 相似度 > 0.7
  → merge episodic + semantic + pitfall → return
```

### 7.2 SearchResultItem 扩展

```go
// v0.2 的 SearchResultItem 增加 pitfall 相关字段
type SearchResultItem struct {
    // ... 现有字段不变 ...
    
    // Pitfall 相关（仅当 type="pitfall" 时填充）
    RootCauseCategory string `json:"root_cause_category,omitempty"`
    FixStrategy       string `json:"fix_strategy,omitempty"`
    WasUserCorrected  bool   `json:"was_user_corrected,omitempty"`
}
```

### 7.3 Pitfall 注入策略

- 每个 entity_id 最多注入 3 条 Pitfall（防止噪声）
- 注入结果在检索结果末尾，标记 `type: "pitfall"`
- 排序优先：was_user_corrected=true > occurrence_count 高 > weight 高

---

## 8. 源监控增强

### 8.1 trust_level 参与排序

v0.1 的 `SemanticRanker.Rank` 仅按 cosine 相似度排序。v0.2 增加 trust_level 加权：

```go
// v0.2: relevance = cosine_similarity * trust_multiplier
func trustMultiplier(level models.TrustLevel) float64 {
    switch level {
    case 5: return 1.25
    case 4: return 1.10
    case 3: return 1.00
    case 2: return 0.85
    case 1: return 0.70
    default: return 1.00
    }
}
```

### 8.2 source_type 影响默认 trust_level

| source_type | 默认 trust_level | 可覆盖条件 |
|-------------|-----------------|-----------|
| USER / USER_DECLARED | 5 | 仅用户显式推翻 |
| AST | 4 | 代码变更自动更新 |
| DEBUG | 3 | 用户纠正或更多证据 |
| INFERRED / SUMMARIZED | 2 | 任何冲突源可覆盖 |
| LLM | 1 | 任何冲突源可覆盖 |

在写入时自动设置 `trust_level`（除非调用方显式指定）：

```go
func DefaultTrustLevel(st SourceType) TrustLevel {
    switch st {
    case SourceTypeUSER, SourceTypeUserDeclared:
        return 5
    case SourceTypeAST:
        return 4
    case SourceTypeDEBUG:
        return 3
    case SourceTypeINFERRED, SourceTypeSummarized:
        return 2
    case SourceTypeLLM:
        return 1
    default:
        return DefaultTrustLevel // models.DefaultTrustLevel (3)
    }
}
```

### 8.3 冲突解决规则

冲突解决在 `SemanticRanker.Rank` 中执行（排序阶段，非写入阶段）。当两条语义规则冲突时（同一 entity_group，内容矛盾），按优先级裁决：

1. trust_level 高的覆盖低的
2. 同等 trust_level → was_user_corrected=true 优先
3. 同等 → weight 高的优先
4. 同等 → 创建 CONFLICTS_WITH 边，标记 "unresolved"，等人工裁决

在排序阶段而非写入阶段执行的理由：冲突判断需要 LLM 理解两条规则是否真的矛盾，这有成本——不应在每次写入时执行，而应在检索时（候选集已缩小到 top_k 量级）按需判断。SemanticRanker 在排序 top_k 结果后，对相邻排名条目检查冲突关系。

```go
// SemanticRanker.Rank 中新增冲突检测
func (sr *SemanticRanker) Rank(
    ctx context.Context,
    candidates []ScoredCandidate,
    conflictResolver *ConflictResolver, // 新增可选依赖
) []ScoredCandidate {
    // 1. 正常排序（cosine * trust_multiplier）
    // 2. 对 top_k 内相邻条目做冲突检测
    // 3. 若冲突 → 按规则降权被覆盖方（不删除，保留在结果中降权显示）
    // 4. 若无法裁决 → 创建 CONFLICTS_WITH 边，双方保留
}
```

---

## 9. API 变更

### 9.1 新增端点

```
POST /api/v0/pitfalls/search
  搜索 Pitfall Memory（双键匹配：entity_id + signature embedding）

  Request:
  {
    "entity_id": "func:NewRedisPool",     // 可选
    "project_id": "my-backend-service",
    "root_cause_category": "CONFIG",      // 可选
    "top_k": 10
  }

  Response:
  {
    "results": [
      {
        "pitfall_id": "uuid",
        "entity_id": "func:NewRedisPool",
        "signature": "Redis MaxOpenConns < 100 causes pool exhaustion under load",
        "root_cause_category": "RESOURCE_EXHAUSTION",
        "fix_strategy": "Set MaxOpenConns >= 200 for production, add connection pool monitoring",
        "was_user_corrected": true,
        "occurrence_count": 5,
        "weight": 3.2,
        "relevance_score": 0.94
      }
    ]
  }
```

### 9.2 修改的端点

| 端点 | 变更 |
|------|------|
| `POST /memories/search` | 检索结果中注入相关 Pitfall |
| `POST /memories/{id}/feedback` | 触发再巩固反向传播 + ApplyFeedback 行为归因权重更新 |
| `POST /memories/{id}/touch` | 更新 last_accessed_at + effective_frequency，重新计算双参照衰减权重（不调用 ApplyFeedback——行为归因属于 feedback handler 职责） |

### 9.3 MCP 工具变更

| 工具 | 变更 |
|------|------|
| `search_memories` | 返回结果中包含 Pitfall 匹配 |
| `write_memory` | 写入时附带 entity_id → 触发 Pitfall 关联检查（Link on Write 扩展） |
| `search_pitfalls` | **新增**：双键匹配 Pitfall 搜索 |

---

## 10. 实现路线

**全局约束**：所有新增存储操作一律通过 `store.MemoryStore` 接口，不直接依赖任何 DB driver。每个 Phase 的变更在三个后端（SQLite / Neo4j+pgvector / In-Memory）中同步实现。CI 中三个后端各自的单元测试和集成测试均需通过。

### Phase 0: v0.1 遗留项补齐（P0，1-2 天）

- [ ] **ClaudeEmbedder 真实实现** — `internal/embedder/claude_embedder.go`，对接 Claude API
- [ ] **ConsolidationEngine LLM 提炼** — `extractRule` 改为调用 LLM
- [ ] **回测真实验证** — `backtest` 改为 LLM 预测验证（采样 20 条 held-out 情境记忆）

### Phase 1: Pitfall 数据模型 + Store 接口扩展（1-2 天）

- [ ] `internal/models/pitfall_memory.go` — PitfallMemory / EntityType / RootCause 定义
- [ ] `MemoryStore` 接口扩展 — 8 个 Pitfall 方法
- [ ] **SQLite Store** — `internal/store/sqlite/pitfall.go`，含 `pitfall_memories` + `pitfall_edges` 表 DDL、BLOB embedding 读写、应用层 cosine 排序
- [ ] **Neo4j+pgvector Store** — `internal/store/neo4jpg/pitfall.go`，含 `(:PitfallMemory)` 节点 + 6 种边类型、pgvector `pitfall_embeddings` 表自动建表
- [ ] **In-Memory Store** — `internal/store/memory/pitfall.go`，含 map + adjacency list + 暴力 cosine
- [ ] 三个后端的 Pitfall 单元测试（每个后端覆盖 WritePitfall / SearchPitfall / SearchPitfallByEntity / 边的创建与查询）

### Phase 2: Pitfall 提取引擎（1-2 天）

- [ ] `internal/core/pitfall_extractor.go` — PitfallExtractor
- [ ] LLM Prompt 调优（Pitfall 结构化提取）
- [ ] ConsolidationEngine.Run 扩展 — 并行执行 rule + pitfall 两条线
- [ ] 单元测试（Pitfall 聚类 + 提取 + 持久化）

### Phase 3: 记忆再巩固（1 天）

- [ ] `internal/core/reconsolidation_engine.go` — ReconsolidationEngine
- [ ] Feedback handler 增强 — 调用 ReconsolidationEngine.Process
- [ ] 溯源链遍历 — DERIVED_FROM 边反向查询
- [ ] 单元测试（纠正 → 反向传播 → 权重更新）

### Phase 4: 行为归因权重（1 天）

- [ ] `internal/core/attribution.go` — `ApplyFeedback` 纯函数，行为 → 权重映射
- [ ] Feedback handler 增强 — 调用 ApplyFeedback
- [ ] 权重明细 API 扩展 — 展示行为归因历史
- [ ] 单元测试（四种 action 的权重变化验证）

### Phase 5: 错误感知检索 + 源监控（1-2 天）

- [ ] `Search handler` 增强 — Pitfall 注入检索结果
- [ ] `SemanticRanker` 增强 — trust_level 加权
- [ ] `DefaultTrustLevel(source_type)` — 写入时自动设置 trust_level
- [ ] MCP `search_pitfalls` 工具
- [ ] 集成测试（完整检索 → Pitfall 注入链路）

### Phase 6: API + MCP + 定时调度（1 天）

- [ ] `POST /api/v0/pitfalls/search` 端点
- [ ] MCP tool 更新（search_memories 扩展 + search_pitfalls 新增）
- [ ] 后台定时调度 — cron 每日凌晨 3 点触发整合
- [ ] OpenAPI 文档更新

---

## 11. 关键决策记录

### D1: Pitfall 为什么不复用 SemanticMemory

独立节点类型的理由见第 2.1 节。核心：检索触发时机不同（规划型 vs 纠错型），衰减曲线不同（慢 vs 中），且独立节点允许未来在 v0.3 引入"进入模块前主动注入"的优化，不需要检索。

### D2: Pitfall 提取阈值为什么 >= 3

ROADMAP 中提到"要求 >= 5 条同一模式才触发提取"。考虑到 v0.2 的数据积累量（debug 记录建议 > 200 条），初始阈值设为 3 以增加召回。若噪声大，后续调高至 5。

### D3: 为什么行为归因权重同步而非异步

v0.2 的反馈量级小（每次检索后最多一次 feedback），同步更新避免了异步队列的复杂性。到 v1.0 多 Agent 场景再考虑异步批量更新。

### D4: Pitfall 注入最多 3 条的硬限制

来自 DESIGN_PRINCIPLES.md 的"7±2 法则"——工作记忆容量有限。Pitfall 是辅助信号而非主要内容，1-3 条足够提供风险提醒，过多会稀释检索结果。

### D5: trust_level 加权系数范围 0.70-1.25

保持加权系数的保守范围，确保信任度调整排序但不主导排序。向量语义相似度仍然是主排序依据，trust 只是微调。

### D6: LLM 提炼和 Pitfall 提取共用一个 LLM 调用还是分开

分开调用。语义规则提炼和 Pitfall 提取的 prompt 结构、输出格式、聚类维度完全不同，共用会导致 prompt 膨胀和输出质量下降。两阶段并行调用，总延迟取 max。

---

## 12. 测试策略

### 单元测试

| 模块 | 覆盖率目标 | 关键场景 |
|------|-----------|---------|
| pitfall_extractor | >85% | 空 debug 集、单 entity 多 bug、LLM 返回异常 JSON |
| reconsolidation_engine | >90% | 纠正语义 → 反向传播到源情境、纠正情境 → 直接更新、重复纠正保护 |
| weight_decay.ApplyFeedback | >90% | 四种 action 组合、连续纠正、边界值 |
| pitfall_search (各 store) | >85% | entity 匹配、signature embedding 匹配、空结果 |
| trust_level ranking | >85% | 极值（1 vs 5）、同等 trust 不同 source_type |

### 集成测试

- Pitfall 提取端到端：写入 20 条 debug 情境记忆 → 触发整合 → 验证产出 Pitfall 节点
- 再巩固端到端：写入情境 → 整合产生语义 → 纠正语义 → 验证源情境权重更新
- 错误感知检索：写入 Pitfall → 搜索相关 entity → 验证 Pitfall 注入
- 权重行为归因：写入记忆 → touch → feedback → 验证权重增量

### 性能基准

| 指标 | v0.2 目标 |
|------|----------|
| Pitfall 提取延迟（单 entity） | < 500ms |
| Pitfall 提取延迟（批量 10 entity） | < 3s |
| 再巩固反向传播 | < 100ms |
| 错误感知检索（含 Pitfall 注入） | < 150ms（命中）/ < 600ms（miss） |
| 行为归因权重更新 | < 50ms |

---

## 13. 风险与缓解

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| debug 记录不足（< 50 条）导致 Pitfall 提取质量低 | 中 | 高 | 人工注入历史调试记录做种子数据；降低 MinClusterSize 到 2 |
| LLM 提取的 Pitfall 噪声大 | 高 | 中 | 严格回测：signature embedding 相似度 > 0.9 的 Pitfall 自动合并；MinClusterSize 可配置 |
| 再巩固反向传播污染源记忆 | 低 | 高 | 仅在被纠正 >= 2 次时才标记 "correction_source"；保留原始 summary 的 append-only 记录 |
| 行为归因权重与衰减曲线交互复杂 | 中 | 低 | ApplyFeedback 只改 weight 不改衰减参数（α/β 保持不变） |
| trust_level 加权导致新鲜记忆被埋没 | 低 | 中 | 加权系数保守（0.70-1.25），向量相似度仍是主排序信号 |

---

**技术方案详见**：
- Phase 2（Pitfall 提取引擎）：
  - `tech-specs/02a-pitfall-clustering.md` — 聚类策略：entity 分组 + 子聚类
  - `tech-specs/02b-pitfall-llm-extraction.md` — LLM 提取：prompt 设计 + 输出解析 + 去重
  - `tech-specs/02c-consolidation-parallel.md` — ConsolidationEngine 并行化改造
- Phase 3（记忆再巩固）：
  - `tech-specs/03a-reconsolidation-traceback.md` — 溯源链反向传播
  - `tech-specs/03b-reconsolidation-protection.md` — 保护级别系统与 Feedback 管线

*本文档是 v0.2 的初始草案。随实现推进，具体接口和参数会细化。所有设计决策应回溯到 `../DESIGN_PRINCIPLES.md` 进行一致性验证。*
