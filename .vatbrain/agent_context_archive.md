# Agent Context Archive

> 历史工作上下文归档。按时间倒序。

---

## 2026-05-08 — Cursor 聊天记录批量导入

1. **Cursor 聊天记录导入工具** (`cmd/vatbrain/import_cursor.go`)
   - 扫描 `~/.cursor/projects/*/agent-transcripts/*/*.jsonl` 解析 JSONL 对话
   - 自动推断 language（go/zh/proto）、task_type（debug/feature/refactor/review）
   - 支持 `--dry-run`（预览）和 `--limit N`（限制数量）
2. **全量导入完成**: 269 条 episodic memory → Neo4j + pgvector
3. **修复 Bug**: `time.Now()` → `time.Now().UTC()`、字节截断 → rune 截断
4. **scanEpisodic/scanSemantic 修复**: 改用 `dbtype.Node` 读取属性

---

## 2026-04-29 — v0.1.1 Phase 3 完成

- Engine 层 Adaption：ConsolidationEngine.Run()/LinkOnWrite 接受 MemoryStore 接口
- API 层 6 handler 重写 + MCP 层 6 tool 重写
- SQLite Store: INSERT OR REPLACE、WorkingMemoryBuffer 替换 Redis
- 编译/测试全绿

---

## 2026-04-27 — Phase 4 MCP Server

### 完成事项

1. **共享初始化** (`internal/app/app.go`)
   - `App` 结构体 + `New()` 构造函数，封装所有 DB/Engine 初始化
   - `cmd/vatbrain/main.go` 简化为 ~25 行

2. **MCP Server** (`internal/mcp/`)
   - 6 个 MCP Tools：write_memory, search_memories, trigger_consolidation, get_memory_weight, touch_memory, health_check
   - `mcp_server_test.go` — 8 个测试

3. **MCP 入口** (`cmd/vatbrain-mcp/main.go`)

### 关键决策
- 工具注册重构为 `xxxTool(a *app.App) server.ServerTool`
- DB nil 安全：health_check / trigger_consolidation 优雅降级
- 测试包：`mcp_test` (外部包)

### 测试结果：55 全通过，go vet 清洁

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

---

## 2026-05-10/11 — Agent Memory Watcher 实施

基于 `docs/v0.2.1/tech-specs/01-agent-memory-sync.md` 设计文档实现。

**Step 1-4 已完成**, Step 5 验证中：

1. **Watcher 基础设施** (`internal/watcher/`)
   - `provider.go` — MemoryProvider 接口、RawMemory、ProviderRegistry、seenSet (LRU + JSON 持久化)
   - `watcher.go` — MemoryWatcher 编排器（周期性轮询、去重、写入管道）
   - `refiner.go` — LLM 提炼 + 启发式回退管线
   - 17 个单元测试全部通过

2. **4 个适配器** (`internal/watcher/adapters/`)
   - `claude_code.go` — Claude Code (P0): 扫描 `~/.claude/projects/*/memory/*.md`，解析 YAML frontmatter
   - `opencode.go` — OpenCode (P1): 骨架适配器（待调研具体格式）
   - `cursor.go` — Cursor (P1): JSONL 增量解析，复用 import_cursor 逻辑
   - `custom.go` — Custom (P1): YAML 驱动，用户可自定义任意 Agent 格式

3. **MCP 工具** (`internal/mcp/`)
   - `list_adapters` — 列出所有适配器及状态
   - `sync_memories` — 手动触发全量同步
   - `configure_adapter` — 运行时创建 Custom 适配器

4. **App 集成** (`internal/app/app.go`)
   - 条件创建：`VATBRAIN_WATCHER_ENABLED=true` 时启动
   - 生命周期：Start (goroutine) / Stop (graceful) + SeenSet 持久化

5. **配置** (`internal/config/config.go`)
   - 新增 WatcherConfig，7 个环境变量

### 测试结果

- `go build ./...` ✅ 通过
- `go vet ./...` ✅ 通过
- `go test ./...` ✅ 395 通过, 4 失败（均来自 tests/ 的 Neo4j/Pgvector E2E，预先存在）
- 新增 17 个 watcher 测试 + 现有 MCP 测试全部通过

## 已知问题

- 无阻断性问题
- OpenCode 适配器为骨架（需调研具体存储格式）
- Cursor 适配器使用轮询而非 fsnotify（当前设计简化，后续可加 Watch 方法）
