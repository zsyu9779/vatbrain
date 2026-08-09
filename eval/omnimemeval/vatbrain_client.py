"""VatBrain adapter for the OmniMemEval User Memory Evaluation harness.

Talks to the ``vatbrain-bench`` HTTP entrypoint
(``cmd/vatbrain-bench``, docs/v0.3/tech-specs/03-omnimemeval-benchmark.md).

Semantics
---------
- ``add(messages, user_id)``: every message is written through VatBrain's
  write pipeline as its own episodic memory. The bench server's gate mode
  controls whether the significance gate filters (``off`` = benchmark the
  storage + retrieval kernel, ``on`` = full production gating).
- ``search(query, user_id, top_k)``: VatBrain's retrieval returns plain-text
  memories, joined here exactly like the other text clients.
- ``delete(user_id)``: purges every memory for the user (``--clear`` and
  streaming modes).

Env vars (all optional):
    VATBRAIN_BENCH_BASE_URL  – bench server base URL (default http://127.0.0.1:18080)
    VATBRAIN_BENCH_API_TOKEN – bearer token required when the bench server
                               binds beyond loopback (send as Authorization header)
    VATBRAIN_BATCH_SIZE      – max messages per /v1/add request (default 50)
    VATBRAIN_TIMEOUT         – per-request timeout seconds (default 60)
"""

from .base_client import (
    BaseApiClient,
    env_int,
    env_str,
    iter_batches,
)


class VatbrainClient(BaseApiClient):
    """HTTP client for the vatbrain-bench evaluation entrypoint."""

    def __init__(self):
        base_url = env_str("VATBRAIN_BENCH_BASE_URL", "http://127.0.0.1:18080")
        headers = {"Content-Type": "application/json", "Accept": "application/json"}
        token = env_str("VATBRAIN_BENCH_API_TOKEN")
        if token:
            headers["Authorization"] = f"Bearer {token}"
        super().__init__(
            base_url=base_url,
            headers=headers,
            timeout=env_int("VATBRAIN_TIMEOUT", 60),
        )
        self._batch_size = env_int("VATBRAIN_BATCH_SIZE", 50, min_value=1)

    def add(self, messages, user_id, *args, **kwargs):
        """Store conversation messages for *user_id*.

        Extra positional/keyword args (session ids, timestamps) are accepted
        and ignored — the bench server keys memories only by user_id.
        """
        formatted = [
            {
                "role": m.get("role", "user"),
                "name": m.get("name"),
                "content": m.get("content", ""),
                "chat_time": m.get("chat_time"),
            }
            for m in messages
        ]
        for batch in iter_batches(formatted, self._batch_size):
            self._retry(_do_post(self, "/v1/add", {"user_id": user_id, "messages": batch}))

    def search(self, query, user_id, top_k):
        """Return retrieved memories for *query* as a plain-text string."""
        resp = self._retry(
            _do_post(self, "/v1/search",
                     {"user_id": user_id, "query": query, "top_k": top_k})
        )
        results = resp.json().get("results", [])
        return "\n".join(r.get("content", "") for r in results)

    def delete(self, user_id):
        """Delete all memories for *user_id* (--clear and streaming modes)."""
        self._retry(_do_post(self, "/v1/delete", {"user_id": user_id}))


def _do_post(client, path, payload):
    """POST *path* and raise_for_status inside the callable so _retry sees
    HTTP 5xx/429 and retries them with backoff (mirrors mem9_client)."""

    def _do():
        resp = client._post(path, json=payload)
        resp.raise_for_status()
        return resp

    return _do
