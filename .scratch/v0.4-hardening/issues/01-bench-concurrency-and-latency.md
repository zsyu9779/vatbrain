# 01 — bench 基建:并发 ingestion + 延迟微基准

**What to build:** 评测入口(vatbrain-bench)的写入不再逐条顺序调 embedding——多条消息并行 embedding(32–64 worker,批量 64 文本/请求),再顺序写库,使大规模记忆导入(LME 13560 次写入)从 ~2.6h 缩短到 ~10 分钟量级;同时暴露写入/检索/整合的 p95 延迟指标,让 ROADMAP 跨版本性能里程碑(写入 <200ms、检索命中 <100ms / miss <500ms)首次可验证。

**Blocked by:** None — can start immediately

**Status:** ready-for-agent

- [ ] 写入路径并发 embedding:worker 池(32–64,可配置),批量 64 文本/请求,429/1302 重试;SQLite 写库保持顺序
- [ ] 延迟微基准:写入 p95、检索 p95(命中/miss)、整合耗时,输出可复现的基准命令与结果记录
- [ ] 基准/回归:相关单测覆盖并发与批量逻辑;文档记录基准方法与首次实测数字

参考:`docs/v0.3/05-agent-memory-handoff.md`【并发调研】(智谱 V1 ≥100 并发、1302/1305/429 重试、批量 API 5 折价)。
