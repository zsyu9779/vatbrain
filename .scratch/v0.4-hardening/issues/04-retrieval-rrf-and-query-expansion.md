# 04 — Retrieval 增强:RRF 融合排序 + Query Expansion

**What to build:** 检索不再只靠语义余弦一个通道——问题先做查询扩展(关键词/实体,复用 CJK-safe 关键词通道),再把词法(关键词)与语义(embedding)两个排序结果用 RRF 融合,使精确事实召回(Basic Fact 43.6%)与多跳检索同步提升,并顺带让 top-k 更准、上下文更精简。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] Query expansion:问题 → 关键词/实体扩展,复用双通道 embedder 的关键词通道
- [ ] RRF:词法 + 语义排序融合(参数 K 可调),检索结果质量不劣于纯语义基线
- [ ] 回归:单元测试覆盖扩展、融合排序、与既有 SurpriseBoost 等排序增强的共存;评测验证在 06 收口
