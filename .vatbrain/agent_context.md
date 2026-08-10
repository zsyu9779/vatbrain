# Agent Context - 当前工作上下文

> 每次交互必读必写

## 最近工作（2026-08-10）— v0.4 ticket 03:Update Tracking(信息更新 → 旧记忆显式废弃/提升)✅

- **worktree**: `/tmp/v0.4-wt-03`(分支 `v0.4/ticket-03`,基于 11ff3468 = ticket 02 合入点)。实现 + 测试 + commit 全部在该 worktree,未触碰主仓库
- **更新检测**: `core.UpdateTracker.DetectUpdate`(纯判定)——复用冲突检测语义基础(同主题 bigram Dice ≥ 0.25 + 指令极性),新增时间维度:新信息 `EffectiveOccurredAt()` 严格晚于旧记忆才构成覆盖;复述守卫(Dice ≥ 0.9 或子串包含)→ 走 pattern-separation append 既有路径;指令极性翻转(不要↔应该)豁免守卫;实体约束(双方锚定 EntityGroup 时须一致)
- **生效动作**: `ApplyUpdate`——被覆盖旧记忆 `MarkObsolete` 废弃;`SUPERSEDED` 边(新→旧,`at`+`reason` props)记录覆盖关系(可解释可追溯);新记忆权重 ×1.5 提升(ClampWeight,与 ReconsolidationEngine 一致)
- **自动检测**: 写管线 `writeMemoryPersist` 在 append 合并**之前**判定;被覆盖候选跳过合并(避免新旧内容混入同一条记忆);`WriteDeps.UpdateTracker` 可注入(nil → Default,默认开启)
- **显式信号**: MCP 工具 `signal_update(memory_id, boost?)`——对既有记忆补发更新信号,返回 detected/applied/pairs/carrier_weight;幂等(已废弃候选跳过,重复执行 0 对、无重复边)
- **测试**: 检测 7 类判定用例(覆盖/非更新/异主题/精确复述/前缀扩展复述/极性翻转/已废弃跳过/实体约束)、Apply 动作+自定义 boost、RunUpdateTracking 端到端+幂等、写管线 4 条集成(废弃旧/复述仍合并/更早事件不触发/异主题不触发)、MCP 工具 3 条(端到端+幂等/无覆盖/不存在)——全绿;全量测试除已知 docker 依赖(neo4j/pgvector smoke+e2e)外通过
- **范围约束遵守**: 不新建并列机制(复用 bigramDice/DetectPolarity/MarkObsolete/CreateEdge/UpdateEpisodicWeight/写管线漏斗);未动 provider 协议签名;语义记忆层不新增更新机制(规则层已有 trust 裁决)
- **待办**: 评测验证在 ticket 06 收口(HaluMem Dynamic Update 28.9% → ≥50% 预期);`docs/v0.4/03-update-tracking.md` 决策表 10 项 + 已知权衡(同义复述风险)

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
