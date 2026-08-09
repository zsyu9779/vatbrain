# OmniMemEval Benchmark Integration for VatBrain

把 VatBrain 作为 memory backend 接入 [OmniMemEval](https://github.com/MemTensor/OmniMemEval)
的 User Memory Evaluation 评测线，与 14 个主流记忆产品在同一套 harness 下横向对比。

设计决策见 `docs/v0.3/tech-specs/03-omnimemeval-benchmark.md`。

## 架构

```text
OmniMemEval harness (Python)
  └── client_factory/vatbrain_client.py   ← 本目录的 adapter（HTTP）
        │  POST /v1/add | /v1/search | /v1/delete
        ▼
cmd/vatbrain-bench (Go HTTP 入口)
  └── 复用 core.WriteMemory + provider.RetrieveEpisodic
        └── 独立 SQLite 评测库（默认 ~/.vatbrain/bench/vatbrain-bench.db）
```

## 需要的东西

| 组件 | 状态 |
|---|---|
| VatBrain 侧 `cmd/vatbrain-bench` | ✅ 本仓库已实现，`go build ./cmd/vatbrain-bench` |
| OmniMemEval 克隆 | 需自行 `git clone https://github.com/MemTensor/OmniMemEval` |
| ANSWER/EVAL LLM 密钥（OpenAI 兼容） | **必需**，跑 answer/judge 阶段需要 |
| VatBrain 语义 embedding 密钥（OpenAI/Voyage） | **强烈建议**；无密钥默认 keyword embedder，分数不可作正式依据 |

> 本机无密钥时仍可本地 smoke（keyword embedder + 只跑 ingestion/search 阶段），
> 已验证：LoCoMo 1540/1540 检索成功。

## 快速开始

```bash
# 1) 构建并启动 bench server（gate off = 测存储+检索内核，推荐）
go build -o ./bin/vatbrain-bench ./cmd/vatbrain-bench
./bin/vatbrain-bench --gate off --port 18080

# 2) 安装 adapter 到 OmniMemEval 克隆（幂等）
bash eval/omnimemeval/setup.sh /path/to/OmniMemEval

# 3) 准备 env（填 ANSWER/EVAL 密钥；embedding 密钥为可选的 VATBRAIN_EMBEDDER_*）
cp eval/omnimemeval/.env.vatbrain.example /path/to/OmniMemEval/.env.vatbrain
#   在 vatbrain-bench 启动进程里配：
#   export VATBRAIN_EMBEDDER_SEMANTIC_PROVIDER=openai
#   export VATBRAIN_EMBEDDER_SEMANTIC_API_KEY=sk-...
#   export VATBRAIN_EMBEDDER_SEMANTIC_BASE_URL=https://api.openai.com/v1
#   export VATBRAIN_EMBEDDER_SEMANTIC_MODEL=text-embedding-3-small

# 4) 跑 benchmark（HaluMem 与 VatBrain 设计点最契合，建议起手）
cd /path/to/OmniMemEval
python data/halumem/prepare_halumem.py
./scripts/run_halumem_eval.sh --lib vatbrain --env .env.vatbrain --version vatbrain_baseline
```

`--version` 隔离结果目录；`--clear 1` 在 ingestion 前通过 `delete()` 清理该版本 user
的旧记忆。

## Gate 模式（评测范围开关）

`vatbrain-bench --gate <off|on>`（或 `VATBRAIN_BENCH_GATE_MODE`）：

- **`off`（默认）**：每条消息都入库，测 VatBrain 的**存储 + 检索内核**。与其它产品
  add() 语义最可比（它们也会存下对话内容）。
- **`on`**：跑真实 `provider.DeriveWriteEvent` 推导 + 显著性门控。"遗忘是默认"的真实
  系统行为。注意：生产输入是 hermes 推导后的 turn 摘要，benchmark 输入是原始消息，
  所以 `on` 会过滤掉大部分英文闲聊 → **预期分数极低，只能作为消融解读**。

想量化门控的影响，同一版本跑两次对比即可。

## 支持的 benchmark

`--lib vatbrain` 可用于全部 5 个 User Memory benchmark：
LoCoMo、LongMemEval、BEAM、PersonaMem v2、HaluMem。
`setup.sh` 已把 `vatbrain` 注册进 LoCoMo 双 speaker 分发和单 user 分发表。

## 清理

```bash
./scripts/run_memory_clear.sh --lib vatbrain --env .env.vatbrain --version <name> --datasets locomo,lme,beam,pmv2,hm --dry-run   # 先看
./scripts/run_memory_clear.sh --lib vatbrain --env .env.vatbrain --version <name> --datasets locomo,lme,beam,pmv2,hm --yes
```

## 测试

```bash
# VatBrain 侧（store delete + bench server）
go test ./internal/store/... ./internal/bench/... ./cmd/...

# Adapter 侧（mock HTTP，无需 bench server / 网络）
cd eval/omnimemeval && OMNIMEMEVAL_SCRIPTS_DIR=/path/to/OmniMemEval/scripts \
  python3 -m unittest test_vatbrain_client -v
```

## 安全说明

`vatbrain-bench` 默认只绑定 `127.0.0.1`（`--host`），adapter 默认也连
`http://127.0.0.1:18080`。**绑定非回环地址必须设置 `VATBRAIN_BENCH_API_TOKEN`**（否则拒绝
启动）；设置后所有端点（除 `/health`）要求 `Authorization: Bearer <token>`。adapter 侧若
设了同名 env 变量会自动带上该 header。回环绑定 + 无 token 的组合最省心，适合本机评测。

## 已知限制

1. 默认 keyword embedder 语义弱——**正式分数必须配语义 embedding 密钥**。
2. 消息 `chat_time` 未写入记忆（D7）——时序推理类问题偏弱。
3. `gate on` 对英文闲聊过滤激进，分数要结合"遗忘是默认"哲学解读。
4. 逐条消息存储，无 LLM 总结步骤——与提取式产品机制不同（公平但不同）。
5. `delete` 只删 episodic 行，不含 edges 清理（bench 不跑 consolidation，无影响）。
