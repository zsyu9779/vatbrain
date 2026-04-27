# VatBrain v0.1 — 设计定稿

> 状态：**定稿**
>
> 定稿时间：2026-04-27
>
> 前置阅读：
> - `../DESIGN_PRINCIPLES.md`（设计基石）
> - `../ROADMAP.md`（版本路线图）
>
> v0.1 目标：跑通"图+向量"复合写入、两阶段检索、权重衰减、睡眠整合的最小闭环。

---

## 1. v0.1 范围定义

### 1.1 纳入 v0.1

- 多级存储：Working Memory（LLM 压缩）、Episodic Memory（Vector DB）、Semantic Memory（Graph DB）
- "图+向量"复合写入（含可分离性判别）
- 两阶段检索（情境过滤 + 语义排序）
- 权重衰减算法（recency-weighted frequency + 双参照时间）
- 睡眠整合引擎（异步触发、回顾→提炼→回测）
- 版本管理（entity_group + obsoleted_at）

### 1.2 纳入 v0.2+

- Pitfall Memory 独立建模
- 反事实推理（睡眠阶段的假设性规则生成）
- 记忆再巩固（检索触发反向传播更新）
- 预测误差信号（Surprise Score）
- 多租户/多用户支持

---

## 2. 技术选型

| 组件 | 选型 | 理由 |
|-----|------|------|
| 图数据库 | Neo4j 5.x | 现有基础，Cypher 查询成熟 |
| 向量数据库 | pgvector (PostgreSQL) 或 Milvus Lite | v0.1 用 pgvector 降低运维成本；数据量大后切换 Milvus |
| 对象存储 | MinIO | 自建，存完整上下文快照 (Level 1) |
| 缓存/热存储 | Redis | 情境过滤缓存、热记忆索引 |
| LLM | Claude API + 本地模型（可选） | 睡眠整合用异步批处理 |
| 消息队列 | Redis Streams / NATS | 异步任务调度（睡眠整合） |
| 语言 | Python 3.11+ | 与现有 MCP server 一致 |
| 框架 | FastAPI + asyncio | HTTP API + 后台任务 |
| ORM/驱动 | neo4j-driver, psycopg2, redis-py | — |

VatBrain 是一个通用的、状态无关的 AI Agent 记忆增强系统。不绑定任何特定项目、领域或 Agent 实现。可作为独立服务或嵌入式库使用，通过标准协议（MCP / HTTP API）与任意 AI Agent 对接。

---

## 3. 核心数据模型

### 3.1 Neo4j 图模型

#### 节点类型

```cypher
-- 情境记忆节点 (Episodic Node)
CREATE (e:EpisodicMemory {
    id: UUID,
    project_id: STRING,           -- 硬约束：项目标识
    language: STRING,             -- 硬约束：语言/框架
    task_type: STRING,            -- debug | feature | refactor | review
    summary: STRING,              -- Level 2 压缩摘要
    source_type: STRING,          -- AST | LLM | USER | DEBUG
    trust_level: INTEGER,         -- 1-5，来源可信度
    weight: FLOAT,                -- 当前权重
    effective_frequency: FLOAT,   -- recency-weighted frequency
    created_at: DATETIME,
    last_accessed_at: DATETIME,
    obsoleted_at: DATETIME,       -- NULL = 有效
    entity_group: STRING,         -- 版本集合标识
    embedding_id: STRING,         -- pgvector 中的对应向量 ID
    context_vector: LIST<FLOAT>,  -- 压缩的情境向量（可选存于此或独立索引）
    full_snapshot_uri: STRING     -- MinIO 中完整快照的路径 (Level 1)
})

-- 语义记忆节点 (Semantic Node)
CREATE (s:SemanticMemory {
    id: UUID,
    type: STRING,                 -- RULE | FACT | PATTERN | CONSTRAINT
    content: STRING,              -- 结构化知识内容
    source_type: STRING,          -- INFERRED | USER_DECLARED | SUMMARIZED
    trust_level: INTEGER,
    weight: FLOAT,
    effective_frequency: FLOAT,
    created_at: DATETIME,
    last_accessed_at: DATETIME,
    obsoleted_at: DATETIME,
    entity_group: STRING,
    consolidation_run_id: STRING, -- 是哪次睡眠整合产生的
    backtest_accuracy: FLOAT,     -- 回测准确率（整合时产生）
    source_episodic_ids: LIST<UUID> -- 溯源链路：指向源情境记忆
})
```

#### 边类型

```cypher
-- 情境 → 情境：时序/因果关系
(:EpisodicMemory)-[:PRECEDES {strength: FLOAT}]->(:EpisodicMemory)
(:EpisodicMemory)-[:CAUSED_BY {confidence: FLOAT}]->(:EpisodicMemory)

-- 语义 → 语义：依赖/冲突/版本链
(:SemanticMemory)-[:DEPENDS_ON {relation_type: STRING}]->(:SemanticMemory)
(:SemanticMemory)-[:CONFLICTS_WITH {resolution: STRING}]->(:SemanticMemory)
(:SemanticMemory)-[:SUPERSEDED {at: DATETIME}]->(:SemanticMemory)

-- 情境 ↔ 语义：实例化/提炼自
(:EpisodicMemory)-[:INSTANTIATES]->(:SemanticMemory)
(:SemanticMemory)-[:DERIVED_FROM {run_id: STRING}]->(:EpisodicMemory)

-- 通用关联边 (Link on Write 产生的连接)
(:EpisodicMemory)-[:RELATES_TO {
    strength: FLOAT,
    dimension: STRING,           -- SEMANTIC | TEMPORAL | CAUSAL
    created_at: DATETIME
}]->(:EpisodicMemory)
```

### 3.2 pgvector 向量模型

```sql
CREATE TABLE episodic_embeddings (
    id UUID PRIMARY KEY,
    memory_id UUID NOT NULL,           -- 对应 Neo4j EpisodicMemory.id
    embedding vector(1536),            -- OpenAI text-embedding-3-small 维度
    summary_text TEXT,                 -- 原文/摘要，用于调试
    project_id VARCHAR(255),
    language VARCHAR(64),
    task_type VARCHAR(64),
    created_at TIMESTAMPTZ DEFAULT now(),
    metadata JSONB
);

CREATE INDEX ON episodic_embeddings
USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);
```

### 3.3 Redis 缓存模型

```
# 键结构
memory:hot:{memory_id}              → 热记忆完整信息 (Hash)
memory:context:{project_id}         → 项目情境候选集 (Sorted Set, score=weight)
memory:entity_group:{group_id}      → 版本集合最新 ID (String)
task:context:{session_id}           → 当前任务情境向量 (Hash)

# TTL
热记忆：7 天未访问 → expire
情境候选集：1 小时（情境切换后重建）
```

---

## 4. 核心算法

### 4.1 权重衰减算法

```python
import math
from datetime import datetime, timezone

class WeightDecayEngine:
    """
    Recency-Weighted Frequency + Dual-Reference Decay + Cooling Threshold
    """

    def __init__(
        self,
        lambda_decay: float = 0.1,       # e^(-lambda * days) 的 lambda
        alpha_experience: float = 0.005,  # 经验丰富度衰减率（慢）
        beta_activity: float = 0.05,      # 活跃度衰减率（快）
        cooling_threshold: float = 0.01,  # 冷却阈值
    ):
        self.lambda_decay = lambda_decay
        self.alpha_experience = alpha_experience
        self.beta_activity = beta_activity
        self.cooling_threshold = cooling_threshold

    def compute_effective_frequency(
        self,
        access_timestamps: list[datetime],
        now: datetime | None = None
    ) -> float:
        """计算 recency-weighted frequency"""
        if not access_timestamps:
            return 0.0
        now = now or datetime.now(timezone.utc)
        return sum(
            math.exp(-self.lambda_decay * self._days_between(ts, now))
            for ts in access_timestamps
        )

    def compute_weight(
        self,
        effective_frequency: float,
        created_at: datetime,
        last_accessed_at: datetime,
        now: datetime | None = None,
    ) -> float:
        """双参照时间衰减：经验 + 活跃度"""
        now = now or datetime.now(timezone.utc)
        experience_decay = math.exp(
            -self.alpha_experience * self._days_between(created_at, now)
        )
        activity_decay = math.exp(
            -self.beta_activity * self._days_between(last_accessed_at, now)
        )
        return effective_frequency * experience_decay * activity_decay

    def is_cooled(self, weight: float) -> bool:
        return weight < self.cooling_threshold

    @staticmethod
    def _days_between(d1: datetime, d2: datetime) -> float:
        return abs((d2 - d1).total_seconds()) / 86400.0
```

**参数调优建议**：
- `lambda_decay = 0.1`：约 10 天后单次访问的权重降为 ~0.37
- `alpha_experience = 0.005`：约 200 天后"经验丰富度"降为 ~0.37（长期慢衰减）
- `beta_activity = 0.05`：约 20 天不访问"活跃度"降为 ~0.37（中期快衰减）
- `cooling_threshold = 0.01`：低于此值进入冷存储

### 4.2 显著性门控

```python
class SignificanceGate:
    """
    写入长时记忆前的门控判断。
    只有通过门控的事件才能进入复合写入流程。
    """

    def should_persist(
        self,
        event: dict,
        working_memory: list[dict],
    ) -> tuple[bool, str]:
        """
        返回 (是否持久化, 原因)
        通过条件（满足任一）：
        1. 用户显式确认（"记住这个"、"这很重要"）
        2. 跨 working-memory 周期的持续性（该信息在 >= 2 个周期的摘要中出现）
        3. 引发了纠正或行为改变（prediction error signal）
        4. 被后续对话主动引用 >= 2 次
        """
        # 条件 1：显式标记
        if event.get("user_confirmed"):
            return True, "user_confirmed"

        # 条件 2：跨周期持续性
        if self._count_in_recent_cycles(event, working_memory) >= 2:
            return True, "cross_cycle_persistence"

        # 条件 3：纠正信号
        if event.get("is_correction") or event.get("caused_behavior_change"):
            return True, "prediction_error"

        # 条件 4：被后续引用
        if event.get("subsequent_reference_count", 0) >= 2:
            return True, "subsequent_reference"

        return False, "below_threshold"

    def _count_in_recent_cycles(self, event, working_memory) -> int:
        # 实现在近期 working memory 摘要中搜索相似主题
        # v0.1 用简单的关键词匹配或 embedding 相似度
        pass
```

### 4.3 可分离性判别（Pattern Separation）

```python
class PatternSeparationCheck:
    """
    在写入前判断新记忆是否应与某存量记忆合并。
    关键：相似 ≠ 应合并。两个不同项目的 Redis 问题应保持独立。
    """

    def __init__(self, similarity_threshold: float = 0.85):
        self.threshold = similarity_threshold

    def should_merge(
        self,
        new_embedding: list[float],
        candidate_embedding: list[float],
        new_context: dict,
        candidate_context: dict,
    ) -> bool:
        """
        返回 True = 应更新旧节点；False = 创建新节点
        """
        semantic_sim = cosine_similarity(new_embedding, candidate_embedding)

        # 第一步：语义相似度必须极高才考虑合并
        if semantic_sim < self.threshold:
            return False

        # 第二步：硬约束维度必须一致
        if new_context.get("project_id") != candidate_context.get("project_id"):
            return False
        if new_context.get("language") != candidate_context.get("language"):
            return False

        # 第三步：实体/主题一致才合并
        # 例如：同一函数的两次不同调用 → 合并；不同函数报同一错误 → 不合并
        if new_context.get("entity_id") != candidate_context.get("entity_id"):
            return False

        return True
```

### 4.4 两阶段检索引擎

```python
class TwoStageRetrieval:
    """
    第一阶段：情境过滤 → 缩小候选集
    第二阶段：语义排序 → 精确匹配
    """

    def __init__(self, context_cache_ttl: int = 3600):
        self.context_cache_ttl = context_cache_ttl  # 1 小时

    async def retrieve(
        self,
        query: str,
        query_embedding: list[float],
        context: dict,           # 当前任务情境
        top_k: int = 10,
    ) -> list[dict]:
        # Stage 1: Contextual Gating
        candidates = await self._contextual_filter(context)
        # Stage 2: Semantic Ranking
        results = await self._semantic_rank(
            query_embedding, candidates, top_k
        )
        return results

    async def _contextual_filter(self, context: dict) -> list[str]:
        """
        硬约束排除 + 软权重排序。
        结果缓存（情境不变时复用）。
        """
        cache_key = f"context:{context.get('project_id')}"
        cached = await redis.get(cache_key)
        if cached:
            return json.loads(cached)

        # 硬约束：project_id + language
        query = """
        MATCH (e:EpisodicMemory)
        WHERE e.project_id = $project_id
          AND e.language = $language
          AND e.obsoleted_at IS NULL
          AND e.weight > $cooling_threshold
        RETURN e.id, e.weight
        ORDER BY e.weight DESC
        LIMIT 500
        """

        # 软权重调整
        results = await self._apply_soft_weights(results, context)
        candidate_ids = [r["id"] for r in results]

        # 缓存
        await redis.setex(cache_key, self.context_cache_ttl, json.dumps(candidate_ids))
        return candidate_ids

    async def _semantic_rank(
        self,
        query_embedding: list[float],
        candidate_ids: list[str],
        top_k: int,
    ) -> list[dict]:
        """
        在候选集内做向量相似度匹配。
        """
        results = await pgvector.query(
            table="episodic_embeddings",
            embedding=query_embedding,
            filter_ids=candidate_ids,
            top_k=top_k,
        )
        return results
```

### 4.5 睡眠整合引擎

```python
class SleepConsolidationEngine:
    """
    异步运行，在 Agent 闲置时触发。
    三阶段：回顾 → 提炼 → 回测
    """

    def __init__(self, backtest_sample_size: int = 50):
        self.backtest_sample_size = backtest_sample_size

    async def run(self, run_id: str):
        """
        完整整合流程。作为后台任务执行。
        """
        # Phase 1: 回顾 —— 扫描最近 24h 的新增情境记忆
        recent_episodics = await self._fetch_recent_episodics(hours=24)
        if len(recent_episodics) < 10:
            return  # 数据量不足，跳过本次整合

        # Phase 2: 提炼 —— 聚类 + LLM 生成候选语义规则
        clusters = await self._cluster_by_pattern(recent_episodics)
        candidate_rules = []
        for cluster in clusters:
            if len(cluster) >= 3:  # 至少 3 条才触发提炼
                rule = await self._llm_extract_rule(cluster)
                if rule:
                    candidate_rules.append({
                        "rule": rule,
                        "source_ids": [m.id for m in cluster],
                    })

        # Phase 3: 回测 —— 验证候选规则
        for candidate in candidate_rules:
            accuracy = await self._backtest(
                candidate["rule"],
                recent_episodics,
            )
            if accuracy >= 0.7:  # 准确率阈值
                await self._persist_semantic_memory(
                    candidate["rule"],
                    candidate["source_ids"],
                    run_id,
                    accuracy,
                )
            else:
                # 规则未通过回测，留在待验证状态
                await self._save_pending_rule(candidate, accuracy)

    async def _cluster_by_pattern(
        self, episodics: list
    ) -> list[list]:
        """
        用 LLM 做模式聚类：不是简单语义聚类，
        而是识别出"描述同一类问题的多条记录"。
        """
        # v0.1: 先用 embedding 相似度做粗聚类
        # 再用 LLM 对每个簇确认是否真的属于同一模式
        pass

    async def _llm_extract_rule(
        self, cluster: list
    ) -> dict | None:
        """
        从一组相关情境记忆中提炼语义规则。
        返回 None 表示该簇不足以提炼为规则。
        """
        # Prompt 设计：
        # - 输入：N 条情境记忆的摘要 + 它们之间的共性
        # - 输出：一条规则陈述 + 适用条件 + 置信度
        pass

    async def _backtest(
        self, rule: dict, recent_episodics: list
    ) -> float:
        """
        用候选规则"预测"近期的情境记忆。
        返回准确率 (0-1)。
        """
        # 随机采样 backtest_sample_size 条
        # 对每条：LLM 判断该规则是否能预测/解释该情境
        # 统计准确率
        pass

    async def _persist_semantic_memory(self, rule, source_ids, run_id, accuracy):
        """将验证通过的规则写入 Neo4j + pgvector"""
        pass
```

---

## 5. API 设计

### 5.1 写入 API

```
POST /api/v0/memories/episodic
  写入情境记忆（经显著性门控 + 复合写入）

Request:
{
  "project_id": "my-backend-service",
  "language": "go",
  "task_type": "debug",
  "content": {
    "summary": "Redis connection pool exhausted at MaxOpenConns=50",
    "entity_id": "func:NewRedisPool",
    "context": {...}
  },
  "user_confirmed": false,
  "is_correction": false
}

Response:
{
  "memory_id": "uuid",
  "persisted": true,
  "gate_reason": "cross_cycle_persistence",
  "merge_action": "created_new",   // created_new | updated_existing
  "weight": 1.0
}
```

### 5.2 检索 API

```
POST /api/v0/memories/search
  两阶段检索

Request:
{
  "query": "How to configure Redis connection pool",
  "context": {
    "project_id": "my-backend-service",
    "language": "go",
    "task_type": "debug",
    "active_files": ["internal/cache/redis.go"],
    "session_id": "xxx"
  },
  "top_k": 10,
  "include_dormant": false   // 是否包含冷记忆
}

Response:
{
  "results": [
    {
      "memory_id": "uuid",
      "type": "semantic",
      "content": "Redis MaxOpenConns must be >= 100 for production",
      "trust_level": 4,
      "weight": 3.2,
      "relevance_score": 0.94,
      "source_ids": ["episodic-uuid-1", "episodic-uuid-2"]
    }
  ],
  "context_filter_stats": {
    "total_candidates": 2450,
    "after_filter": 187,
    "filter_time_ms": 12
  },
  "semantic_rank_time_ms": 45
}
```

### 5.3 行为反馈 API

```
POST /api/v0/memories/{memory_id}/feedback
  记录检索后的行为反馈（用于行为归因权重更新）

Request:
{
  "action": "used",            // used | corrected | ignored | confirmed
  "session_id": "xxx",
  "correction_detail": {       // 仅 action=corrected 时
    "original": "...",
    "corrected_to": "..."
  }
}
```

### 5.4 睡眠整合 API

```
POST /api/v0/consolidation/trigger
  手动触发睡眠整合（debug/测试用）

GET /api/v0/consolidation/runs
  查看历史整合运行记录

GET /api/v0/consolidation/runs/{run_id}
  查看某次整合的详细结果（提炼的规则、回测准确率等）
```

### 5.5 权重管理 API

```
POST /api/v0/memories/{memory_id}/touch
  记录一次检索命中（更新 last_accessed_at 和 effective_frequency）

GET /api/v0/memories/{memory_id}/weight
  查看某条记忆的权重详情（计算过程透明化）
```

---

## 6. 目录结构

```
vatbrain/
├── CLAUDE.md                            # 工作纪律
├── .vatbrain/                           # 工作上下文
├── docs/
│   ├── DESIGN_PRINCIPLES.md             # 设计原理文档
│   ├── ROADMAP.md                       # 版本路线图
│   └── v0.1/
│       └── 00-design.md                 # 本文档
├── src/
│   ├── __init__.py
│   ├── api/
│   │   ├── __init__.py
│   │   ├── episodic.py               # 情境记忆 CRUD
│   │   ├── semantic.py               # 语义记忆 CRUD
│   │   ├── search.py                 # 检索 API
│   │   └── consolidation.py          # 睡眠整合 API
│   ├── core/
│   │   ├── __init__.py
│   │   ├── weight_decay.py           # 权重衰减引擎
│   │   ├── significance_gate.py      # 显著性门控
│   │   ├── pattern_separation.py     # 可分离性判别
│   │   ├── retrieval_engine.py       # 两阶段检索
│   │   └── consolidation_engine.py   # 睡眠整合引擎
│   ├── db/
│   │   ├── __init__.py
│   │   ├── neo4j.py                  # Neo4j 连接与查询
│   │   ├── pgvector.py               # 向量数据库操作
│   │   ├── redis.py                  # 缓存操作
│   │   └── minio.py                  # 对象存储
│   ├── models/
│   │   ├── __init__.py
│   │   ├── episodic_memory.py        # 情境记忆数据模型
│   │   ├── semantic_memory.py        # 语义记忆数据模型
│   │   └── context.py                # 情境向量模型
│   └── mcp/
│       └── server.py                 # MCP Server（对接 AI Agent）
├── tests/
│   ├── test_weight_decay.py
│   ├── test_significance_gate.py
│   ├── test_pattern_separation.py
│   ├── test_retrieval.py
│   └── test_consolidation.py
├── scripts/
│   ├── init_db.sh                    # 初始化 Neo4j + pgvector
│   └── seed_data.py                  # 测试数据填充
├── docker-compose.yml                # Neo4j + pgvector + Redis + MinIO
├── pyproject.toml
└── README.md
```

---

## 7. v0.1 实现路线

> **实现备注（2026-04-27）**：技术栈从 Python/FastAPI 切换为 Go/go-chi。
> 目录结构从 `src/` 调整为 `internal/` + `cmd/`，符合 Go 项目惯例。
> Phase 实际执行顺序与原始规划有调整：核心算法和 API 层在同一天完成，
> 睡眠整合引擎与 API 层合并为 Phase 3，MCP 作为 Phase 4。

### Phase 0: 基础设施 ✅ 已完成

- [x] `docker-compose.yml`：Neo4j + pgvector + Redis + MinIO
- [x] `scripts/init_db.sh`：初始化表结构、索引、约束
- [x] `internal/db/` 连接层：neo4j、pgvector、redis、minio（Go driver）
- [x] `internal/config/`：环境变量配置（61 项，匹配 docker-compose 默认值）

### Phase 1: 数据模型 ✅ 已完成

- [x] Neo4j 节点/边定义（EpisodicMemory / SemanticMemory + 8 边类型）
- [x] pgvector 表 + IVFFlat 索引（`episodic_embeddings`，1536 维）
- [x] Redis 键结构（working_memory、consolidation:run、热记忆缓存）
- [x] `internal/models/`：Go struct + enum（common / episodic_memory / semantic_memory / context / api）

### Phase 2: 核心算法 ✅ 已完成（47 测试通过）

- [x] `internal/core/weight_decay.go` — Recency-Weighted Frequency + 双参照衰减 + 冷却阈值（11 tests）
- [x] `internal/core/significance_gate.go` — 四条件显著性门控（12 tests）
- [x] `internal/core/pattern_separation.go` — 可分离性判别 / 三阶段检查（10 tests）
- [x] `internal/core/retrieval_engine.go` — 两阶段检索 + ContextualGating + SemanticRanker（11 tests）
- [x] `internal/core/consolidation_engine.go` — 扫描→聚类→提炼→回测→持久化（10 tests）

### Phase 3: API 层 ✅ 已完成

- [x] `internal/api/server.go` — go-chi/v5 路由 + 中间件 + 优雅关闭
- [x] `POST /memories/episodic` — 写入：显著性门控 → Embed → Pattern Separation → Neo4j + pgvector
- [x] `POST /memories/search` — 检索：ContextualGating → pgvector similarity → merge semantic
- [x] `POST /memories/{id}/feedback` — 行为反馈 → 权重增量更新
- [x] `POST /consolidation/trigger` + `GET /runs/{id}` — 异步睡眠整合触发 + 状态查询
- [x] `POST /memories/{id}/touch` + `GET /memories/{id}/weight` — 检索命中 + 权重明细
- [x] `GET /health` — 4 数据库并发健康检查（errgroup）
- [x] `internal/embedder/` — Embedder 接口 + StubEmbedder（零向量）+ ClaudeEmbedder 骨架

### Phase 4: MCP 集成 ✅ 已完成（55 测试通过）

- [x] `internal/app/app.go` — 共享初始化逻辑，HTTP 和 MCP 两个入口复用
- [x] `internal/mcp/` — 6 个 MCP Tools（mark3labs/mcp-go v0.49.0，stdio transport）
  - `write_memory` — 写入情境记忆
  - `search_memories` — 两阶段检索
  - `trigger_consolidation` — 触发睡眠整合
  - `get_memory_weight` — 查看权重明细
  - `touch_memory` — 记录检索命中
  - `health_check` — 健康检查
- [x] `cmd/vatbrain-mcp/main.go` — MCP stdio 入口
- [x] 8 个 MCP 集成测试（mcptest，tool 注册 + schema + 参数校验）

### 待完成

- [ ] **ClaudeEmbedder 实现**：当前 StubEmbedder 返回零向量，需对接 Claude API 做真实 embedding
- [ ] **端到端集成测试**：docker-compose 启动完整环境 → curl / MCP client 冒烟测试
- [ ] **LLM 规则提炼 Prompt 调优**：ConsolidationEngine.Extract 当前仅为字符串拼接，需接入 LLM
- [ ] **回测逻辑**：从 stub 实现（集群大小判定）改为真实 LLM 预测验证
- [ ] **后台定时调度**：睡眠整合当前为手动触发，需加 cron（每日凌晨 3 点）
- [ ] **MinIO 快照存储**：EpisodicMemory.full_snapshot_uri 字段已就位，写入链路未实现
- [ ] **RELATES_TO 边创建**：写入时不创建关联边

---

## 8. 关键决策记录

### D1: 为什么 v0.1 不引入 Pitfall Memory 独立建模

错误记忆的数据结构依赖于足够多的"调试记录情境记忆"积累。v0.1 先在情境记忆层存 `task_type=debug` 的记录，让数据自然积累。v0.2 时基于积累数据做 Pitfall 节点提取，比凭空设计 Schema 更可靠。

### D2: 为什么用 pgvector 而不是一开始就用 Milvus

v0.1 的数据规模在万级以下，pgvector 的 IVFFlat 索引足够。运维成本低（一个 PostgreSQL 实例搞定所有关系型+向量需求）。当单表超过 100 万条时再迁移 Milvus，迁移成本可控（只需换向量操作层的 adapter）。

### D3: 睡眠整合的触发频率

v0.1 采用手动触发 + 定时触发（每晚一次）。不采用"闲置检测自动触发"，因为这个判断条件 v0.1 阶段难以可靠实现。定时触发 = 每天凌晨 3 点执行一次。

### D5: 技术栈从 Python 切换到 Go

原设计选用 Python 3.11 + FastAPI + asyncio，实际实现采用 Go 1.25 + go-chi/v5。主要原因：
- VatBrain 作为基础设施服务，Go 的编译为单一二进制、低内存占用、无运行时依赖更适合部署
- Neo4j / pgvector / Redis / MinIO 的 Go driver 成熟度足够
- 团队主要技术栈为 Go，降低维护成本
- 设计文档中的 Python 伪代码保留作为算法参考，实现已全部翻译为 Go

### D4: 情境过滤缓存 TTL = 1 小时

这是基于"用户通常在同一项目上连续工作 1-2 小时"的经验假设。太短（<30min）频繁重建，太长（>4h）可能跨任务污染。1 小时是初始值，后续根据实际使用数据调参。

---

## 9. 测试策略

### 单元测试

| 模块 | 覆盖率目标 | 关键场景 |
|-----|-----------|---------|
| weight_decay | >90% | 极端时间差、零访问、频繁访问模式 |
| significance_gate | >90% | 四类通过条件 + 边界情况 |
| pattern_separation | >90% | 相似但不同项目、相同项目不同实体 |
| retrieval_engine | >85% | 空候选集、全排斥、跨语言场景 |

### 集成测试

- 写入 → 检索 端到端（同一项目、不同项目）
- 权重衰减曲线验证（写入 100 条，观察 7 天衰减）
- 睡眠整合完整流程（人工构造 20 条相关情境记忆 → 触发整合 → 验证产出规则）

### 性能基准

- 写入延迟：< 200ms（含向量定位 + 图写入）
- 检索延迟：< 100ms（情境命中缓存）/ < 500ms（情境 miss）
- 睡眠整合：< 5min（处理 24h 内的 1000 条情境记忆）

---

*本文档随 v0.1 迭代持续更新。所有设计决策都应回溯到 `../DESIGN_PRINCIPLES.md` 进行一致性验证。*
