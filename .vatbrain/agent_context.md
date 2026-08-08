# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: v0.2.1 — Agent Memory Watcher 实施中
- **语言**: Go (go 1.25.5)
- **分支**: `feature/agent-memory-watcher`

## 最近工作（2026-05-19）— 下一阶段演进计划

### 本次完成

- 新增 `docs/EVOLUTION_PLAN.md`，基于竞品调研重排下一阶段路线。
- 核心定位从泛用 AI Agent memory layer 收窄为 **面向 coding agent 的 Pitfall-aware memory layer**。
- 建议版本顺序：v0.2.1 Watcher GA → v0.2.2 Pitfall Workbench → v0.3 Proactive Risk Injection → v0.3.1 Evaluation Harness → v0.4 Public Developer Experience → v1.0 Team Memory。
- 在 `docs/ROADMAP.md` 顶部加入演进计划入口。

### 下一步建议

- 先完成 v0.2.1 Watcher GA 和 5 分钟 demo，不再扩大 v0.3 范围。
- 后续补 `docs/DEMO_SCRIPT.md` 与 `docs/COMPETITIVE_LANDSCAPE.md`，再调整 README 首屏定位。

## 最近工作（2026-05-19）— 业界竞品/相邻项目调研

### 本次结论

- 外部已有多个 AI Agent memory / context infrastructure 项目，赛道已被验证：Mem0、Zep/Graphiti、Letta、Cognee、MIRIX、A-MEM，以及一批面向 coding agent 的 MCP 记忆工具。
- VatBrain 不应定位为泛用“又一个 memory SDK”，更适合收窄到 **coding agent / agent workbench 的经验记忆层**。
- 可差异化方向：Pitfall Memory、错误/纠正驱动的 Reconsolidation、显式 Decay/冷却阈值、上下文门控、Agent Memory Watcher 对多工具记忆的同步。

### 建议下一步

- 补一个 `docs/COMPETITIVE_LANDSCAPE.md` 或 README 的 “Why VatBrain” 小节，明确与 Mem0/Zep/Cognee/Letta 的边界。
- 把 v0.2.1 的 Agent Memory Watcher 做成首个 demo：自动吸收 Claude Code/Cursor/OpenCode 记忆，提炼 pitfall，并在 MCP 检索中注入。

## 最近工作（2026-05-10/11）— Agent Memory Watcher 实施

### 已完成

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

## 下一步

- 提交 commit 到 `feature/agent-memory-watcher` 分支
- 可选：集成测试（需要设置 ANTHROPIC_API_KEY + 真实 Claude Code memory 目录）
