# 05 — Judge 口径对齐重跑 → v0.4 可比基线

**What to build:** 在 ingestion 提速后,用已实现的 `LLM_DISABLE_THINKING=1` 口径重跑 LongMemEval / HaluMem / LoCoMo 三个 User Memory benchmark,产出与官方 gpt-4o-mini 口径可比的 **v0.4 基线分**(旧分是 thinking-on 口径,不可比),并落一份基线结果文档,供后续所有内核改动对照。

**Blocked by:** 01 — bench 基建:并发 ingestion + 延迟微基准

**Status:** ready-for-agent

- [ ] 三 benchmark 在 judge 对齐口径下重跑完成(ingestion 用并发路径)
- [ ] 基线文档:分项分数(LME 六类 / HaluMem 六类 / LoCoMo 四类)、与旧口径差异说明、复现命令
- [ ] 基线分成为后续轮次的参照基准(00-draft §7 准出标准以它为准)
