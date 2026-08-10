# VatBrain v0.4 草案 — 评测驱动精修 + 价值证明

> 状态：**草案（待确认决策点后定稿）**
>
> 日期：2026-08-10
>
> 前置阅读：
> - `docs/EVOLUTION_PLAN.md` — 产品主线（Observe → … → Measure）
> - `docs/v0.3/04-omnimemeval-benchmark-results.md` — User Memory 轨评测结果
> - `docs/v0.3/09-agent-benchmark-results.md` — Agent Memory 轨评测结果（null result）
> - `docs/v0.3/06-v3-iteration-plan.md` — 评测驱动的迭代计划
> - `docs/ROADMAP.md` / `docs/v0.1.1/00-storage-refactor-draft.md` — 路线与 SQLite-only 战略

---

## 1. 背景：三份输入的综合

### 1.1 评测结论（User Memory 轨，LME 74.2% / HaluMem 65.0% / LoCoMo 57.0%）

**强项（第一梯队）**——证明"遗忘是默认"哲学有效，应保持：

| 能力 | 分数 | 说明 |
|---|---:|---|
| 记忆边界 | 96.3% | 诚实识别"记忆里没有"，不捏造（MemOS 91.3） |
| 单 session 记忆 | 97–100% | "这条对话说过什么"接近满分 |
| 跨 session 用户画像 | 72–97% | |

**弱项（明确短板）**——评测直接点名的内核缺陷：

| 短板 | 分数 | 根因 |
|---|---:|---|
| 时序推理 | LoCoMo **15.0%** / LME 52.6% | `chat_time` 未入记忆（D7），answer 看不到事件时间 |
| 动态更新 | HaluMem **28.9%** | 新信息覆盖旧信息时无显式废弃/合并 |
| 事实级精确召回 | Basic Fact **43.6%** | 问题→事实的余弦检索不够精准（池修复 500→5000 已从 10% 回升） |
| 上下文效率 | 10K+ tokens/题 | top-k 噪声多；MemOS 赢在低上下文高分 |

### 1.2 Agent Memory 轨结论（null result）

- baseline 21.00% = vatbrain 21.00%，26 个逐题翻转净零 → **null result**
- 记忆**全链路验证生效**（729 条入库、1209 条注入、provider 每会话 spawn）——失败的是"域 × 协议"，不是记忆系统
- 根因：注入的是**其他题的完整 prompt + verifier 答案**，对独立求解的数学题无可迁移价值
- 教训：10 样本假阳性（10%→20% 未复现）；小样本不可信，必须重复试验 + stderr 对齐
- 建议：换迁移信号存在的域/协议；检索侧**压制任务 prompt、优先 feedback/解法类记忆**；固定工具配置

### 1.3 遗留 feature 盘点（issue #1 剩余 12 项，对照评测证据）

| 遗留项 | 评测证据 | 判断 |
|---|---|---|
| 性能基准测试套件 | 评测套件已落地，缺延迟微基准 | **补全**（写入/检索/整合延迟，ROADMAP 指标） |
| 反事实推理（§8.2） | 评测未点名；依赖"连续 N 次纠正"数据，Agent 轨二轮可积累 | P1 尾 / P2，看数据 |
| 压缩残差（§9.3） | 评测未点名；LoCoMo 弱在时序非摘要精度 | **P2 暂缓** |
| 自适应衰减（§6.2） | 评测未点名；动态更新更接近 Update Tracking | **P2 暂缓** |
| 多级存储 L1（MinIO 快照） | SQLite-only 战略下 MinIO 过时；快照 = SQLite 文件备份 | **降级为低成本备份项** |
| 冷存储物理分层 | SQLite 单库下价值低，`IncludeDormant` 门控已够 | **废弃** |
| 情境向量 / Sensory Buffer | 研究向 | **P2 暂缓** |
| v1.0 Team Memory / v1.x | EVOLUTION_PLAN 明确"单人价值验证前延后" | **不入** |
| RetrievalEngine 内部检索 | v0.1.1 草案目标，工程整洁债 | P2 顺手做 |
| FallbackStore | 生产降级 | **P2 暂缓** |

### 1.4 主线闭合度

```
Observe ✅ → Distill ✅ → Inject ✅ → Capture ✅ → Reconsolidate ✅ → Measure ⚠️
```

主线的每一步能力都已存在，但 **Measure 未闭环**：User Memory 轨证明"内核有强项有短板"；Agent 轨证明"当前协议下 Pitfall 价值 null"。**v0.4 存在的意义 = 闭合 Measure：修短板 + 找到能证明价值的协议。**

---

## 2. 定位与目标

> **v0.4 — 评测驱动精修 + 价值证明**
>
> 把评测暴露的三个内核短板（时序 / 动态更新 / 精确召回）修到可量化改善；
> 用"迁移信号存在"的协议证明 Pitfall 机制在真实 Agent 工作负载上的价值；
> 建立可重复的 benchmark 回归流程，让每轮改动可验证。

三个目标对应三个交付组：

1. **内核精修**（P0）：时序记忆、动态更新跟踪、检索增强 —— 分数可量化提升
2. **Measure 基建**（P0）：并发 ingestion、judge 口径对齐、延迟微基准 —— 让迭代快、可比、可回归
3. **价值证明**（P1）：Agent 轨第二轮（换域/协议 + 注入侧改造）—— 出 trend 或证伪

---

## 3. 范围对照表（证据 → 决策）

| 项 | 来源 | 证据强度 | 决策 |
|---|---|---|---|
| 时序记忆深入版 | 评测双短板 | 强（15% / 52.6%，根因已知） | **P0** |
| 并发 ingestion | v3.0 计划 P0 + 并发调研已完成 | 强（30–60×，降所有迭代成本） | **P0** |
| Judge 口径对齐重跑 | v3.0 计划 P0 | 强（分数不可比即无基线） | **P0** |
| Update Tracking | HaluMem 28.9% | 强（ConflictResolver / reconsolidation 可复用） | **P0** |
| Retrieval 增强（query expansion + RRF） | Basic Fact 43.6% | 中强（池修复已受益，再进一步） | **P0** |
| 延迟微基准 | issue #1 技术债 | 中（评测套件已就位） | **P0** |
| Agent 轨二轮（换域/协议 + 注入压制） | null result 结论 | 强（价值证明是主线缺口） | **P1** |
| Context 精简 | 效率轴（MemOS 低上下文高分） | 中（与 RRF 协同） | **P1** |
| DX 收尾（README 引真实数据等） | EVOLUTION_PLAN v0.4 剩余 | 中（大部分已做） | **P1** |
| 反事实推理 | ROADMAP §8.2 | 弱（无数据支撑） | **P2** |
| 压缩残差 / 自适应衰减 / 情境向量 / FallbackStore | 遗留 | 弱（评测未点名） | **P2** |
| 多级存储 L1 | 遗留 | 弱（SQLite-only 后过时） | 降级为备份 |
| 冷存储物理分层 | 遗留 | 弱 | **废弃** |
| Team Memory / 联邦 / 多租户 | EVOLUTION_PLAN | — | **不入** |

---

## 4. P0 — 内核精修 + Measure 基建

### P0-1 时序记忆（Temporal Memory，深入版）

- **现状**：快速修复已落地（bench 写入把 `[日期]` 前缀进摘要）；answer 能看到时间但无法结构化利用
- **深入版**：
  1. `chat_time`/`occurred_at` 建模为记忆属性（episodic 新列 + 迁移）
  2. 检索支持时间过滤/排序（"上周"、"最近一次"相对时间解析）
  3. 日期前缀保留为兼容层
- **预期**：LoCoMo Temporal 15% → **≥40%**；LME Temporal 52.6% → **≥65%**
- **风险**：`WriteMemory` 签名扩展波及 provider/watcher 全链路 → 增量演进，不一次性大改

### P0-2 并发 ingestion

- **现状**：bench `handleAdd` 顺序 embedding（LME 13560 次写入 ≈ 2.6h）
- **方案**：embedding 并发 32–64 worker（智谱 V1 实测 64 并发全过）+ 批量 64 文本/请求；**并发 embedding、顺序写库**（SQLite 单写者）
- **预期**：ingestion 30–60× 提速（~10 分钟），所有后续重跑成本大降
- 参考：`docs/v0.3/05-agent-memory-handoff.md` 并发调研

### P0-3 Judge 口径对齐重跑

- **现状**：`LLM_DISABLE_THINKING=1` 已实现（`maybe_disable_thinking()`）；本轮分数是 thinking-on 口径，与官方 gpt-4o-mini 不可比
- **方案**：重跑 LME / HaluMem / LoCoMo 三 benchmark，出 **v0.4 可比基线**
- **注意**：对齐后绝对分可能变化（严苛度不同）→ 以重跑基线为参照，目标定"相对提升"

### P0-4 Update Tracking（动态更新）

- **现状**：HaluMem Dynamic Update 28.9%；ConflictResolver（极性+bigram）与 reconsolidation 已就绪
- **方案**：同 entity 新信息（时间上更新）→ 旧记忆显式标记 obsolete / 新记忆权重提升；复用 ConflictResolver 判定语义冲突、reconsolidation 做合并
- **预期**：Dynamic Update 28.9% → **≥50%**
- **接口**：MCP/API 暴露更新信号（或自动检测）

### P0-5 Retrieval 增强（事实级精确召回）

- **现状**：Basic Fact 43.6%（候选池 5000 修复后）；双通道 embedder（关键词 CJK-safe + 语义）已存在
- **方案**：
  1. **Query expansion**：问题 → 关键词/实体扩展（复用关键词通道）
  2. **RRF**：lexical（关键词/权重）+ semantic（embedding）融合排序
  3. 顺带服务 Context 精简（更准的 top-k → 更少噪声）
- **预期**：Basic Fact 43.6% → **≥60%**；Multi-hop 同步受益

### P0-6 延迟微基准（性能基准测试套件补全）

- **现状**：评测套件已落地（OmniMemEval），但"写入/检索/整合延迟"从未验证（issue #1 遗留）
- **方案**：`cmd/vatbrain-bench` 或独立基准：写入 p95、检索 p95（命中/miss）、整合耗时（增量），本地 SQLite
- **目标**：对齐 ROADMAP 跨版本指标（写入 <200ms、检索命中 <100ms / miss <500ms、整合 <15min 增量）

---

## 5. P1 — 价值证明 + 产品化收尾

### P1-1 Agent Memory 轨第二轮（核心）

- **协议改造（注入侧）**：压制"任务 prompt"类记忆注入，优先 feedback / 解法 / 修正类（09 报告建议）
- **域选择（迁移信号）**：三选一（**待用户决策**）：
  - A. **代码修补场景**：同实体反复修改/出错（SWE-Bench 类，本机无 docker → 构造轻量修补任务集）
  - B. **同题不同 seed**：OmniMath 改 seed 重出题，解法可复用
  - C. **重复模式流水线**：重复模式的代码/数据任务，上一题经验可直接复用
- **方法**：小样本（20–30 题）先行出 trend；每配置重复 2–3 次试验区分信号/噪声（09 教训）
- **成功定义**：baseline vs vatbrain 差异 ≥ 2×stderr 且方向稳定
- **成本控制**：MiniMax-M3 首 token ~50s → 小样本 + `--parallel 2-4`

### P1-2 Context 精简（效率轴）

- 去重 / 压缩 / 更准 top-k（与 P0-5 RRF 协同）
- **预期**：context 减半以上，效率轴竞争力（MemOS 模式）

### P1-3 DX 收尾（EVOLUTION_PLAN v0.4 剩余）

- README 引用真实 benchmark 数据（三 benchmark 分 + 定位语）
- good first issue 清单（新 adapter / 新 scenario / 新 Pitfall category / README 验证）
- Before/After 案例数据化（同错误第二次不再犯的实测数字）

### P1-4 反事实推理（视数据）

- 依赖：连续 N 次纠正数据（Agent 轨二轮 + 真实使用积累）
- 协同：Update Tracking 的反馈路径
- **若数据不足则留 P2**

---

## 6. 暂缓 / 不入

| 项 | 原因 |
|---|---|
| 压缩残差（§9.3） | 评测未点名；LoCoMo 弱在时序非摘要精度 |
| 自适应衰减（§6.2） | 评测未点名；动态更新走 Update Tracking 更直接 |
| 多级存储 L1（MinIO 管线） | SQLite-only 战略下降级为 SQLite 文件快照备份（低成本项） |
| 冷存储物理分层 | SQLite 单库下价值低 |
| 情境向量 / Sensory Buffer | 研究向 |
| Team Memory / 联邦 / 多租户 | 单人价值未证明前放大治理复杂度（EVOLUTION_PLAN 明确延后） |
| FallbackStore / RetrievalEngine 内部检索 | 生产降级与工程整洁债，P2 顺手做 |
| Milvus / 托管云 / 复杂 UI / 泛用 RAG | EVOLUTION_PLAN 暂缓清单 |

---

## 7. 准出标准（可量化）

| 维度 | 当前 | 目标（judge 对齐口径重跑后为基线） |
|---|---:|---:|
| LoCoMo Temporal | 15.0% | ≥ 40% |
| LME Temporal | 52.6% | ≥ 65% |
| HaluMem Dynamic Update | 28.9% | ≥ 50% |
| Basic Fact | 43.6% | ≥ 60% |
| 三 benchmark 总分 | 74.2 / 65.0 / 57.0 | 以对齐口径重跑基线为准，各 +8pp 以上 |
| Agent 轨 | null（0.0pp） | ≥ 1 个协议/域出现显著正 trend（≥2×stderr，重复稳定） |
| 写入延迟 p95 | 未测 | < 200ms（本地 SQLite） |
| 检索延迟 p95 | 未测 | 命中 < 100ms / miss < 500ms |
| 回归 | — | 每轮提交 `go test ./internal/...` 全绿 + 10 样本 smoke 无回退 |

---

## 8. 验收节奏（沿用 v3.0 计划节奏，四轮）

| 轮次 | 内容 | 产出 |
|---|---|---|
| 1 | P0-2/3/6（并发、judge 对齐、延迟基准） | 三 benchmark 重跑 **v0.4 可比基线** |
| 2 | P0-1/4/5（时序、更新跟踪、检索增强） | 三 benchmark 二轮重跑，对照基线出提升 |
| 3 | P1-1（Agent 轨二轮：换域 + 注入压制） | trend 结论（正/负/零均记录） |
| 4 | P1-2/3/4（Context 精简、DX 数据化、反事实） | v0.4 发布物 |

每轮结束发布一份结果文档（延续 `docs/v0.3/0X` 编号风格），下一轮基于真实数据调整范围——**范围随证据移动，不固守本文**。

---

## 9. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 换域仍 null（Agent 轨） | 注入侧改造先行（压制 prompt、优先 feedback）；域选择聚焦"经验可复用"；null 也是结论（协议不匹配 → 继续换协议，不投入更多域） |
| 时序签名扩展波及全链路 | 增量演进：先只加列 + 检索排序，不动 provider 协议；兼容层（日期前缀）保留 |
| Judge 对齐后分数下降 | 以对齐口径重跑为基线定目标，不与旧口径混比 |
| SQLite 写锁限速 | 并发 embedding + 顺序写库（已验证方案） |
| 评测成本（时间/费用） | 并发 ingestion 降 ingestion 成本；全量重跑限关键节点；日常用 10 样本 smoke 回归 |
| 小样本假阳性（09 教训） | 所有 trend 必须重复 2–3 次 + stderr 对齐才下结论 |

---

## 10. 关键决策记录（技术决策）

| # | 决策 | 选择 | 理由 |
|---|---|---|---|
| D1 | v0.4 定位 | 评测驱动精修 + 价值证明（非纯 DX 版本） | Measure 主线未闭环；DX 大头已随 v0.4 文档批完成 |
| D2 | 优先修哪个短板 | 时序 > 动态更新 > 精确召回 | 双 benchmark 证据（15% / 52.6%）；根因明确、改动局部 |
| D3 | 并发模型 | embedding 并发、写库顺序 | SQLite 单写者约束；智谱 V1 实测 64 并发无 429 |
| D4 | Agent 轨二轮规模 | 小样本 trend 优先（20–30 题 × 2-3 次） | 09 假阳性教训；全量 9h 成本高 |
| D5 | 遗留存储项 | 多级存储降级为文件备份；冷分层废弃 | SQLite-only 战略下物理分层无意义 |
| D6 | 评测基线 | 先 judge 对齐重跑定新基线 | thinking 口径与官方不可比，旧分无参照意义 |

---

## 11. 待确认决策点（定稿前需用户拍板）

1. **定位确认**：v0.4 = 评测驱动精修 + 价值证明？（替代 EVOLUTION_PLAN 原"纯 Public DX"定位——DX 大头已做完）
2. **Agent 轨二轮域选择**：A 代码修补场景（需构造任务集）/ B 同题不同 seed / C 重复模式流水线？可多选，成本依次递减
3. **时序深入版范围**：是否动 `WriteMemory` 签名/模型列（波及 provider/watcher），还是先只做"属性列 + 检索排序"的最小深入？
4. **反事实推理**：进 P1（本轮）还是 P2（等数据）？
5. **准出目标**：表 7 的目标幅度（+8pp 等）是否合理，还是保守/激进调整？

---

*草案版本。定稿后移入 `docs/v0.4/00-design.md`，按 P0 → P1 顺序实施。*
