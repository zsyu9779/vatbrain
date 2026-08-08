# VatBrain Evolution Plan

> 调研日期：2026-05-19
>
> 目的：基于 AI Agent memory 赛道调研，重新定义 VatBrain 下一阶段的演进重点。

---

## 1. 结论

VatBrain 应继续做，但不要继续扩成泛用 memory SDK。

更清晰的下一阶段定位是：

> **Pitfall-aware memory layer for coding agents.**
>
> 面向 Codex、Claude Code、Cursor、OpenCode 等 coding agent，记住项目决策、失败经验、用户纠正和代码实体相关的风险，并在下一次修改前主动提醒。

这个定位比“AI Agent 的通用长期记忆系统”更窄，但更锋利：

- 避开 Mem0、Letta、Zep/Graphiti、Cognee 在通用 agent memory 上的正面竞争。
- 利用 VatBrain 已经实现的 Pitfall、Decay、Reconsolidation、Attribution、Watcher 能力。
- 把价值落到 coding agent 最痛的问题：重复犯错、跨工具失忆、每次重新解释项目背景。

---

## 2. 竞品信号

### 2.1 泛用 memory layer 已经拥挤

| 项目 | 方向 | 对 VatBrain 的启发 |
|------|------|------------------|
| [Mem0](https://docs.mem0.ai/) | 托管/开源 memory layer，向 hybrid search、entity linking 演进 | 泛用“记忆层”叙事已经被强项目占位；VatBrain 不应主打同一入口 |
| [Zep Graphiti](https://help.getzep.com/graphiti/getting-started/overview) | temporal knowledge graph for agents | VatBrain 的图+时间能力不是唯一卖点，必须靠 coding/pitfall 场景差异化 |
| [Letta](https://docs.letta.com/guides/agents/memory) | stateful agent runtime，agent 自主管理 core/archive memory | 如果做 agent runtime 会变大；VatBrain 更适合作为外部记忆基础设施 |
| [Cognee MCP](https://docs.cognee.ai/cognee-mcp/mcp-overview) | MCP 持久记忆、代码知识、跨工具接入 | MCP + coding workflow 已经是主战场，VatBrain 需要更明确的独特工具链 |
| [LangChain Deep Agents Memory](https://docs.langchain.com/oss/python/deepagents/memory) | 文件型长期记忆、背景 consolidation、agent/user scope | 文件型记忆会成为低门槛默认方案；VatBrain 需要证明结构化记忆值得额外成本 |

### 2.2 Coding-agent memory 子赛道正在快速出现

| 项目 | 已覆盖能力 | VatBrain 需要避开的重复 |
|------|-----------|------------------------|
| [memd](https://memd.dev/) | MCP、决策/错误/约束/任务/checkpoint、hybrid search、TTL cleanup | 不要只做“结构化 CRUD + 搜索” |
| [memtrace](https://www.memtrace.sh/) | 本地优先、跨 agent、自动导入 Claude/Cursor/git、file-aware context、指数衰减 | 不要只做“本地跨工具记忆 + decay” |
| memctl / agentmemory / OpenMemory 类工具 | 云或本地 MCP 记忆、跨会话恢复、上下文类型 | 不要把“AI 不记得”作为唯一卖点 |

### 2.3 研究趋势开始靠近 VatBrain 的核心

2026 年已有针对 coding agent 的 feedback-normalized developer memory 研究，强调 MCP、本地、反馈、审计、决策日志和安全门控。这说明 VatBrain 的“反馈驱动记忆”方向是对的，但也说明必须尽快形成可运行、可评测的工程闭环。

---

## 3. 新产品主线

下一阶段只围绕一条主线推进：

```
Observe → Distill Pitfall → Inject Before Edit → Capture Feedback → Reconsolidate → Measure
```

对应到产品语言：

1. **Observe**：Watcher 被动吸收 Claude Code / Cursor / OpenCode / Codex 的原生记忆、规则、会话摘要、错误修复记录。
2. **Distill Pitfall**：睡眠整合把 debug / correction / failed test 经验提炼成 PitfallMemory。
3. **Inject Before Edit**：Agent 修改文件前，根据 active files、entity_id、git churn、历史 Pitfall 密度注入最多 3 条高置信风险提醒。
4. **Capture Feedback**：用户纠正、测试失败、回滚、重新修改同一实体，全部作为行为归因信号。
5. **Reconsolidate**：纠正信号沿 DERIVED_FROM 回传，更新 Pitfall、Semantic、Episodic 的权重与保护级别。
6. **Measure**：用重复错误减少率、命中精度、干扰率证明系统有用。

这条主线要比“支持更多 memory 类型”优先级更高。

---

## 4. 版本重排建议

### v0.2.1 — Watcher GA

**目标**：让 VatBrain 不依赖 agent 主动调用 `write_memory`，而是能从真实开发工具旁路吸收记忆。

#### 必做

- 完成 Claude Code adapter 的真实路径验证和 README 配置说明。
- 将 Cursor adapter 从复用导入逻辑推进到可重复同步，至少保证去重和增量扫描可靠。
- OpenCode adapter 从骨架升级到可用；若短期调研不到稳定格式，则先转为 Custom adapter 示例。
- 新增 Watcher e2e demo：
  - 从一条 agent 原生记忆开始。
  - 经过 refinement 写入 EpisodicMemory。
  - 经 consolidation 生成 Pitfall。
  - 下一次 MCP 检索可注入该 Pitfall。
- `list_adapters` 输出要包含 last_scan_at、new_count、skipped_count、last_error、provider health。

#### 准出标准

- 至少 2 个真实 agent/source 可同步：Claude Code + Cursor 或 Claude Code + Custom。
- 无 LLM API key 时可使用 heuristic refinement 跑完整链路。
- demo 可在 5 分钟内复现。

---

### v0.2.2 — Pitfall Workbench

**目标**：把 Pitfall 从后端数据模型变成用户能看见、能修正、能相信的工作台。

#### 必做

- MCP 工具：
  - `list_pitfalls`
  - `explain_pitfall`
  - `confirm_pitfall`
  - `suppress_pitfall`
  - `link_pitfall_entity`
- CLI 或 HTTP debug endpoint：
  - 按 project/entity/file 查看 Pitfall。
  - 查看每条 Pitfall 的来源 episodic memories。
  - 查看为什么它会被注入。
- Pitfall 状态机：
  - `proposed`：LLM/heuristic 提取，尚未确认。
  - `confirmed`：用户确认或多次命中后自动提升。
  - `suppressed`：用户认为无效，不再主动注入。
  - `obsolete`：对应实体已重构或修复，默认降权。
- 引入“干扰率”指标：主动注入后用户未采纳或 suppress 的比例。

#### 准出标准

- 用户可以审查和纠正 VatBrain 记住的错误经验。
- 每条 Pitfall 都有可追溯来源，不出现“系统说有风险但不知道为什么”。

---

### v0.3 — Proactive Risk Injection

**目标**：把原 ROADMAP 中的“预测与主动”收窄为 coding agent 修改前风险注入。

#### 必做

- 新增风险评分输入：
  - active files / target files
  - entity_id / entity_group
  - historical Pitfall density
  - recent git churn
  - last corrected time
  - trust_level / source_type
- 新增 MCP 工具：
  - `prepare_edit_context`
  - 输入：files、task_type、language、optional user goal。
  - 输出：relevant memories + top pitfalls + risk score + reason codes。
- 主动注入规则：
  - 默认最多 3 条 Pitfall。
  - 只注入 confirmed 或高置信 proposed。
  - 每条必须附带 entity、signature、fix_strategy、source confidence。
- 反馈闭环：
  - 如果 agent 采纳建议并测试通过，Pitfall 权重增加。
  - 如果用户 suppress 或 agent 未使用，降低主动注入权重。
  - 如果仍然发生相同错误，提升该 Pitfall 的保护级别。

#### 准出标准

- 在至少 20 个手工构造的 coding scenarios 中，重复错误减少率可被测量。
- 主动注入干扰率低于 30%。
- `prepare_edit_context` p95 延迟低于 300ms（本地 SQLite 后端）或 500ms（Neo4j+pgvector 后端）。

---

### v0.3.1 — Evaluation Harness

**目标**：用评测证明 VatBrain 的独特价值，避免只靠概念叙事。

#### 必做

- 构造 `tests/scenarios/`：
  - config pitfall
  - database migration pitfall
  - test fixture pitfall
  - API contract pitfall
  - concurrency/resource pitfall
- 每个 scenario 包含：
  - 初始代码状态。
  - 历史 debug/correction memory。
  - agent 修改任务。
  - 期望注入的 Pitfall。
  - 验证脚本。
- 对比三组：
  - baseline：无记忆。
  - generic memory：只做语义检索。
  - VatBrain：Pitfall + contextual gating + reconsolidation。
- 输出指标：
  - repeated mistake rate
  - useful injection rate
  - false injection rate
  - task completion time
  - token overhead

#### 准出标准

- 至少 20 个 scenario。
- 有一份可公开的 benchmark 报告。
- README 可以引用真实数据，而不是只讲“类脑”概念。

---

### v0.4 — Public Developer Experience

**目标**：从“研究型后端”变成“外部开发者愿意试的工具”。

#### 必做

- README 首屏改写：
  - 主标语从 generic agent memory 改为 coding agent pitfall-aware memory。
  - 首屏展示一个 Before/After：同一个错误第二次不再犯。
- Quick Start 分为两条：
  - SQLite local-first：最短路径，无 Neo4j/pgvector。
  - Full backend：Neo4j + pgvector + Redis + MinIO。
- 增加 `docs/COMPETITIVE_LANDSCAPE.md`：
  - 明确与 Mem0、Graphiti、Cognee、Letta、memd、memtrace 的边界。
- 增加 `docs/DEMO_SCRIPT.md`：
  - 5 分钟复现 Watcher → Pitfall → Risk Injection。
- 增加 `good first issue` 清单：
  - 新 adapter。
  - 新 scenario。
  - 新 Pitfall category。
  - README/Quick Start 验证。

#### 准出标准

- 一个外部用户能在 10 分钟内跑通 SQLite demo。
- 至少 5 个非作者用户反馈。
- GitHub README 首屏不再像“又一个 memory SDK”。

---

### v1.0 — Team Memory

**目标**：在单人 coding agent 价值被验证后，再进入团队共享记忆。

#### 延后原因

多 agent / 多租户很诱人，但目前不是最急。若单人场景还没有证明“能减少重复错误”，团队共享只会放大噪声和治理问题。

#### 必做

- project/team/agent 三级作用域。
- Pitfall review queue。
- shared confirmed pitfalls。
- agent-private episodic memory。
- 冲突规则治理。
- 权限与脱敏。

---

## 5. 暂缓事项

以下事项不建议在 v0.4 前投入主力：

| 暂缓项 | 原因 |
|--------|------|
| 泛用用户画像 memory | Mem0/Letta 已经很强，且不服务 coding pitfall 主线 |
| 大规模文档摄取/RAG | Cognee/Graphiti/传统 RAG 已覆盖，容易稀释定位 |
| Milvus 迁移 | 当前差异化不在百万级向量性能 |
| 多智能体记忆共享 | 单人闭环未验证前会放大治理复杂度 |
| 托管云服务 | 先证明 local-first demo 和开源价值 |
| 复杂 UI | 先用 MCP/CLI 暴露可解释能力，避免前端分散精力 |

---

## 6. 30/60/90 天计划

### 30 天：证明旁路同步可用

- 完成 v0.2.1 Watcher GA。
- 做出第一个 demo：Claude Code/Cursor 记忆 → VatBrain → Pitfall → MCP 检索。
- 写 `docs/DEMO_SCRIPT.md`。
- 更新 README 定位语。

### 60 天：证明 Pitfall 可被信任

- 完成 v0.2.2 Pitfall Workbench。
- 每条 Pitfall 可解释来源、权重、注入原因。
- 支持 confirm/suppress。
- 积累 10-20 条真实项目 Pitfall。

### 90 天：证明能减少重复错误

- 完成 v0.3 Risk Injection 的最小实现。
- 完成 v0.3.1 Evaluation Harness 初版。
- 发布一份 benchmark/案例文章：
  - 不横评所有竞品。
  - 只证明 VatBrain 对 repeated coding mistakes 的改善。

---

## 7. 成功指标

### 产品指标

| 指标 | 目标 |
|------|------|
| 首次跑通 SQLite demo | < 10 分钟 |
| Watcher 同步后可检索 | < 1 分钟 |
| `prepare_edit_context` p95 | SQLite < 300ms；Full backend < 500ms |
| 主动注入条数 | 默认 ≤ 3 |

### 质量指标

| 指标 | 目标 |
|------|------|
| Pitfall useful injection rate | > 60% |
| Pitfall false injection rate | < 30% |
| repeated mistake rate | 相比 baseline 降低 > 25% |
| suppress 后再次注入率 | 0% |

### 社区指标

| 指标 | 目标 |
|------|------|
| 外部用户跑通 demo | ≥ 5 |
| good first issue | ≥ 5 个 |
| 真实 adapter 贡献 | ≥ 1 个 |

---

## 8. README 新定位草案

### 英文

> VatBrain is a pitfall-aware memory layer for coding agents.
>
> It remembers project decisions, failed fixes, user corrections, and repo-specific risks across Codex, Claude Code, Cursor, and OpenCode, then injects the right warning before your agent edits the same code again.

### 中文

> VatBrain 是面向 coding agent 的错误经验记忆层。
>
> 它跨 Codex、Claude Code、Cursor、OpenCode 记住项目决策、失败修复、用户纠正和代码实体风险，并在 Agent 再次修改相关代码前主动注入避坑提醒。

---

## 9. 下一步执行清单

1. 完成 v0.2.1 Watcher GA，不再扩大 v0.3 范围。
2. 新建 `docs/DEMO_SCRIPT.md`，围绕 Pitfall demo 写。
3. 新建 `docs/COMPETITIVE_LANDSCAPE.md`，把差异化讲清楚。
4. 调整 README 首屏定位。
5. 设计 `prepare_edit_context` MCP tool，但先只做 SQLite/local demo。
6. 开始收集 benchmark scenarios，不等系统完全成熟。
