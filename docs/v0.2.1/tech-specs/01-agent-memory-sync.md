# VatBrain v0.2.1 -- Agent Memory Watcher（Agent 记忆同步适配器）

> 所属 Phase：Phase Omicron -- Agent Memory Sync
> 前置阅读：`../v0.2/00-design.md`（第 4、6 节）、`../../DESIGN_PRINCIPLES.md`（第 4.2 节）
> 本文档定义 Agent Memory Watcher 子系统——被动监控 Agent 原生记忆存储、检测变更、经 LLM 提炼后写入 VatBrain 情境记忆的完整管道。

---

## 0. Problem Statement

### 0.1 Current State

VatBrain 当前只有两条记忆进入路径：

```
Path A: Agent 主动调用 write_memory MCP tool  →  SignificanceGate  →  Store.WriteEpisodic
Path B: vatbrain import-cursor CLI              →  直接写入（绕过 LLM 提炼）
```

**两条路径的缺陷**：

| 路径 | 优点 | 缺陷 |
|------|------|------|
| Path A (MCP tool) | 完整经过 SignificanceGate + PatternSeparation + LinkOnWrite | 无 Agent 主动调用——Agent 不会自发写外部记忆 |
| Path B (import-cursor) | 批量导入，覆盖历史会话 | 绕过 LLM 提炼——用 if/else 推断 language/taskType/entityID；仅支持 Cursor；需手动执行 |

**根本问题**：Agent 与外部记忆系统的交互模式是"推"（push）而非"拉"（pull）。Agent 缺乏主动推送记忆的动机和能力。需要一个**被动的记忆旁路**，监控 Agent 自己的原生记忆存储，检测变化后经 LLM 提炼再写入 VatBrain。

### 0.2 Goal State

```
Agent 写入原生记忆（Agent 自动完成，不需感知 VatBrain）
      │
      ▼
   Agent 原生存储（文件系统）
      │
      ▼
   MemoryProvider 适配器（检测变更，解析格式）
      │
      ▼
   RawMemory（标准化中间格式）
      │
      ▼
   LLM Refinement Pipeline（提炼结构化字段）
      │
      ▼
   models.EpisodicMemory  →  Store.WriteEpisodic  →  LinkOnWrite  →  VatBrain 图谱
```

### 0.3 目标 Agent

需要适配的主流 AI Agent：

| Agent | 原生记忆存储 | 适配优先级 |
|-------|-------------|-----------|
| **Claude Code** | `~/.claude/projects/{slug}/memory/*.md` (YAML frontmatter) | P0 — 已调研 |
| **OpenCode** | 待调研（文件系统 / SQLite / JSON） | P1 |
| **Cursor** | `~/.cursor/projects/*/agent-transcripts/*/*.jsonl` | P1（可复用 import_cursor 解析逻辑） |
| **Codex / OpenClaw / Hermes** | 待调研 | P2 |
| **自定义 Agent** | 用户通过 YAML config 文件定义 | P1 — 统一扩展机制 |

---

## 1. Design Principles

### DP1: Opt-in, Never Blocking

Watcher 是可选子系统。`VATBRAIN_WATCHER_ENABLED=false`（默认）时不启动任何文件监控，现有 MCP/HTTP 入口点行为不变。Watcher 初始化失败不阻止 App 启动。

### DP2: Provider Abstraction

每种 Agent 的原生记忆格式不同。`MemoryProvider` 接口统一抽象差异，核心管道只操作 `RawMemory`。

### DP3: Reuse, Don't Replace

Watcher 管线复用现有 Store、Embedder、LLM 基础设施。不引入新的持久化层或 LLM 调用框架。产出 `models.EpisodicMemory` 经由 `Store.WriteEpisodic` + `LinkOnWrite` 写入，与 `write_memory` MCP tool 走完全相同的写入路径。

### DP4: Best-Effort with Observability

单次 watch 事件处理失败不中断整体监控。每个适配器的状态通过 `list_adapters` MCP tool 可观测。LLM 提炼失败时 RawMemory 降级为启发式推断。

### DP5: Config-Driven

适配器启用列表、轮询间隔、LLM 提炼 prompt 模板均通过 env vars 配置。Custom 适配器通过 YAML config file 定义，支持用户扩展新 Agent 而不修改 Go 源码。

---

## 2. Architecture Overview

### 2.1 System Diagram

```
 ┌──────────────────────────────────────────────────────────────────────┐
 │                          App (app.go)                                │
 │                                                                      │
 │  ┌───────────────────┐    ┌──────────────────────────────────────┐  │
 │  │  MemoryWatcher    │    │      LLM Refinement Pipeline          │  │
 │  │                   │    │                                       │  │
 │  │  ┌─────────────┐  │    │  RawMemory ──► LLM Extractor ──► EP   │  │
 │  │  │ Provider 1   │  │    │       │                               │  │
 │  │  │ (ClaudeCode) │──┼────┼───────┘                               │  │
 │  │  ├─────────────┤  │    │       │ (fallback)                      │  │
 │  │  │ Provider 2   │  │    │  HeuristicExtractor ──────────► EP    │  │
 │  │  │ (OpenCode)   │──┼────┼──                                     │  │
 │  │  ├─────────────┤  │    │                                        │  │
 │  │  │ Provider N   │  │    │  Output: models.EpisodicMemory        │  │
 │  │  │ (Custom...)  │──┼────┼──► Store.WriteEpisodic()               │  │
 │  │  └─────────────┘  │    │  ─► LinkOnWrite()                       │  │
 │  └───────────────────┘    └──────────────────────────────────────┘  │
 │                                                                      │
 │  ┌────────────────────┐   ┌─────────────────────────────────────┐   │
 │  │  MCP Tools         │   │  Config (env vars + optional file)   │   │
 │  │  - list_adapters   │   │  - watcher enabled / interval        │   │
 │  │  - sync_memories   │   │  - adapter list                      │   │
 │  │  - configure_adapt.│   │  - prompt templates                  │   │
 │  └────────────────────┘   └─────────────────────────────────────┘   │
 └──────────────────────────────────────────────────────────────────────┘
```

### 2.2 Data Flow per Sync Cycle

```
Poll tick (interval) or manual trigger
      │
      ▼
  For each enabled MemoryProvider:
      │
      ▼
  Provider.Scan(ctx) → []RawMemory
      │
      ▼
  Dedup against seen set (path + modtime + content hash)
      │
      ▼
  For each new RawMemory:
      │
      ├── LLM available? ──Yes──► LLMRefine(raw) → EpisodicMemory
      │                              │
      └── LLM unavailable? ──────► HeuristicRefine(raw) → EpisodicMemory
                                     │
                                     ▼
                              Store.WriteEpisodic(ctx, ep)
                                     │
                                     ▼
                              LinkOnWrite(ctx, s, ep.ID, ...)
                                     │
                                     ▼
                              Add to seen set
```

### 2.3 Directory Layout

```
internal/
  watcher/                          # NEW: watcher subsystem
    watcher.go                      # MemoryWatcher: orchestrator
    watcher_test.go
    provider.go                     # MemoryProvider interface + RawMemory + ProviderRegistry + seenSet
    refiner.go                      # LLM refinement pipeline
    refiner_test.go
    adapters/
      claude_code.go                # Claude Code adapter (P0)
      claude_code_test.go
      opencode.go                   # OpenCode adapter (P1)
      cursor.go                     # Cursor adapter — 复用 import_cursor 解析逻辑 (P1)
      custom.go                     # Custom adapter — YAML-driven (P1)
      custom_test.go
  config/
    config.go                       # MODIFIED: add WatcherConfig
  app/
    app.go                          # MODIFIED: add MemoryWatcher field + wiring
  mcp/
    mcp_server.go                   # MODIFIED: add 3 new tools to RegisteredTools
    adapters_tool.go                # NEW: list_adapters tool
    sync_tool.go                    # NEW: sync_memories tool
    configure_adapter_tool.go       # NEW: configure_adapter tool
```

---

## 3. Core Interfaces and Types

### 3.1 RawMemory（Canonical Intermediate Format）

```go
// internal/watcher/provider.go

// RawMemory is the canonical intermediate format produced by all MemoryProvider
// adapters. It normalizes agent-specific memory formats into a common structure
// consumed by the LLM refinement pipeline.
type RawMemory struct {
    ProviderName   string            `json:"provider_name"`
    SourceURI      string            `json:"source_uri"`
    Content        string            `json:"content"`
    FrontMatter    map[string]string `json:"frontmatter"`
    ContentHash    string            `json:"content_hash"`    // SHA-256 hex digest of Content
    ModifiedAt     time.Time         `json:"modified_at"`
    AgentSessionID string            `json:"agent_session_id"`
    ProjectID      string            `json:"project_id"`
    Metadata       map[string]string `json:"metadata"`
}
```

### 3.2 MemoryProvider Interface

```go
// MemoryProvider abstracts an agent's native memory storage.
type MemoryProvider interface {
    Name() string
    Description() string

    // Watch starts watching the agent's memory storage for changes.
    // Sends RawMemory to ch. Blocks until ctx is cancelled.
    // Implementations must close ch when ctx is cancelled.
    Watch(ctx context.Context, ch chan<- RawMemory) error

    // Scan performs a one-shot scan of all existing memories.
    Scan(ctx context.Context) ([]RawMemory, error)

    // Status returns the current provider health/stats.
    Status() ProviderStatus
}

type ProviderStatus struct {
    Name        string            `json:"name"`
    Description string            `json:"description"`
    Healthy     bool              `json:"healthy"`
    LastScanAt  time.Time         `json:"last_scan_at"`
    LastError   string            `json:"last_error,omitempty"`
    TotalSeen   int               `json:"total_seen"`
    Watching    bool              `json:"watching"`
    WatchPath   string            `json:"watch_path"`
    Config      map[string]string `json:"config"`
}
```

### 3.3 Provider Registry

```go
type ProviderRegistry struct {
    mu        sync.RWMutex
    providers map[string]MemoryProvider
}

func (r *ProviderRegistry) Register(p MemoryProvider) error
func (r *ProviderRegistry) List() []MemoryProvider
func (r *ProviderRegistry) Get(name string) (MemoryProvider, error)
func (r *ProviderRegistry) Statuses() []ProviderStatus
```

---

## 4. Built-in Adapters

### 4.1 Claude Code Adapter（P0）

#### Storage Layout

```
~/.claude/projects/{project-slug}/memory/
    MEMORY.md               ← index file
    {memory-name}.md        ← YAML frontmatter + markdown body
```

Individual memory file:
```markdown
---
name: Memory Title
description: One-line description
type: user  # user | feedback | project | reference
originSessionId: abc-def-123
enabled: true
---

Memory content body (markdown).
```

#### Implementation

```go
// internal/watcher/adapters/claude_code.go

type ClaudeCodeProvider struct {
    homeDir   string
    seen      *seenSet
    fsWatcher *fsnotify.Watcher
    mu        sync.Mutex
    watching  bool
    status    ProviderStatus
}

func NewClaudeCodeProvider(homeDir string) *ClaudeCodeProvider

// Scan: walk ~/.claude/projects/*/memory/, parse .md files (skip MEMORY.md),
//       extract YAML frontmatter → RawMemory.
func (p *ClaudeCodeProvider) Scan(ctx context.Context) ([]RawMemory, error)

// Watch: fsnotify on ~/.claude/projects/ + all existing memory/ subdirs,
//        debounce 2s, parse changed .md files, emit RawMemory.
func (p *ClaudeCodeProvider) Watch(ctx context.Context, ch chan<- RawMemory) error
```

**Frontmatter mapping**:
- `name` → `RawMemory.Metadata["name"]`
- `type` → `RawMemory.Metadata["memory_type"]`（user / feedback / project / reference）
- `originSessionId` → `RawMemory.AgentSessionID`
- Body (after `---`) → `RawMemory.Content`

**Project inference**: `{project-slug}` directory name → `RawMemory.ProjectID`

### 4.2 OpenCode Adapter（P1 — 待调研）

```go
type OpenCodeProvider struct {
    watchPath string   // configured via VATBRAIN_OPENCODE_MEMORY_PATH
    seen      *seenSet
    mu        sync.Mutex
    status    ProviderStatus
}
// 预计路径: macOS ~/Library/Application Support/OpenCode/, Linux ~/.config/opencode/
// 若 watchPath 不可达，Status().Healthy = false，Scan() 返回空列表。
```

### 4.3 Cursor Adapter（P1）

复用 `cmd/vatbrain/import_cursor.go` 的 JSONL 解析逻辑，包装为 `MemoryProvider` 接口。与 import 工具的区别：增量（仅新文件），经过 LLM 提炼管道。

### 4.4 Custom Adapter（P1）

用户通过 YAML config 定义任意 Agent 的记忆格式，不修改 Go 源码。

```yaml
# ~/.vatbrain/adapters/my-agent.yaml
name: my-agent
description: "Watches my custom agent memory files"
enabled: true
watch:
  paths:
    - "~/my-agent/memory/*.md"
    - "~/my-agent/projects/*/memories/*.json"
  exclude_patterns:
    - "*/archive/*"
  poll_interval: 10s
format:
  type: yaml_frontmatter  # yaml_frontmatter | json_lines | raw_text
  field_mappings:
    content: "body"           # source field → RawMemory.Content (required)
    project_id: "project"     # (optional)
    session_id: "session"     # (optional)
    metadata:
      memory_type: "type"
      importance: "priority"
llm_refinement:
  prompt_override: |
    You are analyzing memories from MyAgent...
```

---

## 5. Memory Watcher Daemon

```go
// internal/watcher/watcher.go

type MemoryWatcher struct {
    registry     *ProviderRegistry
    refiner      *Refiner
    store        store.MemoryStore
    pollInterval time.Duration
    stopped      chan struct{}
    mu           sync.Mutex
    running      bool
}

func NewMemoryWatcher(providers []MemoryProvider, refiner *Refiner, s store.MemoryStore, pollInterval time.Duration) *MemoryWatcher

// Start begins the watch daemon. Starts Watch goroutines per provider + periodic scan.
func (w *MemoryWatcher) Start(ctx context.Context)

// Stop gracefully shuts down. Persists seen set before returning.
func (w *MemoryWatcher) Stop()
```

**Double-watch strategy**:

1. **fsnotify Watch** per provider — 低延迟，2s debounce 防止事件洪泛
2. **Periodic Scan** (default 5min) — 防御性 fallback，处理 fsnotify 不可靠场景（NFS、Docker overlay）

Dedup via **seen set** (LRU, max 10K entries, persisted to disk): `(SourceURI, ContentHash)` → prevents re-processing on both Watch and Scan paths.

---

## 6. LLM Refinement Pipeline

```go
// internal/watcher/refiner.go

type Refiner struct {
    LLMClient    llm.Client
    Embedder     embedder.Embedder
    SystemPrompt string
}

// Refine converts RawMemory → EpisodicMemory.
// LLM path with automatic heuristic fallback.
func (r *Refiner) Refine(ctx context.Context, raw RawMemory) (*models.EpisodicMemory, error)
```

### 6.1 LLM Extraction Prompt

LLM 输入：Agent 名称、project、memory type、原始内容
LLM 输出（JSON）：
```json
{
  "summary": "One concise paragraph (≤500 chars)",
  "language": "go | typescript | python | rust | proto | zh | unknown",
  "task_type": "debug | feature | refactor | review | unknown",
  "entity_id": "func:FunctionName | pkg:packagename | file:path/to/file.go",
  "project_id": "project identifier",
  "key_entities": ["entity1", "entity2"],
  "confidence": 0.85
}
```

- `confidence < 0.5` → 跳过该条（return nil）
- LLM 调用失败 / 返回非 JSON → 自动降级为启发式提取

### 6.2 Heuristic Fallback

当 LLM 不可用时，复用并改进 `import_cursor.go` 的推断逻辑：关键词匹配推断 language/taskType，正则提取 entity_id，frontmatter + content 截断构建 summary。不生成 embedding（StubEmbedder 零向量），但结构化检索仍可用。

### 6.3 Error Handling

| 错误类型 | 处理策略 | 是否阻断 |
|---------|---------|---------|
| LLM 调用超时 | 降级为启发式 | 否（单条） |
| LLM 返回非 JSON | 尝试修复；仍失败则降级 | 否 |
| confidence < 0.5 | 跳过该条 | 否 |
| Store.WriteEpisodic 失败 | 记录 ERROR，继续下一条 | 否 |
| fsnotify 不可用 | 降级为纯轮询 | 否（降级运行） |
| Provider.Scan 超时 (30s) | 取消本次 scan，等下一 tick | 否 |

---

## 7. Configuration

### 7.1 WatcherConfig

```go
type WatcherConfig struct {
    Enabled           bool          // VATBRAIN_WATCHER_ENABLED (default false)
    PollInterval      time.Duration // VATBRAIN_WATCHER_INTERVAL_SECS (default 300)
    Adapters          string        // VATBRAIN_WATCHER_ADAPTERS: "claude-code,opencode" or "all"
    AdapterConfigDir  string        // VATBRAIN_WATCHER_CONFIG_DIR: custom adapter YAML dir
    DataDir           string        // VATBRAIN_DATA_DIR: seen set persistence
    RefinePromptFile  string        // VATBRAIN_WATCHER_REFINE_PROMPT_FILE: custom LLM prompt
    ClaudeCodeHomeDir string        // VATBRAIN_CLAUDE_CODE_HOME: override home dir
    OpenCodeMemoryPath string       // VATBRAIN_OPENCODE_MEMORY_PATH
}
```

### 7.2 App Wiring

```go
// In app.New(ctx):
if cfg.Watcher.Enabled {
    providers := buildProviders(&cfg.Watcher)
    refiner := watcher.NewRefiner(llmClient, emb, cfg.Watcher.RefinePromptFile)
    watcherComp = watcher.NewMemoryWatcher(providers, refiner, s, cfg.Watcher.PollInterval)
    watcherComp.RestoreSeenSet(cfg.Watcher.DataDir) // if DataDir != ""
    go watcherComp.Start(ctx)
}

// In App.Close():
if a.MemoryWatcher != nil {
    a.MemoryWatcher.Stop()
    a.MemoryWatcher.DumpSeenSet(a.Config.Watcher.DataDir)
}
```

---

## 8. MCP Tools

### 8.1 list_adapters

列出所有适配器及其状态（名称、健康度、最后扫描时间、已处理数量、是否在 watch 中）。

### 8.2 sync_memories

手动触发同步。可选 `adapter` 参数限定同步单个适配器。返回 `{adapters_scanned, total_found, total_written, total_skipped, total_failed}`。

### 8.3 configure_adapter

运行时创建 Custom 适配器。参数：`name`、`watch_path`、`format_type`（yaml_frontmatter / json_lines / raw_text）、`content_field`、`project_id_field`（可选）、`session_id_field`（可选）、`metadata_fields_json`（可选）。写入 YAML 到 `AdapterConfigDir`，下次 poll 生效。

Wiring：仅在 `a.MemoryWatcher != nil` 时注册这三个 tool。

---

## 9. Implementation Phases

### Phase Omicron-0: Foundation（1 day）

- `internal/watcher/provider.go` — MemoryProvider interface, RawMemory, ProviderRegistry, seenSet
- `internal/watcher/watcher_test.go` — registry + seenSet unit tests
- `internal/config/config.go` — add WatcherConfig struct + env var parsing

**No behavioral change.** All new code inert until `VATBRAIN_WATCHER_ENABLED=true`.

### Phase Omicron-1: Claude Code Adapter + Daemon（2 days）

- `internal/watcher/adapters/claude_code.go` — Scan + Watch (fsnotify)
- `internal/watcher/adapters/claude_code_test.go` — temp dir simulating `~/.claude/projects/*/memory/`
- `internal/watcher/watcher.go` — MemoryWatcher orchestrator
- `internal/watcher/refiner.go` — LLM + heuristic refinement
- `internal/watcher/refiner_test.go` — mock LLM client
- `internal/app/app.go` — wire into App.New() / App.Close()

### Phase Omicron-2: OpenCode + Cursor + Custom Adapters（1 day）

- `internal/watcher/adapters/opencode.go` — 骨架（调研 + 实现）
- `internal/watcher/adapters/cursor.go` — 复用 import_cursor 解析逻辑
- `internal/watcher/adapters/custom.go` — YAML-driven CustomProvider
- `internal/watcher/adapters/custom_test.go`

### Phase Omicron-3: MCP Tools（1 day）

- `internal/mcp/adapters_tool.go` — list_adapters
- `internal/mcp/sync_tool.go` — sync_memories
- `internal/mcp/configure_adapter_tool.go` — configure_adapter
- Update `RegisteredTools()` to conditionally register

### Phase Omicron-4: Persistence + Polish（1 day）

- Seen set persistence (`DumpSeenSet` / `RestoreSeenSet`)
- Integration smoke test: real Claude Code memory dir → EpisodicMemory in Store
- Documentation

---

## 10. Impact Scope

### Files Created

| File | Purpose |
|------|---------|
| `internal/watcher/watcher.go` | MemoryWatcher orchestrator |
| `internal/watcher/watcher_test.go` | Watcher unit tests |
| `internal/watcher/provider.go` | MemoryProvider interface, RawMemory, registry, seenSet |
| `internal/watcher/refiner.go` | LLM refinement pipeline |
| `internal/watcher/refiner_test.go` | Refiner unit tests |
| `internal/watcher/adapters/claude_code.go` | Claude Code adapter |
| `internal/watcher/adapters/claude_code_test.go` | Claude Code adapter tests |
| `internal/watcher/adapters/opencode.go` | OpenCode adapter |
| `internal/watcher/adapters/cursor.go` | Cursor adapter |
| `internal/watcher/adapters/custom.go` | Custom YAML-driven adapter |
| `internal/watcher/adapters/custom_test.go` | Custom adapter tests |
| `internal/mcp/adapters_tool.go` | list_adapters MCP tool |
| `internal/mcp/sync_tool.go` | sync_memories MCP tool |
| `internal/mcp/configure_adapter_tool.go` | configure_adapter MCP tool |

### Files Modified

| File | Change | Risk |
|------|--------|------|
| `internal/config/config.go` | Add WatcherConfig + env var loading | Low |
| `internal/app/app.go` | Add MemoryWatcher field + wiring | Low（conditional） |
| `internal/mcp/mcp_server.go` | Add 3 tools to RegisteredTools (conditional) | Low |

### Files NOT Changed

- `internal/store/*` — reuses existing `WriteEpisodic` + `LinkOnWrite`
- `internal/models/*` — reuses existing `EpisodicMemory`
- `internal/core/*` — reuses existing `LinkOnWrite`
- `internal/llm/*` — reuses existing `llm.Client` interface
- `cmd/vatbrain-mcp/main.go` — MCP startup unchanged
- `internal/api/*` — HTTP API unchanged

### Dependencies

- `gopkg.in/yaml.v3` — Custom adapter config parsing（add to go.mod if not present）
- `github.com/fsnotify/fsnotify` — File watching（add to go.mod if not present）
- 其余复用已有依赖：`github.com/hashicorp/golang-lru/v2`（seenSet LRU）

---

## 11. Key Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Watcher daemon, not CLI | 被动同步不依赖用户记得执行命令，与"被动记忆旁路"需求一致 |
| D2 | fsnotify + polling 双模式 | fsnotify 低延迟但不适用于 NFS/Docker overlay；5min 轮询兜底，seen set 去重 |
| D3 | 复用 llm.Client 接口 | 现有 `Chat(ctx, system, user) (string, error)` 抽象已足够，不需新客户端 |
| D4 | 不新增 Store 方法 | Watcher 产出与 `write_memory` 完全相同的 EpisodicMemory，走相同写入路径，确保图拓扑一致 |
| D5 | seen set JSON 持久化 | 简单够用。10K entries x ~200 bytes = ~2MB，5min 刷一次磁盘 |
| D6 | Provider 级别 MCP 工具不拆分 | 所有交互走 `MemoryProvider` 接口，MCP 工具操作接口层，不耦合具体 Provider |

---

*本文档与 `../../v0.2/00-design.md` 第 4 节（Pitfall 提取引擎）、第 6 节（行为归因权重）共同构成 VatBrain 的完整记忆摄入体系。Watcher 子系统解决"记忆从哪来"的问题（被动同步），与 ConsolidationEngine 的"记忆如何提炼"（主动整合）互补。*
