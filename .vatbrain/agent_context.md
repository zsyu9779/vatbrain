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

- **阶段**: Hermes 集成完成 + backlog issue #1 完成；**Agent Memory 轨 benchmark 全量跑完 = null result（记忆全链路生效但无跨任务迁移价值）；PR #2（OmniMemEval 测评接入）已合入 main** ✅；**v0.4 ticket 01/02/03/04 已完成并合入 main**
- **语言**: Go (go 1.25.5) + Python（hermes 插件 + OmniMemEval AgentBench）
- **分支**: `main`（已合入 PR #2 `feature/omnimemeval-benchmark`）+ v0.4 ticket worktree（01/02/04 各自独立分支合入点）
- **远端**: `origin/main` 已同步（PR #2 合入后本地已 merge）
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点 · `docs/v0.3/05-agent-memory-handoff.md`（Agent 轨启动指南）· `docs/v0.3/07/08/09-*`（benchmark 规划/烟测/正式结果）
- **hermes**: `~/.hermes/hermes-agent`（HEAD 52920747e）+ 插件 `~/.hermes/plugins/vatbrain/` 已激活（config.yaml memory.provider: vatbrain）
- **战略决策（2026-08-08，2026-08-10 确认全面转向）**: 存储全面转向 SQLite（modernc.org/sqlite），Neo4j+pgvector/Redis/MinIO 弃用；概念不变（边表=图、BLOB=向量）；新增能力只实现 SQLite 后端；旧后端代码保留兼容、不再投入、待清理

## 最近工作（2026-08-10）— v0.4 ticket 04:检索增强(RRF 融合排序 + Query Expansion)✅

- **worktree**: `/tmp/v0.4-wt-04`(分支 `v0.4/ticket-04`,基于 11ff3468 = ticket 02 合入点)。实现 + 测试 + commit 全部在该 worktree,未触碰主仓库
- **Query expansion**: `embedder.ExpandQuery`(确定性、CJK-safe、无需外部服务:实体引用 `@?x.ext` 大小写不敏感 + Latin 词元 + 数字,原问题保留为前缀,术语去重);`CharBigrams/BigramOverlap/BigramOverlapFromSets` 从 provider 提升到 embedder(词法基元共享,ASCII 大小写折叠——大小写变体召回的杠杆),provider 本地副本删除
- **RRF 融合**: `EpisodicSearchRequest.Query + RrfK`(默认 60,可调);`SearchEpisodic` 在 Embedding+Query 同时给出时走 `rrfRanked`——语义余弦排序 ∪ 词法 query-vs-summary bigram 排序 → `score = Σ 1/(K+rank)`;无向量候选经词法通道进入排序(精确事实召回);SurpriseBoost 后置乘到融合分(共存);硬约束仍在 SQL WHERE 层先压候选池(测试验证不绕过);词法打分无向量候选也参与;Query 无 Embedding 不激活(纯结构化查询行为不变)
- **provider 接线**: `RetrieveEpisodic` 先 `ExpandQuery` 再 Embed,语义路径请求带 `Query`(RRF 激活),词法回退路径用扩展文本打分;`RetrievePitfalls` 改走 embedder 共享基元(行为不变)
- **测试**: embedder expand 11 用例;sqlite `rrf_test.go` 8 用例(词法救援/无向量救援/不劣于语义基线/K 可调翻序/SurpriseBoost 共存/硬约束/时间排序 rank-then-sort/Query 无 Embedding);provider 端到端 2 用例(case-fold 召回、RRF 无向量召回,后者用 keyword embedder + sqlite store);全量通过(除已知 neo4jpg docker 挂起)
- **范围约束遵守**: 协议签名零变化(仅请求结构体加字段,additive);api/mcp 检索入口未接线扩展(保持原行为,RRF 为 opt-in 机制);memory 内存后端未实现融合(文档注明退化为纯语义)
- **待办**: 评测验证在 ticket 06 收口(Basic Fact 43.6% 回升预期;多跳检索同升)

## 最近工作（2026-08-10）— v0.4 ticket 02:时序记忆最小深入(occurred_at + 时间检索)✅

- **worktree**: `/tmp/v0.4-wt-02`(分支 `v0.4/ticket-02`,基于 2d7c2df = ticket 01 合入点)。实现 + 测试 + commit 全部在该 worktree,未触碰主仓库
- **时间属性**: `episodic_memories.occurred_at TEXT` 列(v0.4 迁移:ALTER + 回填 created_at + `idx_episodic_occurred` 索引,幂等);`EpisodicMemory.OccurredAt`(零值回退 CreatedAt,`EffectiveOccurredAt()`);写管线 `WriteEvent.OccurredAt` 透传(WriteMemory 与 WriteMemoryWithEmbedding 双路径,无显式时间回退写入时刻;merge 保留原发生时间)
- **时间检索**: `EpisodicSearchRequest.OccurredAfter/OccurredBefore/SortByOccurredAt`(含 embed 路径;时间排序优先于余弦;时间查询旁路 hot cache);provider `ParseRelativeTime`(上周/昨天窗口 + 最近一次/最新 时间排序,中英双语);`EpisodicScanItem.OccurredAt` 供词法回退路径过滤
- **兼容层保留**: bench `datePrefixedContent` 的 `[YYYY-MM-DD]` 摘要前缀不动;chat_time 解析抽为 `parseChatTime` 共享,`eventFor` 同时设 OccurredAt
- **测试**: 迁移回填+幂等、读写回环+回退、过滤/排序(结构化+embed 路径+缓存旁路)、双写路径透传、ParseRelativeTime 9 用例、provider 集成(embed/词法双路径)、bench 端到端——全绿;全量测试除已知 docker 依赖(neo4j/pgvector smoke+e2e)外通过
- **范围约束遵守**: 未动 provider 协议签名(MCP/api/JSON-RPC 无变化);neo4jpg 旧后端仅不持久化新字段(弃用,不投入)
- **待办**: 评测验证在 ticket 06 收口(LoCoMo Temporal 15% / LME 52.6% 回升预期)

> ticket 01 条目已由 ticket 04 worker 移入 `.vatbrain/agent_context_archive.md`(2026-08-10)。
