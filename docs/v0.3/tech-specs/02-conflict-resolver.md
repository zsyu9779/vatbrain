# ConflictResolver — 语义规则冲突协调（v1.0 前置，backlog P1-5）

> 状态：✅ 已实现（2026-08-09）。ROADMAP v1.0 冲突协调引擎 + §11 来源分级。

## 1. 目标

不同来源贡献的语义规则矛盾时，按 §11 来源分级裁决：**信任度高的覆盖低的，同等信任人工裁决**。本实现把 v1.0 Team Memory 的冲突协调机制提前落地为单项目语义规则治理——不依赖多租户。

## 2. 冲突检测（启发式）

两条规则构成冲突需同时满足：

1. **同主题**：同一 MemoryType + 字符 bigram Dice 系数 ≥ 0.25（CJK-safe，「不要用 X」与「应该用 X」高度重叠）。
2. **指令相反**：`DetectPolarity` 判定一条为 prohibitive（不要/禁止/never/don't…）、另一条为 affirmative（应该/必须/always/do…）。prohibitive 优先（「不要使用 X」是 prohibitive 而非 affirmative）。
3. **均活跃**：obsoleted 的规则不参与。

确定性、纯函数、可单测（`ConflictDetector.Detect` 无 I/O）。LLM 判定（`ConflictBasisLLM`）预留为后续增强，接口不变。

## 3. 裁决（§11 来源分级）

| 情形 | 动作 |
|---|---|
| trust A > trust B | A 胜，B 标记 obsoleted（`MarkSemanticObsolete`），记录 `CONFLICTS_WITH` 边 + `auto_resolved` |
| trust A < trust B | 对称 |
| trust A == trust B | 保持 `pending`，等人工裁决（Workbench） |

人工裁决：`resolve_rule_conflict`（winner 必须是两规则之一，loser 退休）。

## 4. 持久化与接口

- `models.RuleConflict` + SQLite `rule_conflicts` 表（status: pending / auto_resolved / manual_resolved / dismissed；basis: polarity / llm）。
- `store.RuleConflictStore` **可选能力接口**（type assertion）——SQLite 与内存 store 实现，弃用的 Neo4j+pgvector 不实现，冲突工具对该后端返回「不支持」。这样不污染主 `MemoryStore` 接口、不强迫 3 个后端都改。
- MCP 工具：`detect_rule_conflicts`（检测 + 自动裁决）、`list_rule_conflicts`（状态过滤）、`resolve_rule_conflict`（人工）。
- 幂等：重复运行对已跟踪的 pending 对跳过。

## 5. 验收

`go test ./internal/...`（541 用例全绿）：

- ✅ 极性判定：prohibitive / affirmative / neutral / 大小写 / 「不要使用」优先级。
- ✅ 检测：同主题相反指令 → 冲突；同极性 → 不冲突；不同主题 → 不冲突；obsoleted → 排除。
- ✅ 端到端：trust 5 vs 1 → auto_resolved + loser 退休；trust 3 vs 3 → pending；二次运行幂等。
- ✅ 人工裁决：winner 合法 → loser 退休；winner 不在对内 → 拒绝。
- ✅ SQLite 往返 + 状态过滤；MCP 三工具端到端。

## 6. 后续（并入 v1.0 Team Memory）

- 冲突范围从「同项目」扩展为「同 team / 同 agent」作用域。
- proposed 反事实规则的冲突进入同一队列。
- 冲突记录驱动 Pitfall review queue 的排序。
