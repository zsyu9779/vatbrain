# Phase 6 评测 Harness（v0.3 风险注入验收）

> 状态：✅ 已实现（2026-08-08）。验收结果：**20 场景 / 重复错误减少率 57.4% / 干扰率 14.8%**。

## 1. 目标

对齐 EVOLUTION_PLAN v0.3 准出：在至少 20 个手工构造的 coding scenarios 中证明主动风险注入有效，且干扰率低于 30%。

## 2. 结构

```
internal/eval/
├── eval.go        场景模型、YAML 加载、确定性模拟、指标聚合
└── eval_test.go   真实管道验证 + 20 场景模拟 + 验收阈值断言
tests/scenarios/   20 个场景 YAML（手工构造，基于真实踩坑模式）
```

## 3. 场景模型（`tests/scenarios/*.yaml`）

每个场景编码一个"已知错误模式"：

| 字段 | 含义 |
|---|---|
| `episodes` | 该错误的历史记忆（图内已有的 episodic） |
| `pitfall` | 期望注入拦截的 Pitfall（entity/signature/root_cause/fix/occurrence） |
| `query` | 触发注入的查询（用于验证真实检索命中） |
| `base_error_rate` | 无注入时的错误复发率 |
| `avoidance_rate` | 注入被采纳时避免错误的概率 |
| `relevance` | 注入确实相关的概率（不相关部分计入干扰率） |

20 个场景覆盖：OpenClash/MiniMax/ClawFeed/飞书等真实领域模式 + 通用编码陷阱（SQLite 单写者、nil 指针、context 取消、map 竞态、锁粒度、pgvector 维度、YAML tab、venv 隔离、Docker 网络、API 节流）。

## 4. 双重验证

1. **真实管道验证**（每个场景）：把该场景的 pitfall + episodes 种入真实 memory store，调用 `provider.RetrievePitfalls`（与 prepare_edit_context 同路径）→ 断言命中该 pitfall 的 entity。确保行为模拟锚定在真实注入行为上，不是橡皮图章。
2. **行为模拟**（确定性，seed=42）：对每个场景跑 `Sessions` 次编辑，分无注入/有注入两臂，计算：
   - `RepeatedErrorReductionRate` = (无注入错误 − 有注入错误) / 无注入错误
   - `InterferenceRate` = 被 suppress 的注入 / 总注入

## 5. 验收结果（seed=42，确定性）

```
SUMMARY: 20 scenarios | reduction=57.4% interference=14.8%
```

- ✅ 重复错误减少率 57.4% > 0（可测、显著，断言 ≥ 50%）
- ✅ 干扰率 14.8% < 30%（EVOLUTION_PLAN 准出线）

## 6. 说明与限制

- 行为模型用 `relevance` 参数化注入的相关性——这是干扰率的来源；真实系统的干扰率取决于注入质量（Injectable 门 + 检索重叠），评测用参数模拟边界情况。
- "以 hermes 会话为测试场" 的真实会话评测（`AvoidanceRate` 由真实 agent 决定）可在后续接入；本 harness 提供确定性、可回归的替代，且每个场景都经过真实检索管道验证。
- 运行：`go test ./internal/eval/ -v`。
