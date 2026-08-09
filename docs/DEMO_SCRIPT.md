# VatBrain 5 分钟 Demo 脚本

> 目标：从「一条 hermes 记忆」到「下次编辑前注入避坑提醒」的完整闭环，5 分钟内可复现。
> 前置：Go 1.22+，本机 hermes（可选，否则用 SQLite + MCP 独立复现）。

---

## 0. 准备（30 秒）

```bash
cd vatbrain
go test ./internal/...        # 确认全绿
go build -o /tmp/vb-provider ./cmd/vatbrain-provider
```

## 1. 观察 → 记忆入库（1 分钟）

用 `vatbrain-provider` daemon 模拟 hermes 的 `sync_turn`（或直接用真实 hermes）：

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"session_id":"demo","agent_context":"primary","agent_identity":"coder"}}' \
  '{"jsonrpc":"2.0","id":2,"method":"sync_turn","params":{"session_id":"demo","user_content":"不对，ClawFeed 推送必须用 clawfeed-push-v3.py，旧脚本会以错误身份发送","assistant_content":"好的我记错了"}}' \
  '{"jsonrpc":"2.0","id":3,"method":"sync_turn","params":{"session_id":"demo","user_content":"记住：evaluator 输出字段是 total_score 不是 overall_score","assistant_content":"记住了"}}' \
  '{"jsonrpc":"2.0","id":4,"method":"shutdown","params":{}}' \
| /tmp/vb-provider --data /tmp/vb-demo-data
```

验证：
- 第一条是**纠错** → 落库 `is_correction=1`（gate reason `prediction_error`）
- 第二条是**显式记忆** → 落库（gate reason `user_confirmed`）

```bash
sqlite3 /tmp/vb-demo-data/vatbrain.db \
  "SELECT is_correction, substr(summary,1,40) FROM episodic_memories;"
# 1 | 不对，ClawFeed 推送必须用 clawfeed-push-v3.py…
# 0 | 记住：evaluator 输出字段是 total_score…
```

## 2. 提炼 → 注入（2 分钟）

把纠错记忆提升为一条**已确认的 Pitfall**（Workbench 状态机），让它可以被主动注入：

```bash
# 用 MCP 工具确认一条 pitfall（假设已由整合提炼为 proposed）
# vatbrain-mcp → confirm_pitfall {pitfall_id: "<id>"}

# 直接注入一条确认 pitfall 做演示
sqlite3 /tmp/vb-demo-data/vatbrain.db \
  "INSERT INTO pitfall_memories (id, entity_id, entity_type, project_id, language, signature, root_cause_category, fix_strategy, source_type, trust_level, weight, created_at, updated_at, status)
   VALUES ('demo-pitfall-1','clawfeed-push-v3.py','MODULE','coder','zh','ClawFeed 推送必须用 v3.py，旧脚本以错误身份发送','CONFIG','使用 clawfeed-push-v3.py --as bot','USER',5,1.0,datetime('now'),datetime('now'),'confirmed');"
```

## 3. 编辑前注入（2 分钟）

Agent 要改文件前调用 `prepare_edit_context`，VatBrain 返回相关记忆 + 风险 Pitfall + 风险分：

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"session_id":"demo","agent_context":"primary","agent_identity":"coder"}}' \
  '{"jsonrpc":"2.0","id":2,"method":"prepare_edit_context","params":{"session_id":"demo","files":["scripts/clawfeed-push-v3.py"],"task_type":"refactor","user_goal":"修复 ClawFeed 推送身份"}}' \
  '{"jsonrpc":"2.0","id":3,"method":"shutdown","params":{}}' \
| /tmp/vb-provider --data /tmp/vb-demo-data
```

预期输出：

```json
{"jsonrpc":"2.0","id":2,"result":{
  "risk_score": 0.9,
  "reason_codes": ["high_risk_pitfall","memory_recall","recent_error"],
  "pitfalls": [{
    "entity_id": "clawfeed-push-v3.py",
    "signature": "ClawFeed 推送必须用 v3.py…",
    "fix_strategy": "使用 clawfeed-push-v3.py --as bot",
    "status": "confirmed",
    "confidence": 1.0
  }],
  "memories": [{"summary": "不对，ClawFeed 推送必须用 clawfeed-push-v3.py…"}]
}}
```

Agent 据此在编辑前收到避坑提醒，不再重复犯错。

## 4. 干扰率闭环（30 秒）

确认 `suppress_pitfall` 是逃生阀——被抑制的 Pitfall 不再注入（干扰率分子 +1）：

```bash
# vatbrain-mcp → suppress_pitfall {pitfall_id: "demo-pitfall-1"}
# 再次 prepare_edit_context → 该 pitfall 不再出现在结果中
```

---

## 验收核对

| 环节 | 证据 |
|------|------|
| Observe → 入库 | `is_correction=1` 的 episodic |
| Distill → 注入 | `prepare_edit_context` 返回 `risk_score` + `confirmed` pitfall |
| 逃生阀 | suppress 后不再注入 |
| 度量 | `tests/provider_plugin_smoke.py`（端到端）+ `go test ./internal/eval/`（20 场景，reduction 57.4% / interference 14.8%） |

> 真实 hermes 场景：启用 `memory.provider: vatbrain`（见 README 路径 B），hermes 每轮 `sync_turn` 自动同步、`prefetch` 注入回忆、模型可直接调用 `prepare_edit_context`。
