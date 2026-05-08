# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: v0.2 Phase 2-6 完成 + Code Review 修复
- **语言**: Go (go 1.25.5)
- **版本定位**: v0.2 实现完毕，测试通过

## 最近工作（2026-05-08）— v0.2 Phase 2-4 Code Review 修复

### 已修复的 Bug

1. **P0: Reconsolidation TotalWeightDelta 总是 0**
   - 原因：在计算 delta 前本地 `ep.Weight` 已被修改
   - 修复：在三个路径（processEpisodic, processSemantic, processPitfall）中保存 `oldWeight` 后再计算 delta
   - 文件：`internal/core/reconsolidation_engine.go`

2. **P0: PitfallExtractor 未注入到 ConsolidationEngine**
   - 原因：`app.New()` 中 `PitfallExtractor` 字段从未赋值
   - 修复：在 `app.go` 中创建 `PitfallExtractor` 并赋值到 `consolidation.PitfallExtractor`
   - 文件：`internal/app/app.go`

3. **P1: feedback_handler.go 冗余存储查询 + 错误日志不准确**
   - 修复：提取 `lookupMemory` 方法，一次查询保存结果，避免重复 fetch
   - 修复：错误日志使用最具体的 lookup 错误
   - 文件：`internal/api/feedback_handler.go`

4. **P1: 死代码移除**
   - 删除 `pitfall_extractor.go:482` 的 `var _ = vector.CosineSimilarity`
   - 文件：`internal/core/pitfall_extractor.go`

5. **P1: MaxTracebackHops 未使用**
   - 修复：processSemantic / processPitfall 入口添加 `re.MaxTracebackHops < 1` guard
   - 字段注释明确说明 v0.2 为 1-hop 拓扑，字段保留给未来多跳链
   - 文件：`internal/core/reconsolidation_engine.go`

6. **P2: ApplyFeedback 权重上限未限制**
   - 修复：添加上限 `if newWeight > 1 → 1`，与 `clampWeight` 保持 [0,1] 一致
   - 更新 4 个相关测试用例（Used, Corrected, CorrectedByUser, Confirmed, ConsecutiveCorrections）
   - 文件：`internal/core/attribution.go`, `internal/core/attribution_test.go`

### 验证结果

- `go build ./...` ✅
- `go vet ./...` ✅
- `go test ./internal/core/...` ✅ (全部 Phase 1-6 测试)
- `go test ./internal/mcp/...` ✅
- 集成测试（e2e/smoke）需要 Neo4j+pgvector 不可用，跳过

## 当前文件变更清单

| 文件 | 变更 |
|------|------|
| `internal/core/reconsolidation_engine.go` | TotalWeightDelta 修复 + MaxTracebackHops guard |
| `internal/core/attribution.go` | 权重上限 [0,1] |
| `internal/core/attribution_test.go` | 更新测试以匹配上限行为 |
| `internal/core/pitfall_extractor.go` | 删除死代码 |
| `internal/api/feedback_handler.go` | 提取 lookupMemory，消除冗余查询 |
| `internal/app/app.go` | 注入 PitfallExtractor 到 ConsolidationEngine |

## 已知问题

- 无阻断性问题。所有单元测试通过。
- 集成测试需要 Neo4j+pgvector 可用（CI 环境正常）。
