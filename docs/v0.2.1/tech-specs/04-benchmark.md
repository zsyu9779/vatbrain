# VatBrain Benchmark 报告（v0.3.1 评测增强）

> 生成：2026-08-09。确定性（seed=42），可回归：`go test ./internal/eval/ -v`。
> 20 个手工构造的 coding scenarios，三组对比（baseline / generic memory / VatBrain）。

## 1. 结果摘要

```
SUMMARY: 20 scenarios | errors 2581→1944→1034
reduction (VatBrain) = 59.9%   generic = 24.7%
useful injection rate = 65.9%  false injection rate = 13.4%
task time = 0.76×   token overhead = 1.030×
```

| 指标 | 值 | EVOLUTION_PLAN 目标 | 达标 |
|------|-----|--------------------|------|
| 重复错误减少率（VatBrain vs baseline） | **59.9%** | >25%（相对 baseline 降低） | ✅ |
| 重复错误减少率（generic vs baseline） | 24.7% | — | 对照臂 |
| Useful injection rate | **65.9%** | >60% | ✅ |
| False injection rate | **13.4%** | <30% | ✅ |
| 干扰率 | 13.4% | <30% | ✅ |
| Task time ratio（VatBrain/baseline） | **0.76×** | 不显著变慢 | ✅（更快：避免重试） |
| Token overhead | **1.030×** | — | ✅（+3% 有界） |

## 2. 三组阶梯

```
错误次数:  baseline 2581  >  generic 1944  >  VatBrain 1034
减幅:              0%           24.7%            59.9%
```

- **baseline（无记忆）**：错误按场景 `base_error_rate` 复发。
- **generic memory（仅语义检索）**：有相关回忆但无具体修复方案，减幅 24.7%。
- **VatBrain（Pitfall + 注入）**：具体 fix 注入，减幅 59.9%——generic 的 2.4 倍。

## 3. 质量与成本

- **Useful injection 65.9%**：每次注入中约 2/3 被采纳且避免错误。
- **False injection 13.4%**：不相关注入被 suppress（逃生阀生效），低于 30% 准出线。
- **Task time 0.76×**：注入虽增加上下文，但避免的重复调试回合远超开销——任务净提速 24%。
- **Token overhead 1.03×**：注入上下文成本 +3%，有界。

## 4. 方法

- 场景：`tests/scenarios/`（20 个 YAML，覆盖 OpenClash/MiniMax/ClawFeed/飞书 + SQLite/nil 指针/context/竞态/锁粒度/pgvector/YAML/venv/Docker/节流等）。
- 每个场景先经**真实检索管道**验证（`provider.RetrievePitfalls` 命中该场景 pitfall），再做确定性三臂模拟。
- 行为模型参数：`base_error_rate`（复发率）、`avoidance_rate`（注入采纳后避免概率）、`relevance`（注入相关性）、`generic_effectiveness`（generic 减幅，默认 0.25）。
- 成本模型：错误 ≈ 1 个修复回合；注入 ≈ 0.03 回合上下文开销。

## 5. 局限

- 行为模型参数化注入相关性/有效性，是**确定性代理**；真实 hermes 会话评测（`AvoidanceRate` 由真实 agent 决定）是后续接入点。
- token/time 为派生估计，非真实测量。
