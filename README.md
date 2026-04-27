# VatBrain — Brain in a Vat

> AI Agent 记忆增强系统。借鉴人脑的多级衰减、关联检索和睡眠整合机制，
> 为 AI Agent 提供"图+向量"复合记忆存储、情境感知检索、权重衰减和睡眠整合能力。

**"缸中之脑"（Brain in a Vat）**：AI Agent 的记忆系统是一个泡在营养液（数据）里的大脑——它接收代码仓库、用户对话、工具输出等信号，在自己的缸中建立知识图谱、形成记忆、做出预测，从不直接触碰外部现实。

---

## 架构概览

```
┌─────────────────────────────────────────┐
│              AI Agent (Claude etc.)       │
│    MCP Protocol ── HTTP API              │
├─────────────────────────────────────────┤
│  Write Pipeline:                         │
│    Significance Gate → Embed →           │
│    Pattern Separation → Neo4j + pgvector │
│                                          │
│  Search Pipeline:                        │
│    Contextual Gating → Semantic Ranking  │
│                                          │
│  Sleep Consolidation:                    │
│    Scan → Cluster → Extract → Backtest   │
├─────────────────────────────────────────┤
│  Neo4j (Graph)  │  pgvector (Vector)     │
│  Redis (Cache)  │  MinIO (Snapshots)     │
└─────────────────────────────────────────┘
```

### 多级记忆存储

| 层级 | 存储 | 说明 |
|-----|------|------|
| Working Memory | Redis LIST | 最近 20 条摘要，FIFO 循环 |
| Episodic Memory | Neo4j + pgvector | 情境记忆，图+向量复合存储 |
| Semantic Memory | Neo4j | 整合提炼出的规则/模式 |
| Pitfall Memory | Neo4j（v0.2+） | 错误记忆独立建模 |

### 核心引擎

| 引擎 | 隐喻 | 功能 |
|-----|------|------|
| Significance Gate | 显著性门控 | 写入长时记忆前的四条件过滤 |
| Pattern Separation | 可分离性判别 | 区分"相似但无关"的记忆，防止错误合并 |
| Weight Decay | 权重衰减 | Recency-Weighted Frequency + 双参照时间衰减 |
| Two-Stage Retrieval | 两阶段检索 | 情境过滤（硬约束）→ 语义排序（向量相似度） |
| Sleep Consolidation | 睡眠整合 | 异步扫描→聚类→提炼规则→回测→持久化 |

---

## 快速开始

### 前置依赖

- Go 1.22+
- Docker + Docker Compose

### 1. 启动基础设施

```bash
docker-compose up -d
```

启动 Neo4j、PostgreSQL/pgvector、Redis、MinIO 四个服务。

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
go test ./...        # 全部测试（55 个）
go test ./internal/core/  # 核心引擎测试
go test ./internal/mcp/   # MCP 集成测试
```

---

## API 端点

所有端点位于 `/api/v0` 下：

| 方法 | 路径 | 说明 |
|-----|------|------|
| `POST` | `/memories/episodic` | 写入情境记忆 |
| `POST` | `/memories/search` | 两阶段检索 |
| `POST` | `/memories/{id}/feedback` | 行为反馈（权重更新） |
| `POST` | `/memories/{id}/touch` | 记录检索命中 |
| `GET` | `/memories/{id}/weight` | 查看权重明细 |
| `POST` | `/consolidation/trigger` | 触发睡眠整合 |
| `GET` | `/consolidation/runs/{id}` | 查看整合运行状态 |
| `GET` | `/health` | 健康检查 |

## MCP Tools

| Tool | 说明 |
|------|------|
| `write_memory` | 写入情境记忆（经显著性门控） |
| `search_memories` | 两阶段检索 |
| `trigger_consolidation` | 触发睡眠整合 |
| `get_memory_weight` | 查看权重明细 |
| `touch_memory` | 记录检索命中 |
| `health_check` | 健康检查 |

---

## 项目结构

```
vatbrain/
├── cmd/
│   ├── vatbrain/main.go           # HTTP API Server 入口
│   └── vatbrain-mcp/main.go       # MCP stdio Server 入口
├── internal/
│   ├── api/                       # HTTP handlers (go-chi/v5)
│   ├── app/                       # 共享初始化逻辑
│   ├── config/                    # 环境变量配置
│   ├── core/                      # 核心引擎（5 个）
│   ├── db/                        # 数据库连接层
│   │   ├── neo4j/                 # Neo4j 图数据库
│   │   ├── pgvector/              # pgvector 向量数据库
│   │   ├── redis/                 # Redis 缓存
│   │   └── minio/                 # MinIO 对象存储
│   ├── embedder/                  # Embedding 接口 + 实现
│   ├── mcp/                       # MCP Server + 6 tools
│   └── models/                    # 数据模型 (Go structs)
├── docs/                          # 设计文档
│   ├── DESIGN_PRINCIPLES.md       # 设计基石
│   ├── ROADMAP.md                 # 版本路线图
│   └── v0.1/00-design.md         # v0.1 技术方案
├── scripts/init_db.sh             # 数据库初始化
├── docker-compose.yml             # 基础设施
└── CLAUDE.md                      # Agent 工作纪律
```

---

## 路线图

| 版本 | 主题 | 状态 |
|-----|------|------|
| **v0.1** | 最小闭环 — 图+向量写入、检索、衰减、整合 | 🚧 开发中 |
| v0.2 | 记忆进化 — Pitfall 独立建模、记忆再巩固 | 规划中 |
| v0.3 | 预测与主动 — 风险预测、反事实推理 | 规划中 |
| v1.0 | 多智能体记忆 — 跨 Agent 共享、冲突协调 | 规划中 |

详见 [ROADMAP.md](docs/ROADMAP.md)。

---

## 术语规范

本项目使用脑科学隐喻命名：

| 概念 | 术语 | 禁止 |
|-----|------|------|
| 情境记忆 | Episodic | — |
| 语义记忆 | Semantic | — |
| 错误记忆 | Pitfall | ErrorLog, BugRecord |
| 记忆整合 | Consolidation | Merge, Compress |
| 权重衰减 | Decay | Delete, Remove |
| 情境过滤 | Contextual Gating | Pre-filter |
| 显著性门控 | Significance Gate | Importance Filter |
| 可分离性判别 | Pattern Separation | Dedup |
| 冷却阈值 | Cooling Threshold | Delete Threshold |

---

## 许可证

MIT
