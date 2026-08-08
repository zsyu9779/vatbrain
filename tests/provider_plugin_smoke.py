#!/usr/bin/env python3
"""Smoke test: hermes loads the vatbrain plugin and turns land in SQLite.

Runs against a throwaway HERMES_HOME (never touches the live ~/.hermes),
using hermes's real plugin loader (plugins.memory.load_memory_provider).

Usage:
    go build -o /tmp/vatbrain-provider ./cmd/vatbrain-provider
    VATBRAIN_PROVIDER_BIN=/tmp/vatbrain-provider \
        python3 tests/provider_plugin_smoke.py

Exits 0 on success, non-zero on failure. Output lines start with [vatbrain].
"""

import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time


def main() -> int:
    hermes_root = os.environ.get("HERMES_AGENT_ROOT",
                                 os.path.expanduser("~/.hermes/hermes-agent"))
    if not os.path.isdir(hermes_root):
        print("[vatbrain] hermes-agent source not found at", hermes_root)
        return 2
    sys.path.insert(0, hermes_root)

    repo = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    plugin_src = os.path.join(repo, "plugins", "vatbrain")

    home = tempfile.mkdtemp(prefix="vatbrain-smoke-")
    os.environ["HERMES_HOME"] = home
    os.environ.setdefault("VATBRAIN_PROVIDER_BIN", "/tmp/vatbrain-provider")
    if not os.path.exists(os.environ["VATBRAIN_PROVIDER_BIN"]):
        print("[vatbrain] vatbrain-provider binary missing:",
              os.environ["VATBRAIN_PROVIDER_BIN"])
        return 2

    dst = os.path.join(home, "plugins", "vatbrain")
    os.makedirs(dst, exist_ok=True)
    for f in ("__init__.py", "plugin.yaml"):
        shutil.copy(os.path.join(plugin_src, f), dst)

    from plugins.memory import load_memory_provider, list_memory_provider_names  # noqa: E402

    names = list_memory_provider_names()
    assert "vatbrain" in names, f"vatbrain not discoverable; got {names}"
    print("[vatbrain] discoverable:", names)

    p = load_memory_provider("vatbrain")
    assert p is not None, "vatbrain provider failed to load"
    assert p.is_available(), "is_available() must be True"
    print("[vatbrain] loaded:", p.name, "available:", p.is_available())

    p.initialize("sess-smoke", hermes_home=home, platform="cli",
                 agent_context="primary", agent_identity="coder")
    p.sync_turn("不对，evaluator 输出字段是 total_score 不是 overall_score", "好的")
    p.sync_turn("记住：ClawFeed 推送必须用 clawfeed-push-v3.py", "记住了")
    time.sleep(3)

    # Phase 3: prefetch returns the ingested memory as plain text (hermes
    # wraps it in <memory-context>; the provider never emits the fence).
    p.queue_prefetch("ClawFeed 推送用什么脚本")
    time.sleep(1)
    ctx = p.prefetch("ClawFeed 推送用什么脚本", session_id="sess-smoke")
    assert "[vatbrain memory context]" in ctx, f"prefetch missing header: {ctx!r}"
    assert "clawfeed-push-v3.py" in ctx, f"prefetch missing memory: {ctx!r}"
    print("[vatbrain] prefetch context:", repr(ctx[:120]))

    # Phase 4: built-in memory write mirror → user_explicit episodic
    p.on_memory_write("add", "memory", "用户显式记忆：软路由用 Ruby YAML 解析",
                      {"write_origin": "assistant_tool"})
    p.on_session_end([])  # sleep integration (background, best-effort)
    p.on_session_switch("sess-smoke-2", reset=True)
    time.sleep(3)
    p.shutdown()

    db = os.path.join(home, "vatbrain", "vatbrain.db")
    assert os.path.exists(db), f"sqlite db missing: {db}"
    con = sqlite3.connect(db)

    rows = con.execute(
        "SELECT is_correction, summary, project_id FROM episodic_memories"
    ).fetchall()
    print("[vatbrain] episodic rows:", len(rows))
    for is_corr, summary, pid in rows:
        print(f"  is_correction={is_corr} project={pid} | {summary[:40]}…")
    assert len(rows) == 3, f"expected 3 episodes, got {len(rows)}"
    corrections = [r for r in rows if r[0] == 1]
    assert len(corrections) == 1, f"expected 1 IsCorrection=true, got {len(corrections)}"

    mirrored = con.execute(
        "SELECT source_type, trust_level, full_snapshot_uri FROM episodic_memories "
        "WHERE source_type='USER'"
    ).fetchall()
    assert len(mirrored) == 1, f"expected 1 user_explicit mirror, got {len(mirrored)}"
    uri = mirrored[0][2]
    assert "source=user_explicit" in uri, f"missing source marker: {uri}"
    assert "origin=assistant_tool" in uri, f"missing write_origin: {uri}"
    print("[vatbrain] on_memory_write mirror OK: source_type=USER, source=user_explicit")
    print("[vatbrain] SMOKE OK: correction + prefetch + lifecycle mirror all pass")
    return 0


if __name__ == "__main__":
    sys.exit(main())
