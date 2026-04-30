# Agent Context - 当前工作上下文

> 每次交互必读必写

## 项目状态

- **阶段**: v0.1.1 存储可插拔重构 Phase 4 完成
- **语言**: Go (go 1.25.5)
- **版本定位**: 三个 MemoryStore 后端全部就绪 (SQLite, Neo4j+pgvector, In-Memory)

## 最近工作（2026-04-30）— v0.1.1 Phase 4 完成

### 已完成：Neo4j+pgvector Store 适配器

- **新包**: `internal/store/neo4jpg/` — MemoryStore 接口的 Neo4j+pgvector 实现 (~530 行)
- **15 个接口方法全部实现**：WriteEpisodic (Neo4j CREATE + best-effort pgvector insert)、SearchEpisodic (embedding: pgvector→Neo4j / no embedding: Cypher)、GetEpisodic、TouchEpisodic、UpdateEpisodicWeight、MarkObsolete、WriteSemantic、SearchSemantic、GetSemantic、CreateEdge (fmt.Sprintf 处理动态 rel type)、GetEdges (方向+类型过滤)、ScanRecent、SaveConsolidationRun、GetConsolidationRun、HealthCheck、Close
- **Schema 自动建立**：uniqueness constraints on (:EpisodicMemory), (:SemanticMemory), (:ConsolidationRun)
- **Store 工厂更新**：`NewMemoryStore` 接受 `*neo4j.Client` + `*pgvector.Client` 参数，连接 `neo4j+pgvector` 场景
- **App 启动重排**：neo4j+pgvector 模式先创建客户端（hard-fail），再创建 store；`storeOwnsDB` 标记防止 double-close
- **e2e 测试全面改造**：Phase 1-5 全部通过 Store 接口运行 — WriteEpisodic、LinkOnWrite、SearchEpisodic、ConsolidationEngine.Run
- **smoke 测试更新**：TestSmoke_LinkOnWrite 使用 Store 接口并验证 RELATES_TO 边

### 编译/测试状态
- `go build ./...` 清洁
- `go vet ./...` 清洁
- `go test ./internal/... -short` 全部通过（core、mcp、sqlite、vector）

## 下一步

- v0.2 features (Pitfall memory, advanced consolidation, etc.)
- 集成测试需要 docker-compose services 运行（本地测试）

## 已知问题

- 无
