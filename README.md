# VatBrain — Brain in a Vat

> AI Agent 记忆增强系统 / AI Agent Memory Augmentation System

[中文](#中文) | [English](#english)

**"缸中之脑"（Brain in a Vat）**：AI Agent 的记忆系统是一个泡在营养液（数据）里的大脑——它接收代码仓库、用户对话、工具输出等信号，在自己的缸中建立知识图谱、形成记忆、做出预测，从不直接触碰外部现实。

*AI agents live in a vat of data. VatBrain is their memory system — graph + vector storage, contextual retrieval, weight decay, and sleep consolidation, inspired by human memory neuroscience.*

---

## 架构概览 / Architecture

```
┌──────────────────────────────────────────────────────┐
│                AI Agent (Claude etc.)                 │
│      MCP Protocol ── HTTP REST API                   │
├──────────────────────────────────────────────────────┤
│  Write Pipeline:                                     │
│    Significance Gate → Embed → Pattern Separation    │
│    → Neo4j + pgvector + LinkOnWrite (v0.2)           │
│                                                      │
│  Search Pipeline:                                    │
│    Contextual Gating → Semantic Ranking              │
│    → Pitfall Injection (v0.2)                        │
│                                                      │
│  Sleep Consolidation (parallel, v0.2):               │
│    Rule Line:    Scan → Cluster → Extract → Backtest │
│    Pitfall Line: Filter → HAC Sub-Cluster → LLM → Dedup │
│                                                      │
│  Reconsolidation (v0.2):                             │
│    Correct → Trace DERIVED_FROM → Boost sources      │
├──────────────────────────────────────────────────────┤
│  Neo4j (Graph)  │  pgvector (Vector)                 │
│  Redis (Cache)  │  MinIO (Snapshots)                 │
└──────────────────────────────────────────────────────┘
```

### 多级记忆存储 / Memory Tiers

| 层级 | 存储 | 说明 |
|-----|------|------|
| Working Memory | Redis LIST | 最近 20 条摘要，FIFO 循环 |
| Episodic Memory | Neo4j + pgvector | 情境记忆，图+向量复合存储 |
| Semantic Memory | Neo4j | 整合提炼出的规则/模式 |
| **Pitfall Memory** (v0.2) | Neo4j | 错误记忆独立建模，entity 锚点 |

### 核心引擎 / Core Engines

| 引擎 | 隐喻 | 功能 |
|-----|------|------|
| Significance Gate | 显著性门控 | 写入长时记忆前的四条件过滤 |
| Pattern Separation | 可分离性判别 | 区分"相似但无关"的记忆 |
| Weight Decay | 权重衰减 | Recency-Weighted Frequency + 双参照时间衰减 |
| Two-Stage Retrieval | 两阶段检索 | 情境过滤（硬约束）→ 语义排序（向量相似度）|
| Sleep Consolidation | 睡眠整合 | 规则提取 + Pitfall 提取并行化 |
| **Pitfall Extractor** (v0.2) | 错误提取 | HAC 子聚类 + LLM 结构化 → 独立 Pitfall 节点 |
| **Reconsolidation** (v0.2) | 记忆再巩固 | DERIVED_FROM 溯源链反向传播纠错信号 |
| **Attribution** (v0.2) | 行为归因 | 检索后行为 → 权重 delta 纯函数 |

---

## 快速开始 / Quick Start

### 前置依赖 / Prerequisites

- Go 1.22+
- Docker + Docker Compose

### 1. 启动基础设施

```bash
docker-compose up -d
```

### 2. 初始化数据库

```bash
bash scripts/init_db.sh
```

### 3. 启动 HTTP API Server

```bash
go run ./cmd/vatbrain/
```

API 监听 `:8080`。

### 4. 配置 MCP Server（Claude Code 集成）

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

### 5. 运行测试

```bash
go test ./...                   # 全部测试
go test ./internal/core/        # 核心引擎测试（45+）
go test ./internal/mcp/         # MCP 集成测试
```

---

## API 端点 / Endpoints

所有端点位于 `/api/v0` 下：

| 方法 | 路径 | 说明 |
|-----|------|------|
| `POST` | `/memories/episodic` | 写入情境记忆 |
| `POST` | `/memories/search` | 两阶段检索（含 pitfall 注入） |
| `POST` | `/memories/{id}/feedback` | 行为反馈 + 再巩固 |
| `POST` | `/memories/{id}/touch` | 记录检索命中 |
| `GET` | `/memories/{id}/weight` | 查看权重明细 |
| `POST` | `/pitfalls/search` | 🆕 v0.2 搜索 Pitfall 记忆 |
| `POST` | `/consolidation/trigger` | 触发睡眠整合 |
| `GET` | `/consolidation/runs/{id}` | 查看整合运行状态 |
| `GET` | `/health` | 健康检查 |

## MCP Tools

| Tool | 说明 |
|------|------|
| `write_memory` | 写入情境记忆（经显著性门控） |
| `search_memories` | 两阶段检索 + pitfall 注入 |
| `search_pitfalls` | 🆕 v0.2 搜索 Pitfall 记忆 |
| `trigger_consolidation` | 触发睡眠整合 |
| `get_memory_weight` | 查看权重明细 |
| `touch_memory` | 记录检索命中 |
| `health_check` | 健康检查 |

---

## 项目结构 / Project Structure

```
vatbrain/
├── cmd/
│   ├── vatbrain/main.go           # HTTP API Server 入口
│   └── vatbrain-mcp/main.go       # MCP stdio Server 入口
├── internal/
│   ├── api/                       # HTTP handlers (go-chi/v5)
│   ├── app/                       # 共享初始化逻辑
│   ├── config/                    # 环境变量配置
│   ├── core/                      # 核心引擎
│   │   ├── significance_gate.go
│   │   ├── pattern_separation.go
│   │   ├── weight_decay.go
│   │   ├── retrieval_engine.go
│   │   ├── consolidation_engine.go
│   │   ├── pitfall_extractor.go   # 🆕 v0.2
│   │   ├── reconsolidation_engine.go # 🆕 v0.2
│   │   ├── attribution.go         # 🆕 v0.2
│   │   └── link_on_write.go       # 🆕 v0.2
│   ├── db/                        # 数据库连接层
│   │   ├── neo4j/                 # Neo4j 图数据库
│   │   ├── pgvector/              # pgvector 向量数据库
│   │   ├── redis/                 # Redis 缓存
│   │   └── minio/                 # MinIO 对象存储
│   ├── embedder/                  # Embedding 接口 + 实现
│   ├── llm/                       # LLM Client 接口 (v0.2)
│   ├── mcp/                       # MCP Server + 7 tools
│   ├── models/                    # 数据模型
│   │   ├── episodic_memory.go
│   │   ├── semantic_memory.go
│   │   ├── pitfall_memory.go      # 🆕 v0.2
│   │   └── common.go
│   ├── store/                     # 存储抽象 + 多后端
│   └── vector/                    # 向量操作工具
├── docs/                          # 设计文档
│   ├── DESIGN_PRINCIPLES.md       # 设计基石
│   ├── ROADMAP.md                 # 版本路线图
│   └── v0.2/tech-specs/          # 🆕 v0.2 技术方案
├── scripts/init_db.sh             # 数据库初始化
├── docker-compose.yml             # 基础设施
└── CLAUDE.md                      # Agent 工作纪律
```

---

## 路线图 / Roadmap

| 版本 | 主题 | 状态 |
|-----|------|------|
| v0.1 | 最小闭环 — 图+向量写入、检索、衰减、整合 | ✅ 完成 |
| **v0.2** | **记忆进化 — Pitfall 独立建模、再巩固、行为归因** | ✅ 完成 |
| v0.3 | 预测与主动 — 风险预测、反事实推理 | 🧭 规划中 |
| v1.0 | 多智能体记忆 — 跨 Agent 共享、冲突协调 | 🧭 规划中 |

详见 [ROADMAP.md](docs/ROADMAP.md)。

---

## 术语规范 / Terminology

本项目使用脑科学隐喻命名 / Neuroscience-inspired naming:

| 概念 | 术语 | 禁止 |
|-----|------|------|
| 情境记忆 | Episodic | — |
| 语义记忆 | Semantic | — |
| 错误记忆 | Pitfall | ErrorLog, BugRecord |
| 记忆整合 | Consolidation | Merge, Compress |
| 记忆再巩固 | Reconsolidation | Re-merge, Re-extract |
| 权重衰减 | Decay | Delete, Remove |
| 行为归因 | Attribution | Reward, Scoring |
| 情境过滤 | Contextual Gating | Pre-filter |
| 显著性门控 | Significance Gate | Importance Filter |
| 可分离性判别 | Pattern Separation | Dedup |
| 冷却阈值 | Cooling Threshold | Delete Threshold |

---

## 许可证 / License

MIT

---

## 中文

VatBrain（缸中之脑）是一个通用的、状态无关的 AI Agent 记忆增强系统。借鉴人脑的多级衰减、关联检索和睡眠整合机制，为 AI Agent 提供"图+向量"复合记忆存储、情境感知检索、权重衰减、错误记忆独立建模和睡眠整合能力。

### 核心设计理念

- **多级记忆**：工作记忆（Redis）→ 情境记忆（Neo4j+pgvector）→ 语义记忆（Neo4j），v0.2 新增 Pitfall 错误记忆
- **情境感知检索**：两阶段检索（Contextual Gating → Semantic Ranking），v0.2 新增 entity 锚点的 Pitfall 注入
- **权重衰减**：基于 Recency-Weighted Frequency 模型的双参照时间衰减
- **睡眠整合**：规则提取（LLM / 字符串拼接）+ Pitfall 提取（HAC 聚类 + LLM 结构化），并行执行
- **记忆再巩固**：每次检索都是潜在的写入——纠错信号沿 DERIVED_FROM 溯源链反向传播
- **行为归因**：检索后的使用/纠正/确认/忽略行为驱动权重增量

### 技术栈

- **语言**：Go 1.22+
- **HTTP 框架**：go-chi/v5
- **图数据库**：Neo4j 5.x
- **向量数据库**：pgvector/pg16
- **缓存**：Redis
- **对象存储**：MinIO
- **LLM**：Claude API（HTTP 直调）
- **MCP 协议**：MCP Server（对接 AI Agent）

### v0.2 新增特性

| 特性 | 说明 |
|------|------|
| Pitfall Memory | 错误记忆独立建模，entity_id 锚点，HAC 子聚类 + LLM 结构化提取 |
| Reconsolidation | 溯源链反向传播纠错信号，三种记忆类型完整支持 |
| Attribution | 行为→权重 delta 纯函数，支持 used/corrected/confirmed/ignored |
| Error-aware Search | 检索时自动注入关联 entity 的 Pitfall 记忆 |
| Pitfall API + MCP | `POST /api/v0/pitfalls/search` 端点 + `search_pitfalls` MCP 工具 |
| Consolidation 并行化 | 规则提取与 Pitfall 提取并行执行 |

---

## English

VatBrain is a general-purpose, state-agnostic memory augmentation system for AI agents. Inspired by the brain's multi-level decay, associative retrieval, and sleep consolidation mechanisms, it provides graph+vector composite memory storage, context-aware retrieval, weight decay, independent error memory modeling, and sleep consolidation.

### Core Design Principles

- **Multi-tier Memory**: Working Memory (Redis) → Episodic (Neo4j+pgvector) → Semantic (Neo4j), with Pitfall error memory added in v0.2
- **Context-aware Retrieval**: Two-stage pipeline (Contextual Gating → Semantic Ranking), with entity-anchored Pitfall injection in v0.2
- **Weight Decay**: Dual-reference temporal decay based on Recency-Weighted Frequency model
- **Sleep Consolidation**: Rule extraction (LLM / string concat) + Pitfall extraction (HAC clustering + LLM structuring), executed in parallel
- **Reconsolidation**: Every retrieval is a potential write — correction signals back-propagate through DERIVED_FROM traceability chains
- **Behavior Attribution**: Post-retrieval actions (used/corrected/confirmed/ignored) drive weight increments via pure-function deltas

### Tech Stack

- **Language**: Go 1.22+
- **HTTP Framework**: go-chi/v5
- **Graph DB**: Neo4j 5.x
- **Vector DB**: pgvector/pg16
- **Cache**: Redis
- **Object Storage**: MinIO
- **LLM**: Claude API (direct HTTP)
- **MCP Protocol**: MCP Server (AI Agent integration)

### v0.2 New Features

| Feature | Description |
|------|------|
| Pitfall Memory | Independent error memory modeling with entity_id anchors, HAC sub-clustering + LLM structuring |
| Reconsolidation | Back-propagation of correction signals through DERIVED_FROM chains, supporting all 3 memory types |
| Attribution | Behaviour → weight delta as a pure function (used/corrected/confirmed/ignored) |
| Error-aware Search | Automatic Pitfall injection for entity-anchored queries |
| Pitfall API + MCP | `POST /api/v0/pitfalls/search` endpoint + `search_pitfalls` MCP tool |
| Parallel Consolidation | Rule extraction and Pitfall extraction run concurrently |
