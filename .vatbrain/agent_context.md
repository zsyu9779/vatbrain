# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成完成 + backlog issue #1 完成；**Agent Memory 轨 benchmark 全量跑完 = null result（记忆全链路生效但无跨任务迁移价值）**
- **语言**: Go (go 1.25.5) + Python（hermes 插件 + OmniMemEval AgentBench）
- **分支**: `feature/omnimemeval-benchmark`
- **交接文档**: `docs/v0.3/05-agent-memory-handoff.md`（Agent 轨启动指南）· `docs/v0.3/07-agent-benchmark-plan.md`（规划）· `docs/v0.3/09-agent-benchmark-results.md`（正式结果）
- **战略决策（2026-08-08）**: Neo4j+pgvector 重型存储弃用，SQLite 路径

## 最近工作（2026-08-10）— Agent Memory 轨全量结果 + 生效验证

- **全量跑完**：vatbrain（478 train + 100 test）+ baseline（100 test）。结果文档：`docs/v0.3/09-agent-benchmark-results.md`。
- **核心结果**：test pass@1 **baseline 21.00% = vatbrain 21.00%（对齐 100 题持平）**。26 个逐题翻转（13 错→对 / 13 对→错）净零 → **null result**。vatbrain train pass@1 = 51.88%。10 样本的 10%→20% 提升**未复现，是假阳性**（omni_2292 全量里两边都错）。
- **⚠️ 生效验证（重要教训）**：记忆**全链路确实生效**。最初因检索错字段——查 `~/.hermes/state.db` 的 `content` 列，而记忆注入实际写入 **`api_content` 列**——误判"未生效"。更正后证据：
  - provider 每会话 spawn + initialize（`~/.hermes/logs/agent.log`，1200+ 行 INFO）
  - vatbrain.db 729 条 episodic（531 任务 prompt + 198 verifier feedback），backup/restore 全 0
  - test 窗口 **1209 条 message 的 `api_content` 含 `<memory-context>` 围栏**（648 任务 prompt + 560 feedback 记忆）
- **根因**：域 × 协议不匹配——注入的是**其他题的完整 prompt + 该题 verifier 答案**，对独立求解的数学题**无可迁移价值**（train/test 互不重叠）。"任务记忆 = 完整 prompt（价值低）"隐患在此协议下成为主导。
- **结论**：null result 是关于 benchmark 协议的，不是关于记忆系统的。要验证记忆价值，需换**迁移信号存在的域/协议**（代码修补、同题不同 seed、重复模式流水线），或多次重复试验区分噪声。
- **提交**：`09-agent-benchmark-results.md` + agent_context 已更新并推送到远端 feature 分支。

## 已知问题

- **Agent 轨实跑速度**：MiniMax-M3 首 token 延迟高（~50s），全量 100 test 题跑 ~9h；挂死靠看门狗杀 >240s 进程兜底。
- **reasoning 域 `--train-split` 数值 bug**：`load_tasks` 数字 split 恒加载 test 集 → 用小集用 `--task` 选 id。
- **SWE-Bench 跳过**：本机无 docker，orange 是 aarch64（官方镜像 x86_64）→ 只跑其余 4 域。
- **deriver summary = 完整任务 prompt**（非压缩洞见）→ 记忆价值待 benchmark 暴露（已在全量中验证为低价值，需检索侧压制任务 prompt）。
- **记忆注入字段**：hermes 把 prefetch 注入写进 `api_content`（API 副本），`content` 列不含注入——验证记忆是否注入必须查 `api_content`。
- 插件 `shutdown()` 时后台 sync 线程可能未完成（best-effort，短会话实测能存活）。
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 语义）。
