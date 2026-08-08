# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成 Phase 1 — hermes Watcher 适配器 ✅ 完成（待推送）
- **语言**: Go (go 1.25.5)
- **分支**: `feature/agent-memory-watcher`（本地领先 origin 1 个 commit，待推送）
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点
- **hermes 源码**: 本机 `~/.hermes/hermes-agent`（git 仓库，origin: NousResearch/hermes-agent，HEAD 52920747e，工作树干净）——格式/协议事实可直接查 `tools/memory_tool.py` / `agent/memory_provider.py`

## 最近工作（2026-08-08）— Phase 1 hermes Watcher 适配器完成

### 本次完成

1. **`internal/watcher/adapters/hermes.go`**（新）：
   - 扫描 `$HERMES_HOME/memories/MEMORY.md` + `USER.md`，`"\n§\n"` 分隔（与 hermes `tools/memory_tool.py` 的 `ENTRY_DELIMITER`/`_parse_entries` 语义逐条核对）
   - 块头（`MEMORY (your personal notes)` 等）只进系统提示不进文件——已从源码确认，解析器保留防御性跳过（手改文件）
   - SourceURI `hermes://memories/<target>#<sha256前8>`（内容派生哈希，整体原子重写下 URI 稳定）；ProjectID = home 目录名去点前缀（`~/.hermes` → `hermes`）
   - homeDir 空值解析顺序：`HERMES_HOME` env → `~/.hermes`（多 profile 跟随）
2. **`hermes_test.go`**（12 个用例，含中文）：中文条目解析、§ 分隔/空条目、块头跳过、双文件、缺目录/文件、跨扫描哈希稳定、条目编辑 URI 变化、HERMES_HOME env 解析、ModifiedAt
3. **`hermes_e2e_test.go`**（watcher 包外部测试）：真实管道（provider→heuristic refiner→memory store）三轮轮询——首轮 2 条入库、次轮全 skip 无重复、追加条目后只写新增 1 条；`ScanRecent` 可检索（即验收"写入可检索"）
4. **装配**：`app.go` "all" 名单加 `hermes` + enabled 分支；`config.go` WatcherConfig 加 `HermesHomeDir`（env `VATBRAIN_WATCHER_HERMES_HOME`，空默认）；config_test 补默认/自定义断言（PollInterval 默认 300s ≤ 5min 验收成立）
5. **真实路径验证**（临时测试，已删）：真实 `~/.hermes/memories/MEMORY.md`（3001B）解析出 5 条完整中文条目

### 当前状态

- `go test ./internal/...` 416 全绿；vet 干净；tests/ 4 个基础设施 E2E 失败为预存在
- 验收达成：写入 5 分钟内可检索（轮询默认 300s + E2E 验证）、重复轮询无重复条目（seenSet 按 (SourceURI, ContentHash) 去重）
- 本地领先 origin 1 commit 未推送

## 下一步

1. 推送本次 commit（或连同 Phase 2 一起推）
2. Phase 2 — provider 骨架 + 写路径（`cmd/vatbrain-provider/` stdio JSON-RPC + `$HERMES_HOME/plugins/vatbrain/`），详见 HANDOFF §4 表格
3. v0.2.1 GA 准出顺带达成「至少 2 个真实 source」（claude-code + hermes）

## 已知问题

- OpenCode 适配器仍为骨架（`internal/watcher/adapters/opencode.go:61` TODO，v0.2.1 GA 待办）
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 天然语义，replace/obsolete 镜像属 Phase 4 `on_memory_write` 范畴）
- 本机 `AGENTS.md`（Codex 版工作纪律）与 CLAUDE.md 同构，已在分支上跟踪；切分支时本地未跟踪副本曾冲突（已备份到 /tmp/AGENTS.md.localbackup）
