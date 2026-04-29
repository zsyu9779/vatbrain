# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: v0.1.1 存储可插拔重构 Phase 3 完成
- **语言**: Go (go 1.25.5)
- **版本定位**: MemoryStore 接口统一存储层，SQLite 零依赖启动

## 最近工作（2026-04-29）— v0.1.1 Phase 3 完成

### 已完成：引擎 + API/MCP 层适配

- **Engine 层**：ConsolidationEngine.Run() 和 LinkOnWrite 不再直接依赖 Neo4j driver，接受 MemoryStore 接口
- **API 层**（6个 handler 全部重写）：write、search、consolidation、touch、feedback、health
- **MCP 层**（6个 tool 全部重写）：write_memory、search_memories、trigger_consolidation、get_memory_weight、touch_memory、health_check
- **去重**：移除所有重复的 tokenOverlap/tokenizeLower/isAlphaNum（统一用 core.TokenOverlap）
- **去重**：移除 API 和 MCP 的重复 helper 函数（clampWeight、feedbackDelta、stringFromMeta）
- **共享**：`internal/api/helpers.go` 放置 clampWeight、feedbackDelta
- **测试适配**：MCP 测试 minimalApp 已添加 Store + WorkingMemory；e2e/smoke 测试标记 TODO 等待 Phase 4

### SQLite Store 增强
- WriteEpisodic 改为 INSERT OR REPLACE（支持 merge 场景的幂等写入）

### 编译/测试状态
- `go vet ./...` 清洁
- `go test ./...` 全部通过（core、mcp、sqlite、vector、tests）
- `go build ./...` 无错误

## 下一步

- **Phase 4**：Neo4j+pgvector Store 适配器（`internal/store/neo4j_pg/`），让现有集成测试恢复运行
- **e2e/smoke 测试恢复**：标记为 TODO 的测试需在 Phase 4 后恢复

## 已知问题

- e2e_test.go 和 smoke_test.go 中的 LinkOnWrite 和 Consolidation 集成测试已标记 TODO，需 Phase 4 Neo4j Store adapter 后恢复
