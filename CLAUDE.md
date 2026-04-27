# VatBrain — Agent 工作纪律

> 本文件是 Claude Code 在此项目中工作的强制规则。每次会话自动加载。

## 项目简介

VatBrain 是一个通用的、状态无关的 AI Agent 记忆增强系统。借鉴人脑的多级衰减、关联检索和睡眠整合机制，为 AI Agent 提供"图+向量"复合记忆存储、情境感知检索、权重衰减、错误记忆独立建模和睡眠整合能力。

**"缸中之脑"（Brain in a Vat）**：AI Agent 的记忆系统是一个泡在营养液（数据）里的大脑——它接收代码仓库、用户对话、工具输出等信号，在自己的缸中建立知识图谱、形成记忆、做出预测，从不直接触碰外部现实。

设计文档：`docs/DESIGN_PRINCIPLES.md`（设计基石）
技术方案：`docs/v0.1/00-design.md`

## 核心工作纪律

### 1. Agent Context 必须维护

**每次改动后**，必须同步更新 `.vatbrain/agent_context.md`。

**开始每个任务前**，必须先读取 `.vatbrain/agent_context.md`，了解当前项目状态。

Agent Context 规则：
- 只保留最近 1-3 次交互的上下文
- 更早的上下文**不删除**，移入 `.vatbrain/agent_context_archive.md`
- Agent Context 不是归档文件，是**工作台**——保持精简、当前、可操作

### 2. 同步流程（每次交互）

```
开始任务前：
  1. 读取 .vatbrain/agent_context.md
  2. 了解当前状态、上次做到哪了、有什么待解决问题

完成任务后：
  1. 将 agent_context.md 中超过 3 次交互的旧内容移入 agent_context_archive.md
  2. 更新 agent_context.md，记录：
     - 本次做了什么
     - 当前项目状态
     - 下一步待做事项
     - 已知问题或决策

每完成并测试完一部分代码后：
  1. git add 相关文件（不提交二进制、.env 等）
  2. git commit -m "描述本次改动"（遵循 Conventional Commits）
  3. commit message 以 Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com> 结尾
```

### 3. 术语规范

本项目使用脑科学隐喻命名，所有代码、文档、API 必须遵循：

| 概念 | 隐喻术语 | 禁止使用 |
|------|---------|---------|
| 情境记忆 | Episodic | — |
| 语义记忆 | Semantic | — |
| 错误记忆 | Pitfall | ErrorLog, BugRecord |
| 记忆整合 | Consolidation | Merge, Compress |
| 权重衰减 | Decay | Delete, Remove |
| 情境过滤 | Contextual Gating | Pre-filter |
| 显著性门控 | Significance Gate | Importance Filter |
| 可分离性判别 | Pattern Separation | Dedup |
| 冷却阈值 | Cooling Threshold | Delete Threshold |
| 睡眠阶段 | Sleep Phase | Batch Job, Cron |

### 4. 技术栈

- 语言：Go 1.22+
- HTTP 框架：go-chi/v5（待 Phase 3 引入）
- 图数据库：Neo4j 5.x（neo4j-go-driver v5）
- 向量数据库：pgvector/pg16（pgx v5 + pgvector-go）
- 缓存：Redis（go-redis v9）
- 对象存储：MinIO（minio-go v7）
- 消息队列：Redis Streams / NATS
- LLM：Claude API（HTTP 直调）
- MCP 协议：MCP Server（对接 AI Agent）

### 5. 目录约定

```
vatbrain/
├── CLAUDE.md                    ← 你正在读的这个文件
├── .vatbrain/
│   ├── agent_context.md         ← 当前工作上下文（必读必写）
│   └── agent_context_archive.md ← 历史上下文归档
├── docs/
│   ├── DESIGN_PRINCIPLES.md     ← 设计基石（不频繁修改）
│   ├── ROADMAP.md               ← 版本路线图
│   └── v0.1/
│       ├── 00-design.md         ← v0.1 设计定稿
│       └── tech-specs/          ← 各模块技术方案（按需）
├── cmd/
│   └── vatbrain/
│       └── main.go              ← 入口
├── internal/
│   ├── api/                     ← HTTP handlers
│   ├── core/                    ← 核心引擎（权重/检索/整合）
│   ├── db/                      ← 数据库连接层
│   │   ├── neo4j/
│   │   ├── pgvector/
│   │   ├── redis/
│   │   └── minio/
│   ├── models/                  ← 数据模型
│   └── mcp/                     ← MCP Server
├── tests/
├── scripts/
│   └── init_db.sh               ← 数据库初始化
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md
```

### 6. 代码风格

- 遵循 Effective Go + go vet，line length 100
- 所有 exported 函数/类型必须有 Go doc comment
- Context 传递：所有 I/O 操作必须接受 `context.Context` 参数
- 错误处理：禁止吞错误（`_ = err`），除非 `// best-effort` 注释
- 测试：标准 `testing` 包 + `testify/assert`，文件命名 `*_test.go`
- 并发：goroutine + channel，注意 context 取消传播

### 7. 文档规范

- 设计文档使用 Markdown，中文为主要语言
- 版本号遵循 [SemVer](https://semver.org)
- 关键决策必须记录在设计文档的"技术决策"表格中
- 每个版本有独立的 `docs/v0.x/` 目录
