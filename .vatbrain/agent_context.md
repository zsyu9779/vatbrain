# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: Hermes 集成 Phase 1 — hermes Watcher 适配器（实施中，未动笔）
- **语言**: Go (go 1.25.5)
- **分支**: `feature/agent-memory-watcher`（已推送，工作树干净）
- **交接文档**: `docs/HERMES_INTEGRATION_HANDOFF.md` §0 检查点 = 本机到新机器的入口

## 最近工作（2026-08-08）— Hermes 集成开工：提交切分 + F1-F3 修复

### 本次完成

1. **提交切分 A→D 全部完成并推送**（origin/feature/agent-memory-watcher）：
   - A: `dbf41a1` watcher 推送（新建 upstream）
   - B: `cdbef81` 测试文件 + coverage（20 文件，+5407 行）
   - C: `bc70988` neo4jpg store/pitfall 重构 + mcp ClampWeight 导出
   - D: `5b36f8d` 文档组（EVOLUTION_PLAN / HERMES_INTEGRATION* / tech-specs / AGENTS.md）

2. **F1 CJK 相似度修复**（`1712604`）：
   - `SignificanceGate.Evaluate` 新增 ctx 参数 + `Embedder`/`EmbedSimilarityThreshold` 字段
   - `LinkOnWrite(ctx, emb, s, ...)` 新增 embedder 参数；`linkSimilarity` 优先 embedding 余弦（0.7），无信号回退 token Jaccard（0.15）
   - 新增助手 `embeddingSimilarity`/`vectorHasMagnitude`；测试专用 `runeEmbedder{}`
   - 中文回归测试：gate 跨周期、RELATES_TO 中文边、不相干中文负例

3. **F2 language 软权重**（`2ae7e54`）：
   - language 移出硬约束（project_id 保留），`HardFilterResult` 加 Language 字段
   - 同语言 +10% 软权重；验收测试 `TestRetrievalEngine_CrossLanguageHit`

4. **F3 backtest 橡皮图章修复**（`8d2a5db`）：
   - `backtest(ctx, emb, cl)`：无 LLM 时 embedding 一致性回测（rule vs 簇内 episodics 平均余弦）
   - 无信号（nil/stub/LLM 乱码）→ 0.5 恒值，永不落库
   - 验收 `TestConsolidationEngine_Run_NoSignal_NoRulesPersisted`

### 当前状态

- `go test ./internal/...` 全绿；vet 干净；tests/ 4 个基础设施 E2E 失败为预存在
- Phase 1（hermes 适配器）设计已读完：文件格式、Provider 接口、装配点、去重设计（详见 HANDOFF §0）
- 关键 API 变更清单在 HANDOFF §0（新会话必读）

## 下一步（新机器续作）

1. 实现 `internal/watcher/adapters/hermes.go` + `hermes_test.go`（含中文用例）
2. `buildWatcherProviders` 的 "all" 名单加 "hermes"；WatcherConfig 加 `HermesHomeDir`（env `VATBRAIN_WATCHER_HERMES_HOME`，默认 `~/.hermes`）
3. 验收：`~/.hermes/memories/MEMORY.md` 写入 5 分钟内可检索、重复轮询无重复
4. 每 Phase 独立 commit，验收不过就停

## 已知问题

- 无阻断性问题
- OpenCode 适配器仍为骨架（`internal/watcher/adapters/opencode.go:61` TODO，v0.2.1 GA 待办）
- hermes MEMORY.md 为整体原子重写 → 条目 SourceURI 需稳定设计（handoff §0 有建议）
