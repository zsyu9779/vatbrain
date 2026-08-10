# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成完成 + backlog issue #1 完成；**Agent Memory 轨 benchmark 全量跑完 = null result（记忆全链路生效但无跨任务迁移价值）；PR #2（OmniMemEval 测评接入）已合入 main** ✅
- **语言**: Go (go 1.25.5) + Python（hermes 插件 + OmniMemEval AgentBench）
- **分支**: `main`（已合入 PR #2 `feature/omnimemeval-benchmark` + 本会话文档同步）
- **远端**: `origin/main` 已同步（PR #2 合入后本地已 merge）
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点 · `docs/v0.3/05-agent-memory-handoff.md`（Agent 轨启动指南）· `docs/v0.3/07/08/09-*`（benchmark 规划/烟测/正式结果）
- **hermes**: `~/.hermes/hermes-agent`（HEAD 52920747e）+ 插件 `~/.hermes/plugins/vatbrain/` 已激活（config.yaml memory.provider: vatbrain）
- **战略决策（2026-08-08，2026-08-10 确认全面转向）**: 存储全面转向 SQLite（modernc.org/sqlite），Neo4j+pgvector/Redis/MinIO 弃用；概念不变（边表=图、BLOB=向量）；新增能力只实现 SQLite 后端；旧后端代码保留兼容、不再投入、待清理

## 最近工作（2026-08-10）— v0.4 ticket 02:时序记忆最小深入(occurred_at + 时间检索)✅

- **worktree**: `/tmp/v0.4-wt-02`(分支 `v0.4/ticket-02`,基于 2d7c2df = ticket 01 合入点)。实现 + 测试 + commit 全部在该 worktree,未触碰主仓库
- **时间属性**: `episodic_memories.occurred_at TEXT` 列(v0.4 迁移:ALTER + 回填 created_at + `idx_episodic_occurred` 索引,幂等);`EpisodicMemory.OccurredAt`(零值回退 CreatedAt,`EffectiveOccurredAt()`);写管线 `WriteEvent.OccurredAt` 透传(WriteMemory 与 WriteMemoryWithEmbedding 双路径,无显式时间回退写入时刻;merge 保留原发生时间)
- **时间检索**: `EpisodicSearchRequest.OccurredAfter/OccurredBefore/SortByOccurredAt`(含 embed 路径;时间排序优先于余弦;时间查询旁路 hot cache);provider `ParseRelativeTime`(上周/昨天窗口 + 最近一次/最新 时间排序,中英双语);`EpisodicScanItem.OccurredAt` 供词法回退路径过滤
- **兼容层保留**: bench `datePrefixedContent` 的 `[YYYY-MM-DD]` 摘要前缀不动;chat_time 解析抽为 `parseChatTime` 共享,`eventFor` 同时设 OccurredAt
- **测试**: 迁移回填+幂等、读写回环+回退、过滤/排序(结构化+embed 路径+缓存旁路)、双写路径透传、ParseRelativeTime 9 用例、provider 集成(embed/词法双路径)、bench 端到端——全绿;全量测试除已知 docker 依赖(neo4j/pgvector smoke+e2e)外通过
- **范围约束遵守**: 未动 provider 协议签名(MCP/api/JSON-RPC 无变化);neo4jpg 旧后端仅不持久化新字段(弃用,不投入)
- **待办**: 评测验证在 ticket 06 收口(LoCoMo Temporal 15% / LME 52.6% 回升预期)

## 最近工作（2026-08-10）— v0.4 ticket 01:bench 基建(并发 ingestion + 延迟微基准)✅

- **worktree**: `/tmp/v0.4-wt-01`(分支 `v0.4/ticket-01`,基于 c9cd9b1)。实现 + 测试 + commit 全部在该 worktree,未触碰主仓库
- **并发 ingestion**: `/v1/add` 两阶段——worker 池(默认 32,钳制 [1,64])+ 批量 64 文本/请求 + 429/1302/1305 指数退避重试 并行 embedding,再按请求顺序写库(SQLite 单写者)。语义与顺序流水线完全一致(门控/合并顺序不变)
- **新增**: `internal/embedder/batch.go`(OpenAIProvider.EmbedBatch 分块重试 + Keyword/Dual 的 EmbedBatch + BatchEmbedder 池);`core.WriteMemoryWithEmbedding`(预计算向量 seam,共享 prepareWriteEvent/writeMemoryPersist);bench `Options.IngestWorkers/EmbedBatchSize` + flag `--embed-workers/--embed-batch`(env VATBRAIN_BENCH_* 可覆盖);无批量能力 embedder 自动回退顺序路径
- **延迟微基准**: `internal/bench/latency_bench_test.go`(写入 full/precomputed × 内存/SQLite、检索命中/miss、整合 300 条,p50/p95/p99 nearest-rank)+ `docs/v0.4/01-bench-infra.md`
- **首次实测(Apple M3,关键词 embedder,内核成本)**: 写入 SQLite full p95 58.1ms、precomputed p95 65.4ms;检索命中 p95 39.4ms / miss p95 67.9ms;整合 300 条 p95 4.9ms——**ROADMAP 里程碑(写 <200ms、命中 <100ms、miss <500ms)首次实测全部达标**
- **关键观察**: 写入/检索瓶颈 = pattern-separation 检索拉 `embeddingRankPool=5000` 行 BLOB(1536 维 ~6KB/行)做进程内余弦(HaluMem 召回修复的既定代价),大库下是后续向量索引专项的基线
- 全量测试通过(除已知 docker 依赖:neo4j/pgvector smoke + e2e)

## 最近工作（2026-08-10）— v0.4 草案制定（评测驱动精修 + 价值证明）

- 综合分析三份输入：User Memory 轨评测（LME 74.2/HaluMem 65.0/LoCoMo 57.0，短板=时序 15%/52.6、动态更新 28.9、事实召回 43.6）、Agent 轨 null result（域×协议不匹配、注入=其他题完整 prompt）、遗留 feature（issue #1 余 12 项）→ `docs/v0.4/00-draft.md`
- 核心判断：主线 Measure 未闭环 → v0.4 = 修短板（时序/动态更新/检索增强）+ 价值证明（Agent 轨二轮换域/协议 + 注入压制）
- 范围：P0 六项（时序深入、并发 ingestion、judge 对齐重跑、Update Tracking、RRF+query expansion、延迟微基准）；P1 四项（Agent 轨二轮、Context 精简、DX 数据化、反事实）；暂缓压缩残差/自适应衰减/冷分层；多级存储降级为文件备份；Team Memory 不入
- 决策定案（用户确认 4 项）：定位=评测驱动精修 ✅；Agent 轨二轮域=同题不同 seed（先）+ 代码修补场景（后）；时序=最小深入（occurred_at 列 + 检索排序，不动 provider 协议）；反事实=P2 等数据
- `docs/v0.4/00-draft.md` 状态"草案"→"已定案"，§11 决策记录已更新
- 未提交 benchmark 测试结果（后台 bmdoe0tuy 运行中）

