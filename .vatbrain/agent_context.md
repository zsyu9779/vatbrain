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

## 最近工作（2026-08-10）— OmniMemEval 时序修复：chat_time 日期前缀

- **问题**：LoCoMo Temporal 15%、LongMemEval Temporal 52.6% 弱 → 根因是 bench server 丢弃了 harness 转发的 `chat_time`，检索出的记忆无日期，answer 模型无法做事件排序。
- **改动**（`internal/bench/server.go`，仅 bench server，未动 core/provider）：
  - `handleAdd` 把 `msg.ChatTime` 传入 `eventFor(content, chatTime)`（server.go handleAdd 循环内）
  - `eventFor` 签名改为 `(content, chatTime string)`；新增辅助函数 `datePrefixedContent`：chatTime 非空且能被 `time.Parse` 解析（layout：`2006-01-02T15:04:05Z07:00`、`2006-01-02 15:04:05`）→ 摘要前缀 `[YYYY-MM-DD] `；空/解析失败 → 原样
  - 两个 gate 模式都生效（off 直写 Summary；on 先加前缀再走 `provider.DeriveWriteEvent`）
- **测试**：`internal/bench/server_test.go` 新增 `TestBench_TemporalPrefixInMemory`（带 chat_time 检索含 `[2029-05-04]` + 内容；无 chat_time 摘要原样无 `[`；gate-on 模式同样带前缀）
- **验证**：`go build ./...` + `go test ./internal/bench/ ./cmd/...` 全绿（12 passed）；`go vet ./internal/bench/` 干净
- **未提交**（遵父任务指令）；未改 `core.WriteMemory/WriteEvent/CreatedAt/WeightDecay`

## 最近工作（2026-08-09）— OmniMemEval benchmark 集成（分支 feature/omnimemeval-benchmark，默认不合 main）

- 用户指令：单独开分支做测评，不合入 main，除非 adapter 对项目推进/完整性有正向价值。
- **已完成并提交**（commit `490f99c`，分支 feature/omnimemeval-benchmark，go test 561/25 全绿；LoCoMo 真实 smoke 1540/1540 检索成功）：
  - `cmd/vatbrain-bench`（HTTP add/search/delete/health，复用 core.WriteMemory + provider.RetrieveEpisodic，独立 SQLite 评测库）
  - `internal/bench/`（Gate 模式开关：off=测检索内核默认，on=真实显著性门控消融）
  - `store.EpisodicDeleteStore` 可选能力接口（sqlite + in-memory，沿用 RuleConflictStore 先例）
  - `eval/omnimemeval/`（vatbrain_client.py adapter + patch.py/setup.sh 幂等注册 + 7 个 mock 单测 + 操作指南）
  - 设计文档：`docs/v0.3/tech-specs/03-omnimemeval-benchmark.md`
- **关键决策**：user_id→ProjectID；默认 keyword embedder（零 API 可本地 smoke，正式分数需语义密钥）；`chat_time` 未写入（D7 已知限制）；bench server 默认绑 127.0.0.1，非回环需 VATBRAIN_BENCH_API_TOKEN。
- **对抗性审查**（19 findings → 18 确认）已全部处理：hotCache delete 失效、认证 token、GateMode 校验、decodeJSON 413/尾部、部分失败计数、working-mem 清理、.gitignore 锚定、adapter _retry、sqlite 后端 e2e 测试等。
- **跑真实 benchmark 需要**：ANSWER/EVAL OpenAI 兼容密钥 + VatBrain 语义 embedding 密钥（VATBRAIN_EMBEDDER_SEMANTIC_*）。
- 下一步（待用户决策）：是否提供密钥跑真实分数；是否将 adapter 合并入 main（判断：对项目完整性有正向价值，但遵用户指令默认不合，等明确指示）。

## 最近工作（2026-08-09）— HaluMem 真实评测（smoke 完成 + 全量进行中）

- **端点**：DeepSeek chat（deepseek-v4-flash）+ 智谱 embedding（embedding-3，2048 维），两都验证通过。
- **Smoke 结果**（user 0，164 题，池修复前）：整体 **37.8%**；Memory Boundary **95%**、Basic Fact Recall **10%**。诊断出检索缺陷：`SearchEpisodic` embedding 候选池只取 weight 前 100（上限 500），近均匀权重下任意，具体事实常漏。
- **已修复并提交**（`3cf67ad`）：候选池提至 5000（`embeddingRankPool`）+ 回归测试 + bench 启动打印 embedder。修复后全量 user（2805 记忆）superfood/classical/skydiving 均进 top-20 上下文。
- **全量评测完成（User Memory 轨 3 benchmark）**：
  - **HaluMem 64.97%**（3460 题，20 users）：Boundary 96.3%（顶尖，MemOS 91.3）、Conflict 70.5%、Generalization 58.2%、Multi-hop 51.3%、**Basic Fact 43.6%**（smoke 10%→修复后）、Dynamic Update 28.9%
  - **LoCoMo 57.0%**（914 题）：Single Hop 74.6%、Multi-hop 51.3%、Open Domain 56.1%、**Temporal Reasoning 15.0%**（D7 chat_time 未写入，时序短板）
  - **LongMemEval 待出分**（本轮最后）
- **过程修复**（OmniMemEval 克隆侧，公平于所有产品）：
  1. `extract_label_json` 三连修：容忍 JSON+多余字段 → 裸词/散文 → 无引号值（DeepSeek judge 输出格式不稳）
  2. judge prompt 改为"只输出 JSON"（原 prompt 自相矛盾"先解释+只输出label"）
  3. `LLM_DISABLE_THINKING=1` 开关（下轮开启，对齐官方 gpt-4o-mini 非 thinking 口径；DeepSeek 开 thinking 拖慢 answer/judge）
- 教训：bench 重启必须 source `.env.bench`；后台 Bash watcher 会被杀，改用 Monitor；三路并行需独立 bench 实例（不同端口/DB）。
- **下一轮计划**（待用户决策）：关 thinking 重跑；修时序短板（chat_time 写入）;是否上 Agent Memory 轨（Hermes+vatbrain 插件，可跳 SWE-Bench 或用 orange Docker）。

## 最近工作（2026-08-10）— 文档 + 并发调研

- **报告文档**：`docs/v0.3/04-omnimemeval-benchmark-results.md`（三 benchmark 分数 + 分类 + 对比 + 方法论 + 复现命令）
- **handoff 文档**：`docs/v0.3/05-agent-memory-handoff.md`（Agent Memory 轨新会话启动指南：EvoAgentBench 数据、Hermes+vatbrain 插件、域依赖、SWE-Bench 用 orange Docker、`LLM_DISABLE_THINKING=1`）
- **并发调研**（实测 + 官方）：
  - 智谱 embedding-3：官方按账号等级限**在途并发**（V0=50/V1=100/V2=300/V3=500）；实测 64 并发全过 → 账号至少 V1；建议 32-64
  - DeepSeek chat：实测 32 并发全过；answer/judge 16-32 worker
  - **最大提速点**：bench `handleAdd` 当前顺序 embedding → 改并发可 30-60× 缩短 ingestion
- **v3.0 迭代计划**：`docs/v0.3/06-v3-iteration-plan.md`（P0：时序记忆/并发 ingestion/judge 对齐；P1：Update Tracking/Retrieval 增强；P2：Context 精简；最后 Agent Memory 轨）。
- **精确并发数字**（官方）：
  - DeepSeek v4-flash **2500 并发**上限（v4-pro 500；chat/reasoner 已退役）
  - 智谱 embedding 按账号等级限在途并发：V0=50/V1=100/V2=300/V3=500；实测本账号 ≥64 安全；批量 API 5 折
- **时序修复 subagent 进行中**：bench 写入把 chat_time 前缀进记忆摘要（`[2029-05-04] content`），不碰 CreatedAt/decay。完成验证后重跑可看 LoCoMo Temporal 是否回升。
- 待办：subagent 时序修复验收 → 按 v3.0 计划跑下一轮（并发 ingestion + 关 thinking 重跑）→ Agent Memory 轨。

## 最近工作（2026-08-09）— OmniMemEval 评测框架评估（研究任务，未改代码）

- 克隆 `MemTensor/OmniMemEval` 到 /tmp/OmniMemEval（3.7 万行 Python，浅克隆），评估其作为 VatBrain 记忆能力评测入口的可行性。
- 结论：工程/架构/文档质量高（adapter 层 15 个 backend、六阶段 pipeline、checkpoint/replay、LLM-as-judge+多指标）；风险=发布方 MemTensor 是 MemOS 母公司且 MemOS 全项第一（利益冲突需复核 adapter 配置）、复现分与官方分差距大、PersonaMem v2 多数 backend 贴近随机。
- **对 VatBrain 的启示**：User Memory 线可写 adapter（add/search/delete）评测 VatBrain；**HaluMem 与 ConflictResolver/Pitfall/decay 设计高度契合**，最适合先测。注意：benchmark 全英文对话、search() 需适配成纯文本 top-k。
- 待用户决策：是否写正式评估 artifact / 是否评估"VatBrain adapter"工作量。

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
