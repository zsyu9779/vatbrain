# 07 — Agent 轨二轮:同题不同 seed + 注入压制

**What to build:** Agent Memory 轨第二轮:协议从"注入其他题的完整 prompt"(null result 根因)改为——(a) 注入侧压制任务 prompt 类记忆、优先 feedback/解法/修正类;(b) 用同题不同 seed 重出题(解法可复用),让记忆有真实迁移信号。小样本(20–30 题)× 2–3 次重复,出 trend 结论(差异 ≥2×stderr 且方向稳定才算数,单次小样本差异一律视为噪声)。

**Blocked by:** 05 — Judge 口径对齐重跑 → v0.4 可比基线

**Status:** ready-for-agent

- [ ] 注入侧改造:压制任务 prompt 类记忆,优先 feedback/解法类(09 报告建议)
- [ ] seed 变体协议:OmniMath 改 seed 重出题,数据准备 + 评测命令
- [ ] 小样本 × 2–3 次重复试验完成,baseline vs vatbrain 出 trend 结论(正/负/零均记录)
- [ ] 结果文档:方法、数字、结论、与 09 null result 的对照
