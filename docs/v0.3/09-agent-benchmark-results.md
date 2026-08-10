# 09 — Agent Memory 轨 benchmark：正式结果

> 日期：2026-08-10 · 分支：`feature/omnimemeval-benchmark`
> 评测框架：[OmniMemEval](https://github.com/MemTensor/OmniMemEval) AgentBench · reasoning / OmniMath 域
> 方案见 `docs/v0.3/07-agent-benchmark-plan.md` · smoke 见 `08-agent-benchmark-smoke-results.md`

## 评测配置

| 项 | 配置 |
|---|---|
| Agent 运行时 | Hermes v0.20.0（`hermes chat -q ... -Q --yolo --reasoning none -t vatbrain_none`，禁工具） |
| 记忆插件 | vatbrain（`memory.provider: vatbrain`，`VATBRAIN_GATE_MODE=off`） |
| Agent 模型 | MiniMax-M3（minimax-cn） |
| Judge / Verifier | DeepSeek `deepseek-v4-flash`（`LLM_DISABLE_THINKING=1`） |
| VatBrain embedding | 智谱 `embedding-3`（2048 维） |
| 协议 | `memory_train_backup_test`（clean→train→settle→backup→restore→test） |
| 对照 | `test_only`（hermes-baseline home，无 vatbrain 记忆） |

> `-t ""` 在 argparse 里会变 None 而回落全部工具（agent 会 tool-loop，web_fetch/terminal），
> 因此禁用工具必须用无效工具集名 `-t vatbrain_none`（hermes 校验后静默丢弃，真禁用）。

## 关键前置修复（本轮执行中发现）

1. **provider 激活**：`VATBRAIN_PROVIDER_BIN` 注入（is_available 在 initialize 前无法解析二进制）。
2. **SignificanceGate 阻塞**：单会话单任务协议下所有首次写入被 gate → `VATBRAIN_GATE_MODE=off`。
3. **工具禁用**：hermes 默认 toolset 会让 agent 在纯数学题上 tool-loop → `-t vatbrain_none`。

## 结果（全量：478 train + 100 test）

| 指标 | baseline（无记忆） | vatbrain（有记忆） |
|---|---:|---:|
| **test pass@1** | **21.00%**（21/100） | **21.00%**（21/100） |
| 有效任务 | 100（0 排除） | 100（0 排除） |
| 对齐后差异 | — | **0.0pp（持平）** |
| vatbrain train pass@1 | — | 51.88%（478 题，1 排除） |

> 此前提交记录 baseline 为 **21.65%（21/97）**：当时按 97 个有效任务快照计算；
> 当前 `summary.json` 为 100 任务 / 0 排除 / 21.00%。两种口径本质都是 **21/≈100**。

### 10 题样本（先行对比，已修正解读）

| 指标 | baseline（无记忆） | vatbrain（有记忆） |
|---|---:|---:|
| pass@1 | 10%（1/10） | 20%（2/10） |

> **该样本结果为小样本假阳性，全量未复现**：当时记为"提升点"的 omni_2292 在全量里两边都错，
> 10 题里 1 题的翻转只是 MiniMax 随机性。全量 100 题对齐后两边完全持平。

### 逐题翻转（对齐 100 题，26 题，净零）

| 方向 | 数量 | 任务 id |
|---|---:|---|
| 错→对（vatbrain 改进） | 13 | omni_1143, 1361, 1578, 1912, 2151, 2331, 2447, 2451, 2474, 2499, 2979, 4117, 443 |
| 对→错（vatbrain 回退） | 13 | omni_1184, 1543, 1750, 2090, 2129, 2511, 2941, 3193, 3304, 365, 4321, 4398, 557 |

26 个翻转在单次试验 + MiniMax 随机性下 ≈ 噪声（两侧 stderr 均 0.041，差 0.00）。

## 记忆是否真的生效？（生效验证）

**生效了。** 此前的初步核验曾因检索错字段（查 `state.db` 的 `content` 列，而注入实际写入 API 副本
`api_content` 列）得出过"未生效"的错误中间结论——已更正。证据链（test 窗口 08-10 00:23→09:31）：

| 环节 | 证据 |
|---|---|
| Provider 生命周期 | 每个会话 spawn + initialize 成功（`~/.hermes/logs/agent.log`，1200+ 行 INFO） |
| 训练记忆入库 | vatbrain.db 729 条 episodic（531 条任务 prompt + 198 条 verifier feedback），backup/restore returncode 全 0 |
| **检索注入** | `~/.hermes/state.db` 中 **1209 条 message 的 `api_content` 含 `<memory-context>` 围栏** |
| 注入内容构成 | 648 条任务 prompt + 560 条 feedback 记忆（含 expected 答案文本，如 "omni_3533 … expected: No, there do not exist such…"） |

即：provider 在跑、记忆在库、prefetch 检索在返回、模型 prompt 每轮都在注入 —— 记忆机制全链路工作。

## 分析与结论

1. **全量是 null result**：记忆既不帮助也不伤害（21.00% = 21.00%，26 翻转净零）。
2. **根因是"域 × 协议"不匹配，而非记忆系统失效**：注入的记忆是**其他题的完整 prompt + 该题 verifier
   答案**。对数学竞赛题，上一题的答案对当前题**没有可迁移价值**——每道题都是独立求解；train/test 题目互不重叠，
   记忆注入 = 几百字符"看似相关实则无用"的上下文。这正是 `agent_context` 早先记录的隐患
   "任务记忆 = 完整 prompt（价值低）"在此协议下成为主导。
3. **SignificanceGate 结论**：gate off 是主测量。单会话单任务协议下 4 门控天然全不满足
   （"遗忘是默认"假设长会话跨周期重复）——gate-on 在此协议的代价本身就是结论，见 `07-agent-benchmark-plan.md`。
4. **10 样本假阳性**：10%→20% 的提升未复现，应视为噪声。

## 复现

```bash
# 全量（链式：先 10 样本，完成后自动跑全量）
cd /tmp/OmniMemEval
bash scripts/agentbench/run_agent_bench.sh --reasoning --full --provider vatbrain --provider-config configs/agentbench/memory_plugins/vatbrain.yaml
bash scripts/agentbench/run_agent_bench.sh --reasoning --full --provider baseline

# 结果
# /tmp/OmniMemEval/results/agentbench/hermes-vatbrain-vatbrain_reasoning_full-reasoning/
# /tmp/OmniMemEval/results/agentbench/hermes-baseline-baseline_reasoning_full-reasoning/
```

## 已知限制与下一步

- **单次试验**：每任务 1 次 trial，pass@1 噪声大（stderr 0.041）。26 个翻转需多次重复才能区分信号/噪声。
- **域迁移价值缺失**：本评测的 OmniMath 题彼此独立，不适合暴露记忆的跨任务迁移。下一步应换
  **迁移信号存在的域/协议**——例如上一题的解法/工具用法能复用到下一题的任务（代码修补、重复模式的
  数据流水线、同题不同随机 seed），或让 train/test 共享底层解题模式。
- **注入质量**：任务记忆 = 完整 prompt（体积大、价值低）；feedback 记忆含 expected 答案但对异题无用。
  值得探索检索时压制任务 prompt、优先 feedback/解法类记忆。
- **工具禁用 vs 启用**：工具启用版 baseline 是 40%（terminal 计算器有实质帮助）。若聚焦"记忆价值"，
  应固定在一个工具配置下对照，避免工具差异污染结论。
- **MiniMax 偶发挂死**：约 1/3 请求挂在 MiniMax，靠看门狗杀 >240s 进程兜底；对延迟/分数稳定性有影响。
