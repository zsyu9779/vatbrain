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

## ── 归档于 2026-08-09（backlog P1 完成后整理）──

## 最近工作（2026-08-08）— Phase 6 评测 harness（20 场景）

### 本次完成

1. **`internal/eval/`**（新）：
   - `eval.go`：Scenario 模型 + YAML 加载 + 确定性模拟（两臂：无注入/有注入）+ `RepeatedErrorReductionRate`/`InterferenceRate` 指标聚合
   - `eval_test.go`：20 场景逐一**真实管道验证**（种入 store → `provider.RetrievePitfalls` 命中该场景 pitfall）+ 确定性模拟（seed=42）+ 验收断言
2. **`tests/scenarios/*.yaml`**：20 个手工构造场景（OpenClash/MiniMax/ClawFeed/飞书真实模式 + SQLite 单写者/nil 指针/context 取消/map 竞态/锁粒度/pgvector 维度/YAML tab/venv/Docker 网络/API 节流）
3. **验收结果**：`20 scenarios | reduction=57.4% interference=14.8%`——重复错误减少率可测（>0，断言≥50%）、干扰率 < 30% ✅
4. **文档**：`docs/v0.2.1/tech-specs/03-evaluation-harness.md`
5. `go test ./internal/...` 21/21 包全绿

### 当前状态

- **Hermes 集成 Phase 1-6 全部完成并推送**（watcher/provider/读路径/生命周期/Workbench+风险注入/评测）
- 工作树干净；提交：`7d88dcc`(P2) `713e726`(P3) `575533b`(P4) `1142c02`(P5) `ecb6453`(P6) + `89a8d13`(entryHash 补丁)
- 真实安装已同步：`~/.hermes/plugins/vatbrain/` + `~/.hermes/vatbrain/bin/vatbrain-provider`
- **✅ 已激活（用户授权）**：`~/.hermes/config.yaml` 加 `memory.provider: vatbrain`（备份 `config.yaml.bak-vatbrain`）；激活链验证通过——config 读到 provider=vatbrain、插件发现+is_available=True、daemon spawn+initialize OK（`Memory provider 'vatbrain' registered (1 tools)`）。附发现并修复激活崩溃 bug（插件 `is_available()` 在 initialize 前调用会 AttributeError，类级默认值修复，已提交推送）
- 下次真实 hermes 交互会话将出现 "Memory provider 'vatbrain' activated" 日志；TUI 非终端运行会提前退出是环境限制非配置问题

## 最近工作（2026-08-08）— Phase 5 Pitfall Workbench + v0.3 风险注入

### 本次完成

1. **v0.2.2 Pitfall Workbench 状态机**：
   - `models.PitfallStatus`（proposed/confirmed/suppressed/obsolete）+ `Injectable()`（confirmed 或高置信 proposed=多命中+高权重；suppressed/obsolete 逃生阀）+ `InterferenceRate()`（TimesSuppressed/TimesShown）
   - 持久化：sqlite `pitfall_memories` 加 status/times_shown/times_suppressed 列 + 迁移；memory/neo4j 同步；store 接口 `UpdatePitfallStatus` + `AddPitfallCounters`
2. **5 个 MCP 工具**（`pitfall_workbench.go`）：list_pitfalls（含状态/干扰率）、explain_pitfall（可溯源 source_episodic_ids）、confirm_pitfall、suppress_pitfall（+计数器）、link_pitfall_entity
3. **v0.3 风险注入**：
   - `core/risk_engine.go` `ComputeRisk`：Pitfall 密度（occurrence×trust×时间衰减 exp(-days/30)）→ risk_score∈[0,1] + reason codes（recent_error/high_risk_pitfall/user_corrected/memory_recall/editing_files）；最多注入 3 条
   - MCP `prepare_edit_context` 工具（files/task_type/language/user_goal → memories + pitfalls + risk + reasons，记录 TimesShown）
   - daemon `prepare_edit_context` 方法 + 插件 `get_tool_schemas`/`handle_tool_call`（模型可直接调用）
   - `provider.RetrievePitfalls` 升级为仅 Injectable（Phase 3 prefetch 风险块接入状态机）
4. **验收**：
   - 单测：risk engine 中文用例、Workbench 工具全流程（confirm/suppress 状态迁移+计数器、link 重锚、explain 溯源）、daemon prepare_edit_context（risk>0 + recent_error + TimesShown++）
   - 冒烟：handle_tool_call("prepare_edit_context") → risk_score 返回 ✅
   - `go test ./internal/...` 20/20 全绿（新增 core/mcp/provider 用例）
   - 真实安装已同步（插件 + 二进制）

### 当前状态

- 本地领先 origin 1 commit（Phase 5）未提交未推送
- 待用户授权激活 `~/.hermes/config.yaml` memory.provider

## 最近工作（2026-08-08）— Phase 4 生命周期钩子

### 本次完成

1. **`internal/provider/lifecycle.go`**（新）：`on_session_end` / `on_memory_write` / `on_session_switch`
   - `on_session_end`：后台 goroutine 跑 ConsolidationEngine.Run（规则+Pitfall 双线，120s 超时），不阻塞会话结束
   - `on_memory_write`：镜像内置写 → `SourceType=USER` + `TrustLevel=Max`（最高可信级）；add→新 episodic、replace→新 episodic + `DERIVED_FROM` 边 + 旧条目 obsolete、remove→旧条目 obsolete；内存 `memoryWriteIndex` 追踪版本溯源（重启降级为 best-effort）；FullSnapshotURI 编码 `source=user_explicit&origin=<write_origin>`
   - `on_session_switch`：reset=true→`WorkingMemoryBuffer.Clear`（新增方法）；rewound=true→失效 prefetch 缓存；重绑 session
2. **插件**：`on_session_end`（后台）、`on_memory_write`（后台）、`on_session_switch`（同步）
3. **验收**：
   - 冒烟：on_memory_write → sqlite `source_type=USER` + `source=user_explicit` + `origin=assistant_tool` ✅
   - replace 的 DERIVED_FROM 边 + obsolete、remove 的 obsolete、reset 清缓冲、rewound 失效缓存均有单测 ✅
   - `go test ./internal/...` 20/20 全绿（provider 生命周期中文用例）
   - 真实 `~/.hermes/plugins/vatbrain/` + `~/.hermes/vatbrain/bin/` 已同步

### 当前状态

- 本地领先 origin 1 commit（Phase 4）未提交未推送
- 待用户授权激活 `~/.hermes/config.yaml` memory.provider

## 最近工作（2026-08-08）— Phase 3 读路径 prefetch + Pitfall 注入

### 本次完成

1. **`internal/provider/retrieve.go`**（新）：
   - `RetrieveEpisodic`：embedder 有信号（非零向量）→ `SearchEpisodic` 余弦排序；否则 **CJK 安全 bigram Dice 系数**词法回退（`charBigrams`，中文/拉丁都适用）
   - `RetrievePitfalls`：`SearchPitfall(ProjectID, MinWeight≥0.5)` 候选池 + 文本重叠打分 + 查询内 entity 引用 boost；confirmed/proposed 状态门待 Phase 5 状态机
   - `FormatPrefetch`：`[vatbrain memory context]` 段落 + §6 `[Risk advisory — vatbrain]` 风险块（entity/signature/root cause/日期/trust/Fix）
2. **server.go 扩展**：`prefetch` + `queue_prefetch` 方法；daemon 侧每 session 缓存（queue 预热后台检索、prefetch 读缓存热路径 / 冷路径同步检索）；`_PREFETCH_TIMEOUT_S=7s`（hermes join 8s 留余量）
3. **插件扩展**：`queue_prefetch`（后台 fire-and-forget）、`prefetch`（同步返回文本，hermes manager 包 `<memory-context>` 栅栏——provider 不自带）
4. **验收**：
   - 冒烟脚本覆盖 prefetch：返回含 "clawfeed-push-v3.py" 的记忆上下文 ✅
   - **冷 prefetch p95=24.4ms**（含进程 spawn+initialize；真实热路径为 warm cache µs 级）——远低于 200ms 验收线 ✅
   - `<memory-context>` 栅栏由 hermes `build_memory_context_block`（turn_context.py:78）包——已验证 manager 职责 ✅
   - `go test ./internal/...` 20/20 全绿（provider 新增 retrieve/prefetch 中文用例）

### 当前状态

- 本地领先 origin 1 commit（Phase 3）未提交未推送
- 真实 `~/.hermes/plugins/vatbrain/` + `~/.hermes/vatbrain/bin/` 已同步最新（prefetch 版）
- `~/.hermes/config.yaml` 仍未激活（待用户授权）

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

## 下一步（backlog 实施，issue #1）

**已完成并提交**（`feature/backlog-implementation`，P0-1 至 P1-4）：
- P0-1 v0.4 Public DX：README 定位改写 + DEMO_SCRIPT + COMPETITIVE_LANDSCAPE + CONTRIBUTING（good first issues）
- P0-2 v0.3 反馈闭环：ProtectionLevel + TimesAdopted + feedback_pitfall 工具 + suppress 降权 + 保护衰减
- P0-3 Watcher GA：OpenCode 适配器实现、list_adapters new/skipped_count、GA e2e 测试；修了 extractor 零向量聚类 bug + 泛化签名
- P0-4 hermes 插件钩子：on_turn_start/on_pre_compress/on_delegation/backup_paths + daemon maintenance/pre_compress/on_delegation + SourceTypeDelegation
- P1-1 评测增强：三组对比 + useful/false injection + task time/token 指标 + benchmark 报告（59.9% vs generic 24.7%，useful 65.9%）
- P1-2 模块复杂度入风险评分（ROADMAP 公式）
- P1-3 双通道 Embedder：KeywordEmbedder（CJK 伪向量）+ SemanticProvider（OpenAI 兼容/local）+ EmbedderConfig + env
- P1-4 Pitfall LLM 提取补全：spec prompt + 500 截断 + 数组解析 + 实体上限 50

**待办（下次会话）**：P1-5 ConflictResolver、P1-6 Surprise Score、P1-7 更多适配器（Codex/OpenClaw）、P2 六件套/架构概念、P3 + 技术债（详见 issue #1）
**收尾**：合并 `feature/backlog-implementation` → main（当前工作树干净，21/21 测试全绿）

- Hermes 集成全部完成并已激活（用户授权 config.yaml memory.provider: vatbrain）

---

## 2026-08-09 — HaluMem 真实评测（smoke 完成 + 全量进行中）

- **端点**：DeepSeek chat（deepseek-v4-flash）+ 智谱 embedding（embedding-3，2048 维）。
- **Smoke 结果**（user 0，164 题，池修复前）：整体 **37.8%**；Memory Boundary **95%**、Basic Fact Recall **10%**。诊断出检索缺陷：`SearchEpisodic` embedding 候选池只取 weight 前 100（上限 500）→ 修复为 5000（`embeddingRankPool`，commit `3cf67ad`）。
- **全量评测完成（User Memory 轨 3 benchmark）**：
  - **HaluMem 64.97%**（3460 题，20 users）：Boundary 96.3%（顶尖）、Conflict 70.5%、Generalization 58.2%、Multi-hop 51.3%、Basic Fact 43.6%、Dynamic Update 28.9%
  - **LoCoMo 57.0%**（914 题）：Single Hop 74.6%、Multi-hop 51.3%、Open Domain 56.1%、Temporal 15.0%（时序短板）
  - **LongMemEval 74.2%**（500 题）
- **过程修复**（OmniMemEval 克隆侧）：`extract_label_json` 三连修、judge prompt 改"只输出 JSON"、`LLM_DISABLE_THINKING=1` 开关。
- 教训：bench 重启必须 source `.env.bench`；后台 Bash watcher 会被杀，改用 Monitor；三路并行需独立 bench 实例。

## 2026-08-09 — OmniMemEval 评测框架评估（研究任务，未改代码）

- 克隆 `MemTensor/OmniMemEval` 到 /tmp/OmniMemEval，评估其作为 VatBrain 记忆能力评测入口的可行性。
- 结论：工程/架构/文档质量高（adapter 层 15 个 backend、六阶段 pipeline、LLM-as-judge+多指标）；风险=发布方 MemTensor 是 MemOS 母公司（利益冲突需复核）、复现分与官方分差距大、PersonaMem v2 多数 backend 贴近随机。
- 对 VatBrain 的启示：User Memory 线可写 adapter 评测；**HaluMem 与 ConflictResolver/Pitfall/decay 设计高度契合**。

## 2026-08-09 — backlog issue #1 P0+P1 全部完成 + 合并 main

- **P1-6 Surprise Score**：`models.EpisodicMemory/SemanticMemory.SurpriseScore` + sqlite 迁移；`core.SurpriseScorer`（纠正 0.7/行为改变 0.5/显式指令 0）；`WeightDecayEngine.SurpriseHalfLifeBoost`（最高 3× 半衰期）；检索 `EpisodicSearchRequest.SurpriseBoost`。文档：`docs/v0.3/tech-specs/01-surprise-score.md`
- **P1-5 ConflictResolver**：`core.ConflictDetector`（极性+bigram，CJK-safe）+ `ConflictResolver`（高 trust 覆盖）；`models.RuleConflict` + sqlite 表；`store.RuleConflictStore` 可选能力接口；MCP `detect/list/resolve_rule_conflicts`。文档：`docs/v0.3/tech-specs/02-conflict-resolver.md`
- **P1-7 更多 Watcher 适配器**：`codex.go`（OpenAI Codex 会话 JSONL）+ `openclaw.go`（OpenClaw 记忆 markdown）；配置 `VATBRAIN_CODEX_SESSIONS_PATH`/`VATBRAIN_OPENCLAW_MEMORY_PATH`。
- 验收：`go test ./internal/...` **547/22 包全绿** + go vet 干净；issue #1 checkbox 勾选；feature 分支快进合并 main。
- 用户指令：P1 清完即视为完成，P2/P3 暂缓。

## 2026-08-09 — Logo 三方向设计（另一会话，已并入 main）

- README 首屏加 VatBrain logo（`assets/logo/vatbrain-logo.png`）；`docs/superpowers/` 已 .gitignore

---

## 2026-08-10 — Agent Memory 轨 benchmark：规划 + 打通 + 全量（已结束）

### 规划 + 打通（前半程，结果见 docs/v0.3/09 全量章节）

- **目标**：Hermes + vatbrain 插件跑 EvoAgentBench 5 域，`memory_train_backup_test` 协议，对比 baseline（无记忆）vs vatbrain 提升。规划文档：`docs/v0.3/07-agent-benchmark-plan.md`。
- **OmniMemEval 克隆侧新增**（/tmp/OmniMemEval，git 未提交）：
  - `scripts/agentbench/agents/hermes.py` — HermesAgentAdapter（`hermes chat -q ... -Q --yolo --reasoning none`，每任务独立会话；注入 VATBRAIN_PROVIDER_BIN/HERMES_HOME/GATE_MODE/语义 embedding env）
  - `agents/__init__.py` 注册 `"hermes"`
  - `configs/agentbench/agents/hermes.yaml`（gate off + 语义 embedding）+ `hermes-baseline.yaml`（`/tmp/hermes-baseline`，memory.provider 空 → 无记忆 baseline）
  - `configs/agentbench/memory_plugins/vatbrain.yaml`（clear/backup/restore = pkill vatbrain-provider + 操作 ~/.hermes/vatbrain/vatbrain.db）
  - `.env.agent`（DeepSeek v4-flash judge + 智谱 embedding-3 + VATBRAIN_EMBEDDER_SEMANTIC_*）
- **vatbrain 仓库改动**（已提交 `99a3ce3`，feature 分支）：provider daemon 加 `VATBRAIN_GATE_MODE=off`（ForceConfirm，sync_turn 强制 UserConfirmed，对齐 bench GateModeOff）。已重新构建安装到 `~/.hermes/vatbrain/bin/vatbrain-provider`。
- **关键发现**：
  1. **provider 从未激活**：插件 `is_available()` 在 `initialize()` 前跑，`_hermes_home` 空 → 解析不到二进制 → 注入 `VATBRAIN_PROVIDER_BIN` 修复。
  2. **SignificanceGate 挡掉所有首次写入**：单会话单任务协议下 4 门控全不满足（"遗忘是默认" 假设长会话跨周期重复）→ gate off 是主测量，**gate-on 在此协议的代价本身就是重要结论**。
  3. 短会话 async sync_turn 写入实测能存活（local SQLite 快），无需改插件。
- **Smoke 结果**（reasoning 域，train 2 题 + test 2 题）：train pass@1=0.50、**test pass@1=1.00**；lifecycle clear/backup/restore 全 0；backup 39.3K；DB 记忆写入 6 条。
- **⚠️ 工具禁用关键修复**：`-t ""` 在 argparse 里变 None → 回落全部工具（agent 会 tool-loop）；改用无效工具集名 `-t vatbrain_none` → 真禁用。任务从 5+ 分钟降到 ~10-30 秒。全量必须用此配置。
- **⚠️ MiniMax 偶发挂死**：约 1/3 请求挂在 MiniMax（stale connection，重试新进程可恢复）。用看门狗自动杀 >90s（10 题样本）/ >240s（全量）的 hermes 进程。

### 全量结果 + 生效验证（后半程，2026-08-10 上午收尾）

- **全量跑完**：vatbrain（478 train + 100 test）+ baseline（100 test）全部完成。结果已写入 `docs/v0.3/09-agent-benchmark-results.md`。
- **核心结果**：test pass@1 **baseline 21.00% = vatbrain 21.00%（对齐 100 题持平）**；26 个逐题翻转（13 错→对 / 13 对→错）净零 → **null result**。10 样本的 10%→20% 提升是假阳性，未复现。
- **⚠️ 生效验证（重要教训）**：最初因检索错字段——查 `~/.hermes/state.db` 的 `content` 列，而记忆注入实际写入 **`api_content` 列**——误判"记忆未生效"。更正后确认**全链路生效**：provider 每会话 spawn/initialize（agent.log 1200+ 行）、DB 729 条 episodic、test 窗口 **1209 条 `<memory-context>` 注入**（648 任务 prompt + 560 feedback 记忆）。
- **根因**：域 × 协议不匹配——注入的是其他题的完整 prompt + 该题 verifier 答案，对独立求解的数学题**无可迁移价值**。train/test 互不重叠 → 记忆注入 ≈ 无用上下文。
- **下一步建议**：换迁移信号存在的域/协议（代码修补、同题不同 seed 等），或多次重复试验区分噪声。
---

## （2026-08-10）— 合并 PR #2 + 存储战略文档同步

- 用户合入测评分支 PR #2（`feature/omnimemeval-benchmark` → main）；本地 merge origin/main（仅 agent_context.md 冲突，已解决）
- 存储战略确认"全面转向 SQLite"；同步 4 份文档 + agent_context（commit `f9694b6`）：
  - `docs/v0.1.1/00-storage-refactor-draft.md`：状态"草案"→"已实施"，3 个未决项定案（WAL 默认开、不建反向迁移工具、旧后端只兼容不投入），新增战略决策小节
  - `docs/ROADMAP.md`：v0.1 基础设施交付物标注替代方案；数据留存策略重写为 SQLite 单库（LRU+WAL）；技术债备忘录标记失效项；新增"存储战略决策"小节
  - `docs/DESIGN_PRINCIPLES.md`：头部加实现注记（基石正文不动，旧存储组件名按注记解读）
  - `CLAUDE.md`：§4 技术栈改写（SQLite 为主存储，Go 1.25+）；§5 目录约定标注 db/ 与 neo4jpg 为弃用
- 未动：`docs/v0.1/00-design.md`、`docs/v0.2/00-design.md`（历史定稿文档，保持原样）

---

## （2026-08-10）— Agent Memory 轨全量结果 + 生效验证

- **全量跑完**：vatbrain（478 train + 100 test）+ baseline（100 test）。结果文档：`docs/v0.3/09-agent-benchmark-results.md`。
- **核心结果**：test pass@1 **baseline 21.00% = vatbrain 21.00%（对齐 100 题持平）**。26 个逐题翻转（13 错→对 / 13 对→错）净零 → **null result**。vatbrain train pass@1 = 51.88%。10 样本的 10%→20% 提升**未复现，是假阳性**（omni_2292 全量里两边都错）。
- **⚠️ 生效验证（重要教训）**：记忆**全链路确实生效**。最初因检索错字段——查 `~/.hermes/state.db` 的 `content` 列，而记忆注入实际写入 **`api_content` 列**——误判"未生效"。更正后证据：
  - provider 每会话 spawn + initialize（`~/.hermes/logs/agent.log`，1200+ 行 INFO）
  - vatbrain.db 729 条 episodic（531 任务 prompt + 198 verifier feedback），backup/restore 全 0
  - test 窗口 **1209 条 message 的 `api_content` 含 `<memory-context>` 围栏**（648 任务 prompt + 560 feedback 记忆）
- **根因**：域 × 协议不匹配——注入的是**其他题的完整 prompt + 该题 verifier 答案**，对独立求解的数学题**无可迁移价值**（train/test 互不重叠）。"任务记忆 = 完整 prompt（价值低）"隐患在此协议下成为主导。
- **结论**：null result 是关于 benchmark 协议的，不是关于记忆系统的。要验证记忆价值，需换**迁移信号存在的域/协议**（代码修补、同题不同 seed、重复模式流水线），或多次重复试验区分噪声。
- **提交**：`09-agent-benchmark-results.md` + agent_context 已更新并推送到远端 feature 分支。

## 已知问题

- **Agent 轨实跑速度**：MiniMax-M3 首 token 延迟高（~50s），全量 100 test 题跑 ~9h；挂死靠看门狗杀 >240s 进程兜底。
- **reasoning 域 `--train-split` 数值 bug**：`load_tasks` 数字 split 恒加载 test 集 → 用小集用 `--task` 选 id。
- **SWE-Bench 跳过**：本机无 docker，orange 是 aarch64（官方镜像 x86_64）→ 只跑其余 4 域。
- **deriver summary = 完整任务 prompt**（非压缩洞见）→ 记忆价值待 benchmark 暴露（已在全量中验证为低价值，需检索侧压制任务 prompt）。
- **记忆注入字段**：hermes 把 prefetch 注入写进 `api_content`（API 副本），`content` 列不含注入——验证记忆是否注入必须查 `api_content`。
- 插件 `shutdown()` 时后台 sync 线程可能未完成（best-effort，短会话实测能存活）。
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 语义）。

