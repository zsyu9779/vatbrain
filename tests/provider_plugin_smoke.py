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
    assert len(rows) == 2, f"expected 2 episodes, got {len(rows)}"
    corrections = [r for r in rows if r[0] == 1]
    assert len(corrections) == 1, f"expected 1 IsCorrection=true, got {len(corrections)}"
    print("[vatbrain] SMOKE OK: correction landed with is_correction=1")
    return 0


if __name__ == "__main__":
    sys.exit(main())
