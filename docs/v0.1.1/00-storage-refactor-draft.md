# VatBrain v0.1.1 — 存储层可插拔重构草案

> 状态：**技术调研草案**
>
> 起草时间：2026-04-29
>
> 前置阅读：
> - `docs/DESIGN_PRINCIPLES.md` — 设计基石
> - `docs/ROADMAP.md` — 版本路线图
> - `docs/v0.1/00-design.md` — v0.1 设计定稿
>
> **版本定位**：v0.1.x 系列的工程改进，不挤占 Roadmap 中 v0.2（记忆进化）的功能空间。

---

## 0. 问题诊断

### 0.1 当前架构的痛

v0.1 的存储层是**硬编码的**：`consolidation_engine.go` 直接接受 `neo4j.Client` 和 `pgvector.Client` 的接口，`internal/app/app.go` 初始化 4 个数据库连接：

```
当前链路：
  app.New()
    ├─ neo4j.NewClient()    ──→  bolt://localhost:7687
    ├─ pgvector.NewClient() ──→  localhost:5432
    ├─ redis.NewClient()    ──→  localhost:6379
    └─ minio.NewClient()    ──→  localhost:9000
```

痛点：

1. **无 Docker 跑不起来**——想快速体验 VatBrain 必须先装 4 个容器，违反"先跑通再优化"的开发直觉
2. **每层数据库都耦合到引擎代码**——`RetrievalEngine.Retrieve()` 期望调用方传入候选集（从 Neo4j 查好的 EpisodicMemory 列表），引擎和数据层职责模糊
3. **切换后端不可能**——如果你想在测试中用纯内存、在生产中用 pgvector，代码不变做不到
4. **v0.1 的 Roadmap 明确说了"零进程依赖"是未来目标，但没有任何工程支撑**

### 0.2 重构目标

| 目标 | 说明 |
|------|------|
| **零 Docker 快速启动** | `go run ./cmd/vatbrain/` 一键启动，SQLite 自动建表 |
| **存储可插拔** | 同一套引擎代码，通过配置切换 SQLite / Neo4j+pgvector / 内存 |
| **引擎与存储解耦** | 核心算法不 import 任何 DB driver |
| **降级有底线** | 向量搜索、图关联、热记忆缓存三个核心能力在所有后端都必须保留，只降级实现方式，不降级检索语义 |
| **零破坏性** | 现有 MCP Tools 和 HTTP API 不变，只切换底层实现 |

---

## 1. 新架构设计

### 1.1 分层模型

```
┌──────────────────────────────────────────────────────────┐
│                   API / MCP Layer                         │
│  (write_handler, search_handler, consolidation_handler)   │
│  — 不变，只调用 MemoryStore 接口                          │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│                  Core Engines                             │
│  WeightDecay / SignificanceGate / PatternSeparation       │
│  RetrievalEngine / ConsolidationEngine                    │
│  — 纯算法，不 import 任何 DB driver                       │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│              MemoryStore Interface (新增)                 │
│                                                           │
│  WriteEpisodic / SearchEpisodic / TouchMemory             │
│  GetSemantic / WriteSemantic / CreateEdge                 │
│  ScanRecent / PersistRule / GetConsolidationRun           │
└──┬──────────────┬──────────────┬─────────────────────────┘
   │              │              │
   ▼              ▼              ▼
┌──────┐  ┌────────────┐  ┌──────────┐
│SQLite│  │Neo4j+pg    │  │ Memory   │
│Store │  │Vector Store│  │ Store    │
└──────┘  └────────────┘  └──────────┘
```

### 1.2 核心接口：`MemoryStore`

不再让引擎直接操作 Neo4j/pgvector。所有存储操作通过一个统一接口。接口参数设计为**存储无关**——调用方传入 embedding，后端各自决定怎么存、怎么搜。

```go
// internal/store/memory_store.go（新增包）

// MemoryStore is the single abstraction over all VatBrain persistence.
// Each backend (SQLite, Neo4j+pgvector, in-memory) implements this interface.
type MemoryStore interface {
    // ── Episodic Memory ──────────────────────────────────────
    WriteEpisodic(ctx context.Context, mem *models.EpisodicMemory) error
    SearchEpisodic(ctx context.Context, req EpisodicSearchRequest) ([]models.EpisodicMemory, error)
    TouchEpisodic(ctx context.Context, id uuid.UUID, now time.Time) error
    UpdateEpisodicWeight(ctx context.Context, id uuid.UUID, weight, effFreq float64) error
    MarkObsolete(ctx context.Context, id uuid.UUID, at time.Time) error

    // ── Semantic Memory ─────────────────────────────────────
    WriteSemantic(ctx context.Context, mem *models.SemanticMemory) error
    SearchSemantic(ctx context.Context, req SemanticSearchRequest) ([]models.SemanticMemory, error)

    // ── Edges ───────────────────────────────────────────────
    CreateEdge(ctx context.Context, from, to uuid.UUID, edgeType string, props map[string]any) error
    GetEdges(ctx context.Context, nodeID uuid.UUID, edgeType string, direction string) ([]Edge, error)

    // ── Consolidation ───────────────────────────────────────
    // ScanRecent returns episodic memories modified since a given time.
    ScanRecent(ctx context.Context, since time.Time, limit int) ([]EpisodicScanItem, error)
    PersistRule(ctx context.Context, rule models.ConsolidationRule) error
    SaveConsolidationRun(ctx context.Context, run *models.ConsolidationRun) error
    GetConsolidationRun(ctx context.Context, runID uuid.UUID) (*models.ConsolidationRun, error)

    // ── Lifecycle ───────────────────────────────────────────
    HealthCheck(ctx context.Context) error
    Close() error
}

// EpisodicSearchRequest carries both structured filters and an optional
// embedding for semantic similarity search. Backends that support vector
// search use Embedding; others fall back to structured filtering + in-process
// cosine similarity over the candidate set.
type EpisodicSearchRequest struct {
    ProjectID       string
    Language        string
    TaskType        models.TaskType
    MinWeight       float64
    Limit           int
    IncludeObsolete bool

    // Embedding is the query vector for semantic similarity search.
    // Set by the caller (engine/embedder). Backends that don't natively
    // support vector ops process it in application code.
    Embedding []float64
}

// SemanticSearchRequest carries filters and an optional embedding for
// semantic similarity search over consolidated rules/facts.
type SemanticSearchRequest struct {
    ProjectID  string
    MemoryType models.MemoryType
    Limit      int

    // Embedding is the query vector for semantic similarity search.
    Embedding []float64
}

// EpisodicScanItem is a lightweight projection returned by ScanRecent,
// owned by the store package to avoid circular dependencies with core.
type EpisodicScanItem struct {
    ID           uuid.UUID
    Summary      string
    TaskType     models.TaskType
    ProjectID    string
    Language     string
    EntityGroup  string
    Weight       float64
    LastAccessed time.Time
}

// Edge represents a directed relationship between two memory nodes.
type Edge struct {
    FromID     uuid.UUID
    ToID       uuid.UUID
    EdgeType   string
    Properties map[string]any
    CreatedAt  time.Time
}
```

**设计要点**：

- `SearchEpisodic` 和 `SearchSemantic` 的请求都携带 `Embedding` 字段。调用方（embedder）负责生成向量，后端决定怎么用它。SQLite 后端 SELECT 候选集后在 Go 进程内算余弦相似度；pgvector 后端直接用 `<=>` 操作符。接口不预设任何后端的实现路径。
- `ScanRecent` 返回 `EpisodicScanItem`（store 包自有类型），而非 `core.EpisodicScanResult`。避免 store → core 的反向依赖。
- 接口为一个大接口而非拆分为 `EpisodicStore` / `SemanticStore` / `EdgeStore`。v0.1.1 阶段注入点只有一处（引擎），拆分接口增加的注入复杂度无实际收益。若未来出现只依赖子集的场景，Go 的 interface embedding 可以零成本拆分。

### 1.3 引擎改造

引擎不再直接操作 DB。拿 `ConsolidationEngine` 举例：

**改造前** `Run()` 签名：
```go
func (e *ConsolidationEngine) Run(
    ctx context.Context,
    neo4jClient interface{ ExecuteRead(...); ExecuteWrite(...) },
    pgvectorClient interface{ InsertEmbedding(...) },
    embedder interface{ Embed(...) },
) (models.ConsolidationRunResult, error)
```

**改造后** `Run()` 签名：
```go
func (e *ConsolidationEngine) Run(
    ctx context.Context,
    store MemoryStore,      // ← 统一接口
    embedder Embedder,
) (models.ConsolidationRunResult, error)
```

引擎内部的 `scan()` / `persist` 不再拼 Cypher 查询，改调 `store.ScanRecent()` / `store.PersistRule()`。

`RetrievalEngine` 同理——不再要求调用方传入 `[]models.EpisodicMemory` 列表，而是内部调用 `store.SearchEpisodic()`。

### 1.4 后端注册

```go
// internal/store/registry.go
type Backend string

const (
    BackendSQLite      Backend = "sqlite"
    BackendNeo4jPg     Backend = "neo4j+pgvector"
    BackendMemory      Backend = "memory"
)

// NewMemoryStore creates the configured backend.
func NewMemoryStore(cfg config.StoreConfig) (MemoryStore, error) {
    switch cfg.Backend {
    case BackendSQLite:
        return NewSQLiteStore(cfg.SQLite)
    case BackendNeo4jPg:
        return NewNeo4jPgStore(cfg.Neo4j, cfg.Pgvector)
    case BackendMemory:
        return NewInMemoryStore(), nil
    default:
        return nil, fmt.Errorf("unknown backend: %s", cfg.Backend)
    }
}
```

### 1.5 Fallback 链（v0.1.1 后期可选）

```go
// 支持主备切换：生产用 Neo4j+pgvector，挂了用 SQLite 继续跑
type FallbackStore struct {
    primary   MemoryStore
    fallback  MemoryStore
}

func (f *FallbackStore) WriteEpisodic(ctx context.Context, mem *models.EpisodicMemory) error {
    err := f.primary.WriteEpisodic(ctx, mem)
    if err != nil {
        slog.Warn("primary store failed, falling back", "err", err)
        return f.fallback.WriteEpisodic(ctx, mem)
    }
    return nil
}
```

v0.1.1 先做接口 + 两套实现，fallback 链推迟。

---

## 2. SQLite 后端设计

### 2.1 设计原则：降级实现方式，不降级检索语义

SQLite 后端的核心约束是"单文件、零外部进程"。在这个约束下：

| 能力 | Neo4j+pgvector 实现 | SQLite 实现 | 语义差异 |
|------|-------------------|-------------|---------|
| **向量相似搜索** | pgvector `<=>` 操作符 | BLOB 存 embedding → SELECT 候选集 → Go 进程内余弦相似度 TopK | 无，排序算法相同 |
| **图关联查询** | Cypher MATCH 1-hop | SQL JOIN `memory_edges` 表 | 无，1-hop 完全等价 |
| **热记忆缓存** | Redis Sorted Set | 进程内 LRU（`hashicorp/golang-lru/v2`） | 无，淘汰策略相同（weight 排序） |
| **多跳图遍历** | Cypher 多跳 MATCH | 不实现 | 有损，但当前业务无此需求 |
| **快照存储** | MinIO | 不实现 | 有损，v0.1 本身未完整实现 |

底线：**Link on Write 的完整流程（向量定位 → 可分离性判别 → 建立边 → 空间压缩）在所有后端上逻辑等价**。

### 2.2 新增支撑模块

在写 SQLite 后端之前，先抽两个无外部依赖的工具包：

#### `internal/vector/` — 进程内向量运算

```go
// internal/vector/vector.go

// CosineSimilarity returns the cosine similarity between two vectors.
func CosineSimilarity(a, b []float64) float64

// TopK returns the indices of the top K scores in descending order.
func TopK(scores []float64, k int) []int

// DotProduct returns the dot product of two vectors.
func DotProduct(a, b []float64) float64

// Encode serializes a float64 slice to a binary blob for SQLite BLOB storage.
func Encode(v []float64) []byte

// Decode deserializes a binary blob back to a float64 slice.
func Decode(b []byte) []float64
```

纯数学，零外部依赖。`Encode`/`Decode` 用 `encoding/binary`（little-endian float64 序列化），也支持 JSON text 作为备选存储格式。

#### `internal/store/lru/` — 进程内热记忆缓存

```go
// internal/store/lru/cache.go

// HotCache is an in-process LRU cache for frequently accessed memories.
// It replaces Redis for hot-memory indexing in the SQLite backend.
type HotCache[K comparable, V any] struct {
    // wraps hashicorp/golang-lru/v2 with TTL
}

func NewHotCache[K, V any](maxSize int, ttl time.Duration) *HotCache[K, V]
func (c *HotCache[K, V]) Get(key K) (V, bool)
func (c *HotCache[K, V]) Set(key K, value V)
func (c *HotCache[K, V]) Remove(key K)
```

当 `SearchEpisodic` 命中热缓存时直接返回 TopK，无需扫全表 + 算余弦。缓存 key 为 `(project_id, language, task_type)` 三元组 + query embedding 的近似哈希。

### 2.3 Schema

```sql
-- Episodic memories
CREATE TABLE IF NOT EXISTS episodic_memories (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    language TEXT NOT NULL,
    task_type TEXT NOT NULL,
    summary TEXT NOT NULL,
    source_type TEXT NOT NULL,
    trust_level INTEGER NOT NULL DEFAULT 3,
    weight REAL NOT NULL DEFAULT 1.0,
    effective_frequency REAL NOT NULL DEFAULT 1.0,
    entity_group TEXT DEFAULT '',
    context_vector BLOB DEFAULT NULL,    -- float64 array, binary encoded
    full_snapshot_uri TEXT DEFAULT '',
    created_at DATETIME NOT NULL,
    last_accessed_at DATETIME,
    obsoleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_episodic_project ON episodic_memories(project_id, language);
CREATE INDEX IF NOT EXISTS idx_episodic_task ON episodic_memories(task_type);
CREATE INDEX IF NOT EXISTS idx_episodic_weight ON episodic_memories(weight DESC);

-- Semantic memories (consolidation results)
CREATE TABLE IF NOT EXISTS semantic_memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,               -- RULE | FACT | PATTERN | CONSTRAINT
    content TEXT NOT NULL,
    source_type TEXT NOT NULL,
    trust_level INTEGER NOT NULL DEFAULT 3,
    weight REAL NOT NULL DEFAULT 1.0,
    effective_frequency REAL NOT NULL DEFAULT 1.0,
    entity_group TEXT DEFAULT '',
    consolidation_run_id TEXT DEFAULT '',
    backtest_accuracy REAL DEFAULT 0.0,
    source_episodic_ids TEXT DEFAULT '',  -- JSON array of UUIDs
    context_vector BLOB DEFAULT NULL,     -- float64 array, binary encoded
    created_at DATETIME NOT NULL,
    last_accessed_at DATETIME,
    obsoleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_semantic_type ON semantic_memories(type);
CREATE INDEX IF NOT EXISTS idx_semantic_project ON semantic_memories(entity_group);

-- Edges between memories
CREATE TABLE IF NOT EXISTS memory_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,          -- PRECEDES | CAUSED_BY | RELATES_TO | DERIVED_FROM | ...
    properties TEXT DEFAULT '{}',     -- JSON
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_edges_from ON memory_edges(from_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_to ON memory_edges(to_id, edge_type);

-- Consolidation runs
CREATE TABLE IF NOT EXISTS consolidation_runs (
    id TEXT PRIMARY KEY,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    episodics_scanned INTEGER DEFAULT 0,
    candidate_rules_found INTEGER DEFAULT 0,
    rules_persisted INTEGER DEFAULT 0,
    average_accuracy REAL DEFAULT 0.0
);
```

关键变化：`context_vector` 字段从 `TEXT`（JSON string）改为 `BLOB`，存 `[]float64` 的二进制编码。BLOB 比 JSON text 省 50% 空间，且序列化/反序列化更快。

### 2.4 SearchEpisodic 的 SQLite 实现路径

```
SearchEpisodic(req):
  1. 检查热缓存（LRU），命中直接返回 TopK
  2. SELECT 候选集（硬约束过滤：project_id, language, task_type, min_weight）
  3. 若有 req.Embedding 且候选集有 context_vector：
       Go 进程内算每条的余弦相似度
       按相似度降序 + weight 加权排序
       取 TopK
  4. 若无 req.Embedding（纯结构化查询）：
       按 weight DESC + last_accessed_at DESC 排序
       取 TopK
  5. 写入热缓存，返回结果
```

候选集通常 < 1000 条。1000 条 × 1536 维向量的余弦相似度计算在 Go 中 < 10ms。

### 2.5 配置

```bash
# SQLite 模式（默认，零外部进程依赖）
VATBRAIN_STORE_BACKEND=sqlite
VATBRAIN_SQLITE_PATH=./vatbrain.db

# 完整的 Neo4j+pgvector 模式（需要 Docker）
VATBRAIN_STORE_BACKEND=neo4j+pgvector
NEO4J_URI=bolt://localhost:7687
# ... 其余已有配置
```

`config.LoadFromEnv()` 新增 `StoreConfig` 段。

---

## 3. 对现有代码的影响面

### 3.1 需要改的文件

| 文件 | 改造 | 工作量 |
|------|------|--------|
| `internal/app/app.go` | `New()` 使用 `NewMemoryStore(cfg)` 替代直接初始化 DB | 中 |
| `internal/core/consolidation_engine.go` | `Run()` 签名改为接收 `MemoryStore`，内部调 store 方法 | 中 |
| `internal/core/retrieval_engine.go` | `Retrieve()` 内部调 `store.SearchEpisodic()` 获取候选集 | 小 |
| `internal/api/write_handler.go` | 直接调 `store.WriteEpisodic()` | 小 |
| `internal/api/search_handler.go` | 直接调 `store.SearchEpisodic()` + 引擎 | 小 |
| `internal/api/consolidation_handler.go` | 调引擎时传入 store | 小 |
| `internal/mcp/*_tool.go` | 同 API 层改动 | 小 |
| `internal/config/config.go` | 新增 `StoreConfig` | 小 |

### 3.2 需要新增的文件

| 文件 | 内容 |
|------|------|
| `internal/vector/vector.go` | 余弦相似度、TopK、向量编解码（纯数学，零外部依赖） |
| `internal/vector/vector_test.go` | 向量工具函数测试 |
| `internal/store/lru/cache.go` | 进程内热记忆缓存 |
| `internal/store/memory_store.go` | `MemoryStore` 接口定义 + `EpisodicSearchRequest` 等类型 |
| `internal/store/sqlite/` | SQLite 后端实现（schema, store, tests） |
| `internal/store/neo4j_pg/` | Neo4j+pgvector 后端（重构现有 DB 代码） |
| `internal/store/memory/` | In-memory 后端（测试用） |
| `internal/store/registry.go` | 后端注册 |

### 3.3 不需要改的

- **五个核心引擎的纯算法部分**：`WeightDecay.Weight()`、`SignificanceGate.Evaluate()`、`PatternSeparation.Check()` 等——它们不碰 DB
- **数据模型**：`internal/models/` 不变
- **Embedder**：`internal/embedder/` 不变
- **MCP Server 框架**：`internal/mcp/mcp_server.go` tool 注册方式不变

---

## 4. 实施路线

### Phase 1：基础设施 + 接口定义

```
PR-1: 向量工具包 + LRU 缓存
  → internal/vector/vector.go
  → internal/vector/vector_test.go
  → internal/store/lru/cache.go

PR-2: MemoryStore 接口 + 类型定义
  → internal/store/memory_store.go
  → internal/store/registry.go
  → config.StoreConfig
```

### Phase 2：SQLite 后端

```
PR-3: SQLite 实现
  → internal/store/sqlite/schema.go（建表语句）
  → internal/store/sqlite/sqlite_store.go（完整 MemoryStore 实现）
  → internal/store/sqlite/sqlite_store_test.go
```

**验收**：
- [ ] MemoryStore 全套接口有 SQLite 实现
- [ ] 写入 100 条 → 向量检索 → 衰减 → 整合 端到端通过（使用 stub embedder 生成的随机向量）
- [ ] Link on Write 流程完整：写入时向量定位 → 发现相似记忆 → 建立 RELATES_TO 边
- [ ] 测试覆盖率 ≥ 80%

### Phase 3：引擎 + API 层适配

```
PR-4: 引擎改签名
  → ConsolidationEngine.Run(ctx, store, embedder)
  → RetrievalEngine.Retrieve() 内部调 store
  → 现有测试适配新签名

PR-5: API/MCP 层切换
  → handlers 使用 store 替代 db.Client
  → app.go 用 NewMemoryStore(cfg)
```

**验收**：
- [ ] `VATBRAIN_STORE_BACKEND=sqlite` → `go run ./cmd/vatbrain/` 启动成功，健康检查通过
- [ ] MCP Tools 全量可用（SQLite 后端）
- [ ] 现有测试全 PASS（SQLite 后端）

### Phase 4：Neo4j+pgvector 后端重构

```
PR-6: 现有 DB 代码迁移
  → internal/store/neo4j_pg/ 封装现有 neo4j/pgvector/redis/minio 逻辑
  → 实现 MemoryStore 接口
  → 回归测试确保不降级
```

**验收**：
- [ ] `VATBRAIN_STORE_BACKEND=neo4j+pgvector` 完整功能可用
- [ ] 两种后端跑同一套集成测试，结果一致（向量相似度差异 < 1e-6）
- [ ] docker-compose 环境回归通过

---

## 5. 关键决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| 接口粒度 | 一个大接口 `MemoryStore` | v0.1.1 注入点只有一处，拆分无收益。Go interface embedding 可零成本后拆 |
| 向量搜索 | 所有后端都支持，SQLite 用进程内余弦相似度 | 向量搜索是 Link on Write 和两阶段检索的基石，不能降级为关键词匹配 |
| Embedding 存储 | SQLite 用 BLOB（binary encoding） | 比 JSON text 省 50% 空间，序列化更快 |
| 热缓存 | 进程内 LRU 替代 Redis | golang-lru/v2 零外部依赖，单进程场景比 Redis 更快 |
| 图查询 | SQLite 后端做 1-hop JOIN，不做多跳 | 当前所有业务查询均为 1-hop，JOIN 完全等价 |
| 快照存储 | SQLite 后端不实现 MinIO 写入 | 快照功能 v0.1 本身未完整实现，不是回归 |
| 测试策略 | 每个后端跑同一套一致性测试 | 确保切换后端后向量搜索、权重更新、边创建行为一致 |
| 版本定位 | v0.1.1 而非 v0.2 | v0.2 按 Roadmap 做记忆进化（Pitfall + 再巩固 + 行为归因），存储重构是工程改进 |

---

## 6. 风险与未决项

| 风险 | 缓解 |
|------|------|
| SQLite 并发限制 | VatBrain 当前是单进程，无并发写入场景。若未来需要，WAL 模式可支持读并发 |
| 候选集过大导致进程内余弦计算耗时 | 硬约束（project_id + language + task_type）将候选集压到 < 1000 条，< 10ms |
| Cypher → SQL 语义差异 | 1-hop 查询用 JOIN 等价实现，多跳查询在实现层标记 `TODO` |
| 接口膨胀 | 先按 Phase 1 定义最小接口，实践 2 周后再补缺失方法 |

### 未决项

- 是否需要在 v0.1.1 同时支持 Neo4j+pgvector 后端？（建议：是，保持向后兼容）
- SQLite 是否需要默认开启 WAL 模式？（建议：默认开启）
- 是否需要数据迁移工具——从 SQLite 升到 Neo4j+pgvector？（建议：后续版本考虑）

---

## 7. 与 HOC 项目的协同

HOC v0.4 计划将 VatBrain 作为 Go 库引入。本次重构是 HOC 集成的前置条件：

| 时间线 | VatBrain | HOC |
|--------|----------|-----|
| 当前 | v0.1 完成，4 容器硬依赖 | v0.3 完成 |
| Phase 1-3 | MemoryStore 接口 + SQLite 后端 + 引擎适配 | — |
| Phase 4 | Neo4j+pgvector 后端重构 | — |
| 发布 | 发布 v0.1.1（`go get` 可用） | v0.4 Phase 1：`internal/memory/` 封装 |

HOC 只依赖 `MemoryStore` 接口 + SQLite 后端。Neo4j+pgvector 后端对 HOC 不可见（编译时通过 import path 隔离，类似 `database/sql` 的模式）。

---

*草案版本。待审查后进入技术规约 + Phase 1 实施。*
