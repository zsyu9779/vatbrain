# Contributing

感谢你考虑为 VatBrain 贡献。这个项目把「主动避免 coding agent 重复犯错」作为主线；以下是适合新贡献者的入口。

## 开发纪律

- 阅读 `CLAUDE.md`（强制规则）与 `docs/DESIGN_PRINCIPLES.md`（设计基石）。
- 所有新测试必须含中文用例（本项目用户是中文语境）。
- 遵循 Conventional Commits，commit 以 `Co-Authored-By:` 结尾。
- 提交前 `go build ./... && go vet ./... && go test ./internal/...` 全绿。

## Good First Issues

| 类型 | 例子 | 复杂度 |
|------|------|--------|
| **新 Watcher 适配器** | Codex / OpenClaw 记忆格式适配（参考 `internal/watcher/adapters/claude_code.go`） | 中 |
| **新评测场景** | 在 `tests/scenarios/` 加一个真实踩坑场景（YAML，参考现有 20 个） | 低 |
| **新 Pitfall 根因分类** | 扩展 `models.PitfallMemory.RootCause` 枚举 + 提取启发式 | 低 |
| **README / Quick Start 验证** | 按 README 路径 A 跑通，反馈文档问题 | 低 |
| **文档翻译/校对** | README 中英一致性、术语规范核对 | 低 |

完整待办清单与优先级见 [Issue #1](https://github.com/zsyu9779/vatbrain/issues/1)。

## 提交前检查

```bash
go build ./...
go vet ./...
go test ./internal/...        # 21 包全绿
python3 tests/provider_plugin_smoke.py  # hermes 插件端到端（可选，需本机 hermes）
```
