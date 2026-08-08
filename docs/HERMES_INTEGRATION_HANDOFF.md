# VatBrain × Hermes 开发交接（2026-08-08）

> **本文档是开发会话的起点**。目标：把 VatBrain 以 MemoryProvider 插件形态接入 hermes-agent（本机 `/Users/zhangshiyu/tools/hermes-agent`），落地 EVOLUTION_PLAN 主线。
> 设计基线：[`HERMES_INTEGRATION.md`](./HERMES_INTEGRATION.md)（刚写完，含完整钩子映射/协议/阶段）
> 策略基线：[`EVOLUTION_PLAN.md`](./EVOLUTION_PLAN.md)（Pitfall-aware memory layer for coding agents）
> 本会话**只改 vatbrain 仓库**，hermes 侧零代码改动（仅配置）。

---

## 0. 会话检查点（2026-08-08 第 2 次会话中断于此，机器交接）

> **新会话从本段开始读**。本段之后的原始内容保留作历史/参考。

### 已完成（全部已推送 `origin/feature/agent-memory-watcher`，工作树干净）

| 项 | 提交 | 说明 |
|---|---|---|
| §2.4 commit A（推送 watcher） | `dbf41a1` | ✅ |
| §2.4 commit B（测试文件+coverage） | `cdbef81` | ✅ |
| §2.4 commit C（neo4jpg 重构） | `bc70988` | ✅ |
| §2.4 commit D（文档组） | `5b36f8d` | ✅ |
| **F1** CJK tokenizer → embedding 余弦 | `1712604` | ✅ 含中文回归测试（gate 跨周期 + RELATES_TO 边） |
| **F2** language 硬过滤 → 软权重 | `2ae7e54` | ✅ 含跨语言命中验收测试 |
| **F3** backtest 橡皮图章 → embedding 一致性 | `8d2a5db` | ✅ 无信号恒 0.5，永不落库 |

`go test ./internal/...` 全绿；`tests/` 4 个失败均为 Neo4j/Pgvector 基础设施 E2E（预先存在，需 docker-compose 启动）。

### 关键 API 变更（F1/F3 引入，新会话必须知道）

1. `SignificanceGate.Evaluate(ctx, event, workingMemory)` — 新增 ctx 参数；gate 新增 `Embedder`/`EmbedSimilarityThreshold` 字段（`app.go` 已接线 `significanceGate.Embedder = emb`）。
2. `core.LinkOnWrite(ctx, emb, s, ...)` — 第 2 参数为 `embedder.Embedder`（nil → token 回退）。
3. `watcher.NewMemoryWatcher(providers, refiner, emb, s, pollInterval, seenMaxEntries)` — 新增 emb。
4. `ConsolidationEngine.backtest(ctx, emb, cl)` — 无 LLM 时做 embedding 一致性回测，无信号返回 0.5（永不达标）。
5. `HardFilterResult` 新增 `Language` 字段；language 从硬约束移入软权重（同语言 +10%），project_id 仍硬约束。
6. 新助手（package core）：`linkSimilarity` / `embeddingSimilarity` / `vectorHasMagnitude`；测试专用 `runeEmbedder{}`（`test_embedder_test.go`，CJK 回归的确定性 embedder）。
7. 新常量：`embeddingSimilarityThreshold = 0.7`（gate 跨周期与 RELATES_TO 共用，与 consolidation AccuracyThreshold 对齐）、`tokenLinkThreshold = 0.15`。

### 下一步：Phase 1 hermes Watcher 适配器（代码未动笔，设计已读完）

**要写**：`internal/watcher/adapters/hermes.go` + `hermes_test.go` + 装配。

已确认的实现事实：
- 存储格式（§5.4）：`$HERMES_HOME/memories/MEMORY.md` + `USER.md`；条目分隔符 `"\n§\n"`；块头 `MEMORY (your personal notes)` / `USER PROFILE (who the user is)`（读取时跳过）；无 frontmatter。
- Provider 接口（`internal/watcher/provider.go`）：`Name() / Description() / Scan(ctx) ([]RawMemory, error) / Status()`。
- 参考模板：`internal/watcher/adapters/claude_code.go`（Scan → parseFile → RawMemory{SourceURI, Content, ContentHash}）；emit 前调 `HashContent()`。
- 装配点：`internal/app/app.go` `buildWatcherProviders`（L270-280，`"all"` 名单 `{"claude-code","opencode","cursor"}` → 加 `"hermes"`）；`internal/config/config.go` WatcherConfig（默认值约 L203）。
- **去重设计要点**：MEMORY.md 是整体原子重写文件 → 条目 SourceURI 必须稳定（建议 `MEMORY.md#<sha256 前 8>` 或索引位），seenSet 按 (SourceURI, ContentHash) 去重，hermes 编辑导致索引漂移时靠 ContentHash 兜底。
- 路径可配：环境变量（如 `VATBRAIN_WATCHER_HERMES_HOME`），默认 `~/.hermes`（可用 `os.UserHomeDir()`，参考 claude_code 的 homeDir 模式）。
- 验收（§4 Phase 1）：`~/.hermes/memories/MEMORY.md` 写入 → 5 分钟内可被 vatbrain 检索到；重复轮询无重复条目。默认 poll interval 需 ≤ 5min。
- 新测试必须含中文用例（§6 纪律）。

**之后**：Phase 2-4 见 §4 表格；F1-F3 的回归护栏（runeEmbedder + 中文用例）是后续所有相似度逻辑的测试模板。

---

## 1. 已定决策（本次对话确认，不要回退）

| # | 决策 | 理由 |
|---|---|---|
| D1 | 传输 = **stdio JSON-RPC**（新二进制 `cmd/vatbrain-provider`），不用 HTTP、不用 MCP | hermes spawn/杀、生命周期跟随、无端口、多 profile 隔离；stdin EOF 检测防孤儿进程 |
| D2 | hermes 插件开发期放 **`$HERMES_HOME/plugins/vatbrain/`** 用户安装形态（不动 hermes 代码）；跑通后再议 bundled | `load_memory_provider` 双目录都支持；用户目录开发无耦合 |
| D3 | provider 默认后端 **SQLite**（`--store sqlite`） | 轻量；neo4j+pgvector 保留给原有 HTTP/MCP 使用方 |
| D4 | 镜像为**单向桥**：内置 memory 工具写 → vatbrain 图；vatbrain 永不回写 hermes | 防循环；与 honcho create_conclusion 镜像同构 |
| D5 | 集成不碰内置 MEMORY.md 的注入位（冻结快照常驻） | 互补分工：内置管"谁/状态"，vatbrain 走每轮 `<memory-context>` 管"情景/失败模式/风险" |

---

## 2. 本地仓库状态盘点（开工第一步）

`git status` 共 **32 个文件**未推送/未提交。**先整理再开发**：

### 2.1 未推送的 commit（1 个）
```
dbf41a1  feat: Agent Memory Watcher (Phase Omicron) — multi-agent memory sync subsystem  (2026-05-11)
```
实现 `docs/v0.2.1/tech-specs/01-agent-memory-sync.md`：被动监控 agent 原生记忆（claude-code/cursor/opencode/custom 4 adapters）+ LLM Refiner + seenSet 去重 + 3 个 MCP 工具，`VATBRAIN_WATCHER_ENABLED=true` 门控。17 个单测，build/vet 通过。

### 2.2 已修改未提交（11 个）
```
 M .gitignore
 M .vatbrain/agent_context.md          # 工作台，正常滚动
 M docs/ROADMAP.md                     # 加了 EVOLUTION_PLAN 引用
 M internal/core/consolidation_engine_test.go   (+296)
 M internal/core/link_on_write_test.go          (+70)
 M internal/mcp/helpers.go                      (±小改)
 M internal/mcp/mcp_server_test.go              (+545)
 M internal/mcp/write_tool.go                   (±小改)
 M internal/store/neo4jpg/pitfall.go            (±重构)
 M internal/store/neo4jpg/store.go              (±174 行重构)
 M internal/store/sqlite/store_test.go          (+310)
```

### 2.3 未跟踪（21 个）
```
AGENTS.md                                 # Codex 版工作纪律（与 CLAUDE.md 同构）
coverage.out                              # 5/8 覆盖率报告：总 57.6%
docs/EVOLUTION_PLAN.md                    # ★ 5/19 策略文档（关键资产，必提交）
docs/HERMES_INTEGRATION.md                # ★ 本次设计基线（新写）
docs/HERMES_INTEGRATION_HANDOFF.md        # ★ 本文档
docs/v0.1/tech-specs/01-embedder-architecture.md
docs/v0.2.1/tech-specs/01-agent-memory-sync.md
internal/api/helpers_test.go, server_test.go
internal/app/app_test.go
internal/config/config_test.go
internal/db/neo4j/neo4j_test.go, pgvector/pgvector_test.go, redis/redis_test.go
internal/embedder/embedder_test.go
internal/llm/llm_test.go
internal/models/common_test.go, pitfall_test.go
internal/store/lru/cache_test.go, memory/memory_store_test.go, working_memory_test.go
internal/store/neo4jpg/neo4jpg_test.go
```

### 2.4 建议的提交切分
1. **commit A**：`dbf41a1`（watcher）直接 `git push`。
2. **commit B**：全部未提交/未跟踪**测试文件 + coverage 相关**（`internal/**/*_test.go`）——它们是对 v0.1/v0.2 的覆盖补充，`go test ./...` 确认绿后提交。
3. **commit C**：neo4jpg store/pitfall 重构 + `internal/mcp/` 小改——**先确认重构是否完成**（本地无 commit 记录，若半途请先跑 `go test ./internal/store/...`），完成则单独提交。
4. **commit D**：文档组（EVOLUTION_PLAN / v0.2.1 tech-spec / v0.1 tech-spec / AGENTS.md / HERMES_INTEGRATION*）。

### 2.5 迭代计划执行状态盘点（2026-08-08 核对，含代码证据）

| 计划 | 状态 | 证据 |
|---|---|---|
| v0.1 最小闭环 | ✅ 完成 | 已提交 |
| v0.2 记忆进化（6 Phase） | ✅ 完成 | ROADMAP 全绿 + 测试 |
| v0.1.1 存储重构 | ⚠️ 文档草案、代码已落地 | `docs/v0.1.1/00-storage-refactor-draft.md` 标记"技术调研草案"但 SQLite/neo4j adapter 已实现（04-29/30 提交）——**文档可补定稿** |
| v0.2.1 Watcher GA | 🟡 ~90%，未 GA | `internal/watcher/adapters/opencode.go:61` 有 `TODO: Implement OpenCode-specific memory format parsing`（stub）；cursor/custom 无 TODO 视为完整；commit `dbf41a1` 未推送；GA 准出（真实路径验证、README 配置说明、5 分钟 demo）未做 |
| **v0.2.2 Pitfall Workbench** | ❌ 完全未动 | 5 个 MCP 工具（list_pitfalls/explain_pitfall/confirm_pitfall/suppress_pitfall/link_pitfall_entity）零实现——注册表只有 search_pitfalls；状态机（proposed/confirmed/suppressed/obsolete）**连 `models/pitfall_memory.go` 字段都没有**；干扰率指标无 |
| **v0.3 Risk Injection** | ❌ 完全未动 | `prepare_edit_context` 与 risk-score 引擎全仓库零命中 |
| **v0.3.1 Evaluation Harness** | ❌ 完全未动 | `tests/scenarios/` 不存在（tests/ 仅 smoke/e2e） |
| v0.4 Public DX | ❌ 大部分未动 | README 首屏仍为泛用定位；`DEMO_SCRIPT.md` / `COMPETITIVE_LANDSCAPE.md` 不存在 |
| v1.0 Team Memory | ⏸ 延后 | 符合 EVOLUTION_PLAN 设计 |
| ROADMAP 原 v0.3 六件套（Surprise Score/反事实/残差/自适应衰减） | ❌ 未实现 | EVOLUTION_PLAN 收窄后未保留，勿再铺开 |

**时间线对照（5/19 EVOLUTION_PLAN 30/60/90 → 8/8）**：30 天目标 0/4、60 天目标 0/5、90 天目标 0/2 完成——最接近收尾的是 Watcher（差 opencode adapter + 验证 + 推送）。

**对当前工作的含义**：
1. 开发顺序 = Watcher 收尾 GA（补 opencode 或转 Custom 示例 + 推送）→ 交接 Phase 1 hermes adapter（顺带补"第 2 个真实 source"GA 准出）→ Phase 2+ provider 插件。
2. Workbench/Risk Injection 处于零状态是**优势**：Pitfall 数据模型可以带着状态机（proposed/confirmed/suppressed/obsolete）一起设计，无需迁就旧代码。
3. `docs/PROMOTION_STRATEGY.md` 被 .gitignore 排除（本地独占），5/8 更新后状态未知，不参与远端协作。

---

## 3. Phase 0 任务单：修三个已知缺陷（改完再建 provider）

> **状态：三项全部完成**（提交见 §0 检查点表格）。以下为原始描述，保留作记录。

### F1 — CJK tokenizer（硬伤，中文内容静默失效）✅ `1712604`
- 位置：`internal/core/significance_gate.go` —— `IsAlphaNum`（L135-137）只认 `a-z A-Z 0-9`；中文 tokenize 后为空集。
- 影响：`TokenOverlap`（cross-cycle 门条件）与 `link_on_write.go` 的 `tokenSimilarity`（RELATES_TO 边）对中文全部失效。
- 改法：不修 tokenizer，**直接替换**——`countRecentCycles` 改用 embedding 余弦（vatbrain 已有 `embedder.Embedder`），`tokenSimilarity` 同理（调用方已有 embedding 或现场 embed）。保留 `Tokenize` 仅作 fallback。
- 验收：中文两条相似摘要 → link_on_write 产生 RELATES_TO 边。✅（`TestLinkOnWrite_RelatesToEdges_Chinese` + gate 跨周期中文测试）

### F2 — language 硬过滤与设计文档矛盾 ✅ `2ae7e54`
- 位置：`internal/core/retrieval_engine.go` L57-59（`ApplyHardConstraints` 硬排除 language 不同）。
- 矛盾：`DESIGN_PRINCIPLES.md` §4.1 明确"跨技术栈的通用经验（如'并发问题通常出在锁粒度'）仍有迁移价值"。
- 改法：language 移出硬约束 → 软权重（language 匹配 +10% 之类）；project_id 保留硬约束。
- 验收：Go 项目检索命中 Python 项目的通用并发教训（`ContextFilterStats` 可见）。✅（`TestRetrievalEngine_CrossLanguageHit`）

### F3 — backtest 橡皮图章 ✅ `8d2a5db`
- 位置：`internal/core/consolidation_engine.go` L306-310 —— 无 LLM 时 cluster ≥ MinClusterSize 恒返回 1.0，设计文档 §8.1 称回测是"关键安全阀"。
- 改法：无 LLM 时用 embedding 一致性做廉价回测（簇内 episodics 与候选 rule 的平均相似度，低于阈值不落库）；或退化为 0.5 常值（永不达标，宁缺毋滥）。
- 验收：无 API key 时 `consolidation/trigger` 不再批量产生未经验证的 rule。✅（`TestConsolidationEngine_Run_NoSignal_NoRulesPersisted`）

---

## 4. 开发阶段任务单（详情见 HERMES_INTEGRATION.md §7）

| Phase | 内容 | 交付物 | 验收 |
|---|---|---|---|
| 1 | hermes Watcher 适配器 🔄（进行中，实现要点见 §0） | `internal/watcher/adapters/hermes.go` + 装配 | `~/.hermes/memories/MEMORY.md` 写入 → 5 分钟内可被 vatbrain 检索到；重复轮询无重复条目 |
| 2 | provider 骨架 + 写路径 | `cmd/vatbrain-provider/`（stdio JSON-RPC）+ `$HERMES_HOME/plugins/vatbrain/` | hermes 启动日志 "Memory provider 'vatbrain' activated"；用户纠错 → 图中出现 `IsCorrection=true` episodic |
| 3 | 读路径 | `queue_prefetch`/`prefetch` + Pitfall 注入 | LLM 请求副本出现 `<memory-context>` 栅栏；hot path p95 < 200ms |
| 4 | 生命周期 | `on_session_end`→整合、`on_memory_write` 镜像、`on_session_switch` 重绑 | `/new` 后旧会话整合出 candidate rule/pitfall；内置写同步到图（source=user_explicit） |
| 5 | v0.3 风险注入（依赖 v0.2.2 Workbench） | `prepare_edit_context` 工具 + risk-score 引擎 | 对齐 EVOLUTION_PLAN v0.3 准出 |
| 6 | 评测 | 20 scenarios 以 hermes 会话为测试场 | 重复错误减少率可测、干扰率 < 30% |

**注意顺序**：Phase 5 的主动注入必须有 v0.2.2 Pitfall Workbench（confirmed/suppressed 状态机 + 干扰率）先行，否则注入无逃生阀。

---

## 5. hermes 侧技术事实速查（开工所需，无需重读 hermes 代码）

### 5.1 MemoryProvider ABC（`agent/memory_provider.py`，hermes 仓库）
- **必须实现**：`name`(property) / `is_available()`（只查配置禁网络）/ `initialize(session_id, **kwargs)` / `get_tool_schemas()`。
- **可选钩子**（override 才生效，其余有默认）：`system_prompt_block()`、`prefetch(query, *, session_id="")`、`queue_prefetch(query, *, session_id="")`、`sync_turn(user_content, assistant_content, *, session_id="", messages=None)`、`handle_tool_call(name, args)`、`shutdown()`、`on_turn_start(turn_number, message, **kwargs)`、`on_session_end(messages)`、`on_session_switch(new_session_id, *, parent_session_id="", reset=False, rewound=False)`、`on_pre_compress(messages) -> str`、`on_delegation(task, result, *, child_session_id="")`、`on_memory_write(action, target, content, metadata=None)`、`get_config_schema()` / `save_config()`、`backup_paths()`。
- `initialize` kwargs 约定：必有 `hermes_home`（profile 存储根）、`platform`（"cli"/"telegram"/...）、`agent_context`（**"primary" / "subagent" / "cron" / "flush"——非 primary 必须跳过写**）；可有 `agent_identity`（profile 名）、`user_id`、`session_title`、`chat_id` 等。
- 插件模块形态：`register(ctx)` 函数**或** `MemoryProvider` 子类均可（`plugins/memory/__init__.py` `_load_provider_from_dir` 自动识别）；放 `$HERMES_HOME/plugins/<name>/__init__.py` 即被发现。

### 5.2 加载链（hermes 侧无需改，只配 config）
```
config.yaml: memory.provider: vatbrain
→ agent_init.py:1702-1710: load_memory_provider("vatbrain") → is_available() → add_provider → initialize_all(session_id, platform, hermes_home, agent_context="primary", ...)
```
- 配置路径：`~/.hermes/config.yaml`（或 profile 目录）`memory.provider`。
- 参考实现：`plugins/memory/mem0/`（628 行，最小模板）；`plugins/memory/honcho/`（prefetch 缓存模式）。

### 5.3 注入面
- **prefetch 返回纯文本**，hermes manager 包装成：
  ```
  <memory-context>
  [System note: The following is recalled memory context, NOT new user input. ...]
  <你的文本>
  </memory-context>
  ```
  附加到本轮 API 副本的用户消息（sidecar，持久化内容保持干净）。**provider 绝不自带栅栏**（manager 会 sanitize 剥离）。
- 前置过滤：hermes 的 `is_trivial_prompt`（"hi"/"thanks" 等）已挡在 provider 之前，provider 无需自建。
- 调用语义：`prefetch` 在 daemon 线程 join 8s 超时；`sync_turn` 走单 worker FIFO（turn N 先于 N+1）——**顺序保证由 hermes 提供**。
- 失败语义：provider 抛异常只记 warning，**不影响主流程**（镜像/摄入都是 best-effort）。

### 5.4 内置记忆文件格式（Watcher adapter 用）
- 目录：`~/.hermes/memories/`（`$HERMES_HOME/memories/`）。
- `MEMORY.md` / `USER.md`：`§` 分隔（`ENTRY_DELIMITER = "\n§\n"`），每条 entry 可多行，无 frontmatter。
- 块头（读取时可跳过，用于识别）：`MEMORY (your personal notes)` / `USER PROFILE (who the user is)`。
- 写侧语义：原子替换 + flock 锁文件（`.lock`）；drift guard（round-trip 不匹配拒绝写）。Watcher 只读，不写，无冲突风险。

---

## 6. 风险备忘（HERMES_INTEGRATION.md §8 的速记版）

- **孤儿进程**：daemon 读 stdin EOF → 2s 自杀；hermes `shutdown()` 发 `vatbrain.shutdown` → SIGTERM。macOS 无 `prctl(PDEATHSIG)`，别依赖。
- **LLM 成本**：WriteEvent 推导规则先行、LLM 兜底、每会话节流。
- **顺序**：don't 重复造顺序保证——hermes 单 worker FIFO 已给。
- **CJK 测试**：所有新测试必须含中文用例（本项目用户是中文语境，F1 修复的回归护栏）。

---

## 7. 开工建议

1. ~~本会话先执行 §2.4 的提交切分（A→D）。~~ ✅ 已完成（§0）
2. ~~修 F1-F3（§3），各配回归测试。~~ ✅ 已完成（§0）
3. 按 §4 Phase 1 → 4 推进，每 Phase 独立 commit。（当前：Phase 1 未动笔，要点见 §0）
4. 每 Phase 验收不过就停，不要带病进入下一 Phase。

*交接人：jayden（hermes-agent 会话，2026-08-08）。后续问题查 [`HERMES_INTEGRATION.md`](./HERMES_INTEGRATION.md)，再不行翻 hermes 仓库 `agent/memory_provider.py`。*
