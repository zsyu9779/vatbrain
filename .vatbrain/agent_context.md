# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成完成 + backlog issue #1 完成；**Agent Memory 轨 benchmark 全量跑完 = null result（记忆全链路生效但无跨任务迁移价值）；PR #2（OmniMemEval 测评接入）已合入 main** ✅
- **语言**: Go (go 1.25.5) + Python（hermes 插件 + OmniMemEval AgentBench）
- **分支**: `main`（已合入 PR #2 `feature/omnimemeval-benchmark` + 本会话文档同步）
- **远端**: `origin/main` 已同步（PR #2 合入后本地已 merge）
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点 · `docs/v0.3/05-agent-memory-handoff.md`（Agent 轨启动指南）· `docs/v0.3/07/08/09-*`（benchmark 规划/烟测/正式结果）
- **hermes**: `~/.hermes/hermes-agent`（HEAD 52920747e）+ 插件 `~/.hermes/plugins/vatbrain/` 已激活（config.yaml memory.provider: vatbrain）
- **战略决策（2026-08-08，2026-08-10 确认全面转向）**: 存储全面转向 SQLite（modernc.org/sqlite），Neo4j+pgvector/Redis/MinIO 弃用；概念不变（边表=图、BLOB=向量）；新增能力只实现 SQLite 后端；旧后端代码保留兼容、不再投入、待清理

## 最近工作（2026-08-10）— v0.4 草案制定（评测驱动精修 + 价值证明）

- 综合分析三份输入：User Memory 轨评测（LME 74.2/HaluMem 65.0/LoCoMo 57.0，短板=时序 15%/52.6、动态更新 28.9、事实召回 43.6）、Agent 轨 null result（域×协议不匹配、注入=其他题完整 prompt）、遗留 feature（issue #1 余 12 项）→ `docs/v0.4/00-draft.md`
- 核心判断：主线 Measure 未闭环 → v0.4 = 修短板（时序/动态更新/检索增强）+ 价值证明（Agent 轨二轮换域/协议 + 注入压制）
- 范围：P0 六项（时序深入、并发 ingestion、judge 对齐重跑、Update Tracking、RRF+query expansion、延迟微基准）；P1 四项（Agent 轨二轮、Context 精简、DX 数据化、反事实）；暂缓压缩残差/自适应衰减/冷分层；多级存储降级为文件备份；Team Memory 不入
- 5 个待确认决策点（定位、Agent 轨域选择、时序签名范围、反事实排期、准出目标幅度）
- 未提交 benchmark 测试结果（后台 bmdoe0tuy 运行中）

## 最近工作（2026-08-10）— issue #1 状态同步（checkbox 2/20 → 8/20）

- 发现 issue #1 滞后于代码库（agent_context 曾声称"checkbox 已勾选"，实际仅 2/20）。核对代码证据后同步：
  - 新勾选 5 项：v0.4 Public Developer Experience、v0.3 反馈闭环（ProtectionLevel）、v0.2.1 Watcher GA、v0.3.1 评测增强+报告、模块复杂度纳入风险评分
  - 标废弃 1 项：SQLite→Neo4j+pgvector 迁移工具（存储战略转向后无需求）
  - 性能基准套件：留 [ ]，注记"OmniMemEval 质量评测已落地，延迟微基准未单独做"
- 剩余 12 项未勾：P2 六件套（反事实推理/压缩残差/自适应衰减/多级存储/冷分层/情境向量）、P3（Team Memory/v1.x/明确暂缓）、技术债 3 项

## 最近工作（2026-08-10）— 合并 PR #2 + 存储战略文档同步

- 用户合入测评分支 PR #2（`feature/omnimemeval-benchmark` → main）；本地 merge origin/main（仅 agent_context.md 冲突，已解决）
- 存储战略确认"全面转向 SQLite"；同步 4 份文档 + agent_context（commit `f9694b6`）：
  - `docs/v0.1.1/00-storage-refactor-draft.md`：状态"草案"→"已实施"，3 个未决项定案（WAL 默认开、不建反向迁移工具、旧后端只兼容不投入），新增战略决策小节
  - `docs/ROADMAP.md`：v0.1 基础设施交付物标注替代方案；数据留存策略重写为 SQLite 单库（LRU+WAL）；技术债备忘录标记失效项；新增"存储战略决策"小节
  - `docs/DESIGN_PRINCIPLES.md`：头部加实现注记（基石正文不动，旧存储组件名按注记解读）
  - `CLAUDE.md`：§4 技术栈改写（SQLite 为主存储，Go 1.25+）；§5 目录约定标注 db/ 与 neo4jpg 为弃用
- 未动：`docs/v0.1/00-design.md`、`docs/v0.2/00-design.md`（历史定稿文档，保持原样）

## 最近工作（2026-08-10）— Agent Memory 轨全量结果 + 生效验证

- **全量跑完**：vatbrain（478 train + 100 test）+ baseline（100 test）。结果文档：`docs/v0.3/09-agent-benchmark-results.md`。
- **核心结果**：test pass@1 **baseline 21.00% = vatbrain 21.00%（对齐 100 题持平）**。26 个逐题翻转（13 错→对 / 13 对→错）净零 → **null result**。vatbrain train pass@1 = 51.88%。10 样本的 10%→20% 提升**未复现，是假阳性**（omni_2292 全量里两边都错）。
- **⚠️ 生效验证（重要教训）**：记忆**全链路确实生效**。最初因检索错字段——查 `~/.hermes/state.db` 的 `content` 列，而记忆注入实际写入 **`api_content` 列**——误判"未生效"。更正后证据：
  - provider 每会话 spawn + initialize（`~/.hermes/logs/agent.log`，1200+ 行 INFO）
  - vatbrain.db 729 条 episodic（531 任务 prompt + 198 verifier feedback），backup/restore 全 0
  - test 窗口 **1209 条 message 的 `api_content` 含 `<memory-context>` 围栏**（648 任务 prompt + 560 feedback 记忆）
- **根因**：域 × 协议不匹配——注入的是**其他题的完整 prompt + 该题 verifier 答案**，对独立求解的数学题**无可迁移价值**（train/test 互不重叠）。"任务记忆 = 完整 prompt（价值低）"隐患在此协议下成为主导。
- **结论**：null result 是关于 benchmark 协议的，不是关于记忆系统的。要验证记忆价值，需换**迁移信号存在的域/协议**（代码修补、同题不同 seed、重复模式流水线），或多次重复试验区分噪声。
- **提交**：`09-agent-benchmark-results.md` + agent_context 已更新并推送到远端 feature 分支。

## 已知问题

- **Agent 轨实跑速度**：MiniMax-M3 首 token 延迟高（~50s），全量 100 test 题跑 ~9h；挂死靠看门狗杀 >240s 进程兜底。
- **reasoning 域 `--train-split` 数值 bug**：`load_tasks` 数字 split 恒加载 test 集 → 用小集用 `--task` 选 id。
- **SWE-Bench 跳过**：本机无 docker，orange 是 aarch64（官方镜像 x86_64）→ 只跑其余 4 域。
- **deriver summary = 完整任务 prompt**（非压缩洞见）→ 记忆价值待 benchmark 暴露（已在全量中验证为低价值，需检索侧压制任务 prompt）。
- **记忆注入字段**：hermes 把 prefetch 注入写进 `api_content`（API 副本），`content` 列不含注入——验证记忆是否注入必须查 `api_content`。
- 插件 `shutdown()` 时后台 sync 线程可能未完成（best-effort，短会话实测能存活）。
- hermes 条目编辑会因内容哈希变化被当作新条目入库（watcher 语义）。
