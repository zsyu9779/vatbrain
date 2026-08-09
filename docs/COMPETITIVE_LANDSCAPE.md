# VatBrain 竞品边界

> 依据 `docs/EVOLUTION_PLAN.md` §2 的竞品调研（2026-05-19），明确 VatBrain 与相邻项目的差异化边界。
> 定位：**面向 coding agent 的 pitfall-aware memory layer**，不是泛用 memory SDK。

---

## 1. 一句话定位

> VatBrain 记住 coding agent 的**失败经验、用户纠正、代码实体风险**，并在 Agent **再次修改相关代码前主动注入避坑提醒**。专注「重复犯错」这一个痛点。

## 2. 竞品对照

### 2.1 泛用 memory layer（不做正面竞争）

| 项目 | 方向 | 与 VatBrain 的边界 |
|------|------|--------------------|
| [Mem0](https://docs.mem0.ai/) | 托管/开源通用 memory layer，hybrid search、entity linking | Mem0 做「通用记忆层」入口；VatBrain 不做泛用画像，只做 coding pitfall 主线 |
| [Zep Graphiti](https://help.getzep.com/graphiti/getting-started/overview) | temporal knowledge graph for agents | Graphiti 的图+时间能力不是 VatBrain 独有卖点；VatBrain 靠「错误经验 + 注入」差异化 |
| [Letta](https://docs.letta.com/guides/agents/memory) | stateful agent runtime，自主管理 core/archive memory | Letta 是 agent runtime；VatBrain 是外部记忆基础设施，不接管 agent 生命周期 |
| [Cognee MCP](https://docs.cognee.ai/cognee-mcp/mcp-overview) | MCP 持久记忆 + 代码知识 + 跨工具接入 | 同走 MCP + coding workflow；VatBrain 提供**可评测**的注入闭环（干扰率 <30% 有证据） |
| [LangChain Deep Agents Memory](https://docs.langchain.com/oss/python/deepagents/memory) | 文件型长期记忆 + 背景 consolidation | 文件型是低门槛默认方案；VatBrain 用结构化记忆证明「值得额外成本」（错误记忆独立建模） |

### 2.2 Coding-agent memory 子赛道（避开重复）

| 项目 | 已覆盖能力 | VatBrain 差异 |
|------|-----------|--------------|
| [memd](https://memd.dev/) | MCP、决策/错误/约束/任务/checkpoint、hybrid search、TTL | 不只做「结构化 CRUD + 搜索」——VatBrain 有显著性门控 + 睡眠整合提炼 Pitfall + 主动注入 |
| [memtrace](https://www.memtrace.sh/) | 本地优先、跨 agent、自动导入、file-aware context、指数衰减 | 不只做「本地跨工具记忆 + decay」——VatBrain 有 Pitfall 状态机（可 confirm/suppress）、行为归因、干扰率度量 |
| memctl / agentmemory / OpenMemory 类 | 云/本地 MCP 记忆、跨会话恢复 | 不以「AI 不记得」为唯一卖点——VatBrain 以「主动避免重复错误」为卖点 |

## 3. VatBrain 的独特组合（不易被单点复制）

1. **错误记忆独立建模**（Pitfall ≠ Semantic）：回答「怎么炸的」而非「怎么做」，独立衰减曲线与检索时机（DESIGN_PRINCIPLES §7）。
2. **显著性门控 + 行为归因**：不是每条交互都入库；重要性由后续行为（引用/纠正/确认/忽略）推断，不由当下 LLM 打分。
3. **Pitfall Workbench 状态机**：proposed → confirmed → suppressed，注入有逃生阀，干扰率可度量。
4. **可评测的闭环**：20 个 coding scenarios → 重复错误减少率 57.4%、干扰率 14.8%（确定性可回归）。
5. **零依赖快速体验**：SQLite local-first，`go run` 即可；hermes 全量集成（stdio JSON-RPC provider）。

## 4. 明确的「不做」

| 不做 | 原因 |
|------|------|
| 泛用用户画像 memory | Mem0/Letta 已强，不服务 coding pitfall 主线 |
| 大规模文档摄取/RAG | Cognee/Graphiti/传统 RAG 已覆盖 |
| 百万级向量性能（Milvus 迁移） | 差异化不在向量规模，而在「注入时机与正确性」 |
| 托管云服务 | 先证明 local-first demo 与开源价值 |
| 复杂前端 UI | 先用 MCP/CLI 暴露可解释能力 |

## 5. 一句话销售话术

> 「别人让 Agent 记得更多；VatBrain 让 Agent **少犯一次同样的错**，并且证明给你看。」
