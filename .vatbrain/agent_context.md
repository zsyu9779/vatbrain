# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成 Phase 1-6 完成；**backlog issue #1 P0+P1 全部完成，feature/backlog-implementation 已合并入 main** ✅
- **语言**: Go (go 1.25.5) + Python（hermes 插件）
- **分支**: `main`（合并了 backlog 分支 + 另一会话的 logo 提交）
- **远端**: `origin/main` 已含 Hermes 全量 + logo（`assets/logo/vatbrain-logo.png`）
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点
- **hermes**: `~/.hermes/hermes-agent`（HEAD 52920747e）+ 插件 `~/.hermes/plugins/vatbrain/` 已激活（config.yaml memory.provider: vatbrain）
- **战略决策（2026-08-08）**: Neo4j+pgvector 重型存储弃用，后续只投入 SQLite 路径

## 最近工作（2026-08-09）— backlog issue #1 P0+P1 全部完成 + 合并 main

### 本次完成

1. **P1-6 Surprise Score**（DESIGN_PRINCIPLES §12，预测误差信号）：
   - `models.EpisodicMemory`/`SemanticMemory.SurpriseScore` + sqlite 列/迁移
   - `core.SurpriseScorer`（纠正 0.7 / 行为改变 0.5 / 显式指令 0）；`WeightDecayEngine.SurpriseHalfLifeBoost` + `WeightWithSurprise`（最高 **3× 半衰期**）
   - 写管线持久化 surprise（合并取 max）；reconsolidation 把被用户纠正的记忆提升到 ≥0.7
   - 检索：`EpisodicSearchRequest.SurpriseBoost`（默认 0 不改变既有行为）；provider 读路径启用 0.25
   - 验收：7 天存留 high > low×1.2（ROADMAP 准出）、scorer 信号、持久化往返、排序增强
   - 文档：`docs/v0.3/tech-specs/01-surprise-score.md`
2. **P1-5 ConflictResolver**（ROADMAP v1.0 冲突协调引擎 + §11 来源分级）：
   - `core.ConflictDetector`（极性 + bigram 相似度，CJK-safe、纯函数）+ `ConflictResolver`（高 trust 覆盖并退休 loser；同 trust 待人工）+ `ResolveManually`
   - `models.RuleConflict` + sqlite `rule_conflicts` 表；`store.RuleConflictStore` **可选能力接口**（SQLite/memory 实现，弃用的 neo4j 后端不实现）
   - MCP：`detect_rule_conflicts` / `list_rule_conflicts` / `resolve_rule_conflict`（检测幂等）
   - 文档：`docs/v0.3/tech-specs/02-conflict-resolver.md`
3. **P1-7 更多 Watcher 适配器**（issue #1 P1，Observe 覆盖面）：
   - `codex.go` — OpenAI Codex 会话 JSONL 转录（`~/.codex/sessions`，string/block 内容，布局漂移容忍）
   - `openclaw.go` — OpenClaw 记忆 markdown（`~/.openclaw/memory`，frontmatter+body，镜像 opencode）
   - 配置 `VATBRAIN_CODEX_SESSIONS_PATH` / `VATBRAIN_OPENCLAW_MEMORY_PATH`；加入 `"all"`
4. **收尾**：3 个 commit 已推送；`feature/backlog-implementation` 快进合并 → main；与另一会话的 logo 提交合并（README 首屏 = logo + pitfall-aware 标题）
5. 验收：`go test ./internal/...` **547/22 包全绿** + go vet 干净；issue #1 相关 checkbox 已勾选

### 下一步（P2/P3 明确暂缓）

- 用户指令（2026-08-09）：P1 清完即视为完成，P2/P3 暂缓。
- 若后续继续：反事实推理（§8.2）、压缩残差存储（§9.3）、自适应衰减参数（§6.2）、多级存储 Level 1（MinIO 快照管线）、冷存储物理分层、编码特异性情境向量/Sensory Buffer、v1.0 Team Memory、技术债（RetrievalEngine 内部检索 / 性能基准套件）——详见 issue #1 P2/P3。

## 最近工作（2026-08-09）— Logo 三方向设计（另一会话，已并入 main）

- README 首屏加 VatBrain logo（`assets/logo/vatbrain-logo.png`）；`docs/superpowers/` 已 .gitignore
- 若需进一步品牌化：透明背景、SVG 精修、favicon、暗色变体

## 已知问题

- 插件 `shutdown()` 时后台 sync 线程可能未完成（best-effort，daemon 退出即弃）
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 语义）
- ConflictResolver 检测为启发式（极性 + bigram 相似度）；LLM 判定（`ConflictBasisLLM`）预留未接
