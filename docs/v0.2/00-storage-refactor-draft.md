# VatBrain v0.2 — 存储层可插拔重构草案

> 状态：**技术调研草案**
>
> 起草时间：2026-04-29
>
> 前置阅读：
> - `docs/DESIGN_PRINCIPLES.md` — 设计基石
> - `docs/ROADMAP.md` — 版本路线图
> - `docs/v0.1/00-design.md` — v0.1 设计定稿

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

1. **无 Docker 跑不起来**——想快速体验 VatBrain 必须先装 4 个容器，这违反了"先跑通再优化"的开发直觉
2. **每层数据库都耦合到引擎代码**——`RetrievalEngine.Retrieve()` 期望调用方传入候选集（从 Neo4j 查好的 EpisodicMemory 列表），引擎和数据层职责模糊
3. **切换后端不可能**——如果你想在测试中用纯内存、在生产中用 pgvector，代码不变做不到
4. **v0.1 的 Roadmap 明确说了"零进程依赖"是未来目标，但没有任何工程支撑**

### 0.2 重构目标

| 目标 | 说明 |
|------|------|
| **零 Docker 快速启动** | `go run ./cmd/vatbrain/` 一键启动，SQLite 自动建表 |
| **存储可插拔** | 同一套引擎代码，通过配置切换 SQLite / Neo4j+pgvector / 内存 |
| **热回退** | 外部 DB 不可用时自动降级到 SQLite（可选） |
| **引擎与存储解耦** | 核心算法不 import 任何 DB driver |
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
│(v0.2)│  │(当前实现)   │  │(测试用)  │
└──────┘  └────────────┘  └──────────┘
```

### 1.2 核心接口：`MemoryStore`

不再让引擎直接操作 Neo4j/pgvector。所有存储操作通过一个统一接口：

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
    ScanRecent(ctx context.Context, since time.Time, limit int) ([]core.EpisodicScanResult, error)
    PersistRule(ctx context.Context, rule models.ConsolidationRule) error
    SaveConsolidationRun(ctx context.Context, run *models.ConsolidationRun) error
    GetConsolidationRun(ctx context.Context, runID uuid.UUID) (*models.ConsolidationRun, error)

    // ── Lifecycle ───────────────────────────────────────────
    HealthCheck(ctx context.Context) error
    Close() error
}

type EpisodicSearchRequest struct {
    ProjectID     string
    Language      string
    TaskType      models.TaskType
    MinWeight     float64
    Limit         int
    IncludeObsolete bool
}

type SemanticSearchRequest struct {
    ProjectID   string
    MemoryType  models.MemoryType
    Limit       int
}
```

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
        return NewMemoryStore(), nil
    default:
        return nil, fmt.Errorf("unknown backend: %s", cfg.Backend)
    }
}
```

### 1.5 Fallback 链（可选，v0.2 后期）

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

v0.2 先做接口 + 两套实现，fallback 链可推迟到 v0.3。

---

## 2. SQLite 后端设计

### 2.1 Schema

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
    context_vector TEXT DEFAULT '',   -- JSON array of floats, or null
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

### 2.2 设计约束

- **不做图遍历**：SQLite 不支持 Cypher。关联查询用 JOIN。跨两跳以上的图查询在 v0.2 中不实现，需要时通过多次 SELECT 拼装
- **不做向量搜索**：无 pgvector。搜索用 `LIKE` + 关键词匹配 + 权重排序。语义相似度检索在 v0.2 降级为结构化过滤
- **不缓存热记忆**：无 Redis。用 `ORDER BY weight DESC LIMIT 100` 替代
- **不做快照存储**：无 MinIO。`full_snapshot_uri` 字段保留但 v0.2 不写入

**这些降级是可接受的**——v0.2 SQLite 后端的目标是"能跑通全链路"，不是"性能 100 分"。

### 2.3 配置

```bash
# SQLite 模式（默认，零依赖）
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
| `internal/store/memory_store.go` | `MemoryStore` 接口定义 |
| `internal/store/sqlite/` | SQLite 后端实现 |
| `internal/store/neo4j_pg/` | Neo4j+pgvector 后端（重构现有 DB 代码） |
| `internal/store/memory/` | In-memory 后端（测试用） |
| `internal/store/registry.go` | 后端注册 + Fallback 逻辑 |
| `internal/store/*_test.go` | 各后端一致性测试 |

### 3.3 不需要改的

- **五个核心引擎的纯算法部分**：`WeightDecay.Weight()`、`SignificanceGate.Evaluate()`、`PatternSeparation.Check()` 等——它们不碰 DB
- **数据模型**：`internal/models/` 不变
- **Embedder**：`internal/embedder/` 不变
- **MCP Server 框架**：`internal/mcp/mcp_server.go` tool 注册方式不变

---

## 4. 实施路线

### Phase 1：接口定义 + SQLite 后端

```
PR-1: MemoryStore 接口
  → internal/store/memory_store.go
  → internal/store/registry.go
  → config.StoreConfig

PR-2: SQLite 实现
  → internal/store/sqlite/sqlite_store.go
  → internal/store/sqlite/schema.go（建表语句）
  → internal/store/sqlite/sqlite_store_test.go
```

**验收**：
- [ ] MemoryStore 全套接口有 SQLite 实现
- [ ] 写入 100 条 → 检索 → 衰减 → 整合 端到端通过（用 stub embedder）
- [ ] 测试覆盖率 ≥ 80%

### Phase 2：引擎 + API 层适配

```
PR-3: 引擎改签名
  → ConsolidationEngine.Run(ctx, store, embedder)
  → RetrievalEngine.Retrieve() 内部调 store
  → 现有 55 个测试适配新签名

PR-4: API/MCP 层切换
  → handlers 使用 store 替代 db.Client
  → app.go 用 NewMemoryStore(cfg)
```

**验收**：
- [ ] `VATBRAIN_STORE_BACKEND=sqlite` → `go run ./cmd/vatbrain/` 启动成功，健康检查通过
- [ ] MCP Tools 全量可用（SQLite 后端）
- [ ] 现有 55 个测试全 PASS（SQLite 后端）

### Phase 3：Neo4j+pgvector 后端重构

```
PR-5: 现有 DB 代码迁移
  → internal/store/neo4j_pg/ 封装现有 neo4j/pgvector/redis/minio 逻辑
  → 实现 MemoryStore 接口
  → 回归测试确保不降级
```

**验收**：
- [ ] `VATBRAIN_STORE_BACKEND=neo4j+pgvector` 完整功能可用
- [ ] 两种后端跑同一套集成测试，结果一致
- [ ] docker-compose 环境回归通过

### Phase 4（可选）：Fallback 链 + In-Memory 后端

```
PR-6: Fallback + Memory Store
  → FallbackStore 实现
  → MemoryStore 纯内存实现（给测试 + 快速原型用）
```

---

## 5. 关键决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| 接口粒度 | 一个大接口 `MemoryStore` | 拆分过细的接口（`EpisodicStore`/`SemanticStore`）增加注入复杂度，v0.2 无需过度抽象 |
| 向量搜索降级 | SQLite 后端不做向量相似度 | pgvector 替换成本高，v0.2 用结构化过滤 + 关键词替代，够用 |
| 图查询降级 | SQLite 后端不做多跳遍历 | 大多数场景是 1 跳（RELATES_TO、DERIVED_FROM），JOIN 足够 |
| 缓存降级 | SQLite 后端不做 Redis 热缓存 | `ORDER BY weight DESC LIMIT N` 在万级数据下性能可接受 |
| 快照降级 | SQLite 后端不做 MinIO | 快照功能 v0.1 本身未完整实现，不是回归 |
| 测试策略 | 每个后端跑同一套一致性测试 | 确保切换后行为一致 |

---

## 6. 风险与未决项

| 风险 | 缓解 |
|------|------|
| SQLite 并发限制 | VatBrain 当前是单进程，无并发写入场景。若未来需要，WAL 模式可支持读并发 |
| Cypher → SQL 语义差异 | 图关系用 JOIN 表模拟，1 跳查询等价，多跳查询先在实现层标记 `TODO` |
| Embedding 存储差异 | pgvector 存向量，SQLite 存空。`SearchEpisodic` 的 SQLite 实现用文本过滤替代 |
| 接口膨胀 | 先按 Phase 1 定义最小接口，实践 2 周后再补缺失方法 |

### 未决项

- 是否需要在 v0.2 同时支持 Neo4j+pgvector 后端？（建议：是，保持向后兼容）
- SQLite 是否需要支持 WAL 模式？（建议：默认开启）
- 是否需要数据迁移工具——从 SQLite 升到 Neo4j+pgvector？（建议：v0.3 考虑）

---

## 7. 与 HOC 项目的协同

HOC v0.4 计划将 VatBrain 作为 Go 库引入。本次重构是 HOC 集成的前置条件：

| 时间线 | VatBrain | HOC |
|--------|----------|-----|
| 当前 | v0.1 完成，4 容器硬依赖 | v0.3 完成 |
| Phase 1-2 | MemoryStore 接口 + SQLite 后端 | — |
| Phase 3 | Neo4j+pgvector 后端重构 | — |
| Phase 4 | 发布 v0.2（`go get` 可用） | v0.4 Phase 1：`internal/memory/` 封装 |

HOC 只依赖 `MemoryStore` 接口 + SQLite 后端。Neo4j+pgvector 后端对 HOC 不可见（编译时通过 import path 隔离，类似 `database/sql` 的模式）。

---

*草案版本。与 HOC v0.4 集成方案联动，待两边方案稳定后进入技术规约阶段。*
