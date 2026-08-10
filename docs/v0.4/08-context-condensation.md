# v0.4 — Ticket 08:Context 精简(效率轴)

> 状态:已落地(2026-08-10)
>
> 前置:04 — Retrieval 增强(RRF 融合排序 + Query Expansion)
>
> 对应票:08 — Context 精简(效率轴)——检索注入的上下文从"10K+ tokens/题"减半以上:去重、压缩、更准的 top-k。

## 1. 背景与定位

评测暴露的弱项之一:**上下文效率 10K+ tokens/题**(docs/v0.3/06-v3-iteration-plan.md §P2-6)。MemOS 赢在"低上下文高分";VatBrain 的注入上下文含大量 top-k 噪声。v0.4 草案 P1-2 将其定为效率轴交付,并与 P0-5(Retrieval 增强 = ticket 04 的 RRF + Query Expansion)协同——"更准的 top-k"由 RRF 提供,本票负责**近重复压制 + token 预算精简 + 不让精简破坏排序**。

### 1.1 10K+ 的构成(为什么注入面有冗余)

写管线的 pattern-separation 追加路径**刻意保留 restatement**(RestatementSimilarity 0.9 之上不合并、不废弃,只追加新记忆)——存储层这是正确的"可分离性判别"语义。但代价是:同一事实被反复写入后,检索 top-k 里满是近重复记忆,注入时逐条搬运,上下文被冗余副本撑大。**精简的落点因此是注入面,不是存储面**:被抑制的不是记忆,只是本次注入中信息冗余的副本。

## 2. 方案

### 2.1 注入侧精简(与 RRF 协同,不另起炉灶)

新增 `internal/provider/condense.go`,三个机制,全部复用既有基元:

| 机制 | 实现 | 与既有语义的关系 |
|---|---|---|
| **近重复压制** | 按 RRF 序走一遍,与已保留代表做 `redundantRestatement` 判定(文本相同 / 子串包含 / bigram Dice ≥ 0.9),是则抑制;pitfall 压制带**实体锚**约束(仅同 EntityID 可比,不同实体的建议绝不折叠) | Dice 阈值 0.9 = 写路径 `RestatementSimilarity`(core.UpdateTracker 默认)——**召回侧抑制的恰是写路径判定为"信息冗余 restatement"的记忆**,与 pattern-separation append、reconsolidation 语义对齐,不冲突;细节差异(Dice < 0.9)绝不压制,唯一例外是写路径同款的子串包含规则(守门用例:"for thinking" vs "for text" Dice ≈ 0.84 → 保留) |
| **token 预算裁剪** | 格式化后 `EstimateTokens` 超预算则从 rank 尾部裁 episodes,再裁 pitfalls;软预算硬地板:每个非空列表至少保留 top-1,绝不注入空上下文 | 预算默认 2048 tokens,是默认 5+3 注入的 3-4 倍——**常见路径不触发,零回归**;病态上下文被硬性封顶 |
| **可测量输出** | `PrefetchStats{InputTokens, OutputTokens, Suppressed*, BudgetHit}` + `EstimateTokens`(确定性:每 CJK 字符 1 token,其余 4 字符 1 token) | 效率轴(评测 context tokens/题)的仓库内测量点;`buildPrefetch` 注入路径输出 Debug 日志含统计 |

### 2.2 接线

- `buildPrefetch`(JSON-RPC prefetch / queue_prefetch 真实注入路径)改用 `FormatPrefetchCondensed(episodes, pitfalls, DefaultCondenseOptions())`;无重复且预算不触发时输出与旧 `FormatPrefetch` **逐字节一致**(回归守卫测试)。
- `RetrieveEpisodic` / `RetrievePitfalls` / MCP search / bench `/search` **不改**:检索 API 保持纯净,RRF 排序原样;精简只发生在注入面(票面明确"在注入前合并/压制")。

### 2.3 术语

按 CLAUDE.md §3:机制名为 **condensation(精简)** 与 **suppression(压制)**,复用写路径的 **restatement** 词汇;不引入 Merge/Compress/Delete/Dedup 等禁用术语。

## 3. 验收对照

| 验收项 | 证据 |
|---|---|
| 精简落地:近重复压制 + 预算精简 + 更准 top-k 协同(票面"去重/压缩/更准 top-k"——与 RRF 协同,不另起炉灶) | `condense.go` + `buildPrefetch` 接线;RRF 序由 04 提供,本票只做"子序列保持"的压制与裁尾,守卫测试见下 |
| token 量可测量,减半以上可复现 | `EstimateTokens` + `PrefetchStats`;**复现数字(condense_test.go TestFormatPrefetchCondensed_Halving):入 676 tokens(18 条 episode + 3 条 pitfall)→ 出 226 tokens(3 条 + 3 条),减少 66.6%**;18 条近重复被压制 15 条,全部独立信息点(每簇代表 + 3 个风险提示)保留。注:该数字是仓库内复现(夹具模拟"同一事实反复写入"的评测形态);harness 实测 context tokens/题 随 05/06 重跑收口(见 §6) |
| 回归:recall 质量不劣化(05 基线为参照,单元测试覆盖) | 05(真实 benchmark)已 defer;以单元测试为回归网:① 细节差异不压制(0.84 < 0.9);② 常见路径与旧格式逐字节一致;③ 注入顺序保持 RRF 子序列不变、top-1 恒在;④ 预算只裁 rank 尾部;⑤ 接线级测试(两条相同记忆在库 → prefetch 只注入一条)。基准级回归随 05/06 重跑收口 |

## 4. 测试清单

`internal/provider/condense_test.go`(10 个顶层用例 + EstimateTokens 8 个子用例,全绿):

- `TestEstimateTokens_Literals` — 计量确定性(空/ASCII/CJK/混合/边界,字面算术)
- `TestCondenseEpisodes_SuppressesNearDuplicates` — 相同 + 前缀扩展抑制;thinking/text 细节差异保留(手算 Dice 46/55 ≈ 0.84)
- `TestCondenseEpisodes_OrderPreservedForDistinct` — 无重复不误伤、序不变
- `TestCondensePitfalls_SuppressesNearDuplicates` — 同实体近重复签名只留一条;不同实体、相同签名不折叠(实体锚守卫)
- `TestFormatPrefetchCondensed_MatchesFormatPrefetchWithoutDuplicates` — 常见路径逐字节一致(回归守卫)
- `TestFormatPrefetchCondensed_BudgetTrimsRankTail` — 预算字面算术(2×13+7 ≤ 40 < 3×13+7),只裁尾部
- `TestFormatPrefetchCondensed_BudgetKeepsTopOnePerList` — 软预算硬地板,不注入空上下文
- `TestFormatPrefetchCondensed_Halving` — **减半复现:676 → 226,66.6%**
- `TestFormatPrefetchCondensed_RrfOrderPreserved` — RRF 协同守卫:无预算时子序列完全一致;预算命中时保留的是前缀
- `TestServe_Prefetch_CondensesNearDuplicates` — 接线级:真实注入路径上两条 restatement 只注入一条

## 5. 技术决策

| # | 决策点 | 选择 | 理由 |
|---|---|---|---|
| C1 | 精简落点 | 注入面(FormatPrefetchCondensed / buildPrefetch),不改检索 API 与存储 | 票面"在注入前合并/压制";存储层 pattern-separation 语义不动;检索 API 保持纯净 |
| C2 | 压制阈值与锚 | episodes:bigram Dice ≥ 0.9 + 文本相同 + 子串包含;pitfalls:同 EntityID 前提下的同规则 | 与写路径 RestatementSimilarity 同界——召回侧抑制的恰是写路径判定冗余的记忆;细节差异(Dice < 0.9)绝不压制(唯一例外:子串包含);不同实体的建议绝不因签名相同而折叠(实体锚守卫,Spec 审查发现并修复) |
| C3 | 预算精简机制 | token 预算(默认 2048)裁 rank 尾部,软预算硬地板(top-1 恒在) | 可测量、可封顶;常见 5+3 注入不触发(零回归);极端预算下不注入空上下文 |
| C4 | token 计量 | 确定性估算:每 CJK 字符 1 token,其余每 4 字符 1 token | 无需外部服务,同文本恒同值;单调;作效率轴仓库内测量点,评测侧数字随 05/06 对齐 |
| C5 | 是否在检索侧取更大候选池填充 | 不做(fill-up 未选) | 最小范围;压制不损失信息(被抑制的都是 restatement),密度略降换来 token 减半,符合效率轴定位 |
| C6 | bench /search、MCP search 是否接精简 | 不接 | 协议与检索语义稳定优先;用户轨 harness 侧注入由评测轮(05/06)验证 |

## 6. 已知权衡与边界

- **软预算**:极端预算下输出可能略超预算(地板规则保证 top-1 恒在);默认预算 2048 远高于常见注入,仅在病态上下文触发。
- **同义复述边界**:Dice 0.84-0.9 之间的近义记忆不被压制(保守侧),宁多注入不误伤信息。
- **评测侧数字**:676 → 226 是仓库内复现(测试夹具模拟"同一事实反复写入"的评测形态);harness 实测 context tokens/题 的减半数字随 ticket 05/06 真实 benchmark 重跑收口。
- **范围外**:Agent 轨注入压制(任务 prompt 类记忆)属 P1-1,不在本票。

## 7. 文件清单

- `internal/provider/condense.go`(新)— EstimateTokens / CondenseOptions / PrefetchStats / FormatPrefetchCondensed / 近重复压制
- `internal/provider/retrieve.go` — FormatPrefetch 抽私有 formatPrefetch(行为不变,供精简路径复用渲染)
- `internal/provider/server.go` — buildPrefetch 接线精简路径 + Debug 统计日志
- `internal/provider/condense_test.go`(新)— 10 用例
- `docs/v0.4/08-context-condensation.md`(本文)
