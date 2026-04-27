# Agent Context Archive

> 历史工作上下文归档。按时间倒序。

---

## 2026-04-27 — Phase 3 API 层

### 完成事项

1. **基础设施增强**
   - `internal/db/redis/redis.go` — +`LPush`/`LTrim`/`LRange`（working-memory 循环存储）
   - `internal/db/pgvector/pgvector.go` — +`GetEmbedding`（pattern separation 用）
   - 新增依赖：`go-chi/chi/v5`、`golang.org/x/sync`

2. **新包**
   - `internal/config/` — `Config` 结构体 + `LoadFromEnv()`（61 个环境变量，匹配 docker-compose 默认值）
   - `internal/embedder/` — `Embedder` 接口 + `StubEmbedder`（零向量）+ `ClaudeEmbedder` 骨架

3. **ConsolidationEngine** (`internal/core/consolidation_engine.go`)
   - Scan → Cluster（by project_id+task_type）→ Extract（拼接 summaries）→ Backtest → Persist
   - 11 个单元测试

4. **API 层** (`internal/api/`)
   - `server.go` — go-chi/v5 路由 + 中间件 + 优雅关闭
   - `write_handler.go` — Significance Gate → Embed → Pattern Separation → Neo4j + pgvector
   - `search_handler.go` — ContextualGating → pgvector similarity → merge semantic
   - `feedback_handler.go` — 行为反馈 → 权重增量更新
   - `touch_handler.go` / `consolidation_handler.go` / `health_handler.go`
   - 8 个 HTTP endpoints

### 关键决策

- **无 Repository 层**：handlers 直接 Cypher
- **StubEmbedder** 返回零向量 → pattern separation 总是判 merge
- **Consolidation** 聚类/提取/回测均为 v0.1 桩实现

---

## 2026-04-27 (下午) — Phase 2 核心算法

1. `internal/core/weight_decay.go` — Recency-Weighted Frequency + 双参照衰减 + 冷却阈值
2. `internal/core/significance_gate.go` — 四条件显著性门控
3. `internal/core/pattern_separation.go` — 可分离性判别（三阶段检查）
4. `internal/core/retrieval_engine.go` — 两阶段检索（ContextualGating + SemanticRanker）
5. 47 个单元测试全通过

---

## 2026-04-27 (中) — Phase 1 数据模型

1. `internal/models/common.go` — 9 枚举 + IsValid() + 常量
2. `internal/models/episodic_memory.go` — EpisodicMemory + 4 边类型
3. `internal/models/semantic_memory.go` — SemanticMemory + 4 边类型
4. `internal/models/context.go` — SearchContext
5. `internal/models/api.go` — 14 API 请求/响应类型

---

## 2026-04-27 (早) — Phase 0 基础设施搭建

1. 技术栈从 Python 切换到 Go
2. Go 项目骨架：`go.mod`、`cmd/vatbrain/main.go`
3. `docker-compose.yml`：Neo4j 5 + pgvector/pg16 + Redis 7 + MinIO
4. `scripts/init_db.sh`：Neo4j 约束 + pgvector 表 + 健康检查
5. `internal/db/` 连接层：neo4j、pgvector、redis、minio
