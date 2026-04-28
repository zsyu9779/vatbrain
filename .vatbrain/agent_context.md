# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: v0.1 完成，v0.2 调研启动
- **语言**: Go (go 1.25.5)
- **版本定位**: MCP Server 就位，AI Agent 可通过 MCP 协议调用记忆系统

## 最近工作（2026-04-29）— v0.2 存储重构调研

### 存储层可插拔重构草案

- 创建 `docs/v0.2/00-storage-refactor-draft.md`
- 核心目标：定义 `MemoryStore` 接口，让 VatBrain 支持 SQLite / Neo4j+pgvector / In-Memory 三种后端
- SQLite 后端可实现零 Docker 快速启动，解决 v0.1 "必须 4 容器才能跑"的痛点
- 与 HOC v0.4 集成方案联动：HOC 将 VatBrain 作为 Go 库引入，需要 SQLite 后端

## 下一步

- 等待审查技术草案
- 确定后进入详细技术规约 + Phase 1 实施（MemoryStore 接口定义）

---

## 历史（2026-04-27）— Phase 4 MCP Server

### 完成事项

1. **共享初始化** (`internal/app/app.go`)
   - `App` 结构体 + `New()` 构造函数，封装所有 DB/Engine 初始化
   - `cmd/vatbrain/main.go` 简化为 ~25 行

2. **MCP Server** (`internal/mcp/`)
   - `mcp_server.go` — `NewMCPServer(a)` + `RegisteredTools(a)` 导出函数
   - 6 个 MCP Tools，全部使用 `mark3labs/mcp-go` v0.49.0 stdio transport：
     | Tool | 对应 API |
     |------|---------|
     | `write_memory` | `POST /memories/episodic` |
     | `search_memories` | `POST /memories/search` |
     | `trigger_consolidation` | `POST /consolidation/trigger` |
     | `get_memory_weight` | `GET /memories/{id}/weight` |
     | `touch_memory` | `POST /memories/{id}/touch` |
     | `health_check` | `GET /health` |
   - `helpers.go` — `stringFromMeta`、`clampWeight` 共用的工具函数
   - `mcp_server_test.go` — 8 个测试（外部包 `mcp_test`，使用 `mcptest`）

3. **MCP 入口** (`cmd/vatbrain-mcp/main.go`)
   - stdio transport，`app.New()` → `mcp.NewMCPServer()` → `server.ServeStdio()`

### 关键决策

- **工具注册重构**: 从 `registerXxx(s *server.MCPServer, a *app.App)` 改为 `xxxTool(a *app.App) server.ServerTool`，以便测试使用 `mcptest.NewServer(t, tools...)`
- **测试包**: `mcp_test` (外部包)，避免与 `mark3labs/mcp-go/mcp` 的包名冲突
- **DB nil 安全**: health_check 和 trigger_consolidation 在 DB 为 nil 时优雅降级

### 测试结果

- 47 核心测试 + 8 MCP 测试 = 55 全通过
- `go vet ./...` 清洁
- 两个二进制均编译成功

## 下一步（Phase 5: 端到端集成测试）

1. Docker 环境验证（docker-compose up + 冒烟测试）
2. `ClaudeEmbedder` 实现（替代 StubEmbedder）
3. 项目配置 Claude Code MCP 工具（.claude/settings.local.json 添加 vatbrain-mcp）
4. 端到端场景验证：写入 → 检索 → 整合

---
