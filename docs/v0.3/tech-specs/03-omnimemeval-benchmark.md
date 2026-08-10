# 03 — OmniMemEval Benchmark 集成

> 分支：`feature/omnimemeval-benchmark`。默认不合入 main，除非 adapter 被证明对项目
> 推进和完整性有正向价值。

## 目标

让 VatBrain 作为 memory backend 接入 [OmniMemEval](https://github.com/MemTensor/OmniMemEval)
的 User Memory Evaluation 评测线，与 Mem0 / MemOS / Zep 等 14 个主流记忆产品在同一套
harness（相同数据、prompt、judge、指标）下横向对比。

评测对象：VatBrain 的**写入 + 检索**能力（`add()` / `search()` / `delete()`），即记忆
内核的存储与召回质量，而不是 Hermes 集成或 Watcher。

## 架构总览

```text
OmniMemEval harness (Python)
  └── client_factory/vatbrain_client.py   ← 新增 adapter（HTTP）
        │  POST /v1/add | /v1/search | /v1/delete
        ▼
cmd/vatbrain-bench (Go HTTP 入口, 独立进程)
  └── internal/bench.Server
        ├── add()    → core.WriteMemory（显著性门控 → 模式分离 → 持久化 → 关联）
        ├── search() → provider.RetrieveEpisodic（语义 + 词法回退）+ 文本格式化
        └── delete() → store.EpisodicDeleteStore.DeleteEpisodicByProject（新能力）
  复用 app.New 装配（Store/Embedder/各引擎），独立 SQLite 评测库
```

## 关键决策

| # | 决策 | 理由 | 影响 |
|---|------|------|------|
| D1 | 独立 `cmd/vatbrain-bench` HTTP 入口，不动生产 `vatbrain-provider`（D1 约束：生产走 stdio JSON-RPC，无 HTTP/MCP） | 评测语义完全可控；评测库与生产库隔离；与 OmniMemEval 其它 15 个 backend 同为 HTTP，adapter 最标准 | 多一个可执行文件 |
| D2 | `user_id` → `ProjectID` 直映射；`Language` 默认 `en`（benchmark 数据全英文） | VatBrain 模型天然按 ProjectID 划分记忆域；benchmark 与生产（hermes 中文）语言无关 | 无 |
| D3 | **Gate 模式开关** `VATBRAIN_BENCH_GATE_MODE`（默认 `off`）：`off` 强制过闸（`event.UserConfirmed=true`，零生产改动），`on` 走真实 `provider.DeriveWriteEvent` 推导 | `off` 测存储+检索核心（与其它产品公平对比，它们 add() 都会存下对话内容）；`on` 测"遗忘是默认"的真实系统行为（预期极低分，作为消融） | gate-mode=on 时 `DeriveWriteEvent` 的正则面向中英文，对英文闲聊过滤很激进，分数必须结合此语义解读 |
| D4 | 新增 `store.EpisodicDeleteStore` **可选能力接口**（沿用 `RuleConflictStore` 先例），sqlite + in-memory 实现 | OmniMemEval 的 `--clear` 与 streaming 模式依赖 delete；可选接口避免破坏弃用的 neo4j+pgvector 后端 | bench 对不实现该接口的后端返回 501 |
| D5 | `add()` 逐条消息写入（一条消息 = 一条 episodic memory） | 保留全部对话信息（与其它产品一致，它们提取后基本全存）；不引入 LLM 总结，基准分数不依赖额外模型 | speaker 身份由 harness 嵌在 content（如 LoCoMo 的 `"speaker: text"`），VatBrain 按整条存储+召回，不额外合成前缀 |
| D6 | 默认 keyword embedder（零 API，本地可跑 smoke）；真实评测切 `VATBRAIN_EMBEDDER_SEMANTIC_PROVIDER=openai` | keyword embedder 确定性、CJK-safe、非零向量，可端到端验证 harness；但语义召回弱，**不能作为正式分数依据** | 本地无密钥也能 smoke；正式分数需要语义 embedding 密钥 |
| D7 | 消息 `chat_time` 暂不写入 `CreatedAt`（写入用 `time.Now()`） | 生产语义即"现在"；honor chat_time 需要扩展 WriteMemory 签名，列入已知限制 | 时序推理类 benchmark（LoCoMo temporal）会偏弱，需在结果解读中说明 |

## HTTP 契约（bench server）

| 端点 | 请求 | 响应 |
|---|---|---|
| `POST /v1/add` | `{"user_id": str, "messages": [{"role","name","content","chat_time"?}]}` | `{"persisted": n, "skipped": m, "gate_reason_counts": {...}}` |
| `POST /v1/search` | `{"user_id": str, "query": str, "top_k": int}` | `{"results": [{"content": str, "weight": float}]}` |
| `POST /v1/delete` | `{"user_id": str}` | `{"deleted": n}`；后端不支持 → 501 |
| `GET /health` | — | `{"ok": true}` |

`add` 对 gate-mode=off 时逐条强制过闸；`search` 复用 `RetrieveEpisodic`（limit=top_k），
返回摘要纯文本，adapter 侧 `\n`.join 成 OmniMemEval 期望的字符串。

认证：bench server 默认只绑 `127.0.0.1`；绑非回环必须设 `VATBRAIN_BENCH_API_TOKEN`，
此时除 `/health` 外所有端点要求 `Authorization: Bearer <token>`（无其它认证手段）。

## OmniMemEval 侧改动（`eval/omnimemeval/`）

- `vatbrain_client.py`：继承 `BaseApiClient`，实现 `add(messages, user_id, **kw)`、
  `search(query, user_id, top_k) -> str`、`delete(user_id)`。读 `VATBRAIN_BENCH_BASE_URL`
  （默认 `http://127.0.0.1:18080`）。
- 注册点（对 OmniMemEval 克隆的 4 处小补丁，由 `setup.sh` 幂等应用）：
  1. `scripts/client_factory/registry.py` → `"vatbrain": ("vatbrain_client", "VatbrainClient")`
  2. `scripts/locomo/locomo_search.py` → `_search_dispatch["vatbrain"] = generic_text_search`
  3. `scripts/utils/search_helpers.py` → `DEFAULT_SEARCH_DISPATCH["vatbrain"] = generic_text_search`
  4. `env_examples/.env.vatbrain`（由 `.env.vatbrain.example` 复制）

## 运行方式

```bash
# 1) 启动 bench server（gate off，检索核心）
VATBRAIN_BENCH_GATE_MODE=off go run ./cmd/vatbrain-bench

# 2) OmniMemEval 侧
bash eval/omnimemeval/setup.sh /path/to/OmniMemEval
cp eval/omnimemeval/.env.vatbrain.example .env.vatbrain   # 填 ANSWER/EVAL LLM 密钥
cd /path/to/OmniMemEval
python data/halumem/prepare_halumem.py
./scripts/run_halumem_eval.sh --lib vatbrain --env .env.vatbrain
```

完整指南见 `eval/omnimemeval/README.md`。

## 已知限制

1. **默认 keyword embedder 语义弱**——正式分数必须配 `VATBRAIN_EMBEDDER_SEMANTIC_PROVIDER=openai`。
2. **`chat_time` 未写入记忆**——时序推理类问题偏弱（D7）。
3. **gate-mode=on 对英文闲聊过滤激进**——分数要结合"遗忘是默认"的哲学解读。
4. **逐条消息存储**——无 LLM 总结步骤，与 MemOS/Mem0 的提取式 add() 存在机制差异（公平但不同）。
5. **delete 不含 edges 清理**——只删 episodic 行；bench 不跑 consolidation，悬空边无影响。

## 验收

- `go vet ./...` + `go test ./internal/...` 全绿（含新增 bench/store 测试）。
- adapter 单测（mock HTTP）覆盖 add/search/delete 与错误分支。
- 本地无密钥时 `cmd/vatbrain-bench` + 一个 fake 请求可端到端 smoke（keyword embedder）。
