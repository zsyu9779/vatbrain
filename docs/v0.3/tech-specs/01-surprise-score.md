# Surprise Score — 预测误差信号（v0.3）

> 状态：✅ 已实现（backlog issue #1，P1-6）。ROADMAP v0.3 准出：**高惊讶度事件 7 天后权重显著高于同期低惊讶度事件**（见 §5 验收）。

## 1. 目标

DESIGN_PRINCIPLES §12：多巴胺神经元编码的核心是**奖励预测误差**——预期与实际之间的差距。最高权重的记忆不应是被反复使用的记忆，而应是**让预期落空的记忆**。一条规则被用了 100 次不如 1 次意外失败信息量大。

工程推论：引入独立的「惊讶度」维度（Surprise Score），与常规权重**并行但独立计算**。高惊讶度事件在衰减曲线上获得更长半衰期（最高 3×）。

## 2. 惊讶度来源

惊讶度 = 预期被打破的程度，在**写入时**从事件信号计算：

| 信号 | 贡献 | 说明 |
|---|---|---|
| `IsCorrection`（用户纠正） | 0.7 | 记忆的预期被用户推翻——最强的预测误差 |
| `CausedBehaviorChange`（行为改变） | 0.5 | 事件导致 Agent 改变行为——中等预测误差 |
| `UserConfirmed`（显式指令） | 抑制为 0 | 「记住：…」是**deliberate 指令**，与「预期落空」相反 |

两者叠加超过 1 时钳制为 1（全惊讶）。`DefaultSurpriseScorer`：CorrectionSurprise=0.7，BehaviorChangeSurprise=0.5。

记忆被**用户纠正**时（reconsolidation 路径），其 SurpriseScore 至少提升到 0.7——被纠正的记忆本身就是一次预测误差。

## 3. 衰减半衰期扩展

`WeightDecayEngine` 新增 `SurpriseHalfLifeBoost`（默认 2.0）。惊讶度 s ∈ [0,1] 通过拉伸系数把两个衰减指数同时放慢：

```
decay_scale = 1 / (1 + SurpriseHalfLifeBoost · s)
Weight = EF · e^(-α · days · scale) · e^(-β · days · scale)
```

- s=0 → scale=1，无变化；
- s=1 → scale=1/3，**半衰期 ×3**。

创建时刻（days=0）惊讶度不改变初始权重——它只影响**时间累积**的衰减曲线，这正是「7 天后显著更高」的机制来源。

## 4. 持久化与检索

- `EpisodicMemory` / `SemanticMemory` 新增 `SurpriseScore` 字段（SQLite `surprise_score REAL DEFAULT 0`，带迁移）。
- 写管线 `WriteMemory` 在门控通过后计算惊讶度并持久化（合并路径取 max）。
- 检索：`EpisodicSearchRequest.SurpriseBoost`（默认 0，**不改变既有行为**）。>0 时按 `weight × (1 + SurpriseBoost · surprise)` 或 `cosine × (1 + SurpriseBoost · surprise)` 排序，把高惊讶记忆抬到同等竞争者的前面。
- 实时读取路径（`provider.RetrieveEpisodic`）启用保守 `surpriseRankingBoost=0.25`——排序仍以语义为主，只是让过去的纠正记忆在同分时胜出。

## 5. 验收

`go test ./internal/...`（527 用例全绿，含新增 surprise 用例）：

- ✅ **7 天存留**：同为 7 天龄、同样未访问的记忆，surprise=1 权重比 surprise=0 高 ≥ 20%（实测 ~29%），且被 3× 半衰期封顶（< 1.5× 上界）。
- ✅ 惊讶度评分：纠正=0.7 / 行为改变=0.5 / 叠加钳制=1.0 / 显式指令=0 / 普通事件=0。
- ✅ 衰减边界：s=0→scale 1；s=1→scale 1/3；越界输入钳制到 [0,1]。
- ✅ 写管线：纠正事件持久化 surprise=0.7；显式指令持久化 0。
- ✅ 存储往返：SQLite episodic/semantic surprise_score 读写一致。
- ✅ 排序增强：同等权重下 SurpriseBoost 把高惊讶记忆排第一；默认（boost=0）不改变排序。
- ✅ 再巩固：用户纠正把记忆 surprise 提升到 0.7 且 trust 3→4。

## 6. 后续

- 语义规则继承源 episodic 的惊讶度（字段已就位，consolidation 提炼时把源惊讶度聚合到规则）。
- 反事实推理（P2）：连续 N 次纠正生成的 proposed 假设性规则天然高惊讶——可复用本维度做初始权重。
- 惊讶度与衰减 α/β 的联合微调可纳入自适应衰减参数（P2）。
