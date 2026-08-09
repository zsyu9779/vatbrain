# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成 Phase 1-6 已完成并入 main；**backlog issue #1 实施中（P0+P1 部分完成）**
- **语言**: Go (go 1.25.5) + Python（hermes 插件）
- **分支**: `feature/backlog-implementation`（P0-1 至 P1-4 已提交推送，工作树待同步）
- **远端**: `origin/feature/agent-memory-watcher` 已删；main 已含 Hermes 全量
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点
- **hermes 源码**: 本机 `~/.hermes/hermes-agent`（HEAD 52920747e，工作树干净）
- **hermes 插件**: 已安装 `~/.hermes/plugins/vatbrain/` + daemon 二进制 `~/.hermes/vatbrain/bin/vatbrain-provider`
- **战略决策（2026-08-08）**: Neo4j+pgvector 重型存储将弃用，后续只投入 SQLite 路径

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

## 已知问题

- OpenCode 适配器仍为骨架（`internal/watcher/adapters/opencode.go:61` TODO，v0.2.1 GA 待办）
- 插件 `shutdown()` 时后台 sync 线程可能未完成（best-effort，daemon 退出即弃）
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 语义；replace/obsolete 镜像属 Phase 4）
