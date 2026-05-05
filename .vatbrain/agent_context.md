# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: v0.2 Phase 1 完成，进入 Phase 2
- **语言**: Go (go 1.25.5)
- **版本定位**: v0.2 实现中

## 最近工作（2026-05-05）— v0.2 Phase 1 完成

### 已完成

1. **PitfallMemory 模型**：`internal/models/pitfall_memory.go`
   - `PitfallMemory` struct（所有字段对齐 00-design.md 2.3 节）
   - `EntityType` enum / `RootCause` enum
   - Pitfall 边类型 structs + sentinel errors

2. **MemoryStore 接口扩展**：`internal/store/memory_store.go`
   - 7 个 Pitfall 方法 + `UpdateSemanticWeight`
   - `PitfallSearchRequest` struct（双键匹配）

3. **SQLite Pitfall 实现**：`internal/store/sqlite/pitfall.go` + schema 更新
   - `pitfall_memories` 表 + `pitfall_edges` 表 + 索引
   - `SaveConsolidationRun`/`GetConsolidationRun` 支持新字段（pitfalls_extracted, pitfalls_merged, pitfalls_persisted, rules_error, pitfall_error）

4. **Neo4j+pgvector Pitfall 实现**：`internal/store/neo4jpg/pitfall.go` + store.go 更新
   - `(:PitfallMemory)` 节点 + UNIQUE 约束
   - `SaveConsolidationRun`/`GetConsolidationRun` 支持新字段
   - pgvector 签名 embedding 双写

5. **In-Memory Pitfall 实现**：`internal/store/memory/pitfall.go` + Store struct 扩展

6. **Config 扩展**：`PitfallDecayConfig`（LambdaDecay=0.15, AlphaExperience=0.008, BetaActivity=0.03, CoolingThreshold=0.005）+ 环境变量

7. **ConsolidationRunResult 扩展**：`internal/models/api.go` 新增 pitfall 统计字段

## 下一步

- **Phase 2**: PitfallExtractor + ConsolidationEngine 并行化
  1. `internal/core/pitfall_extractor.go` — PitfallExtractor（HAC 子聚类 + LLM prompt）
  2. `internal/core/consolidation_engine.go` — 重构 Run 为并行双线（语义规则 + Pitfall）
  3. `internal/core/link_on_write.go` — TRIGGERED_PITFALL 边关联检查

## 已知问题

- 无阻断性问题。go build / go vet / go test 全部通过。
