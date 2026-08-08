# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成 Phase 2 — vatbrain-provider daemon + hermes 插件 ✅ 完成（未提交）
- **语言**: Go (go 1.25.5) + Python（hermes 插件）
- **分支**: `feature/agent-memory-watcher`（本地领先 origin 1 commit，未推送）
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点
- **hermes 源码**: 本机 `~/.hermes/hermes-agent`（HEAD 52920747e，工作树干净）
- **hermes 插件**: 已安装 `~/.hermes/plugins/vatbrain/` + daemon 二进制 `~/.hermes/vatbrain/bin/vatbrain-provider`
- **战略决策（2026-08-08）**: Neo4j+pgvector 重型存储将弃用，后续只投入 SQLite 路径

## 最近工作（2026-08-08）— Phase 2 vatbrain-provider daemon + hermes 插件

### 本次完成

1. **`internal/core/write_pipeline.go`**（新）：共享写路径 `WriteMemory(ctx, deps, event, projectID, language, entityID, taskType)`——gate → embed → pattern-separation merge → persist → link-on-write → working-mem push。MCP `write_memory` 与 daemon `sync_turn` 共用，行为不漂移。`ClampWeight` 移入 core（mcp 转发兼容）。
2. **IsCorrection 持久化**：`models.EpisodicMemory.IsCorrection bool` + sqlite `is_correction` 列（`migrate()` 含 ALTER 迁移）+ neo4j 属性（重型后端仅保留一致性，不再投入）。
3. **`internal/provider/`**（新）：
   - `derive.go` — WriteEvent 规则层（§5）：`UserConfirmed` regex（记住/记得/remember this…）、`IsCorrection` regex（不对/应该是/actually/别用/改成…，短消息<200字）；**UserConfirmed 优先于纠正判定**；`Summary` 去 `继续：` 前缀 + 截断 500 字
   - `server.go` — stdio 行分隔 JSON-RPC 2.0：`initialize`/`sync_turn`/`ping`/`shutdown`；非 primary context 只读；sync_turn 30s 写超时；FIFO 由 hermes 单 worker 保证
4. **`cmd/vatbrain-provider/main.go`**（仓库首个可执行二进制）：flags `--store sqlite` `--data`；env 配置化走 `app.New`；SIGTERM/中断优雅退出
5. **hermes 插件 `plugins/vatbrain/`**：`register(ctx)` 模式、spawn daemon（stdio Popen）、sync_turn 后台线程 + io_lock FIFO、`is_available()` 检查二进制；安装到真实 `~/.hermes/plugins/vatbrain/`
6. **验收达成**：
   - daemon 冒烟：纠错 → `prediction_error` + `is_correction:true`，sqlite `is_correction=1` ✅
   - hermes 加载器发现 `vatbrain` + `is_available()==true` + 真实 venv 全链验证（`tests/provider_plugin_smoke.py`）✅
   - `go test ./internal/...` 20/20 包全绿（provider 14 用例含中文）
   - 已安装到真实 HERMES_HOME（插件 + 二进制）✅
7. **设计文档**：`docs/v0.2.1/tech-specs/02-vatbrain-provider.md`（协议/架构/验收/后续）

### 当前状态

- 本地领先 origin 1 commit 未提交未推送
- 真实 `~/.hermes/config.yaml` **未激活**（被权限门拦下）：需加 `memory: {provider: vatbrain}` 才能让 hermes 加载 provider 并出现 "Memory provider 'vatbrain' activated" 日志——**待用户授权**
- `~/.hermes/config.yaml` 无 memory 段（内置 memory 工具仍关，符合 D5）

## 下一步

1. 提交 Phase 2（独立 commit）→ 推送
2. **待用户授权**：激活 `~/.hermes/config.yaml` `memory.provider: vatbrain`（手动或授权我做），下次 hermes 启动验证 activated 日志
3. Phase 3 — 读路径：daemon `prefetch`/`queue_prefetch` + Pitfall 注入（hermes manager 负责 `<memory-context>` 栅栏，provider 不自带）
4. Phase 4 — 生命周期：`on_session_end`/`on_memory_write`/`on_session_switch`

## 已知问题

- OpenCode 适配器仍为骨架（`internal/watcher/adapters/opencode.go:61` TODO，v0.2.1 GA 待办）
- 插件 `shutdown()` 时后台 sync 线程可能未完成（best-effort，daemon 退出即弃）
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 语义；replace/obsolete 镜像属 Phase 4）
