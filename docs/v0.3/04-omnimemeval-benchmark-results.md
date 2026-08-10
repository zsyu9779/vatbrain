# 04 — VatBrain OmniMemEval User Memory 评测报告

> 日期：2026-08-09/10 · 分支：`feature/omnimemeval-benchmark`
> 评测框架：[OmniMemEval](https://github.com/MemTensor/OmniMemEval) User Memory Evaluation
> 集成方案见 `docs/v0.3/tech-specs/03-omnimemeval-benchmark.md`

## 评测配置

| 项 | 配置 |
|---|---|
| 评测对象 | VatBrain 单记忆引擎（`add()`/`search()`/`delete()`，经 `cmd/vatbrain-bench`） |
| Significance Gate | **off**（测存储+检索内核；`on` 为消融） |
| Answer / Judge 模型 | `deepseek-v4-flash`（⚠️ thinking 开启，与官方快照 gpt-4o-mini 非 thinking 口径不同，见下） |
| Embedding | 智谱 `embedding-3`（2048 维，`open.bigmodel.cn/api/paas/v4`） |
| 检索候选池 | `embeddingRankPool` 5000（修复后，原 500） |
| 评测数据 | HaluMem-Medium（20 users）、LoCoMo（10 convs）、LongMemEval-S（500） |

## 结果总览

| Benchmark | VatBrain | 官方复现快照中位 | 第一梯队 |
|---|---:|---:|---:|
| **LongMemEval** | **74.20%** | ~66% | MemOS 89.2 / EverOS 80.4 / Zep 79.8 |
| **HaluMem** | **64.97%** | ~73% | EverOS 88.7 / Letta 85.4 / Hindsight 84.0 |
| **LoCoMo** | **57.00%** | ~73% | MemOS 88.8 / Cognee 83.5 |

### LongMemEval（500 题）— 最强项

| 类别 | 分数 |
|---|---:|
| single-session-assistant | 100.0% |
| single-session-user | 97.1% |
| single-session-preference | 80.0% |
| multi-session | 72.9% |
| knowledge-update | 71.8% |
| temporal-reasoning | 52.6% |

### HaluMem（3460 题）

| 类别 | 分数 |
|---|---:|
| Memory Boundary（记忆边界） | **96.3%**（第一梯队，MemOS 91.3） |
| Memory Conflict（冲突） | 70.5% |
| Generalization | 58.2% |
| Multi-hop | 51.3% |
| Basic Fact Recall（事实召回） | 43.6%（smoke 时 10%，池修复后大幅回升） |
| Dynamic Update | 28.9% |

### LoCoMo（914 题）

| 类别 | 分数 |
|---|---:|
| Single Hop | 74.6% |
| Open Domain | 56.1% |
| Multi Hop | 51.3% |
| Temporal Reasoning | **15.0%**（短板） |

## 结论画像

**VatBrain 在 14 款记忆产品中处于中游**（自托管 SQLite vs 云产品），但分布极不均匀：

**强项（第一梯队）**
1. **记忆边界 96.3%** —— 诚实识别"记忆里没有的信息"、不捏造。这是 VatBrain"遗忘是默认"设计哲学的直接验证
2. **LongMemEval 单 session 记忆 97-100%** —— "这条对话里说过什么"接近满分
3. **跨 session 用户画像/偏好 72-97%**

**弱项（明确短板）**
1. **时序推理**（LoCoMo 15%、LME 52.6%）—— `chat_time` 未写入记忆（D7 已知限制），时序是硬伤
2. **动态更新 28.9%**（HaluMem）—— 信息更新后的状态跟踪弱
3. 事实级精确定位 43.6% —— 池修复后从 10% 大幅回升，但仍有提升空间

## 方法论说明与修复

1. **检索候选池修复**（`3cf67ad`）：`SearchEpisodic` embedding 候选池 500→5000。原实现按 weight 预截断，近均匀权重下取到任意子集，具体事实常漏 —— 这是 HaluMem Basic Fact 从 10%→43.6% 的直接原因。
2. **Judge 兼容修复**（OmniMemEval 克隆侧，公平于所有产品）：
   - `extract_label_json` 容忍 JSON+多余字段 / 裸词 / 无引号值（DeepSeek judge 输出格式不稳）
   - judge prompt 改为"只输出 JSON"（原 prompt 自相矛盾）
   - ⚠️ 修复过程中引入过 `.format()` 花括号 bug，已转义修复
3. **⚠️ judge 模型口径差异**：本报告分数用 `deepseek-v4-flash`（thinking 开）judge，官方复现快照用 `gpt-4o-mini`（非 thinking）。两者严苛度不同，**绝对分不完全可比**。下一轮已实现 `LLM_DISABLE_THINKING=1` 对齐官方口径。

## 复现

```bash
# bench server（gate off，智谱 embedding）
bash eval/omnimemeval/run_bench.sh --gate off --port 18080

# 三个 benchmark（OmniMemEval 克隆内）
./scripts/run_halumem_eval.sh  --lib vatbrain --env .env.vatbrain --version vatbrain_v2
./scripts/run_locomo_eval.sh   --lib vatbrain --env .env.vatbrain --version vatbrain_locomo
./scripts/run_lme_eval.sh      --lib vatbrain --env .env.vatbrain --version vatbrain_lme --streaming 1
```

结果目录：`results/{halumem,locomo,lme}/vatbrain-<version>/`。
完整配置/credential 见 `eval/omnimemeval/README.md` 与 `.env.bench` / `.env.vatbrain`。

## 已知限制与下一步

1. **时序短板**：把 `chat_time` 写入记忆（扩展 `WriteMemory` 签名）预计能显著提升 LoCoMo Temporal / LME Temporal。
2. **judge 口径**：下轮 `LLM_DISABLE_THINKING=1` 重跑，对齐官方。
3. **并发**：本轮的 embedding 调用是顺序的（bench server 单写者）。已调研智谱 embedding 并发上限，见 Agent Memory handoff 文档 —— 下轮可开大并发大幅缩短 ingestion。
4. **Agent Memory 轨**（Hermes + vatbrain 插件）是 VatBrain 主场，尚未跑，见 `docs/v0.3/05-agent-memory-handoff.md`。
