"""vatbrain — hermes MemoryProvider plugin (one-way bridge, D4).

Spawns the ``vatbrain-provider`` Go daemon (stdio JSON-RPC) and mirrors hermes
turns into the VatBrain graph. hermes → vatbrain only; vatbrain never writes
back to hermes (D4). The provider contributes no tools and no system-prompt
block (D5) — per-turn recall flows through the prefetch channel (Phase 3).

Design notes (verified against hermes memory_manager.py):
- hermes serialises sync_turn on a single FIFO worker, so each call here
  spawns a short-lived daemon thread; a lock guards the daemon's stdin/stdout
  so requests stay serialised even if two threads race.
- Non-``primary`` agent_context (subagent/cron/flush) must skip writes — the
  daemon enforces this too; the plugin short-circuits before sending.

Configuration
-------------
VATBRAIN_PROVIDER_BIN — path to the vatbrain-provider binary (default:
``shutil.which("vatbrain-provider")``, then ``$HERMES_HOME/vatbrain/bin/``).
The daemon keeps its SQLite DB under ``$HERMES_HOME/vatbrain/vatbrain.db``.
"""

from __future__ import annotations

import json
import logging
import os
import select
import shutil
import subprocess
import threading
from typing import Any, Dict, List, Optional

from agent.memory_provider import MemoryProvider

logger = logging.getLogger(__name__)

_JSONRPC = "2.0"
_IO_TIMEOUT_S = 60.0
_INIT_TIMEOUT_S = 30.0
# hermes joins external prefetch on a daemon thread with an 8s timeout
# (memory_manager._EXTERNAL_PREFETCH_TIMEOUT_S); stay under it so the
# provider result lands before hermes gives up.
_PREFETCH_TIMEOUT_S = 7.0


class VatBrainMemoryProvider(MemoryProvider):
    """hermes MemoryProvider that mirrors turns into VatBrain via stdio JSON-RPC."""

    # Class-level defaults so is_available()/backup_paths() work BEFORE
    # initialize() runs — hermes calls is_available() at activation, then
    # initialize() only if it returned True (agent_init.py activation chain).
    _hermes_home = ""
    _session_id = ""

    @property
    def name(self) -> str:
        return "vatbrain"

    # -- Core lifecycle ----------------------------------------------------

    def is_available(self) -> bool:
        """True when the vatbrain-provider binary is resolvable. No network."""
        return self._resolve_binary() is not None

    def initialize(self, session_id: str, **kwargs: Any) -> None:
        self._session_id = session_id
        self._hermes_home = kwargs.get("hermes_home", "") or os.getenv("HERMES_HOME", "")
        self._agent_context = kwargs.get("agent_context", "primary")
        self._agent_identity = kwargs.get("agent_identity", "") or "hermes"
        self._platform = kwargs.get("platform", "cli")

        self._io_lock = threading.Lock()
        self._proc: Optional[subprocess.Popen] = None
        self._spawn_lock = threading.Lock()
        self._binary = self._resolve_binary()
        if self._binary is None:
            logger.warning("vatbrain: vatbrain-provider binary not found — provider inactive")
            return

        try:
            self._spawn()
            self._rpc("initialize", {
                "session_id": session_id,
                "hermes_home": self._hermes_home,
                "platform": self._platform,
                "agent_context": self._agent_context,
                "agent_identity": self._agent_identity,
            }, timeout=_INIT_TIMEOUT_S)
            logger.info("vatbrain: provider initialized (session=%s, project=%s)",
                        session_id, self._agent_identity)
        except Exception as exc:  # best-effort — never break agent init
            logger.warning("vatbrain: initialize failed: %s", exc)
            self._shutdown_proc()

    def sync_turn(self, user_content: str, assistant_content: str, *,
                  session_id: str = "", messages: Optional[List[Dict[str, Any]]] = None) -> None:
        """Mirror a completed turn to the daemon in the background."""
        if self._agent_context not in ("", "primary"):
            return  # non-primary contexts must not write (hermes contract)
        if self._proc is None or self._proc.poll() is not None:
            return  # daemon not spawned or already exited

        params = {
            "session_id": session_id or self._session_id,
            "user_content": user_content,
            "assistant_content": assistant_content,
            "agent_context": self._agent_context,
        }
        threading.Thread(
            target=self._background_sync,
            args=(params,),
            name="vatbrain-sync",
            daemon=True,
        ).start()

    def _background_sync(self, params: Dict[str, Any]) -> None:
        try:
            self._rpc("sync_turn", params, timeout=_IO_TIMEOUT_S)
        except Exception as exc:
            logger.warning("vatbrain: sync_turn failed (best-effort): %s", exc)

    def queue_prefetch(self, query: str, *, session_id: str = "") -> None:
        """Warm the daemon's recall cache for the next turn (fire-and-forget)."""
        if self._proc is None or self._proc.poll() is not None:
            return
        params = {"session_id": session_id or self._session_id, "query": query}
        threading.Thread(
            target=self._background_prefetch_queue,
            args=(params,),
            name="vatbrain-queue-prefetch",
            daemon=True,
        ).start()

    def _background_prefetch_queue(self, params: Dict[str, Any]) -> None:
        try:
            self._rpc("queue_prefetch", params, timeout=_IO_TIMEOUT_S)
        except Exception as exc:
            logger.debug("vatbrain: queue_prefetch failed (best-effort): %s", exc)

    def prefetch(self, query: str, *, session_id: str = "") -> str:
        """Return relevant recall text. The daemon returns a warm cache hit or
        runs a fast synchronous retrieval; hermes wraps the text in the
        <memory-context> fence (providers never emit the fence)."""
        if self._proc is None or self._proc.poll() is not None:
            return ""
        try:
            result = self._rpc("prefetch", {
                "session_id": session_id or self._session_id,
                "query": query,
            }, timeout=_PREFETCH_TIMEOUT_S)
            return result.get("context", "")
        except Exception as exc:
            logger.debug("vatbrain: prefetch failed (best-effort): %s", exc)
            return ""

    def on_session_end(self, messages: List[Dict[str, Any]]) -> None:
        """Trigger sleep integration (rule + pitfall extraction) in the daemon."""
        threading.Thread(
            target=self._background_rpc,
            args=("on_session_end", {"session_id": self._session_id}),
            name="vatbrain-session-end",
            daemon=True,
        ).start()

    def on_session_switch(self, new_session_id: str, *,
                          parent_session_id: str = "", reset: bool = False,
                          rewound: bool = False, **kwargs: Any) -> None:
        """Rebind the daemon session after /new /reset /branch /resume."""
        try:
            self._rpc("on_session_switch", {
                "session_id": self._session_id,
                "new_session_id": new_session_id,
                "parent_session_id": parent_session_id,
                "reset": reset,
                "rewound": rewound,
            }, timeout=_IO_TIMEOUT_S)
        except Exception as exc:
            logger.debug("vatbrain: on_session_switch failed (best-effort): %s", exc)
        self._session_id = new_session_id

    def on_memory_write(self, action: str, target: str, content: str,
                        metadata: Optional[Dict[str, Any]] = None) -> None:
        """Mirror a built-in hermes memory write into the graph as a
        user-explicit episodic (SourceType=USER, highest trust)."""
        meta = {k: str(v) for k, v in (metadata or {}).items() if v is not None}
        threading.Thread(
            target=self._background_rpc,
            args=("on_memory_write", {
                "session_id": self._session_id,
                "action": action,
                "target": target,
                "content": content,
                "metadata": meta,
            }),
            name="vatbrain-memory-write",
            daemon=True,
        ).start()

    def _background_rpc(self, method: str, params: Dict[str, Any]) -> None:
        try:
            self._rpc(method, params, timeout=_IO_TIMEOUT_S)
        except Exception as exc:
            logger.warning("vatbrain: %s failed (best-effort): %s", method, exc)

    def on_turn_start(self, turn_number: int, message: str, **kwargs: Any) -> None:
        """Per-turn tick: every maintenance_interval turns, fire a lightweight
        daemon maintenance RPC (weight recompute / cold-store migration hook)."""
        if not getattr(self, "_turn_count", None):
            self._turn_count = 0
        self._turn_count += 1
        interval = int(os.getenv("VATBRAIN_MAINTENANCE_INTERVAL", "10") or 10)
        if self._turn_count % interval == 0:
            threading.Thread(
                target=self._background_rpc,
                args=("maintenance", {"session_id": self._session_id}),
                name="vatbrain-maintenance",
                daemon=True,
            ).start()

    def on_pre_compress(self, messages: List[Dict[str, Any]]) -> str:
        """Derive a compression-worthy insight from messages about to be
        discarded; hermes folds it into the compression summary prompt."""
        texts = []
        for m in messages or []:
            content = m.get("content", "") if isinstance(m, dict) else str(m)
            if isinstance(content, str) and content.strip():
                texts.append(content.strip())
        if not texts:
            return ""
        try:
            result = self._rpc("pre_compress", {
                "session_id": self._session_id,
                "messages": texts[-20:],  # 有界：只送最近 20 条
            }, timeout=_PREFETCH_TIMEOUT_S)
            return result.get("insight", "")
        except Exception as exc:
            logger.debug("vatbrain: pre_compress failed (best-effort): %s", exc)
            return ""

    def on_delegation(self, task: str, result: str, *,
                      child_session_id: str = "", **kwargs: Any) -> None:
        """Parent-side observation of a subagent delegation → Episodic ingest."""
        threading.Thread(
            target=self._background_rpc,
            args=("on_delegation", {
                "session_id": self._session_id,
                "task": task,
                "result": result,
                "child_session_id": child_session_id,
            }),
            name="vatbrain-delegation",
            daemon=True,
        ).start()

    def backup_paths(self) -> List[str]:
        """No external paths — the daemon keeps its SQLite under
        $HERMES_HOME/vatbrain/, already covered by `hermes backup`."""
        return []

    def get_tool_schemas(self) -> List[Dict[str, Any]]:
        """Expose the v0.3 proactive risk-injection tool to the model."""
        return [{
            "name": "prepare_edit_context",
            "description": "Before editing files, get relevant memories + top Pitfall risks + a risk score for the files about to be modified.",
            "parameters": {
                "type": "object",
                "properties": {
                    "files": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "File paths being edited",
                    },
                    "task_type": {
                        "type": "string",
                        "enum": ["debug", "feature", "refactor", "review"],
                    },
                    "language": {"type": "string"},
                    "user_goal": {"type": "string", "description": "Optional user goal"},
                },
                "required": ["files"],
            },
        }]

    def handle_tool_call(self, tool_name: str, args: Dict[str, Any], **kwargs: Any) -> str:
        """Route a tool call to the daemon and return its JSON result."""
        if tool_name != "prepare_edit_context":
            raise NotImplementedError(f"vatbrain does not handle tool {tool_name}")
        params = dict(args)
        params["session_id"] = self._session_id
        try:
            result = self._rpc("prepare_edit_context", params, timeout=_IO_TIMEOUT_S)
            return json.dumps(result)
        except Exception as exc:
            return json.dumps({"error": str(exc)})

    def system_prompt_block(self) -> str:
        """Empty — vatbrain recall flows through prefetch, not the stable block."""
        return ""

    def shutdown(self) -> None:
        self._shutdown_proc()

    # -- Internal ----------------------------------------------------------

    def _resolve_binary(self) -> Optional[str]:
        env = os.getenv("VATBRAIN_PROVIDER_BIN", "").strip()
        if env:
            return env if os.path.exists(env) else None
        on_path = shutil.which("vatbrain-provider")
        if on_path:
            return on_path
        home = self._hermes_home or os.getenv("HERMES_HOME", "")
        if home:
            candidate = os.path.join(home, "vatbrain", "bin", "vatbrain-provider")
            if os.path.exists(candidate):
                return candidate
        return None

    def _spawn(self) -> None:
        with self._spawn_lock:
            if self._proc and self._proc.poll() is None:
                return
            data_dir = os.path.join(self._hermes_home or os.path.expanduser("~/.hermes"), "vatbrain")
            self._proc = subprocess.Popen(
                [self._binary, "--store", "sqlite", "--data", data_dir],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                bufsize=1,
            )
            logger.info("vatbrain: spawned vatbrain-provider pid=%s data=%s",
                        self._proc.pid, data_dir)

    def _rpc(self, method: str, params: Dict[str, Any], timeout: float = _IO_TIMEOUT_S) -> Dict[str, Any]:
        """Send one line-delimited JSON-RPC request and read its response."""
        proc = self._proc
        if proc is None:
            raise RuntimeError("vatbrain: daemon not spawned")

        req = {"jsonrpc": _JSONRPC, "id": 1, "method": method, "params": params}
        with self._io_lock:
            proc.stdin.write(json.dumps(req) + "\n")
            proc.stdin.flush()

            ready, _, _ = select.select([proc.stdout], [], [], timeout)
            if not ready:
                raise TimeoutError("vatbrain: no response from daemon within %ss" % timeout)
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError("vatbrain: daemon closed stdout")
            resp = json.loads(line)
        if resp.get("error"):
            raise RuntimeError("vatbrain: rpc error: %s" % resp["error"])
        return resp.get("result") or {}

    def _shutdown_proc(self) -> None:
        proc = self._proc
        self._proc = None
        if proc is None:
            return
        try:
            self._rpc("shutdown", {}, timeout=5.0)
        except Exception:
            pass  # best-effort; terminate below anyway
        try:
            proc.wait(timeout=3.0)
        except Exception:
            proc.terminate()
            try:
                proc.wait(timeout=2.0)
            except Exception:
                proc.kill()


def register(ctx) -> None:
    """Register vatbrain as a memory provider plugin."""
    ctx.register_memory_provider(VatBrainMemoryProvider())
