# 08 — Context 精简(效率轴)

**What to build:** 检索注入的上下文从"10K+ tokens/题"减半以上:去重、压缩、更准的 top-k,使 VatBrain 在效率轴(低上下文高分,MemOS 模式)具备竞争力,同时不损失 recall 质量。

**Blocked by:** 04 — Retrieval 增强:RRF 融合排序 + Query Expansion

**Status:** ready-for-agent

- [ ] 去重/压缩/更准 top-k 落地(与 RRF 排序协同,不另起炉灶)
- [ ] 上下文 token 量可测量,减半以上可复现
- [ ] 回归:recall 质量不劣化(以 05 基线为参照,单元测试覆盖去重/压缩正确性)
