# Embedder 双通道架构 — v0.1 补充设计

> 2026-04-28 | jayden

## 1. 问题背景

VatBrain 的 `Embedder` 接口负责将文本转为向量，供语义检索（pgvector）和可分离性判别（PatternSeparation）使用。当前唯一实现 `StubEmbedder` 返回全零向量，导致：

- **语义检索失效**：全零向量查询 pgvector → 所有记录距离相等，无意义排序
- **去重失效**：零向量间 cosine 相似度恒为 1.0 → 新记忆总是被判为"需合并"

这两个路径是 v0.1 的核心功能。修复方案需要兼顾开源项目的推广需求：**零配置开箱即用，同时保留接入真实模型的渐进通道**。

## 2. 设计原则

1. **零配置可运行**：`docker compose up -d neo4j postgres` 后即可获得合理的检索和去重效果
2. **渐进增强**：设置环境变量即可切换到语义通道，不修改代码
3. **同接口双实现**：`Embedder` 接口不变，配置驱动选择后端
4. **文本通道作为绝对兜底**：即使语义通道配置错误 fallback 到文本通道，而不是报错

## 3. 双通道架构

```
┌──────────────────────────────────────────────────────┐
│                   Embedder 接口                        │
│           Embed(ctx, text) ([]float32, error)          │
└──────────────────────┬───────────────────────────────┘
                       │
         ┌─────────────┴─────────────┐
         │                           │
    ┌────▼────┐                ┌─────▼─────┐
    │ 文本通道 │                │ 语义通道   │
    │ (默认)   │                │ (可选)    │
    └────┬────┘                └─────┬─────┘
         │                           │
    StubEmbedder               OpenAIEmbedder
    (文本启发式)               VoyageEmbedder
                               LocalEmbedder
```

### 3.1 文本通道（默认）

`StubEmbedder` 不再返回零向量，改为**基于文本的伪向量生成**：

```go
// StubEmbedder (重构后) — 文本启发式
func (s *StubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    // 1. Tokenize 文本
    // 2. 对每个 token 做 hash → float32
    // 3. 填充到 DefaultEmbeddingDim 维度的伪向量
    // 4. L2 归一化
}
```

**关键特性**：
- 相同/相似文本 → 相近伪向量 → pgvector 相似度排序有效
- 完全相同的文本 → 完全相同向量 → PatternSeparation 判重有效
- 确定性算法，无外部依赖，零延迟

**适用范围**：关键词级别匹配、精确去重、工程规则检索

### 3.2 语义通道（可选）

通过环境变量启用，调用外部 embedding 服务：

```
EMBEDDER_PROVIDER=openai           # stub | openai | voyage | local
EMBEDDER_API_KEY=sk-xxx            # API key
EMBEDDER_MODEL=text-embedding-3-small  # 模型名
EMBEDDER_BASE_URL=https://...      # 自定义 endpoint（本地模型）
```

| Provider | 默认模型 | 维度 | 说明 |
|----------|---------|------|------|
| `stub`（默认） | — | 1536 | 文本启发式，零配置 |
| `openai` | text-embedding-3-small | 1536 | 需 API key |
| `voyage` | voyage-3 | 1024 | 需 API key |
| `local` | — | 可配 | 兼容 Ollama / sentence-transformers 等 HTTP 服务 |

### 3.3 通道切换

`app.go` 初始化时根据 `EMBEDDER_PROVIDER` 选择实现，失败 fallback 到 stub：

```go
func New(ctx context.Context) (*App, error) {
    // ...
    emb := initEmbedder(cfg)
    // ...
}

func initEmbedder(cfg config.Config) embedder.Embedder {
    switch cfg.Embedder.Provider {
    case "openai":
        e, err := embedder.NewOpenAIEmbedder(cfg.Embedder)
        if err == nil {
            return e
        }
        slog.Warn("openai embedder init failed, falling back to stub", "err", err)
    case "local":
        e, err := embedder.NewLocalEmbedder(cfg.Embedder)
        if err == nil {
            return e
        }
        slog.Warn("local embedder init failed, falling back to stub", "err", err)
    }
    return embedder.NewStubEmbedder() // 默认兜底
}
```

## 4. 文本启发式向量算法

### 4.1 设计目标

- 相同文本 → 完全相同向量（精确去重）
- 相似文本 → 相近向量（模糊检索）
- 确定性（相同输入永远相同输出）
- 维度匹配 `DefaultEmbeddingDim`（1536）与 OpenAI 模型兼容

### 4.2 算法

```
1. 文本预处理：lowercase + 去标点
2. Tokenize：按空格/标点分词，去停用词
3. N-gram 扩展：生成 1-gram + 2-gram（捕获短语）
4. 对每个 n-gram 做 FNV-1a hash → uint32
5. 将 hash 值映射到 1536 维向量：
   for each n-gram:
       slot = hash % 1536
       vec[slot] += sign(hash)  // +1 或 -1
6. L2 归一化
```

### 4.3 伪代码

```go
func textToPseudoVector(text string, dim int) []float32 {
    tokens := tokenize(strings.ToLower(text))
    ngrams := append(tokens, bigrams(tokens)...)

    vec := make([]float32, dim)
    for _, ng := range ngrams {
        h := fnv1a(ng)
        slot := int(h % uint32(dim))
        if h&0x80000000 == 0 {
            vec[slot]++
        } else {
            vec[slot]--
        }
    }

    return l2Normalize(vec)
}
```

### 4.4 局限性（在文档中诚实说明）

- 同义词无法匹配（"fix bug" vs "resolve issue"）→ 需要语义通道
- 跨语言无效（中文 vs 英文同样含义的文本）→ 需要语义通道
- 长文本信息稀释（n-gram 向量累加后噪声增加）→ 截断 200 token
- 不适用于"表述不同但含义相同"的语义检索场景

## 5. 双通道对上层的影响

### 5.1 检索链路

```
search_memories(query)
    │
    ├── embedder.Embed(query) → queryVec （文本通道 or 语义通道）
    │
    ├── Neo4j Cypher → Episodic 候选（project_id + language 过滤）
    │
    ├── ContextualGating → 权重过滤 + 冷却阈值
    │
    ├── pgvector SimilaritySearch(queryVec, topK) → 排序
    │       ↑ 文本通道：基于伪向量 → 关键词重叠排序
    │       ↑ 语义通道：基于语义向量 → 语义相似度排序
    │
    ├── Semantic 候选 → tokenOverlap（纯文本，不受 embedding 影响）
    │
    └── 合并排序 → topK
```

**无需修改检索链路代码**，仅 `embedder.Embed()` 返回不同质量的向量。

### 5.2 Pattern Separation 链路

```
write_memory(summary)
    │
    ├── SignificanceGate → 显著性判断（文本属性，不受影响）
    │
    ├── embedder.Embed(summary) → newVec
    │
    ├── pgvector SimilaritySearch(newVec, topK=5) → 候选
    │
    ├── for each candidate:
    │       PatternSeparation.Check(newVec, candidateVec)
    │           cosine_similarity > 0.85 → MERGE (不创建新记忆)
    │           else → SEPARATE (创建新记忆)
    │
    └── 文本通道行为：
           完全相同文本 → 相同伪向量 → cosine=1.0 → MERGE ✅
           相似文本 → 相近伪向量 → cosine≈0.7-0.9 → 可能 MERGE
           不相关文本 → 不同伪向量 → cosine≈0.1-0.3 → SEPARATE ✅
```

### 5.3 Consolidation 链路

Consolidation 本身是文本拼接 + LLM 提炼，embedding 仅用于生成新语义记忆的向量。文本通道下语义记忆带有伪向量，**至少保证精确去重有效**。

## 6. 配置结构

```go
// config.go 新增
type EmbedderConfig struct {
    Provider string // stub | openai | voyage | local
    APIKey   string
    BaseURL  string
    Model    string
    Dimension int   // 向量维度，默认 1536
}

func LoadFromEnv() Config {
    return Config{
        // ...
        Embedder: EmbedderConfig{
            Provider:  envStr("EMBEDDER_PROVIDER", "stub"),
            APIKey:    envStr("EMBEDDER_API_KEY", ""),
            BaseURL:   envStr("EMBEDDER_BASE_URL", ""),
            Model:     envStr("EMBEDDER_MODEL", "text-embedding-3-small"),
            Dimension: envInt("EMBEDDER_DIMENSION", models.DefaultEmbeddingDim),
        },
    }
}
```

## 7. 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/embedder/embedder.go` | 不改 | 接口保持不变 |
| `internal/embedder/stub_embedder.go` | **重写** | 文本启发式伪向量替代零向量 |
| `internal/embedder/openai_embedder.go` | **新增** | 调 OpenAI embeddings API |
| `internal/embedder/local_embedder.go` | **新增** | 调本地 HTTP embedding 服务 |
| `internal/embedder/claude_embedder.go` | **删除** | 名称误导，Claude API 无 embedding 端点 |
| `internal/config/config.go` | **修改** | 新增 EmbedderConfig |
| `internal/app/app.go` | **修改** | 根据配置选择 embedder 实现 |

**上层调用方不需要改动**：`search_tool.go`、`write_tool.go`、`search_handler.go`、`write_handler.go` 已通过 `Embedder` 接口解耦。

## 8. 测试策略

| 测试 | 类型 | 说明 |
|------|------|------|
| `TestStubEmbedder_Deterministic` | 单元 | 相同文本 → 相同向量 |
| `TestStubEmbedder_SimilarTexts` | 单元 | 相似文本 cosine > 不相关文本 cosine |
| `TestStubEmbedder_ExactMatchCosine` | 单元 | 完全相同文本 cosine = 1.0 |
| `TestOpenAIEmbedder_Integration` | 集成 | 需 API key，`testing.Short()` 跳过 |
| `TestLocalEmbedder_Integration` | 集成 | 需本地服务，`testing.Short()` 跳过 |
| `TestSmoke_SearchNoEmbedding` | 冒烟 | 文本通道下 search → 返回结果 |

## 9. 技术决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 文本通道算法 | FNV-1a hash + sparse vector | 确定性强，无依赖，比 minhash/LSH 简单 |
| 维度 | 1536 固定 | 与 OpenAI 兼容，pgvector 无维度限制 |
| 接口是否变化 | 不变 | `Embed(ctx, text) ([]float32, error)` 完全足够 |
| 通道切换机制 | 环境变量 + fallback | 零配置默认可用，配置错误不崩溃 |
| Voyage AI | 暂不实现 | v0.1 先做 openai + local，Voyage 可后期加 |

## 10. 与 v0.1 主设计的兼容性

本文档是 v0.1 主设计（`00-design.md`）的**补充**，不替代任何已有设计决策。核心变更仅在于 `StubEmbedder` 的行为从"返回零向量"升级为"返回文本启发式伪向量"，所有下游代码无需改动。
