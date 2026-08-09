# 09 — Agent Memory 轨 benchmark：正式结果

> 日期：2026-08-10 · 分支：`feature/omnimemeval-benchmark`
> 评测框架：[OmniMemEval](https://github.com/MemTensor/OmniMemEval) AgentBench · reasoning / OmniMath 域
> 方案见 `docs/v0.3/07-agent-benchmark-plan.md` · smoke 见 `08-agent-benchmark-smoke-results.md`

## 评测配置

| 项 | 配置 |
|---|---|
| Agent 运行时 | Hermes v0.20.0（`hermes chat -q ... -Q --yolo --reasoning none -t ""`，禁工具） |
| 记忆插件 | vatbrain（`memory.provider: vatbrain`，`VATBRAIN_GATE_MODE=off`） |
| Agent 模型 | MiniMax-M3（minimax-cn） |
| Judge / Verifier | DeepSeek `deepseek-v4-flash`（`LLM_DISABLE_THINKING=1`） |
| VatBrain embedding | 智谱 `embedding-3`（2048 维） |
| 协议 | `memory_train_backup_test`（clean→train→settle→backup→restore→test） |
| 对照 | `test_only`（hermes-baseline home，无 vatbrain 记忆） |

## 关键前置修复（本轮执行中发现）

1. **provider 激活**：`VATBRAIN_PROVIDER_BIN` 注入（is_available 在 initialize 前无法解析二进制）。
2. **SignificanceGate 阻塞**：单会话单任务协议下所有首次写入被 gate → `VATBRAIN_GATE_MODE=off`。
3. **工具禁用**：hermes 默认 toolset 会让 agent 在纯数学题上 tool-loop（web_fetch 抓 PDF）→ `-t ""`，单任务从 5+ 分钟降到 ~1 分钟。

## 结果

### 10 题样本（先行对比）

| 指标 | baseline（无记忆） | vatbrain（有记忆） |
|---|---:|---:|
| pass@1 | **10%**（1/10） | **20%**（2/10） |
| test 任务数 | 10 | 10 |

> 工具禁用（`-t vatbrain_none`）后 MiniMax-M3 无记忆仅 10%。vatbrain 记忆 +10pp，omni_2292 从错到对。样本小仅示方向；注意工具禁用版 baseline（10%）远低于工具启用版（40%）—— terminal 计算器在部分题上有实质帮助。

### 全量（478 train + 100 test）

| 指标 | baseline（无记忆） | vatbrain（有记忆） |
|---|---:|---:|
| pass@1 | 待填充 | 待填充 |
| test 任务数 | 100 | 100 |

### 类别分析（全量）

| 类别 | baseline | vatbrain | 差值 |
|---|---:|---:|---:|
| （按 difficulty/domain 分桶） | | | |

## 分析与结论

（待数据填充：vatbrain 是否提升、哪些类别受益、记忆质量、gate 结论）

## 复现

（命令待填充）

## 已知限制与下一步

- （待填充）
