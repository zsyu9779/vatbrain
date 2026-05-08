# VatBrain — Brain in a Vat

> AI Agent 记忆增强系统 / AI Agent Memory Augmentation System

[English](#english) | [中文](#中文)

---

## English

VatBrain is a general-purpose, state-agnostic memory augmentation system for AI agents. Inspired by the brain's multi-level decay, associative retrieval, and sleep consolidation mechanisms, it provides **graph + vector composite memory storage**, **context-aware retrieval**, **weight decay**, **independent error memory modeling**, and **sleep consolidation**.

### Architecture

```
┌──────────────────────────────────────────────────────┐
│                AI Agent (Claude etc.)                 │
│      MCP Protocol  ──  HTTP REST API                 │
├──────────────────────────────────────────────────────┤
│  Write: Significance Gate → Embed → Pattern Sep.     │
│         → Neo4j + pgvector + LinkOnWrite             │
│  Search: Contextual Gating → Semantic Ranking        │
│          → Pitfall Injection (v0.2)                  │
│  Consolidation (parallel, v0.2):                     │
│    Rule line:    Scan → Cluster → Extract → Backtest │
│    Pitfall line: Filter → HAC → LLM → Dedup         │
│  Reconsolidation (v0.2):                             │
│    Correct → Trace DERIVED_FROM → Boost sources      │
├──────────────────────────────────────────────────────┤
│  Neo4j (Graph)  │  pgvector (Vector)                 │
│  Redis (Cache)  │  MinIO (Snapshots)                 │
└──────────────────────────────────────────────────────┘
```

### Memory Tiers

| Tier | Backend | Description |
|------|---------|-------------|
| Working Memory | Redis LIST | Last 20 summaries, FIFO cycle |
| Episodic Memory | Neo4j + pgvector | Episodic events, graph+vector composite |
| Semantic Memory | Neo4j | Extracted rules & patterns |
| Pitfall Memory (v0.2) | Neo4j | Independent error memory, entity-anchored |

### Core Engines

| Engine | Function |
|--------|----------|
| Significance Gate | Four-condition filter before long-term write |
| Pattern Separation | Distinguish "similar but unrelated" memories |
| Weight Decay | Recency-Weighted Frequency + dual-reference temporal decay |
| Contextual Gating | Hard-constraint filtering before semantic ranking |
| Sleep Consolidation | Rule extraction + Pitfall extraction in parallel |
| Pitfall Extractor (v0.2) | HAC sub-clustering + LLM structuring → Pitfall nodes |
| Reconsolidation (v0.2) | Back-propagate correction signals through DERIVED_FROM chains |
| Attribution (v0.2) | Behaviour → weight delta as a pure function |

### Quick Start

**Prerequisites**: Go 1.22+, Docker + Docker Compose

```bash
# 1. Start infrastructure
docker-compose up -d

# 2. Initialize databases
bash scripts/init_db.sh

# 3. Start HTTP API (listens on :8080)
go run ./cmd/vatbrain/

# 4. Run tests
go test ./...
```

**MCP Server setup** (Claude Code integration):

Add to `.claude/settings.local.json`:

```json
{
  "mcpServers": {
    "vatbrain": {
      "command": "go",
      "args": ["run", "./cmd/vatbrain-mcp/"],
      "cwd": "/path/to/vatbrain"
    }
  }
}
```

### API Endpoints

All under `/api/v0`:

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/memories/episodic` | Write episodic memory |
| `POST` | `/memories/search` | Two-stage retrieval (with pitfall injection) |
| `POST` | `/memories/{id}/feedback` | Record feedback + trigger reconsolidation |
| `POST` | `/memories/{id}/touch` | Record retrieval hit |
| `GET` | `/memories/{id}/weight` | View weight details |
| `POST` | `/pitfalls/search` | Search pitfall memories (v0.2) |
| `POST` | `/consolidation/trigger` | Trigger sleep consolidation |
| `GET` | `/consolidation/runs/{id}` | View consolidation run status |
| `GET` | `/health` | Health check |

### MCP Tools

| Tool | Description |
|------|-------------|
| `write_memory` | Write episodic memory through significance gate |
| `search_memories` | Two-stage retrieval + pitfall injection |
| `search_pitfalls` | Search pitfall memories (v0.2) |
| `trigger_consolidation` | Trigger sleep consolidation |
| `get_memory_weight` | View weight details |
| `touch_memory` | Record retrieval hit |
| `health_check` | Health check |

### Project Structure

```
vatbrain/
├── cmd/
│   ├── vatbrain/main.go
│   └── vatbrain-mcp/main.go
├── internal/
│   ├── api/          # HTTP handlers (go-chi/v5)
│   ├── app/           # Shared bootstrap logic
│   ├── config/        # Env-based configuration
│   ├── core/          # Core engines (8 total)
│   ├── db/            # DB clients (neo4j, pgvector, redis, minio)
│   ├── embedder/      # Embedding interface + implementations
│   ├── llm/           # LLM client interface (v0.2)
│   ├── mcp/           # MCP Server + 7 tools
│   ├── models/        # Data models
│   ├── store/         # Storage abstraction + multi-backend
│   └── vector/        # Vector utilities
├── docs/              # Design documents
├── scripts/           # Init scripts
├── docker-compose.yml
└── CLAUDE.md
```

### Roadmap

| Version | Theme | Status |
|---------|-------|--------|
| v0.1 | Minimal loop — graph+vector write, retrieve, decay, consolidate | ✅ Done |
| v0.2 | Memory evolution — Pitfall, Reconsolidation, Attribution | ✅ Done |
| v0.3 | Prediction — risk scoring, counterfactual reasoning | 🧭 Planned |
| v1.0 | Multi-agent — cross-agent memory sharing, conflict resolution | 🧭 Planned |

See [ROADMAP.md](docs/ROADMAP.md) for details.

### Terminology

This project uses neuroscience metaphors:

| Concept | Term | Avoid |
|---------|------|-------|
| Episodic memory | Episodic | — |
| Semantic memory | Semantic | — |
| Error memory | Pitfall | ErrorLog, BugRecord |
| Memory consolidation | Consolidation | Merge, Compress |
| Memory reconsolidation | Reconsolidation | Re-merge |
| Weight decay | Decay | Delete, Remove |
| Behavior attribution | Attribution | Reward, Scoring |
| Contextual gating | Contextual Gating | Pre-filter |
| Significance gate | Significance Gate | Importance Filter |
| Pattern separation | Pattern Separation | Dedup |
| Cooling threshold | Cooling Threshold | Delete Threshold |

### License

MIT

---

## 中文

VatBrain（缸中之脑）是一个通用的、状态无关的 AI Agent 记忆增强系统。借鉴人脑的多级衰减、关联检索和睡眠整合机制，为 AI Agent 提供**"图+向量"复合记忆存储**、**情境感知检索**、**权重衰减**、**错误记忆独立建模**和**睡眠整合**能力。

### 架构概览

```
┌──────────────────────────────────────────────────────┐
│                AI Agent (Claude 等)                    │
│      MCP Protocol  ──  HTTP REST API                 │
├──────────────────────────────────────────────────────┤
│  写入: 显著性门控 → Embed → 可分离性判别              │
│        → Neo4j + pgvector + LinkOnWrite              │
│  检索: 情境过滤 → 语义排序 → Pitfall 注入 (v0.2)     │
│  睡眠整合（并行, v0.2）:                              │
│    规则线: 扫描 → 聚类 → 提取 → 回测                  │
│    错误线: 过滤 → HAC 子聚类 → LLM → 去重             │
│  记忆再巩固 (v0.2):                                   │
│    纠错 → 溯源 DERIVED_FROM → 增强源记忆               │
├──────────────────────────────────────────────────────┤
│  Neo4j (图)  │  pgvector (向量)                       │
│  Redis (缓存) │  MinIO (对象存储)                     │
└──────────────────────────────────────────────────────┘
```

### 多级记忆存储

| 层级 | 后端 | 说明 |
|------|------|------|
| 工作记忆 | Redis LIST | 最近 20 条摘要，FIFO 循环 |
| 情境记忆 | Neo4j + pgvector | 情境事件，图+向量复合存储 |
| 语义记忆 | Neo4j | 整合提炼的规则与模式 |
| 错误记忆 (v0.2) | Neo4j | 错误记忆独立建模，entity 锚点 |

### 核心引擎

| 引擎 | 功能 |
|------|------|
| 显著性门控 | 写入长时记忆前的四条件过滤 |
| 可分离性判别 | 区分"相似但无关"的记忆，防止错误合并 |
| 权重衰减 | Recency-Weighted Frequency + 双参照时间衰减 |
| 情境过滤 | 语义排序前的硬约束过滤 |
| 睡眠整合 | 规则提取 + Pitfall 提取并行 goroutine |
| Pitfall 提取器 (v0.2) | HAC 子聚类 + LLM 结构化 → 独立 Pitfall 节点 |
| 记忆再巩固 (v0.2) | DERIVED_FROM 溯源链反向传播纠错信号 |
| 行为归因 (v0.2) | 检索后行为 → 权重 delta 纯函数 |

### 快速开始

**前置依赖**: Go 1.22+, Docker + Docker Compose

```bash
# 1. 启动基础设施
docker-compose up -d

# 2. 初始化数据库
bash scripts/init_db.sh

# 3. 启动 HTTP API（监听 :8080）
go run ./cmd/vatbrain/

# 4. 运行测试
go test ./...
```

**MCP Server 配置**（Claude Code 集成）：

在 `.claude/settings.local.json` 中添加：

```json
{
  "mcpServers": {
    "vatbrain": {
      "command": "go",
      "args": ["run", "./cmd/vatbrain-mcp/"],
      "cwd": "/path/to/vatbrain"
    }
  }
}
```

### API 端点

所有端点位于 `/api/v0` 下：

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/memories/episodic` | 写入情境记忆 |
| `POST` | `/memories/search` | 两阶段检索（含 pitfall 注入） |
| `POST` | `/memories/{id}/feedback` | 行为反馈 + 触发再巩固 |
| `POST` | `/memories/{id}/touch` | 记录检索命中 |
| `GET` | `/memories/{id}/weight` | 查看权重明细 |
| `POST` | `/pitfalls/search` | 搜索 Pitfall 记忆 (v0.2) |
| `POST` | `/consolidation/trigger` | 触发睡眠整合 |
| `GET` | `/consolidation/runs/{id}` | 查看整合运行状态 |
| `GET` | `/health` | 健康检查 |

### MCP 工具

| 工具 | 说明 |
|------|------|
| `write_memory` | 写入情境记忆（经显著性门控） |
| `search_memories` | 两阶段检索 + pitfall 注入 |
| `search_pitfalls` | 搜索 Pitfall 记忆 (v0.2) |
| `trigger_consolidation` | 触发睡眠整合 |
| `get_memory_weight` | 查看权重明细 |
| `touch_memory` | 记录检索命中 |
| `health_check` | 健康检查 |

### 项目结构

```
vatbrain/
├── cmd/
│   ├── vatbrain/main.go       # HTTP API Server 入口
│   └── vatbrain-mcp/main.go   # MCP stdio Server 入口
├── internal/
│   ├── api/                   # HTTP handlers (go-chi/v5)
│   ├── app/                   # 共享初始化逻辑
│   ├── config/                # 环境变量配置
│   ├── core/                  # 核心引擎（8 个）
│   ├── db/                    # 数据库连接层 (neo4j, pgvector, redis, minio)
│   ├── embedder/              # Embedding 接口 + 实现
│   ├── llm/                   # LLM Client 接口 (v0.2)
│   ├── mcp/                   # MCP Server + 7 工具
│   ├── models/                # 数据模型
│   ├── store/                 # 存储抽象 + 多后端
│   └── vector/                # 向量操作工具
├── docs/                      # 设计文档
├── scripts/                   # 初始化脚本
├── docker-compose.yml
└── CLAUDE.md
```

### 路线图

| 版本 | 主题 | 状态 |
|------|------|------|
| v0.1 | 最小闭环 — 图+向量写入、检索、衰减、整合 | ✅ 完成 |
| v0.2 | 记忆进化 — Pitfall 独立建模、再巩固、行为归因 | ✅ 完成 |
| v0.3 | 预测与主动 — 风险预测、反事实推理 | 🧭 规划中 |
| v1.0 | 多智能体记忆 — 跨 Agent 共享、冲突协调 | 🧭 规划中 |

详见 [ROADMAP.md](docs/ROADMAP.md)。

### 术语规范

本项目使用脑科学隐喻命名：

| 概念 | 术语 | 禁止 |
|------|------|------|
| 情境记忆 | Episodic | — |
| 语义记忆 | Semantic | — |
| 错误记忆 | Pitfall | ErrorLog, BugRecord |
| 记忆整合 | Consolidation | Merge, Compress |
| 记忆再巩固 | Reconsolidation | Re-merge |
| 权重衰减 | Decay | Delete, Remove |
| 行为归因 | Attribution | Reward, Scoring |
| 情境过滤 | Contextual Gating | Pre-filter |
| 显著性门控 | Significance Gate | Importance Filter |
| 可分离性判别 | Pattern Separation | Dedup |
| 冷却阈值 | Cooling Threshold | Delete Threshold |

### 许可证

MIT
