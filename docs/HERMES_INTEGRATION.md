# VatBrain × Hermes 集成方案（MemoryProvider 适配器）

> 撰写日期：2026-08-08
> 前置阅读：[`EVOLUTION_PLAN.md`](./EVOLUTION_PLAN.md)（定位收窄）、[`01-agent-memory-sync.md`](./v0.2.1/tech-specs/01-agent-memory-sync.md)（Watcher 架构）、hermes-agent 仓库 `agent/memory_provider.py`（ABC 接口，本方案的事实基准）
> 本文档定义 VatBrain 与 hermes-agent 的两层集成：**Watcher 适配器（浅层，被动）+ MemoryProvider 插件（深层，主动钩子）**。

---

## 0. 定位与结论

`EVOLUTION_PLAN.md`（2026-05-19）已把 VatBrain 下一阶段收窄为：

> **Pitfall-aware memory layer for coding agents** —— 记住项目决策、失败经验、用户纠正和代码实体相关的风险，在下一次修改前主动提醒。

本方案的结论：

1. **hermes-agent 是验证这条主线的最佳宿主**——它是本机日常主力 agent，且提供 `MemoryProvider` 插件机制（`plugins/memory/<name>/`），是目前生态里对记忆系统**最深的集成面**（对比 memos-local-plugin 也是走这个接口接入 Hermes 的）。
2. **两层集成，先浅后深**：
   - **Layer 1（v0.2.1 内）**：Watcher 的 `hermes` 适配器——被动监控 `~/.hermes/memories/*.md`，直接满足 v0.2.1 准出"至少 2 个真实 source 可同步"。
   - **Layer 2（v0.2.3）**：`MemoryProvider` 插件——真实轮次钩子（`sync_turn`/`prefetch`/`on_session_end`），把 EVOLUTION_PLAN 主线 `Observe → Distill Pitfall → Inject Before Edit → Capture Feedback → Reconsolidate → Measure` 在 hermes 上完整落地。
3. **hermes 宿主天然补上 VatBrain 的结构性缺口**：`sync_turn` 回调携带完整 messages（含 tool calls / results），`WriteEvent` 的 `IsCorrection` / `CausedBehaviorChange` 字段可由系统自动推导，不再依赖 agent 自觉传 flag（这是原 MCP 手动路径下门控条件永远凑不齐的根因，见 §5）。

---

## 1. 现状盘点（本地 vs GitHub）

| 项 | GitHub（远端） | 本地（2026-08-08） |
|---|---|---|
| 版本 | v0.2 完成（2026-05-08） | v0.2 + `dbf41a1` Agent Memory Watcher（05-11，**未推送**） |
| 工作区 | — | **11 个文件未提交**（+1377/−122）：mcp_server_test +545、consolidation_engine_test +296、sqlite store_test +310、link_on_write_test +70、neo4jpg store/pitfall 重构 |
| 策略文档 | — | `EVOLUTION_PLAN.md`（05-19，竞品调研后收窄定位） |
| 已实现能力 | 8 引擎 + MCP 7 工具 | 上述 + `internal/watcher/`（ProviderRegistry / Refiner / seenSet / 4 adapters / 3 MCP 工具，`VATBRAIN_WATCHER_ENABLED=true` 门控） |

**动作**：
- **先提交未推送的 Watcher commit 和未提交测试**（它们是完整的、可编译的——commit message 注明 `go build`/`go vet` 通过，测试覆盖 v0.2 主路径），再开始新工作。
- Watcher 的 claude-code adapter 监控的正是 `~/.claude/projects/*/memory/`（Claude Code 原生记忆格式）；hermes 的记忆在 `~/.hermes/memories/`，格式不同（§ 分隔、无 YAML frontmatter），需要新 adapter。

---

## 2. 集成架构（总览）

```
hermes-agent（Python）                         vatbrain daemon（Go，sidecar）
┌─────────────────────────────┐   stdio     ┌──────────────────────────────────┐
│ plugins/memory/vatbrain/    │  JSON-RPC   │ cmd/vatbrain-provider/           │
│   __init__.py  VatBrainProvider │ ◄──────► │   main.go    入口（复用 app.New） │
│   _client.py  stdio client  │             │   rpc.go      行分隔 JSON-RPC    │
│   plugin.yaml               │             │   handlers.go provider 端点      │
│                             │             │                                  │
│ 内存态: 缓存/超时/门控       │             │ 引擎: SignificanceGate /         │
│                             │             │   PatternSeparation / WeightDecay│
│                             │             │   RetrievalEngine / Pitfall /    │
│                             │             │   Reconsolidation / Consolidation│
│                             │             │   Watcher（可选，同进程）          │
└─────────────────────────────┘             │ Store: SQLite（默认后端）        │
                                            └──────────────────────────────────┘
```

**传输选择：stdio JSON-RPC（新二进制 `cmd/vatbrain-provider`），不用 HTTP，不用 MCP**：

| 方案 | 缺点 |
|---|---|
| HTTP localhost | 端口管理、多 profile 冲突、daemon 需手动拉起；debug 虽方便但生命周期失控 |
| 复用现有 MCP server | MCP 工具是 agent 向（write_memory 等手动语义），provider 需要的内部端点（turn.sync / session.bind）不该暴露给模型 |
| **stdio JSON-RPC（选定）** | hermes `initialize()` 时 spawn、`shutdown()` 时终止，生命周期跟随 agent；无端口、多 profile 天然隔离；hermes 崩溃 → stdin EOF 可检测自杀 |

协议：**每行一个 JSON-RPC 2.0 请求/响应**（内容均为结构化 JSON，无需 Content-Length 分帧——行分隔已足够且可用 stdlib 实现，hermes 侧零新依赖）。

---

## 3. MemoryProvider 钩子映射（核心）

hermes 契约见 `agent/memory_provider.py`（抽象：`name`/`is_available`/`initialize`/`get_tool_schemas`；可选钩子全表）。映射如下：

| hermes 钩子 | vatbrain 引擎 | 行为设计 |
|---|---|---|
| `initialize(session_id, hermes_home, platform, agent_context, agent_identity, user_id, ...)` | `app.New()` + session 绑定 | 首次 spawn daemon（stdin 传 `--store sqlite --data $HERMES_HOME/vatbrain/`）；`agent_context != "primary"`（cron/subagent/flush）→ **只读模式**（拒绝写，契约注释明言"cron system prompts would corrupt user representations"） |
| `on_turn_start(turn_number, message)` | — | 轮计数；每 N 轮（可配，默认 10）触发一次 daemon 侧轻量维护（权重重算/冷存储迁移），与 hermes 内置 nudge 节奏解耦 |
| `queue_prefetch(query)` + `prefetch(query)` | `RetrievalEngine`（两阶段）+ Pitfall 注入 | `queue_prefetch` 排后台检索缓存；`prefetch` hot path 读缓存返回。**hot path < 200ms 预算**（hermes 侧 manager 的 join 超时 8s 只是兜底）。命中 Pitfall 时注入风险块（格式见 §6）。返回纯文本——`<memory-context>` 栅栏 + "NOT new user input" 语义由 hermes manager 包装（`build_memory_context_block`），provider 绝不自带栅栏 |
| `sync_turn(user_content, assistant_content, session_id, messages)` | `SignificanceGate` → `Store.WriteEpisodic` → `LinkOnWrite` | **自动 WriteEvent 推导**（§5）。只传摘要/提炼级内容。非阻塞（daemon 后台落库）。hermes manager 单 worker FIFO 已保证 turn 顺序，daemon 侧无需自建顺序 |
| `on_memory_write(action, target, content, metadata)` | Episodic ingest，`SourceType=USER`（最高可信级） | 内置 MEMORY.md/USER.md 每次写 → 镜像进图：`add`→新 episodic；`replace`→替换对应记忆（经 DERIVED_FROM 溯源）；`remove`→标记 obsoleted。`metadata.write_origin`（assistant_tool / background_review）透传给 daemon 做来源标注。**staged 写不会到达此钩子**（manager 失败关闭），天然安全 |
| `on_session_end(messages)` | `ConsolidationEngine`（睡眠整合） | 触发规则线 + Pitfall 线并行提取（已有 120s 超时与 goroutine 架构）；daemon 后台跑，不阻塞会话结束 |
| `on_session_switch(new_session_id, reset, parent_session_id, rewound)` | session 重绑 | `reset=True`（/new、/reset）→ 清 working memory 缓冲；`rewound=True`（压缩截断）→ 失效缓存；否则只换绑定 |
| `on_pre_compress(messages)` | LLM 摘要（可选） | 返回 daemon 提炼的洞察文本 → 进 hermes 压缩摘要 prompt |
| `on_delegation(task, result, child_session_id)` | Episodic ingest（`SourceType=DELEGATION`） | 子任务观测：task+result 作为一条带 delegator 标记的记忆 |
| `get_tool_schemas()` + `handle_tool_call()` | `vatbrain_search` / `vatbrain_pitfall` 工具 | **v0.3 落地面**：`prepare_edit_context(files, task_type, language, user_goal)` → 相关记忆 + top pitfalls + risk score + reason codes（对齐 EVOLUTION_PLAN v0.3 必做清单）。首版可返回空列表（纯自动注入），工具在 v0.3 阶段加 |
| `backup_paths()` | — | 数据在 `$HERMES_HOME/vatbrain/` 内 → 已被 `hermes backup` 覆盖，无需声明外部路径 |
| `system_prompt_block()` | — | 返回空串（保持快照字节稳定）；预取上下文走 prefetch 通道，不占 stable 段 |
| `shutdown()` | daemon 关闭 | 发 `vatbrain.shutdown` → SIGTERM → 等待退出（2s 上限，超时 SIGKILL） |

**与内置记忆的边界（不竞争，互补）**：

| 维度 | 内置 MEMORY.md（保留） | vatbrain provider（新增） |
|---|---|---|
| 内容 | "who 用户是谁 + 当前状态"（user 策展、可重写） | 情景事实 + **失败模式/风险**（自动摄入、行为归因） |
| 注入 | 冻结快照常驻 system prompt（2200/1375 字符硬预算） | 每轮 prefetch 检索注入（权重衰减 + 冷却阈值替代硬预算） |
| 写入口 | memory 工具（审批门默认 off + strict 威胁扫描） | sync_turn 自动摄入（显著性门 + WriteEvent 推导） |
| 质量控制 | strict 模式集 + 去重 + 预算 | 显著性门 4 条件 + 行为归因 + Pitfall 状态机（v0.2.2） |

---

## 4. 传输协议（provider RPC 方法表）

方法（`method` 字段，均幂等，失败返回 error 且**不重试**——hermes 侧 manager 约定失败静默）：

| 方法 | 方向 | 载荷 | 说明 |
|---|---|---|---|
| `vatbrain.ping` | C→S | `{}` | 存活探测（is_available 时连发） |
| `vatbrain.session.bind` | C→S | `session_id, agent_context, agent_identity, platform, user_id?` | 绑定/重绑；非 primary → daemon 切只读 |
| `vatbrain.turn.start` | C→S | `turn_number, message, remaining_tokens?` | on_turn_start |
| `vatbrain.turn.sync` | C→S | `user_content, assistant_content, session_id, messages?` | sync_turn；daemon 侧异步返回 `{accepted: true}` |
| `vatbrain.prefetch` | C→S | `query, session_id, top_k?` | 返回 `{context: str, pitfalls: [...], risk_items: [...]}` |
| `vatbrain.prefetch.queue` | C→S | `query, session_id` | queue_prefetch |
| `vatbrain.memory_write.mirror` | C→S | `action, target, content, metadata` | on_memory_write |
| `vatbrain.session.end` | C→S | `session_id, message_count` | on_session_end → 触发 consolidation（后台） |
| `vatbrain.session.switch` | C→S | `new_session_id, parent_session_id, reset, rewound` | on_session_switch |
| `vatbrain.consolidation.status` | C→S | `session_id` | 可选：供 `trigger_consolidation` MCP 工具查询 run 状态 |
| `vatbrain.shutdown` | C→S | `{}` | 优雅关闭（flush 队列） |

**生命周期兜底**：daemon 读 stdin EOF → 2s 后自杀（hermes 崩溃/被杀时无孤儿进程）；hermes 侧 `shutdown()` 用 atexit + manager 的 `shutdown_all()` 路径（已有 5s drain）双保险。

---

## 5. WriteEvent 自动推导（关键设计）

hermes `sync_turn` 的 `messages` 含 OpenAI 风格完整轮次（assistant tool calls + tool results）。推导规则（顺序执行，**规则先行、LLM 兜底、批量节流**）：

| WriteEvent 字段 | 推导来源 | 成本策略 |
|---|---|---|
| `UserConfirmed` | 用户消息命中显式记忆指令（`记住`/`记得`/`以后都`/`remember this`/`记一下`）→ regex 快速命中 | 规则层，零 LLM |
| `IsCorrection` | 规则：用户消息短（< 200 字符）+ 纠正动词（`不对`/`应该是`/`actually`/`别用`/`改成`/`不要`）→ true；可疑但未命中 → daemon 侧 LLM 分类（`is_this_a_correction` 二分类 prompt） | 规则层兜底 LLM；LLM 仅在 debug/feature 类任务且规则层无结论时调用，按轮节流（每会话最多 N 次） |
| `CausedBehaviorChange` | 相邻轮 tool-call 序列 diff（同 task_type 下工具集/顺序显著变化）+ 紧邻一次 IsCorrection → LLM 判定 | 同上，与 IsCorrection 共用一次 LLM 调用 |
| cross-cycle 条件 | **daemon 常驻 → working memory 缓冲跨会话真实累计**（修复原 MCP 路径"进程内缓冲无历史"缺陷） | 零成本 |
| `SubsequentReferenceCount` | 检索命中 → `touch_memory` +1（已实现） | 零成本 |

**推导出的 WriteEvent 在 daemon 侧进 `SignificanceGate.Evaluate`，四条件全部可触发**——设计文档 §4.2 的四条件门控第一次在真实路径上完整生效。

---

## 6. Pitfall 注入格式（Inject Before Edit 的 hermes 形态）

`prefetch` 返回文本中附加风险块（≤ 3 条，仅 `confirmed` 或高置信 `proposed`，对齐 EVOLUTION_PLAN v0.2.2 状态机）：

```
[Risk advisory — vatbrain]
- <entity>: <signature>
  root cause: <category> · 曾于 <date> 发生 · confidence: <trust_level>
  Fix: <fix_strategy>
```

- hermes 侧 `build_memory_context_block` 会将其整体包进 `<memory-context>` 栅栏 + "NOT new user input" 系统注记——注入文本天然被隔离，不会与用户输入混淆（这是 hermes 为这类注入设计的防护，直接复用）。
- v0.3 的 `prepare_edit_context` 工具与 prefetch 共用同一 risk-score 引擎（Pitfall 密度 × 时间衰减 × trust_level），只是输入从"用户消息"换成"待改文件列表"。

---

## 7. 实施阶段（对齐 EVOLUTION_PLAN 版本）

### Phase 0 — 收尾与修复（建议 1–2 天，可并行）
- commit 本地未推送改动（Watcher + 测试扩展）。
- 修复三个已知缺陷（原分析报告）：
  1. **CJK tokenizer**（`core/significance_gate.go` `IsAlphaNum` 只认 ASCII → 中文摘要 TokenOverlap/TokenSimilarity 静默失效）→ 全部改 embedding 相似度（vatbrain 已有 embedder，`link_on_write`、`countRecentCycles` 替换）。
  2. **language 硬过滤**（`retrieval_engine.go` 硬排除 ≠ 设计文档 §4.1 宣称的"跨技术栈经验迁移价值"）→ 改为软权重。
  3. **backtest 橡皮图章**（`consolidation_engine.go` 无 LLM 时恒 1.0）→ 无 LLM 时用 embedding 一致性做廉价回测。
- 验收：`go test ./...` 全绿；中文内容下 link_on_write 产生边。

### Phase 1 — Layer 1：hermes Watcher 适配器（v0.2.1 内，0.5 天）
- 新文件 `internal/watcher/adapters/hermes.go`：
  - 监控 `~/.hermes/memories/MEMORY.md` + `USER.md`（`$HERMES_HOME` 可配，多 profile 时按 env 解析）。
  - § 分隔解析（hermes `ENTRY_DELIMITER = "\n§\n"`），每条 entry → `RawMemory`（`ProjectID` 取 profile 名；`SourceURI` 用 `hermes://memories/<target>#<entry_hash>`）。
  - 无 frontmatter → 走 Refiner 的 heuristic 路径推导 language/task_type/entity_id。
  - seenSet 按 (SourceURI, ContentHash) 去重 → 增量扫描天然成立（hermes 快照冻结语义不影响 watcher：文件有变才重扫）。
- 注册进 `cmd/vatbrain` 的 watcher 装配 + `list_adapters` 输出 `hermes`。
- **验收**：hermes 会话里 `memory(action=add)` 一条 → 5 分钟内 vatbrain 图内出现对应 episodic（`vatbrain-search` 可检索到）；重复轮询不产生重复条目。
- 顺带满足 v0.2.1 准出"至少 2 个真实 source 可同步"（claude-code + hermes）。

### Phase 2 — Layer 2 骨架 + 写路径（v0.2.3，2–3 天）
- hermes 侧 `plugins/memory/vatbrain/`（bundled 形态，注册即随 hermes 分发；也可以先放 `$HERMES_HOME/plugins/` 用户安装形态——`load_memory_provider` 两者都支持）。
- `cmd/vatbrain-provider`：stdio JSON-RPC 骨架 + `vatbrain.ping/session.bind/turn.sync/shutdown` 四个方法。
- 实现 `initialize`（spawn + 只读模式）、`sync_turn`（WriteEvent 推导 → gate → 落库）、`shutdown`。
- **验收**：`memory.provider: vatbrain` 配好后，hermes 启动日志出现 "Memory provider 'vatbrain' activated"；用户纠错后 `vatbrain` 图内出现 `IsCorrection=true` 的 episodic（`get_memory_weight` 可见 gate_reason）；`below_threshold` 拒绝可观测（daemon 日志）。

### Phase 3 — 读路径：prefetch + Pitfall 注入（v0.2.3 内，1–2 天）
- 实现 `queue_prefetch`/`prefetch`：两阶段检索（contextual gating + 语义排序）→ pitfall 注入 → 缓存。
- **验收**：请求发往 LLM 的 API 副本中出现 `<memory-context>` 栅栏（用 hermes 的 `substitute_api_content` 路径验证）；hot path p95 < 200ms；trivial 消息（"hi"）不触发检索（hermes `is_trivial_prompt` 已挡在 provider 之前）。

### Phase 4 — 生命周期：consolidation + 镜像 + 重绑（v0.2.3 内，1 天）
- `on_session_end` → `vatbrain.session.end` → daemon 触发整合；`on_memory_write` 镜像；`on_session_switch` 重绑。
- **验收**：`/new` 后 old 会话的 episodic 被整合出 candidate rule/pitfall（`/consolidation/runs/{id}` 可见）；内置 MEMORY.md 的 add/replace 同步到图（source=user_explicit）；`/reset` 后新一轮写入落到新 session 绑定。

### Phase 5 — v0.3 风险注入（对齐 EVOLUTION_PLAN v0.3）
- `prepare_edit_context` MCP 工具 + risk-score 引擎；prefetch 注入升级为按 risk 排序。
- 依赖 v0.2.2 Pitfall Workbench（状态机 + 干扰率指标）先完成，否则注入没有 suppress 逃生阀。

### Phase 6 — 评测（对齐 EVOLUTION_PLAN v0.3.1）
- `tests/scenarios/` 的 20 个 coding scenarios 直接以 hermes 会话为运行环境（vatbrain 只做记忆后端，被测对象是"注入后 agent 行为变化"）。
- 指标：重复错误减少率 / 有用注入率 / 干扰率 / token 开销。

---

## 8. 风险与对策

| 风险 | 对策 |
|---|---|
| hermes 崩溃 → daemon 孤儿进程 | stdin EOF 自杀（2s）+ hermes `shutdown()` 双保险；macOS 无 `prctl(PR_SET_PDEATHSIG)`，不依赖它 |
| WriteEvent 推导的 LLM 成本失控 | 规则先行（regex/启发式），LLM 仅兜底 + 每会话节流；推导与摄取批量合并为一次 daemon 调用 |
| prefetch 拖慢轮次 | hermes 侧已有 daemon 线程 + 8s 超时兜底；provider 侧 hot path 只读缓存（<200ms 预算），检索在 `queue_prefetch` 提前做 |
| 与内置记忆抢注入位置 | 不竞争：MEMORY.md 冻结快照常驻（谁/状态），vatbrain 走每轮 `<memory-context>`（情景/风险）；两侧内容策略已在 §3 表格明确 |
| 镜像写造成循环 | `on_memory_write` 只从内置工具写触发，vatbrain 侧落库不回写 hermes——单向桥（与 honcho 的 `create_conclusion` 镜像同构） |
| 双后端维护负担 | SQLite 为 provider 默认后端（`--store sqlite`）；neo4j+pgvector 保留给原有 HTTP/MCP 使用方 |

---

## 9. 度量（Measure 落地）

| 指标 | 定义 | 目标 |
|---|---|---|
| prefetch hot path 延迟 | provider 返回至注入 | p95 < 200ms |
| 有用注入率 | 注入后被采纳/引用的比例 | 待基线（v0.3.1 场景实测） |
| 干扰率 | 注入后用户未采纳或 suppress 的比例 | < 30%（EVOLUTION_PLAN v0.3 准出） |
| 重复错误减少率 | 同类错误二次发生率 | 可测量（20 scenarios） |
| token 开销 | 注入文本占轮次 token 比例 | 报告，不设硬目标（对比 baseline） |

---

*本文档是 VatBrain × hermes 集成的实施基线。所有工程决策应回溯到 [`EVOLUTION_PLAN.md`](./EVOLUTION_PLAN.md) 的主线（Observe → Distill → Inject → Feedback → Reconsolidate → Measure）进行验证。*
