# 10 — Agent 轨二轮:代码修补场景

**What to build:** 在 seed 变体结果确认"迁移信号协议"可行后,构造轻量代码修补任务集(同实体反复修改/出错,SWE-Bench 类但本机无 docker → 自建小集),验证 Pitfall 机制最贴合的定位场景:同一代码错误第二次不再犯。若 07 无趋势,本 ticket 按风险预案收缩或终止。

**Blocked by:** 07 — Agent 轨二轮:同题不同 seed + 注入压制

**Status:** deferred — 真实评测暂缓执行（用户指令 2026-08-10；恢复后跑）

- [ ] 轻量修补任务集构造完成(无 docker 依赖,可本地运行 verifier)
- [ ] baseline vs vatbrain 对照完成,重复试验,出 trend 结论
- [ ] 结果文档:Pitfall 机制在"重复错误减少"上的实证(或证伪)
