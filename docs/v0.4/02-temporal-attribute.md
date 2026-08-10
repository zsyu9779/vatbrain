# v0.4 Ticket 02 — 时序记忆最小深入(occurred_at + 时间检索)

> 评测证据:LoCoMo Temporal 15.0% / LME Temporal 52.6%,根因 D7:`chat_time` 未入记忆,
> answer 只能依赖摘要里碰巧出现的前缀文本。本票把记忆从"文本日期前缀"升级为
> "结构化时间属性"。评测验证在 ticket 06 收口。

## 实现内容

1. **时间属性**:`episodic_memories.occurred_at TEXT`(UTC RFC3339)+ 迁移回填 + 索引;
   `EpisodicMemory.OccurredAt`;写管线 `WriteEvent.OccurredAt` 透传。
2. **时间检索**:`EpisodicSearchRequest.OccurredAfter/OccurredBefore/SortByOccurredAt`;
   `provider.ParseRelativeTime` 解析"上周/昨天"(窗口)与"最近一次/最新"(时间排序),中英双语。
3. **兼容层**:bench `[YYYY-MM-DD]` 摘要前缀原样保留;读取端零值回退 `CreatedAt`。

## 技术决策

| # | 决策 | 理由 | 备选 |
|---|------|------|------|
| 1 | `occurred_at` 存 UTC RFC3339 文本,过滤用同格式字符串比较 | 与 `created_at` 既有模式一致;同格式下字典序 = 时间序,零新依赖 | unix epoch INTEGER(正确性等价,但引入第二套时间表示) |
| 2 | 迁移:ALTER 加列 + `UPDATE ... WHERE occurred_at IS NULL` 回填 + 索引 | 幂等(duplicate column 容错 + 回填天然幂等);索引放 migrate() 而非 schemaSQL——旧库 schemaSQL 先跑会在缺失列上建索引直接失败 | 索引进 schemaSQL(会破坏既有库迁移) |
| 3 | 零 OccurredAt 回退 CreatedAt(`EffectiveOccurredAt()`,写侧 sqlite 落库时归一化) | "若写路径无显式时间,回退 CreatedAt"是 ticket 语义;落库归一化保证行级无 NULL | 读侧 COALESCE(语义相同,但写侧归一化更简单且查询可走索引) |
| 4 | pattern-separation 合并时保留原 OccurredAt,仅当新事件显式更早时前移锚点 | 合并多为同一事件的复述;前移规则保证时间推理仍能看到故事起点 | 覆盖为新事件时间(丢失原锚点) |
| 5 | `SortByOccurredAt` = rank-then-sort:先按相关性取 top-K,再按 occurred_at 倒序重排 | "最近一次"问的是"最近的相关记忆",纯时间序会丢相关性(比如"Alice 最近做了什么"会返回无关话题的最新记忆) | 纯时间序(实现简单但语义错误,审查否决) |
| 6 | 时间窗口/排序查询旁路 hot cache | 时间查询对新鲜度敏感,且窗口边界每次调用都变,缓存必然过期 | 窗口加入缓存 key(边界每调用变化,缓存形同虚设) |
| 7 | "上周/昨天" = 滚动窗口(now-7d / now-24h) | LoCoMo 事件相对会话时间分布,滚动窗口匹配评测数据;自然历周语义留待"彻底的时间建模" | 自然历周/历日(复杂,无评测证据支持) |
| 8 | 时间过滤/排序语义仅接入 provider 检索层 + store 请求;不动 provider 协议签名 | 范围约束定案"最小深入" | 协议扩展(超范围) |
| 9 | chat_time → OccurredAt 接线仅在 bench(评测 harness);MCP/api/JSON-RPC 写入口无 chat_time,走 CreatedAt 回退 | 生产协议无时间字段,加字段即破坏"不动协议"约束 | 给协议加时间字段(超范围) |
| 10 | neo4jpg 旧后端不持久化 OccurredAt(字段静默忽略) | 旧后端已弃用、不再投入,读取端模型字段回退 CreatedAt 保持兼容 | 同步改旧后端(违背弃用决策) |

## 范围外(留待后续)

- 自然历周/历日语义、时区感知、时间区间查询语法
- 协议层(provider/MCP)携带事件时间
- 跨记忆时间推理(如"X 之后发生了什么"的因果时序检索)
- 评测验证(06 收口):LoCoMo Temporal / LME Temporal 回升测量
