# vatbrain-provider — hermes MemoryProvider 写路径（Phase 2 设计定稿）

> 状态：✅ 已实现（2026-08-08）。设计基线：`docs/HERMES_INTEGRATION.md` §5（WriteEvent 自动推导）。
> 决策 D1-D5 全部落地；本阶段只做写路径，读路径（prefetch）与生命周期钩子在 Phase 3/4。

## 1. 架构

```
hermes (config.yaml memory.provider: vatbrain)
  └─ plugins/vatbrain/__init__.py (MemoryProvider, register(ctx))
       │  initialize(): spawn 子进程 + stdio JSON-RPC
       │  sync_turn():  后台线程 → _rpc("sync_turn", …)
       ▼
  vatbrain-provider (Go, cmd/vatbrain-provider/)
       │  bufio.Scanner 读 stdin 行 → provider.Server.handle
       ▼
  core.WriteMemory (shared write pipeline)   ← 与 MCP write_memory 共用
       │  SignificanceGate → embedding → PatternSeparation merge
       │  → WriteEpisodic → LinkOnWrite → working-memory push
       ▼
  SQLite ($HERMES_HOME/vatbrain/vatbrain.db)
```

- **传输**（D1）：stdio 行分隔 JSON-RPC 2.0，一请求一行，无 Content-Length 分帧。
- **后端**（D3）：SQLite。Neo4j+pgvector 重型存储已弃用方向（2026-08-08 决策），不再投入。
- **单向桥**（D4）：hermes → vatbrain，永不回写。
- **注入面**（D5）：不碰系统提示稳定块；provider 无 `system_prompt_block`，无工具（Phase 5 再加）。

## 2. 协议（行分隔 JSON-RPC 2.0）

### initialize
```json
{"jsonrpc":"2.0","id":1,"method":"initialize",
 "params":{"session_id":"sess-A","hermes_home":"/Users/x/.hermes",
           "platform":"cli","agent_context":"primary","agent_identity":"coder"}}
→ {"jsonrpc":"2.0","id":1,"result":{"provider":"vatbrain","store_backend":"sqlite",
   "read_only_mode":false,"project_id":"coder"}}
```
`agent_context != "primary"`（subagent/cron/flush）→ `read_only_mode: true`，后续 sync_turn 拒绝写（hermes 契约）。

### sync_turn
```json
{"jsonrpc":"2.0","id":2,"method":"sync_turn",
 "params":{"session_id":"sess-A","user_content":"不对，evaluator 输出字段是 total_score",
           "assistant_content":"好的","agent_context":"primary"}}
→ {"jsonrpc":"2.0","id":2,"result":{"persisted":true,"memory_id":"…",
   "gate_reason":"prediction_error","is_correction":true,
   "merge_action":"created_new","weight":1}}
```

### 其他
- `ping` → `{"pong":true}`
- `shutdown` → daemon 优雅退出（响应后结束 Serve 循环）
- 错误：`{"error":{"code":-32600/…,"message":"…"}}`

## 3. WriteEvent 推导（规则层，HERMES_INTEGRATION.md §5）

`internal/provider/derive.go`，Phase 2 只实现规则层（零 LLM）：

| 字段 | 规则 | 备注 |
|---|---|---|
| `UserConfirmed` | regex `(?i)记住|记得|以后都|记一下|记到|remember this|remember that` | 显式记忆指令 |
| `IsCorrection` | 短消息（<200 字）+ 纠正动词 regex `(?i)不对|错了|应该是|actually|别用|改成|不要|纠正|should be|…` | **UserConfirmed 优先**：`记住…不要用…` 是指令不是纠错 |
| `CausedBehaviorChange` | 恒 false（需相邻 tool-call diff，Phase 4） | — |
| `Summary` | 用户消息去前缀（`继续：` 等）+ 截断 500 字 | — |

LLM 兜底分类（§5「可疑未命中 → is_this_a_correction」）留待后续；当前 rule-only 可满足验收。

## 4. 写路径复用

`internal/core/write_pipeline.go`（新增）：`WriteMemory(ctx, deps, event, projectID, language, entityID, taskType) (WriteResult, error)`。

- MCP `write_memory` 与 daemon `sync_turn` 共用同一条管道（gate → embed → pattern-separation merge → persist → link-on-write → working-memory push），保证各入口行为不漂移。
- `WriteEvent.IsCorrection` 现**持久化**：`models.EpisodicMemory.IsCorrection bool` + sqlite `is_correction` 列（`migrate()` ALTER 迁移）+ neo4j 属性（重型后端保留一致性，不再投入）。
- `ClampWeight` 从 mcp 移入 core（mcp 转发保留兼容）。

## 5. working memory 跨会话累计

daemon 常驻 → `store.WorkingMemoryBuffer`（ring 20，keyed by projectID）在**整个 hermes 生命周期**持续累计，跨 `/new` 会话成立——修复原 MCP 路径"进程内缓冲无历史"缺陷（§5 表格 cross-cycle 行）。projectID = `agent_identity`（profile 名），默认 `"hermes"`。

## 6. 验收（已达成）

- [x] daemon 二进制冒烟：纠错 → `persisted:true / gate_reason:prediction_error / is_correction:true`，sqlite `is_correction=1`
- [x] hermes 加载器发现 `vatbrain`、`is_available()==true`、initialize + sync_turn → 纠错落库（真实 venv + 临时 HERMES_HOME 验证）
- [x] `go test ./internal/...` 全绿（provider 包 14 用例含中文）
- [x] 插件 + 二进制已安装至真实 `~/.hermes/plugins/vatbrain/` 与 `~/.hermes/vatbrain/bin/`
- [ ] **待用户授权**：`~/.hermes/config.yaml` 加 `memory: {provider: vatbrain}` 激活 → 下次 hermes 启动出现 `Memory provider 'vatbrain' activated`（agent_init.py:1774）。加载链已验证，仅差该配置开关。

## 7. 后续（Phase 3/4）

- `prefetch`/`queue_prefetch`：daemon 检索 + Pitfall 注入；hermes manager 负责 `<memory-context>` 栅栏（provider 绝不自带，memory_manager.py `build_memory_context_block`）。
- `on_session_end`/`on_memory_write`/`on_session_switch`：生命周期钩子（协议扩展）。
- daemon 数据目录 `$HERMES_HOME/vatbrain/` 在 `hermes backup` 覆盖范围内（无需 `backup_paths()`）。
