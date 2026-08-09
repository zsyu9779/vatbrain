# 06 — VatBrain v3.0 迭代计划（基于 OmniMemEval benchmark 暴露的弱项）

> 基准：`docs/v0.3/04-omnimemeval-benchmark-results.md`
> 分数：LongMemEval 74.2% / HaluMem 65.0% / LoCoMo 57.0%

## 弱项 → 迭代项映射

| Benchmark 暴露的弱项 | 分数 | 根因 | v3.0 迭代项 | 优先级 |
|---|---|---|---|---|
| **时序推理** | LoCoMo 15% / LME 52.6% | `chat_time` 未写入记忆（D7），answer 看不到事件时间 | **Temporal Memory**：时间戳进记忆 + 时序检索 | P0 |
| **动态更新** | HaluMem 28.9% | 信息更新后旧状态未显式废弃/合并 | **Update Tracking**：reconsolidation + conflict 增强 | P1 |
| **事实级精确召回** | Basic Fact 43.6% | 问题→事实的 cosine 检索不够精准 | **Retrieval 增强**：query expansion / RRF / rerank | P1 |
| **上下文效率** | context 10K+/题 | top-k 太多噪声 | **Context 精简**：去重 / 压缩 / 更好的 top-k | P2 |
| **judge 口径** | — | deepseek thinking 与官方 gpt-4o-mini 不一致 | **LLM_DISABLE_THINKING=1** 重跑对齐 | P0（先做） |
| **ingestion 太慢** | — | bench 顺序 embedding | **并发 ingestion**（32-64 workers，30-60×） | P0（先做） |

## P0（本轮立即做）

### 1. Temporal Memory（时序记忆）
- **快速修复**：bench 写入时把 `chat_time` 前缀进记忆摘要（`[2029-05-04] content`），让 answer 能看到事件时间 → 已起 subagent 实现（见 `05-agent-memory-handoff.md` 之后的 commit）。
- **深入**：时序检索（按时间过滤/排序、相对时间解析）、把时间戳建模为记忆属性而非仅文本前缀。
- **预期**：LoCoMo Temporal 从 15% 显著回升（LME Temporal 同步受益）。

### 2. 并发 ingestion
- bench `handleAdd` 顺序 embedding → 改并发（32-64 workers，智谱 V1 账号 ≥100 并发）。
- **预期**：ingestion 30-60× 提速（LME 13560 次写入从 ~2.6h → ~10 分钟），大幅降低迭代成本。

### 3. Judge 口径对齐
- `LLM_DISABLE_THINKING=1` 重跑三个 benchmark，与官方 gpt-4o-mini 口径可比。

## P1（v3.0 主迭代）

### 4. Update Tracking（动态更新）
- HaluMem Dynamic Update 28.9%：当新信息覆盖旧信息时，显式标记旧记忆 obsolete / 提升新记忆权重。
- **对齐现有设计**：reconsolidation 已在做相似合并，可增强"时间上更新"的信号；ConflictResolver（P1-5 已完成）可作为冲突判定基础。

### 5. Retrieval 增强（事实级精确召回）
- Basic Fact 43.6%（已从 10% 回升，仍有空间）：
  - Query expansion（问题→关键词/实体扩展）
  - **RRF**（hybrid：embedding + lexical 融合排序）
  - 可选 reranker（轻量 LLM 或 cross-encoder）
- **预期**：Basic Fact / Multi-hop 提升；Context 更精准。

## P2（后续）

### 6. Context 精简（效率轴）
- OmniMemEval 的"效率"指标是 context tokens。MemOS 赢在"低上下文高分"。VatBrain 当前 10K+/题，需去重/压缩/更准的 top-k。
- **预期**：context 减半以上，效率轴竞争力提升。

### 7. Agent Memory 轨（VatBrain 主场）
- Hermes + vatbrain 插件跑 5 域任务（见 `05-agent-memory-handoff.md`）。这才是 VatBrain Pitfall/错误记忆设计的真正验证场。

## 验收节奏

1. **先**：并发 ingestion + 时序快速修复 + judge 对齐 → 重跑三 benchmark 出对比。
2. **再**：Retrieval 增强 + Update Tracking → 二轮重跑。
3. **最后**：Agent Memory 轨（Hermes+vatbrain）。

## 与 ROADMAP 的关系

本计划聚焦 benchmark 驱动的**记忆内核质量**迭代；ROADMAP 中已有的反事实推理（§8.2）、压缩残差（§9.3）、自适应衰减（§6.2）等不冲突，可并行规划。具体排期待用户确认。
