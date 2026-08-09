# 07 — Agent Memory 轨 Benchmark 规划与执行方案

> 日期：2026-08-10 · 分支：`feature/omnimemeval-benchmark`
> 前置：`05-agent-memory-handoff.md`（新会话启动指南）· `04-omnimemeval-benchmark-results.md`（User Memory 轨结果）

## 1. 目标

评测 **Hermes + vatbrain 记忆插件**在 EvoAgentBench 5 个任务域上的任务完成率提升：

```
对比：baseline（无 vatbrain 记忆） vs vatbrain（有记忆）   →  提升 = trend 信号
协议：memory_train_backup_test（清理→train→沉淀→备份→恢复→test）
```

这不是绝对分数竞赛，而是 **VatBrain Pitfall/错误记忆设计在真实 Agent 工作负载下的验证场**。

## 2. 架构（本方案新增/改动的部分）

```
OmniMemEval AgentBench runner
├── scripts/agentbench/agents/hermes.py            ← 新增：HermesAgentAdapter
├── scripts/agentbench/agents/__init__.py          ← 注册 "hermes"
├── configs/agentbench/agents/hermes.yaml          ← 新增：vatbrain 记忆配置
├── configs/agentbench/agents/hermes-baseline.yaml ← 新增：无记忆 baseline 配置
├── configs/agentbench/memory_plugins/vatbrain.yaml← 新增：clear/backup/restore 生命周期
└── .env.agent                                     ← 新增：DeepSeek judge + 智谱 embedding

VatBrain repo
├── internal/provider/server.go                     ← ForceConfirm（gate off）
├── cmd/vatbrain-provider/main.go                   ← VATBRAIN_GATE_MODE=off 开关
└── ~/.hermes/vatbrain/bin/vatbrain-provider       ← 重新构建安装
```

**HermesAgentAdapter**（继承 `AgentAdapter`，最小实现）：
- 每个任务 = 一次 `hermes chat -q <prompt> -Q --yolo --reasoning none`（独立会话 → 每个任务一条 episode）
- `-Q` 让 stdout 只剩最终回答（verifier 直接用），stderr 末尾带 `session_id` 供收集
- `_get_subprocess_env` 注入：`VATBRAIN_PROVIDER_BIN`、`HERMES_HOME`、`VATBRAIN_GATE_MODE=off`、语义 embedding env
- hermes 会话不落 jsonl（存 state.db），`collect_session` 返回 0 统计（不影响 verifier 出分）

## 3. 关键发现与修复（执行中暴露）

| # | 问题 | 根因 | 修复 |
|---|---|---|---|
| 1 | provider 从未激活 | 插件 `is_available()` 在 `initialize()` 前跑，`_hermes_home` 空 → 解析不到二进制 | env 注入 `VATBRAIN_PROVIDER_BIN` 指向 `~/.hermes/vatbrain/bin/vatbrain-provider` |
| 2 | 写入全部被拒 | SignificanceGate 默认把所有首次接触记忆 gate 掉（"遗忘是默认"），单会话单任务协议下 4 个门控条件全不满足 | `VATBRAIN_GATE_MODE=off` → sync_turn 强制 `UserConfirmed=true`（对齐 User Memory 轨 GateModeOff） |
| 3 | （验证通过）短会话写入丢失 | sync_turn 是 daemon 线程异步写 | 实测 local SQLite 快，写能存活，无需改 |

> **Gate-off 决策**：Agent 轨主测量 = 存储+检索内核（与 User Memory 轨一致）。Gate-on 是独立消融（`VATBRAIN_GATE_MODE=on`），衡量"遗忘是默认"在该协议下的代价。**这个发现本身就是重要结论**：VatBrain 的 gate 假设长会话持续交互（跨周期重复），与"每任务独立会话"的 Agent 工作负载不匹配。

## 4. 数据与模型

| 域 | Benchmark | Train/Test | 依赖 | 状态 |
|---|---|---|---|---|
| reasoning | OmniMath | 478/100 | 无 | ✅ 已下载，smoke 通过 |
| information_retrieval | BrowseCompPlus | 154/65 | 智谱 embedding + dense index | ⏳ 需 build index |
| knowledge_work | GDPVal | 87/58 | poppler-utils/libreoffice | ⏳ 需系统依赖 |
| code_implementation | LiveCodeBench | 97/39 | LiveCodeBench verifier pkg | ⏳ 需 git clone + pip install |
| software_engineering | SWE-Bench | 101/26 | **Docker x86_64** | ⛔ 本机无 docker，orange 是 aarch64 → **跳过**（handoff 决策） |

- Agent 模型：hermes 默认 MiniMax-M3（可 `-m` 覆盖）
- Judge：DeepSeek `deepseek-v4-flash`（`LLM_DISABLE_THINKING=1`，对齐官方 gpt-4o-mini 口径）
- VatBrain embedding：智谱 `embedding-3`（`VATBRAIN_EMBEDDER_SEMANTIC_*`，从 `.env.agent` 解析）

## 5. 运行命令

```bash
# smoke（已跑通）：train 2 题 + test 2 题
cd /tmp/OmniMemEval
export LLM_DISABLE_THINKING=1
PYTHON=~/miniforge3/envs/agentmem/bin/python ./scripts/run_agent_eval.sh \
  --agent hermes --domain reasoning \
  --protocol memory_train_backup_test --memory-plugin vatbrain \
  --version vatbrain_reasoning_smoke \
  --test-runs 1 --trials 1 --parallel 1 \
  --task omni_35,omni_41,omni_2080,omni_2185

# vatbrain 记忆轨（单域）
./scripts/run_agent_eval.sh --agent hermes --domain reasoning \
  --protocol memory_train_backup_test --memory-plugin vatbrain \
  --version vatbrain_reasoning --test-runs 1 --trials 1 --parallel 1

# baseline 无记忆轨（同域同题）
./scripts/run_agent_eval.sh --agent hermes \
  --agent-config configs/agentbench/agents/hermes-baseline.yaml \
  --domain reasoning --protocol test_only \
  --version baseline_reasoning --trials 1 --parallel 1

# 五域（排除 SWE-Bench）
./scripts/run_agentbench_memory_train_backup_test.sh \
  --agent hermes --memory-plugin vatbrain --version vatbrain_4domain \
  --test-runs 1 --trials 1 --parallel 1
```

## 6. 验收节奏

1. **reasoning 域对比**（已 smoke）→ 出 trend
2. 扩到 4 域（信息检索需先 build dense index，code 需装 LiveCodeBench）
3. 每组输出：`pass@1`、reward、结果目录 `results/agentbench/<profile>-<version>-<domain>/`
4. 与 `04` 报告同风格写 `08-agent-benchmark-results.md`

## 7. 已知风险与决策点

- **速度**：MiniMax-M3 首 token 延迟高（~50s），单任务可能数分钟。实跑前评估：`-m deepseek-v4-flash`（若 hermes 配置支持）或 `--parallel 2-4`；train 478 题全量很贵，可抽样。
- **`--train-split` 数值 bug**：reasoning 域 `load_tasks` 对数字 split 恒加载 test 集 → 用 `--task` 选 id 或跑全量。
- **baseline 公平性**：baseline home（`/tmp/hermes-baseline`）无 vatbrain，同模型同题，只差记忆 → 干净对比。
- **gate 结论**：即便 gate off，也应记录 gate-on 消融，供产品决策（gate 是否适配 Agent 工作负载）。
