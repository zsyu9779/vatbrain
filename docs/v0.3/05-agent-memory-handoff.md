# 05 — Agent Memory 轨 Handoff（新会话启动指南）

> 本文件供**新会话**接手 Agent Memory 轨评测。User Memory 轨已完成（见 `docs/v0.3/04-omnimemeval-benchmark-results.md`）。

## 接手前须知（上一会话状态）

- **分支**：`feature/omnimemeval-benchmark`（新会话先 `git checkout feature/omnimemeval-benchmark`）
- **OmniMemEval 克隆**：`/tmp/OmniMemEval`（含已应用的 vatbrain adapter、patch、judge 修复）
- **OmniMemEval venv**：`/tmp/oe-venv`（含 torch/transformers/NLTK 数据，用于 judge）
- **bench server**：`cmd/vatbrain-bench`（User Memory 用，Agent Memory 轨**不需要**它——Agent Memory 走 hermes 插件直连）
- **凭据文件**（本地，勿提交）：
  - `eval/omnimemeval/.env.bench` —— 智谱 embedding key
  - `/tmp/OmniMemEval/.env.vatbrain` —— DeepSeek answer/judge key
- **Hermes + vatbrain 插件**：已安装且激活（`~/.hermes/plugins/vatbrain/`，`memory.provider: vatbrain`）

## 用户明确要求（务必执行）

1. **DeepSeek 关闭 thinking**：新 benchmark 所有 answer/judge 调用必须设 `LLM_DISABLE_THINKING=1`。
   - 已实现：OmniMemEval 的 `scripts/utils/llm_client.py` 加了 `maybe_disable_thinking()`，设此 env 即注入 `thinking: {type: disabled}`。
   - 原因：官方快照 judge 用 gpt-4o-mini（非 thinking），口径要一致。
2. **Embedding 并发最大化**：调研智谱 embedding-3 允许的并发量（见下方【并发调研】），调大 worker 数，大幅缩短 ingestion。

## Agent Memory 轨总览

评测对象：**Hermes + vatbrain 记忆插件**（或 OpenClaw + vatbrain）在 5 个任务域上的任务完成率提升。

- 数据源：`EverMind-AI/EvoAgentBench`（HF）
- 域：reasoning(OmniMath)、information_retrieval(BrowseCompPlus)、software_engineering(SWE-Bench)、code_implementation(LiveCodeBench)、knowledge_work(GDPVal)
- 协议：`memory_train_backup_test`（清理→train→沉淀→备份→恢复→test）
- 参考：OmniMemEval 官方 `docs/agent_memory/` 与 `results_zh.md`（MemOS 在 Hermes 上 45.15%→53.05%）

## 步骤

### 1. 环境

```bash
git checkout feature/omnimemeval-benchmark
# Agent Memory 独立 conda 环境（勿与 omnimemeval 混用）
conda create -n agentmem python=3.12 -y
conda activate agentmem
pip install -r /tmp/OmniMemEval/requirements_agentbench.txt
```

### 2. 数据

```bash
cd /tmp/OmniMemEval
mkdir -p data/agentbench
huggingface-cli download EverMind-AI/EvoAgentBench --repo-type dataset --local-dir ./data/agentbench
```

### 3. Hermes + vatbrain 插件验证

```bash
openclaw --version   # Agent 轨需要 agent 运行时；Hermes 已有
hermes --version     # 确认 hermes CLI 在 PATH
# 确认 vatbrain 插件激活（memory.provider: vatbrain）
cat ~/.hermes/plugins/vatbrain/config.yaml
```

### 4. 域依赖

| 域 | 依赖 | 备注 |
|---|---|---|
| SWE-Bench | **Docker** | 本机无 docker；**orange**（`ssh orange`）有 Docker 28.1.1 但 **aarch64(ARM)**，SWE-Bench 官方镜像是 x86_64 → 需评估 QEMU 模拟或换方案 |
| LiveCodeBench | `git clone https://github.com/LiveCodeBench/LiveCodeBench` + `pip install --no-deps -e` | |
| BrowseCompPlus | embedding 服务 + dense index（`scripts/agentbench/utils/browsecomp-plus-tools/`） | 可复用智谱 embedding |
| GDPVal | poppler-utils / libreoffice | PDF/PPTX 处理 |
| OmniMath | 无额外 | |

### 5. 配置

- 复制 `.env.agent` 模板：`cp env_examples/.env.agent .env.agent`，填：
  - `LLM_BASE_URL` / `LLM_API_KEY`（DeepSeek）
  - `JUDGE_MODEL` / `JUDGE_API_BASE` / `JUDGE_API_KEY`
  - `IR_EMBEDDING_*`（BrowseComp 检索，可复用智谱）
  - `EVALUATION_*`（外部 verifier）
- `configs/agentbench/agents/openclaw.yaml`：模型/provider
- `configs/agentbench/memory_plugins/vatbrain.yaml`：新增 vatbrain 插件生命周期配置（clear/backup/restore 命令，参考 `memos.yaml`）
- **关闭 thinking**：启动命令前 `export LLM_DISABLE_THINKING=1`

### 6. 运行

```bash
# 单域 smoke（先验证链路）
export LLM_DISABLE_THINKING=1
./scripts/run_agent_eval.sh --agent hermes --domain reasoning \
  --protocol memory_train_backup_test --memory-plugin vatbrain \
  --version vatbrain_reasoning_smoke --test-runs 1 --trials 1 --parallel 1

# 五域顺序（确认单域 OK 后）
./scripts/run_agentbench_memory_train_backup_test.sh \
  --memory-plugin vatbrain --version vatbrain_5domain --test-runs 1 --trials 1 --parallel 1
```

## 【并发调研】— 智谱 embedding 与 DeepSeek 速率限制

**结论（官方 + 实测，2026-08-10）**：

**智谱 embedding-3 并发上限（官方，按账号等级限制在途请求数，非 QPS）：**

| 账号等级 | 并发上限 |
|---|---|
| V0（0-2000 points） | 50 |
| V1（2000-10000） | 100 |
| V2（10000-50000） | 300 |
| V3（≥50000） | 500 |

- **实测**：本账号 64 并发、128 请求全部 200（~80 req/s），无 429 → 至少 V1（≥100）。建议 **上限保守设 32-64**（低于任何等级限制）。
- 单请求硬限：64 文本/请求、3072 token/文本。**批量到 64 文本/请求**可最大化单请求吞吐。
- 限流错误码：1302（并发上限）、1305/429（过载）；`_retry` 需处理 1302。
- **批量 API**（非实时）：10K 请求/文件、2M 队列、**5 折价**——大规模 ingestion 可选。
- 定价：0.5 元/百万 token（输入计费），无免费档。
- 来源：https://docs.bigmodel.cn/cn/guide/models/embedding/embedding-3 · https://docs.bigmodel.cn/cn/api/rate-limit

**DeepSeek chat（官方 + 实测）**：
- **官方并发上限（账号级，非 QPS/RPM/TPM）**：`deepseek-v4-flash` = **2500 并发**，`deepseek-v4-pro` = 500。超限返回 429；请求从发送到响应完成占一个槽位。
- 实测 32 并发全过 → answer/judge worker 可开 **100-500**（远高于之前保守的 16-32）。
- ⚠️ thinking 模式占槽位更久 + 多耗 token → 已用 `LLM_DISABLE_THINKING=1` 关闭。
- ⚠️ 模型名：2026-07-24 起 deepseek-chat/reasoner 已退役，只有 v4-flash / v4-pro。

**关键机会**：当前 bench server 的 embedding 是**顺序**调用（逐条消息）。把写入改成**并发 embedding**（32-64），ingestion 可缩短 **30-60×**（LME 13560 次写入从 ~2.6h 降到 ~10 分钟量级）。这是下一轮最大的提速点。
- 实现提示：`cmd/vatbrain-bench` 的 `handleAdd` 顺序调 `WriteMemory`（SQLite 单写者）。可先并发做 embedding、再顺序写库；或评估 SQLite 写锁。
- 参考：https://docs.bigmodel.cn/cn/guide/models/embedding/embedding-3 · https://docs.bigmodel.cn/cn/api/rate-limit

## 关键风险与决策点

1. **SWE-Bench 在 ARM 上的可行性**：orange 是 aarch64，SWE-Bench 镜像 x86_64 → QEMU 模拟慢/不稳。可先跑其余 4 域，SWE-Bench 单独评估。
2. **并发**：开大并发前先小规模验证不触发限流（429 有 `_retry` 兜底，但会拖慢）。
3. **Hermes vs OpenClaw**：先 Hermes（vatbrain 已是其插件），OpenClaw 需另装。
4. **本轮结论先看 trend**：Agent Memory 轨重点对比 baseline（无记忆）vs vatbrain 插件的提升，而非绝对分数。
