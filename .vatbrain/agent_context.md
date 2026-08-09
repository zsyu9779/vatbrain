# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成完成 + backlog issue #1 完成；**Agent Memory 轨 benchmark 已打通 smoke**
- **语言**: Go (go 1.25.5) + Python（hermes 插件 + OmniMemEval AgentBench）
- **分支**: `feature/omnimemeval-benchmark`
- **交接文档**: `docs/v0.3/05-agent-memory-handoff.md`（Agent 轨启动指南）· `docs/v0.3/07-agent-benchmark-plan.md`（本次规划）
- **战略决策（2026-08-08）**: Neo4j+pgvector 重型存储弃用，SQLite 路径

## 最近工作（2026-08-10）— Agent Memory 轨 benchmark：规划 + 打通（本次）

- **目标**：Hermes + vatbrain 插件跑 EvoAgentBench 5 域，`memory_train_backup_test` 协议，对比 baseline（无记忆）vs vatbrain 提升。规划文档：`docs/v0.3/07-agent-benchmark-plan.md`。
- **OmniMemEval 克隆侧新增**（/tmp/OmniMemEval，git 未提交）：
  - `scripts/agentbench/agents/hermes.py` — HermesAgentAdapter（`hermes chat -q ... -Q --yolo --reasoning none`，每任务独立会话；注入 VATBRAIN_PROVIDER_BIN/HERMES_HOME/GATE_MODE/语义 embedding env）
  - `agents/__init__.py` 注册 `"hermes"`
  - `configs/agentbench/agents/hermes.yaml`（gate off + 语义 embedding）+ `hermes-baseline.yaml`（`/tmp/hermes-baseline`，memory.provider 空 → 无记忆 baseline）
  - `configs/agentbench/memory_plugins/vatbrain.yaml`（clear/backup/restore = pkill vatbrain-provider + 操作 ~/.hermes/vatbrain/vatbrain.db）
  - `.env.agent`（DeepSeek v4-flash judge + 智谱 embedding-3 + VATBRAIN_EMBEDDER_SEMANTIC_*）
- **vatbrain 仓库改动**（已提交 `99a3ce3`，feature 分支）：provider daemon 加 `VATBRAIN_GATE_MODE=off`（ForceConfirm，sync_turn 强制 UserConfirmed，对齐 bench GateModeOff）。已重新构建安装到 `~/.hermes/vatbrain/bin/vatbrain-provider`。
- **关键发现**（Agent 轨执行中暴露）：
  1. **provider 从未激活**：插件 `is_available()` 在 `initialize()` 前跑，`_hermes_home` 空 → 解析不到二进制 → 注入 `VATBRAIN_PROVIDER_BIN` 修复。
  2. **SignificanceGate 挡掉所有首次写入**：单会话单任务协议下 4 门控全不满足（"遗忘是默认" 假设长会话跨周期重复）→ gate off 是主测量，**gate-on 在此协议的代价本身就是重要结论**。
  3. 短会话 async sync_turn 写入实测能存活（local SQLite 快），无需改插件。
- **Smoke 结果**（reasoning 域，train 2 题 + test 2 题）：train pass@1=0.50、**test pass@1=1.00**；lifecycle clear/backup/restore 全 0；backup 39.3K；DB 记忆写入 6 条。
- **Baseline 对比**（同 2 道 test 题，`hermes-baseline` home 无记忆）：baseline pass@1=0.50（omni_2080 错/omni_2185 对），vatbrain 1.00（全对）→ **omni_2080 从错到对**。n=2 仅示方向。报告：`docs/v0.3/08-agent-benchmark-smoke-results.md`。
- **验证**：recall 链路直连测试通过（语义 embedding 下 prefetch 返回训练记忆 2476 字符）；hermes 会话不落 jsonl（存 state.db）→ collect_session 返回 0 统计（不影响 verifier 出分）。
- **用户决策（2026-08-10）**：hermes agent 模型**保持 MiniMax-M3**（便宜），不换 deepseek。
- **记忆质量观察**：任务记忆=完整 prompt（价值低）；**feedback 记忆=含 expected answer 的丰富知识（主要跨任务价值）**。

## 最近工作（2026-08-10）— OmniMemEval 时序修复：chat_time 日期前缀

- **问题**：LoCoMo Temporal 15%、LongMemEval Temporal 52.6% 弱 → 根因是 bench server 丢弃了 harness 转发的 `chat_time`。
- **改动**（`internal/bench/server.go`，仅 bench server）：`handleAdd` 把 `msg.ChatTime` 传入 `eventFor`，可解析则摘要前缀 `[YYYY-MM-DD] `；两个 gate 模式都生效。
- **测试**：`TestBench_TemporalPrefixInMemory`；`go test ./internal/bench/` 全绿。未改 core。
- **分支/提交**：feature/omnimemeval-benchmark，commit `0650720`。

## 最近工作（2026-08-09）— OmniMemEval benchmark 集成（分支 feature/omnimemeval-benchmark，默认不合 main）

- 用户指令：单独开分支做测评，不合 main，除非 adapter 对项目推进有正向价值。
- **已完成并提交**（commit `490f99c`，go test 561/25 全绿；LoCoMo 真实 smoke 1540/1540 检索成功）：
  - `cmd/vatbrain-bench`（HTTP add/search/delete/health，gate off/on 消融）
  - `internal/bench/`（Gate 模式开关）+ `store.EpisodicDeleteStore` 可选能力接口
  - `eval/omnimemeval/`（vatbrain_client.py adapter + patch.py/setup.sh + 7 mock 单测）
  - 设计文档：`docs/v0.3/tech-specs/03-omnimemeval-benchmark.md`
- **关键决策**：user_id→ProjectID；默认 keyword embedder（零 API 可本地 smoke，正式分数需语义密钥）；bench server 默认绑 127.0.0.1。
- 对抗性审查 18 findings 已全部处理。

## 最近工作（2026-08-10）— 文档 + 并发调研

- `docs/v0.3/04-omnimemeval-benchmark-results.md`（User Memory 三 benchmark 分数报告）
- `docs/v0.3/05-agent-memory-handoff.md`（Agent Memory 轨新会话启动指南）
- `docs/v0.3/06-v3-iteration-plan.md`（benchmark 驱动迭代计划）
- **精确并发数字**（官方+实测）：DeepSeek v4-flash 2500 并发上限；智谱 embedding 按账号限在途（V0=50/V1=100/V2=300/V3=500），实测本账号 ≥64 安全。

## 已知问题

- **Agent 轨实跑速度**：MiniMax-M3 首 token 延迟高（~50s），单任务数分钟；478 train 题全量很贵，实跑前评估 `-m deepseek-v4-flash` 或 `--parallel 2-4`、抽样。
- **reasoning 域 `--train-split` 数值 bug**：`load_tasks` 数字 split 恒加载 test 集 → 用小集用 `--task` 选 id。
- **SWE-Bench 跳过**：本机无 docker，orange 是 aarch64（官方镜像 x86_64）→ 只跑其余 4 域。
- **deriver summary = 完整任务 prompt**（非压缩洞见）→ 记忆价值待 benchmark 暴露。
- 插件 `shutdown()` 时后台 sync 线程可能未完成（best-effort，短会话实测能存活）。
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 语义）。
