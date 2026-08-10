# v0.4 Ticket 03 — Update Tracking(信息更新 → 旧记忆显式废弃/提升)

> 评测证据:HaluMem Dynamic Update 28.9%,根因:新信息覆盖旧信息时无显式废弃,
> 新旧记忆在检索时并存竞争,answer 被旧状态污染。本票让"时间上更新"的同一主题
> 新信息显式废弃(obsolete)旧记忆并提升新记忆权重,使信息更新后的状态跟踪可验证。
> 评测验证在 ticket 06 收口(HaluMem Dynamic Update 28.9% → ≥50% 预期)。

## 实现内容

1. **更新检测**(`internal/core/update_tracker.go`):`UpdateTracker.DetectUpdate`——
   纯判定(无 I/O),复用冲突检测的语义基础(同主题字符 bigram Dice + 指令极性),
   新增时间维度:新信息 `EffectiveOccurredAt()` **严格晚于** 旧记忆才构成覆盖。
   三重门槛:
   - 同主题:Dice ≥ 0.25(与 `ConflictDetector` 同默认值)
   - 非复述:复述(Dice ≥ 0.9)走 pattern-separation append 既有路径,不废弃;
     **指令极性翻转(不要↔应该)例外**——显式反转即使 bigram 高度重合也是更新
   - 时间上更新:新信息 occurred_at 严格 After 旧记忆;同批写入(时间相等)不触发
   - 实体约束:双方都锚定 EntityGroup 时必须一致;单侧未锚定由主题相似度决定
2. **生效动作**(`UpdateTracker.ApplyUpdate`):被覆盖旧记忆 `MarkObsolete` 废弃;
   `SUPERSEDED` 边(新→旧,props 携带 `at` + `reason`)记录覆盖关系——动作可解释、
   可追溯;新记忆权重 ×1.5(`WeightBoost`,与 ReconsolidationEngine 一致)提升。
3. **自动检测**:写管线(`writeMemoryPersist`)在 pattern-separation append **之前**
   对候选做更新判定;被覆盖候选**不进入 append 合并**(避免新旧内容混入同一条记忆),
   直接废弃;新记忆(或承载新信息的合并记忆)权重提升。`WriteDeps.UpdateTracker`
   可注入自定义 tracker(默认 `DefaultUpdateTracker()`,nil 即默认)。
4. **显式信号**:新 MCP 工具 `signal_update(memory_id, boost?)`——对一条已有记忆
   补发更新信号(自动检测的补位路径),返回 `{detected, applied, pairs, carrier_weight}`,
   每个 pair 带 old_id/时间/bigram 相似度/极性/reason,动作可解释。
5. **幂等**:`DetectUpdate` 跳过已废弃候选(ObsoletedAt != nil);重复执行同一信号
   找到 0 对 → 无重复边、无重复提升。

## 技术决策

| # | 决策 | 理由 | 备选 |
|---|------|------|------|
| 1 | 更新判定 = 同主题(bigram ≥ 0.25,复用冲突检测)+ 严格更新的 occurred_at | "可复用冲突判定作为语义基础";时间维度是 ticket 新增信号 | 要求极性相反(漏掉中性事实更新,如"偏好 PostgreSQL"→"改用 SQLite") |
| 2 | 复述守卫:Dice ≥ 0.9 **或** 子串包含(一方是另一方子串)且非极性翻转 → 走 append 合并,不废弃 | 同事件复述保留故事锚点(ticket 02 决策 4 的语义);子串包含 = 新陈述携带旧陈述全部信息,合并零信息损失("alpha beta gamma"→"alpha beta gamma delta");精确复述 Dice=1.0 必然走守卫 | 无守卫(精确复述/前缀扩展会被误判为更新,破坏合并语义与并发写序测试) |
| 3 | 极性翻转(不要↔应该)豁免复述守卫 | 指令反转与原文仅差 2 字,Dice≈0.93 会误入复述分支;且"不要用 X 了,应该用 Y"这类反转包含旧句子子串,必须靠极性豁免;显式反转是 HaluMem 动态更新核心形态 | 守卫一刀切(反转被 append 合并,新旧内容混入同一条记忆) |
| 4 | 判定在 pattern-separation append 之前,被覆盖候选跳过合并 | 更新事件若与旧记忆合并会把新旧内容混成一条(检索返回"PostgreSQL\nSQLite" 双态文本);先废弃再新写,检索只看到新状态 | 判定在合并后(混内容问题无解) |
| 5 | 废弃用既有 `MarkObsolete`(episodic),不新建状态字段 | 两条检索路径(embedding/词法 ScanRecent)均已过滤 obsoleted_at IS NULL,废弃即从检索消失——闭环已存在,零新机制 | 新建"superseded"状态列(并列机制,违背 ticket 约束) |
| 6 | 可追溯:SUPERSEDED 边(新→旧,`at`+`reason` props) | 动作"可解释、可追溯";边表本就是 SQLite 的"图"表达,零新存储 | 仅废弃不记录(无法回答"为什么这条被废弃") |
| 7 | 权重提升 ×1.5 复用 ClampWeight,与 ReconsolidationEngine.CorrectionBoost 一致 | 提升幅度与既有纠正路径一致,语义统一;新记忆天然 1.0(钳制后不可见),提升主要作用于衰减中的旧承载 | 绝对提升(引入第二套幅度语义) |
| 8 | 显式信号走 `SortByOccurredAt` 检索最近 500 条 peers | 时间排序天然旁路 hot cache(ticket 02 决策 6),信号立即看到新写入;500 条有界扫描 | 无排序检索(命中 5 分钟陈旧缓存,信号看不到刚写的记忆) |
| 9 | 自动检测默认开启(nil tracker → Default),不经独立开关 | 写管线是唯一写入漏斗(MCP write / provider sync / bench 全走 WriteMemory),自动检测必须在漏斗内才惠及评测;复述/异主题/非更新时间均不触发,行为兼容 | 仅 bench 路径开启(生产无更新跟踪,与 ticket 意图不符) |
| 10 | 语义记忆(规则)层不新增更新机制 | 规则层已有 ConflictResolver(trust 裁决 + MarkSemanticObsolete)与人工裁决;时间维度属 episodic(occurred_at 仅 episodic 有) | 给语义记忆加时间更新机制(超出 ticket 范围,与既有冲突机制并列) |
| 11 | 自动检测的候选范围 = pattern separation 同一 top-5 相似池 | 被覆盖的旧记忆必然是"最相似"的记忆之一(同主题 → 高 cosine);与既有合并判定共用同一池,零额外检索成本;池外场景由显式信号 `signal_update`(最近 500 条)覆盖 | 扩大候选池(改变 pattern separation 既有合并范围,行为风险) |

## 已知权衡

- **同义复述风险**:不同措辞陈述同一事实(Dice 0.25–0.9、无极性翻转)会被判为更新
  并废弃旧条目,可能丢失旧条目独有的补充细节。取舍方向是 ticket 明示的"新旧记忆
  不再并存竞争";同主题高相似(≥0.85 cosine)的复述实际会被 pattern separation
  提前合并,不会到达更新判定。评测(ticket 06)将验证该权衡的方向性。
- **新记忆权重钳制**:新写记忆权重=1.0,×1.5 提升被 ClampWeight 钳制不可见;提升
  的实际效果作用于衰减中的承载记忆(如合并路径 sim+0.1 的权重、显式信号下已衰减
  的承载),保证其胜过被废弃者——单元测试在衰减承载上断言提升精确生效(0.2→0.3)。
- **跨信号权重复乘**:同一承载记忆在不同时刻分别覆盖不同旧记忆时,每次显式信号
  都会在**当前**权重上再乘 1.5(1.5² 等)。每次信号都是独立的新覆盖事件,语义上
  是"承载记忆被反复确认为权威";同一信号内的多个 pair 只提升一次。重复发送
  **同一**信号不提升(已废弃候选跳过,幂等测试覆盖)。

## 范围外(留待后续)

- 语义记忆层的时间感知更新(规则/约束由 trust 裁决,见决策 10)
- 检索端对 SUPERSEDED 边的消费(如"被废弃前的历史状态"查询;ticket 04 RRF 之后评估)
- 更新判定的 LLM 增强(现为确定性启发式;评测若有证据再引入)
- 评测验证(06 收口):HaluMem Dynamic Update 28.9% → ≥50% 测量
