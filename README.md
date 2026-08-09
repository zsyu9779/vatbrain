# VatBrain — Pitfall-Aware Memory Layer for Coding Agents

> 面向 coding agent 的错误经验记忆层 / A pitfall-aware memory layer for coding agents.

[English](#english) | [中文](#中文)

---

## English

VatBrain remembers **project decisions, failed fixes, user corrections, and code-entity risks** across Codex, Claude Code, Cursor, OpenCode, and hermes — then injects the right warning **before your agent edits the same code again**.

It is a memory layer, not a memory SDK: it observes your agent's native memory, distills **Pitfalls** (how things broke, not how they should work), and proactively surfaces them before the same mistake recurs.

### Before / After

**Before** — your agent repeats the same mistake across sessions:

```
You: 软路由 OpenClash 覆写脚本把 VLESS 节点改坏了
Agent: 让我用文本 gsub 覆写脚本…（同样的错误再次发生，因为它不记得上次怎么炸的）
```

**After** — VatBrain recalled the pitfall and injected it before the edit:

```
Agent: ⚠ 检测到 clawfeed-push-v3.py 的高风险（曾于 2026-05-08 发生）
       root cause: CONFIG · Fix: 使用 clawfeed-push-v3.py --as bot
Agent: 我用正确脚本推送，不再犯同样的错。
```

### How It Works

```
Observe → Distill Pitfall → Inject Before Edit → Capture Feedback → Reconsolidate → Measure
```

| Stage | What VatBrain does |
|-------|--------------------|
| **Observe** | Agent Memory Watcher passively syncs native memory (Claude Code, Cursor, OpenCode, hermes). hermes is fully integrated via a stdio JSON-RPC provider daemon. |
| **Distill Pitfall** | Sleep consolidation clusters debug/correction episodes → LLM/heuristic extraction → Pitfall nodes with root cause + fix strategy + traceable source. |
| **Inject Before Edit** | `prepare_edit_context` returns relevant memories + top ≤3 pitfalls + risk score before the agent edits files. |
| **Capture Feedback** | Corrections, suppressions, and repeated errors feed back through the significance gate and the Pitfall Workbench state machine (proposed → confirmed → suppressed). |
| **Reconsolidate** | Correction signals propagate through `DERIVED_FROM` chains; weights and trust levels update. |
| **Measure** | An evaluation harness over 20 hand-crafted scenarios measures repeated-error reduction (57.4%) and injection interference (<30%). |

### Quick Start

Two paths. **SQLite local-first is the default — zero external processes**, just Go.

#### Path A — SQLite local-first (fastest, no Docker)

```bash
# 1. Set up (no Docker, no external DB)
export VATBRAIN_STORE_BACKEND=sqlite      # default
export VATBRAIN_SQLITE_PATH=./vatbrain.db # default

# 2. Run tests
go test ./internal/...

# 3. Start the MCP server
go run ./cmd/vatbrain-mcp/
```

Wire the MCP server into Claude Code (`.claude/settings.local.json`):

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

#### Path B — hermes integration (full loop)

VatBrain ships as a hermes memory provider. Install and activate:

```bash
# 1. Build & install the provider daemon + plugin
go build -o ~/.hermes/vatbrain/bin/vatbrain-provider ./cmd/vatbrain-provider
cp -r plugins/vatbrain/ ~/.hermes/plugins/vatbrain/

# 2. Enable in hermes config
#    ~/.hermes/config.yaml:
#    memory:
#      provider: vatbrain
```

Next hermes start logs `Memory provider 'vatbrain' activated`. Turns sync into the graph, prefetch injects recalled context, and the model can call `prepare_edit_context` before edits. See [`docs/DEMO_SCRIPT.md`](docs/DEMO_SCRIPT.md) for a 5-minute demo.

#### Path C — Full backend (Neo4j + pgvector + Redis + MinIO)

```bash
docker-compose up -d
bash scripts/init_db.sh
export VATBRAIN_STORE_BACKEND=neo4j+pgvector
go run ./cmd/vatbrain-mcp/
```

### MCP Tools (13)

| Tool | Description |
|------|-------------|
| `write_memory` | Write episodic memory through the significance gate |
| `search_memories` | Two-stage retrieval + pitfall injection |
| `search_pitfalls` | Search pitfall memories |
| `list_pitfalls` | List pitfalls with status + interference rate (Workbench) |
| `explain_pitfall` | Explain one pitfall incl. traceable source episodes |
| `confirm_pitfall` | Promote a pitfall to confirmed (injectable) |
| `suppress_pitfall` | Suppress a pitfall — the injection escape valve |
| `link_pitfall_entity` | Re-anchor a pitfall to a code entity |
| `prepare_edit_context` | Relevant memories + top pitfalls + risk score for files to edit |
| `trigger_consolidation` | Trigger sleep consolidation |
| `get_memory_weight` | View weight details |
| `touch_memory` | Record a retrieval hit |
| `health_check` | Health check |

### Project Structure

```
vatbrain/
├── cmd/
│   ├── vatbrain-mcp/           # MCP stdio server entry
│   └── vatbrain-provider/      # hermes provider daemon (stdio JSON-RPC)
├── plugins/
│   └── vatbrain/               # hermes MemoryProvider plugin (Python)
├── internal/
│   ├── api/                    # HTTP handlers (go-chi/v5)
│   ├── app/                    # Shared bootstrap
│   ├── config/                 # Env-based configuration
│   ├── core/                   # Engines: gate, decay, separation, retrieval, consolidation, risk
│   ├── embedder/               # Embedding interface + implementations
│   ├── eval/                   # Evaluation harness (20 scenarios)
│   ├── llm/                    # LLM client
│   ├── mcp/                    # MCP Server + 13 tools
│   ├── models/                 # Data models
│   ├── provider/               # hermes provider daemon logic
│   ├── store/                  # Storage abstraction: SQLite / Neo4j+pgvector / memory
│   ├── vector/                 # Vector utilities
│   └── watcher/                # Agent Memory Watcher (multi-adapter)
├── tests/
│   ├── scenarios/              # 20 evaluation scenarios (YAML)
│   └── provider_plugin_smoke.py# hermes plugin end-to-end smoke test
├── docs/                       # Design documents
└── docker-compose.yml
```

### Roadmap

| Version | Theme | Status |
|---------|-------|--------|
| v0.1 / v0.2 | Minimal loop + memory evolution (Pitfall, Reconsolidation, Attribution) | ✅ Done |
| v0.2.1 | Agent Memory Watcher (Claude Code / Cursor / OpenCode / hermes) | ✅ Done |
| v0.2.2 | Pitfall Workbench (state machine + interference rate) | ✅ Done |
| v0.3 | Proactive risk injection (`prepare_edit_context` + risk engine) | ✅ Done |
| v0.3.1 | Evaluation harness (20 scenarios) | ✅ Done |
| v0.4 | Public developer experience | 🧭 Planned |

Remaining ideas live in [Issue #1](https://github.com/zsyu9779/vatbrain/issues/1). See [ROADMAP.md](docs/ROADMAP.md) and [EVOLUTION_PLAN.md](docs/EVOLUTION_PLAN.md).

### Terminology

Neuroscience metaphors drive the API: episodic memory **Episodic**, error memory **Pitfall** (never "ErrorLog"), consolidation **Consolidation** (never "Merge"), weight decay **Decay** (never "Delete"), contextual gating **Contextual Gating** (never "Pre-filter").

### License

MIT

---

## 中文

VatBrain 是面向 coding agent 的**错误经验记忆层**：跨 Codex、Claude Code、Cursor、OpenCode、hermes 记住项目决策、失败修复、用户纠正和代码实体风险，并在 Agent **再次修改相关代码前主动注入避坑提醒**。

它是记忆层而非记忆 SDK：旁路吸收 Agent 原生记忆 → 提炼 **Pitfall**（怎么炸的，而非该怎么工作）→ 在下一次犯同样的错之前主动提醒。

### Before / After

**Before** —— Agent 跨会话重复同样的错误：

```
你: 软路由 OpenClash 覆写脚本把 VLESS 节点改坏了
Agent: 让我用文本 gsub 覆写脚本…（同样的错误再次发生，因为它不记得上次怎么炸的）
```

**After** —— VatBrain 在编辑前检索并注入了 Pitfall：

```
Agent: ⚠ 检测到 clawfeed-push-v3.py 的高风险（曾于 2026-05-08 发生）
       root cause: CONFIG · Fix: 使用 clawfeed-push-v3.py --as bot
Agent: 我用正确脚本推送，不再犯同样的错。
```

### 工作原理

```
Observe → Distill Pitfall → Inject Before Edit → Capture Feedback → Reconsolidate → Measure
```

| 阶段 | VatBrain 做什么 |
|------|----------------|
| **Observe** | Agent Memory Watcher 被动同步原生记忆（Claude Code / Cursor / OpenCode / hermes）；hermes 通过 stdio JSON-RPC provider daemon 全量集成 |
| **Distill Pitfall** | 睡眠整合聚类 debug/纠错情境 → LLM/启发式提取 → Pitfall 节点（根因 + 修复策略 + 可溯源来源） |
| **Inject Before Edit** | `prepare_edit_context` 在 Agent 改文件前返回相关记忆 + 最多 3 条 Pitfall + 风险分 |
| **Capture Feedback** | 纠正/suppress/重复错误经显著性门控与 Pitfall Workbench 状态机（proposed → confirmed → suppressed）回流 |
| **Reconsolidate** | 纠错信号沿 `DERIVED_FROM` 链反向传播，更新权重与可信度 |
| **Measure** | 评测 harness（20 个手工场景）度量重复错误减少率（57.4%）与注入干扰率（<30%） |

### 快速开始

两条路径。**SQLite local-first 为默认——零外部进程**，只要 Go。

#### 路径 A — SQLite 本地优先（最快，无需 Docker）

```bash
# 1. 配置（默认即 sqlite）
export VATBRAIN_STORE_BACKEND=sqlite      # 默认
export VATBRAIN_SQLITE_PATH=./vatbrain.db # 默认

# 2. 运行测试
go test ./internal/...

# 3. 启动 MCP server
go run ./cmd/vatbrain-mcp/
```

接入 Claude Code（`.claude/settings.local.json`）：

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

#### 路径 B — hermes 集成（完整闭环）

VatBrain 以 hermes 记忆 provider 形态交付：

```bash
# 1. 构建并安装 provider daemon + 插件
go build -o ~/.hermes/vatbrain/bin/vatbrain-provider ./cmd/vatbrain-provider
cp -r plugins/vatbrain/ ~/.hermes/plugins/vatbrain/

# 2. 在 hermes 配置启用
#    ~/.hermes/config.yaml:
#    memory:
#      provider: vatbrain
```

下次 hermes 启动出现 `Memory provider 'vatbrain' activated`。轮次同步进图、prefetch 注入回忆、模型可在编辑前调用 `prepare_edit_context`。5 分钟 demo 见 [`docs/DEMO_SCRIPT.md`](docs/DEMO_SCRIPT.md)。

#### 路径 C — Full 后端（Neo4j + pgvector + Redis + MinIO）

```bash
docker-compose up -d
bash scripts/init_db.sh
export VATBRAIN_STORE_BACKEND=neo4j+pgvector
go run ./cmd/vatbrain-mcp/
```

### MCP 工具（13 个）

| 工具 | 说明 |
|------|------|
| `write_memory` | 经显著性门控写入情境记忆 |
| `search_memories` | 两阶段检索 + pitfall 注入 |
| `search_pitfalls` | 搜索 Pitfall 记忆 |
| `list_pitfalls` | 列出 Pitfall（含状态 + 干扰率，Workbench） |
| `explain_pitfall` | 解释单个 Pitfall（含可溯源来源） |
| `confirm_pitfall` | 提升为 confirmed（可注入） |
| `suppress_pitfall` | 抑制——注入的逃生阀 |
| `link_pitfall_entity` | 重锚定到代码实体 |
| `prepare_edit_context` | 改文件前的相关记忆 + 风险 Pitfall + 风险分 |
| `trigger_consolidation` | 触发睡眠整合 |
| `get_memory_weight` | 查看权重明细 |
| `touch_memory` | 记录检索命中 |
| `health_check` | 健康检查 |

### 项目结构

```
vatbrain/
├── cmd/
│   ├── vatbrain-mcp/           # MCP stdio server 入口
│   └── vatbrain-provider/      # hermes provider daemon（stdio JSON-RPC）
├── plugins/
│   └── vatbrain/               # hermes MemoryProvider 插件（Python）
├── internal/
│   ├── api/                    # HTTP handlers（go-chi/v5）
│   ├── app/                    # 共享初始化
│   ├── config/                 # 环境变量配置
│   ├── core/                   # 引擎：门控/衰减/分离/检索/整合/风险
│   ├── embedder/               # Embedding 接口 + 实现
│   ├── eval/                   # 评测 harness（20 场景）
│   ├── llm/                    # LLM client
│   ├── mcp/                    # MCP Server + 13 工具
│   ├── models/                 # 数据模型
│   ├── provider/               # hermes provider daemon 逻辑
│   ├── store/                  # 存储抽象：SQLite / Neo4j+pgvector / memory
│   ├── vector/                 # 向量工具
│   └── watcher/                # Agent Memory Watcher（多适配器）
├── tests/
│   ├── scenarios/              # 20 个评测场景（YAML）
│   └── provider_plugin_smoke.py# hermes 插件端到端冒烟
├── docs/                       # 设计文档
└── docker-compose.yml
```

### 路线图

| 版本 | 主题 | 状态 |
|------|------|------|
| v0.1 / v0.2 | 最小闭环 + 记忆进化（Pitfall / 再巩固 / 行为归因） | ✅ 完成 |
| v0.2.1 | Agent Memory Watcher（Claude Code / Cursor / OpenCode / hermes） | ✅ 完成 |
| v0.2.2 | Pitfall Workbench（状态机 + 干扰率） | ✅ 完成 |
| v0.3 | 主动风险注入（`prepare_edit_context` + 风险引擎） | ✅ 完成 |
| v0.3.1 | 评测 harness（20 场景） | ✅ 完成 |
| v0.4 | 公开开发者体验 | 🧭 规划中 |

剩余构想见 [Issue #1](https://github.com/zsyu9779/vatbrain/issues/1)。详见 [ROADMAP.md](docs/ROADMAP.md) 与 [EVOLUTION_PLAN.md](docs/EVOLUTION_PLAN.md)。

### 术语规范

本项目使用脑科学隐喻命名：情境记忆 **Episodic**、错误记忆 **Pitfall**（禁用 ErrorLog）、记忆整合 **Consolidation**（禁用 Merge）、权重衰减 **Decay**（禁用 Delete）、情境过滤 **Contextual Gating**（禁用 Pre-filter）。

### 许可证

MIT
