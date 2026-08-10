# v0.4-01 bench 基建:并发 ingestion + 延迟微基准

> 状态:已实施(ticket 01 完成,2026-08-10)
> 对应 ticket:`.scratch/v0.4-hardening/issues/01-bench-concurrency-and-latency.md`
> 前置调研:`docs/v0.3/05-agent-memory-handoff.md`【并发调研】

## 1. 目标

1. 评测入口(`vatbrain-bench`)的写入不再逐条顺序调 embedding——多条消息**并行 embedding**(worker 池 32–64 可配置、批量 64 文本/请求、429/1302 重试),**再顺序写库**。大规模记忆导入(LME 13560 次写入)从 ~2.6h 缩短到 ~10 分钟量级。
2. 暴露写入 / 检索(命中/miss)/ 整合的 **p95 延迟指标**,让 ROADMAP 跨版本性能里程碑(写入 <200ms、检索命中 <100ms / miss <500ms)首次可验证。

## 2. 架构:并发 embedding、顺序写库

`/v1/add` 的处理拆成两阶段,语义与原先的顺序流水线完全一致:

```
Phase 1(并行):  构建全部 WriteEvent → 摘要文本分块(64/块)→ worker 池(默认 32,≤64)
                并行调批量 embedding(429/1302/1305 指数退避重试)→ 按输入顺序重组向量
Phase 2(顺序):  按请求顺序逐条执行 write pipeline(显著性门控 → 相似检索 → 可分离性判别
                → 持久化 → working-memory 累积),复用 Phase 1 的预计算向量
```

- **SQLite 是单写者**(modernc.org/sqlite,WAL):写库保持顺序;并发只发生在 embedding 网络调用上。
- **语义不变**:门控仍在 Phase 2 按顺序评估(working-memory 跨周期判定依赖顺序);pattern-separation 合并方向依赖顺序,预计算向量对同文本是确定性的,不改变任何判定。
- **代价**:GateModeOn 下被门控拒掉的空消息也会先被 embedding(浪费调用,结果不变)。Benchmark 主模式 GateModeOff(UserConfirmed 全部持久化)无浪费。

### 2.1 分层

| 层 | 位置 | 职责 |
|---|---|---|
| `OpenAIProvider.EmbedBatch` | `internal/embedder/batch.go` | 单次 HTTP 批量请求;内部按 **64 文本/请求** 硬限分块;对限流响应(**HTTP 429、错误码 1302/1305**,字符串或数字形态)指数退避重试(默认 5 次,250ms 起、8s 封顶);向量按输入顺序返回 |
| `KeywordEmbedder.EmbedBatch` / `DualChannelEmbedder.EmbedBatch` | 同上 | 关键词通道批量等价;双通道批量 = 语义批量 + 逐文本关键词回退(与单文本 `Embed` 语义完全一致:错误或零向量 → 关键词向量) |
| `BatchEmbedder` | 同上 | **worker 池**(默认 32,钳制 [1,64])+ 分块 + 顺序重组;首个 chunk 错误即 fail-fast 取消剩余任务;内层不支持批量时返回 `ErrBatchNotSupported` |
| `core.WriteMemoryWithEmbedding` | `internal/core/write_pipeline.go` | write pipeline 的 seam:门控/检索/合并/持久化与 `WriteMemory` 完全一致,仅跳过内部 embedding 调用(空向量报错)。两函数共享 `prepareWriteEvent`(校验 + 门控 + surprise)与 `writeMemoryPersist`(检索 → 合并|新建 → 持久化) |
| `bench.Server` | `internal/bench/server.go` | `NewServer` 把 embedder 包进 `BatchEmbedder`;`handleAdd` 两阶段执行;无批量能力的 embedder(如 Claude embedder)自动回退顺序路径;`Options.IngestWorkers` / `EmbedBatchSize` 可配置(默认 32 / 64) |

### 2.2 配置

`cmd/vatbrain-bench` 新增 flag(env 可覆盖):

```
--embed-workers 32   # 并行 embedding worker 池,钳制 [1,64](env VATBRAIN_BENCH_EMBED_WORKERS)
--embed-batch   64   # 每请求文本数,provider 硬限 64(env VATBRAIN_BENCH_EMBED_BATCH)
```

## 3. 延迟微基准

### 3.1 命令(可复现)

```bash
go test ./internal/bench/ -bench . -benchtime 1s -count 1   # 首次实测记录使用此命令
go test ./internal/bench/ -bench . -benchtime 2s -count 3   # 取稳定均值时使用(约 10 分钟)
```

基准位于 `internal/bench/latency_bench_test.go`,输出 `p50/p95/p99`(nearest-rank,`internal/bench/latency.go`):

| 基准 | 测量对象 | 条件 |
|---|---|---|
| `BenchmarkWriteLatency/{memory,sqlite}/{full,precomputed}` | 写入 p95:`full` = 完整流水线(`WriteMemory`);`precomputed` = 并发 ingestion 的 Phase 2(`WriteMemoryWithEmbedding`,向量在计时外预计算) | keyword embedder(无外部 API,测内核成本);`SkipLinkOnWrite: true` 对齐 bench 入口;语料 2000 条循环,库稳定在 ~2000 行(中期 ingestion 规模) |
| `BenchmarkSearchLatency/{hit,miss}` | 检索 p95:`hit` = 查询来自已存记忆原文;`miss` = 与库中词汇无交集 | 500 条已存记忆的 sqlite 库;`provider.RetrieveEpisodic`(即 `/v1/search` 路径) |
| `BenchmarkConsolidation/300-memories` | 整合单次耗时(scan → cluster → extract → backtest) | 内存库 + 300 条记忆,每轮全新库;无 LLM 时拼接规则在 0.7 阈值下不通过 backtest,故不含语义记忆持久化 |

### 3.2 指标口径(务必注意)

- **关键词 embedder**:数字覆盖 VatBrain 内核(门控 → 检索 → 持久化),不含付费 embedding 的网络延迟——那是外部成本,不是记忆内核成本。与真实语义 embedder 的差距 = embedding API 延迟 × 调用次数。
- **写入延迟随库增长**:每次写入都做 pattern-separation 检索(候选池 `embeddingRankPool = 5000` 行 + 进程内余弦,这是 HaluMem 召回修复的代价),所以大库写入比小库慢。p50/p95 反映跑完时的库规模。
- 机器:Apple M3 / darwin arm64。

### 3.3 首次实测数字(2026-08-10,本机 Apple M3 / darwin arm64)

命令:`go test ./internal/bench/ -bench . -benchtime 1s -count 1`(单次运行,数字含运行间噪声;取稳定值用 `-count 3`)

| 指标 | p50 | p95 | p99 | ROADMAP 里程碑 | 结论 |
|---|---|---|---|---|---|
| 写入(内存库,full) | 6.19ms | 8.57ms | 11.98ms | 写入 <200ms | 达标 |
| 写入(内存库,precomputed) | 6.22ms | 7.28ms | 8.18ms | 写入 <200ms | 达标 |
| 写入(SQLite,full) | 26.2ms | 58.1ms | 65.5ms | 写入 <200ms | 达标 |
| 写入(SQLite,precomputed) | 30.1ms | 65.4ms | 71.8ms | 写入 <200ms | 达标 |
| 检索命中(SQLite,500 条) | 30.2ms | 39.4ms | 48.4ms | 命中 <100ms | 达标 |
| 检索 miss(SQLite,500 条) | 30.8ms | 67.9ms | 112.9ms | miss <500ms | 达标 |
| 整合(300 条,内存库) | 3.59ms | 4.93ms | 7.92ms | —(无里程碑) | — |

**结论:ROADMAP 性能里程碑首次实测全部达标**(写入 p95 58.1ms < 200ms、检索命中 p95 39.4ms < 100ms、检索 miss p95 67.9ms < 500ms)。口径见 3.2:内核成本(关键词 embedder)、写入基准为 2000 行库的稳态。

**precomputed 与 full 的差异在噪声内**(关键词 embedding 单次 ~µs,占写入成本 <1%)——并发 ingestion 的提速收益不在内核,而在把付费语义 embedder 的网络调用从逐条(≈690ms/条,顺序 2.6h)变成 32–64 并行 + 64 批量(网络往返成为主要残余成本),该收益需真实 API 实测(见 §5)。

**两个值得注意的观察**(非本 ticket 优化项,记录供后续):
- SQLite 写入 p95(58ms)远高于内存库(8.6ms),且 2000 行稳态下明显高于小库——每次写入的 pattern-separation 检索要拉取候选池 `embeddingRankPool=5000` 行的 context_vector BLOB(1536 维 ≈ 6KB/行)做进程内余弦。这是 HaluMem 召回修复的既定代价,大库下是写入/检索的共同瓶颈,适合作为后续向量索引专项的基线。
- 整合(300 条,无 LLM)3.6ms p50:拼接规则过不了 0.7 backtest 阈值,故不含语义持久化成本(见 §5)。

## 4. 技术决策

| # | 决策 | 备选 | 理由 |
|---|---|---|---|
| D1 | 并发只做 embedding;SQLite 写库保持顺序 | 并发写库(评估过 SQLite 写锁) | 单写者约束;顺序写保证门控/合并语义与顺序流水线完全一致 |
| D2 | 预计算向量复用走 `WriteMemoryWithEmbedding` seam,不复制流水线 | bench 自建 write 逻辑 | benchmark 必须测生产语义;共享 `prepareWriteEvent`/`writeMemoryPersist` 防止两处漂移 |
| D3 | 重试(429/1302/1305)归属 `OpenAIProvider.EmbedBatch`(网络层),worker 池归属 `BatchEmbedder` | 重试放池层 | 限流错误码是 provider 的知识;池层只负责并发/分块/顺序 |
| D4 | 双通道批量回退语义 = 逐文本关键词回退(与 `Embed` 一致) | 整批失败即报错 | benchmark 测的是真实流水线;保持与单文本路径逐字节一致 |
| D5 | `NewServer` 无条件把 embedder 包进 `BatchEmbedder`;无批量能力时回退顺序路径 | 仅批量型 embedder 走新路径 | 路径统一、配置始终生效;回退用 `ErrBatchNotSupported` 哨兵 |
| D6 | 微基准用关键词 embedder + 本地 SQLite,可离线可复现 | 真实 API 端到端压测 | 内核成本是 VatBrain 拥有的部分;embedding 网络延迟是外部成本,单独记录 |
| D7 | 写入基准语料 2000 条循环,库稳定在 ~2000 行 | 无界增长 | 无界增长让单次基准跑到数十分钟且不可复现;2000 行近似中期 ingestion 规模 |

## 5. 已知限制与后续

- GateModeOn 下预 embedding 会为被门控消息浪费调用(结果不变)——如成为成本问题,可在 Phase 1 前用 cheap 信号预筛。
- 无 LLM 时整合的拼接规则过不了 0.7 backtest 阈值,整合基准不含语义持久化成本。要覆盖持久化成本需 LLM 引擎(真实 backtest)或调低 AccuracyThreshold——注意 0.85 合并阈值与 0.7 backtest 阈值之间的狭窄区间使无 LLM 场景下持久化结构性难以触发,属引擎行为而非基准缺陷;接入 LLM 后补测。
- 大规模(LME 13560)实测需真实 embedding API key;预计提速 30–60×(并发调研:顺序 ~690ms/写入 → 并发批量后网络往返成为主要残余成本)。
- 检索候选池 `embeddingRankPool=5000` 的 BLOB 解码是大库检索/write 检索的主要成本;属 HaluMem 召回修复的既定代价,后续可用向量索引专项优化(不在本 ticket 范围)。
