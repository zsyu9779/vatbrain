# 08 — Agent Memory 轨 benchmark：Smoke 验证与首轮对比

> 日期：2026-08-10 · 分支：`feature/omnimemeval-benchmark`
> 评测框架：[OmniMemEval](https://github.com/MemTensor/OmniMemEval) AgentBench
> 方案见 `docs/v0.3/07-agent-benchmark-plan.md` · 启动指南见 `05-agent-memory-handoff.md`

## 目的

本报告验证 Agent Memory 轨的**链路可用性**（不是正式分数）：Hermes + vatbrain 插件能否跑通 `memory_train_backup_test` 协议、记忆能否写入/召回、baseline 对比机制是否成立。

## 评测对象与配置

| 项 | 配置 |
|---|---|
| Agent 运行时 | Hermes v0.20.0（`hermes chat -q ... -Q --yolo --reasoning none`） |
| 记忆插件 | vatbrain（`~/.hermes/plugins/vatbrain/`，`memory.provider: vatbrain`） |
| 记忆内核 | vatbrain-provider daemon（`VATBRAIN_GATE_MODE=off`，force-confirm 写全部） |
| Agent 模型 | MiniMax-M3（hermes 默认，minimax-cn） |
| Judge / Verifier | DeepSeek `deepseek-v4-flash`（`LLM_DISABLE_THINKING=1`） |
| VatBrain embedding | 智谱 `embedding-3`（2048 维，语义检索） |
| 协议 | `memory_train_backup_test`（clear→train→settle→backup→restore→test） |
| 域 | reasoning / OmniMath（train 2 题 + test 2 题） |

## Smoke 结果

### 链路验证（全部通过）

| 环节 | 结果 |
|---|---|
| `clear`（删 vatbrain.db） | ✅ returncode 0 |
| `train`（2 题，写入记忆） | ✅ vatbrain.db 6 条 episodic |
| `backup`（tar 快照） | ✅ 39.3K，`~/vatbrain-backups/vatbrain-reasoning-...tar.gz` |
| `restore`（恢复快照） | ✅ 记忆完整恢复 |
| recall（prefetch 召回） | ✅ 语义检索返回 2476 字符，含训练记忆 |
| judge/verifier（DeepSeek） | ✅ 正常判定 |
| hermes 会话收集 | ✅ session_id 捕获（token 统计为 0，见下） |

### 同题对比（vatbrain vs baseline）

```
同一 2 道 test 题，同一 MiniMax-M3，唯一差异 = 是否有 vatbrain 记忆
```

| task | baseline（无记忆） | vatbrain（有记忆） |
|---|---:|---:|
| omni_2080 | 0.0 ✗ | 1.0 ✓ |
| omni_2185 | 1.0 ✓ | 1.0 ✓ |
| **pass@1** | **50%** | **100%** |

> ⚠️ **n=2，仅示方向**：vatbrain 两道题都不输 baseline，且 omni_2080 从错到对。单题差异可能含模型随机性，正式结论需更大样本。

### 记忆内容质量（重要观察）

- **任务记忆** = 完整任务 prompt（deriver 未压缩）→ 跨任务直接价值有限。
- **Feedback 记忆** = 丰富（reward + expected answer + actual + 反馈）→ **真正的跨任务知识**。
  例：`omni_35` 的 feedback 记忆含 `expected: "P(x,y,z) = x^2 + y^2 + z^2 + 2xyz"`。
- 含义：**benchmark 的实际测量 = feedback 通路带来的任务知识迁移**，而不是任务重述。

## 关键发现（对产品有实质意义）

1. **SignificanceGate 与 Agent 工作负载不匹配**（gate-on 时）：
   - 协议是"每任务独立短会话"，gate 的 4 个门控条件（用户确认/跨周期重复/预测误差/后续引用）在单次任务里全不满足 → **所有首次接触都被拒**。
   - 本报告用 `VATBRAIN_GATE_MODE=off`（force-confirm）测内核；**gate-on 在此负载下的代价 = 记忆完全不沉淀**，是后续产品决策输入。
2. **Provider 二进制解析竞态**：插件 `is_available()` 在 `initialize()` 前运行，`_hermes_home` 为空时解析不到二进制 → 依赖 env 注入 `VATBRAIN_PROVIDER_BIN`。这是插件层的可靠性 bug，已规避。
3. **hermes 会话不落 jsonl**（存 state.db）→ `collect_session` 返回 0 token 统计。不影响 verifier 出分，但失去 token/效率指标。

## 复现

```bash
# vatbrain 记忆轨 smoke
cd /tmp/OmniMemEval && export LLM_DISABLE_THINKING=1
PYTHON=~/miniforge3/envs/agentmem/bin/python ./scripts/run_agent_eval.sh \
  --agent hermes --domain reasoning \
  --protocol memory_train_backup_test --memory-plugin vatbrain \
  --version vatbrain_reasoning_smoke --test-runs 1 --trials 1 --parallel 1 \
  --task omni_35,omni_41,omni_2080,omni_2185

# baseline 无记忆轨（同题）
./scripts/run_agent_eval.sh --agent hermes \
  --agent-config configs/agentbench/agents/hermes-baseline.yaml \
  --domain reasoning --protocol test_only \
  --version baseline_reasoning_smoke --trials 1 --parallel 1 \
  --task omni_2080,omni_2185
```

结果目录：`results/agentbench/{hermes-vatbrain,hermes-baseline}-<version>-reasoning/`

## 下一步

1. **更大样本的 reasoning 对比**（如 test 10-20 题）出真 trend —— 需确认时长（MiniMax ~2-4min/题）。
2. **扩 4 域**：information_retrieval（先 build dense index）、code_implementation（装 LiveCodeBench）、knowledge_work（poppler/libreoffice）；SWE-Bench 跳过（无 x86 Docker）。
3. **gate-on 消融**：同题跑 `VATBRAIN_GATE_MODE=on`，量化 gate 在此负载的代价。
4. **feedback 通路优化**：既然 feedback 记忆是主要价值，可研究是否把 feedback 单独建模/加权。
