"""Unit tests for the VatBrain OmniMemEval adapter.

The adapter lives inside an OmniMemEval checkout (installed by setup.sh), so
these tests locate the OmniMemEval ``scripts`` dir and mock the HTTP layer —
no bench server and no network needed.

Run:
    OMNIMEMEVAL_SCRIPTS_DIR=/path/to/OmniMemEval/scripts \
      python3 -m unittest test_vatbrain_client -v
"""

from __future__ import annotations

import os
import sys
import unittest

SCRIPTS_DIR = os.environ.get(
    "OMNIMEMEVAL_SCRIPTS_DIR",
    "/tmp/OmniMemEval/scripts",
)
if not os.path.isdir(SCRIPTS_DIR):
    raise SystemExit(
        f"OmniMemEval scripts dir not found at {SCRIPTS_DIR}. "
        "Set OMNIMEMEVAL_SCRIPTS_DIR or run setup.sh first."
    )
sys.path.insert(0, SCRIPTS_DIR)


# ── Fake HTTP layer ─────────────────────────────────────────────────────────

class FakeResponse:
    def __init__(self, status=200, payload=None):
        self.status_code = status
        self._payload = payload if payload is not None else {}

    def raise_for_status(self):
        if self.status_code >= 400:
            raise RuntimeError(f"HTTP {self.status_code}")

    def json(self):
        return self._payload


class FakeSession:
    """Records every POST and routes canned responses by path."""

    def __init__(self):
        self.calls = []  # [(method, url, kwargs)]
        self.headers = {}
        self.add_payload = {"persisted": 2, "skipped": 0, "gate_reason_counts": {}}
        self.search_payload = {"results": [{"content": "Alice likes hiking", "weight": 1.0}]}
        self.delete_payload = {"deleted": 2}
        self.error_path = None

    def post(self, url, **kwargs):
        self.calls.append(("post", url, kwargs))
        if url.endswith("/v1/add"):
            return FakeResponse(200, self.add_payload)
        if url.endswith("/v1/search"):
            return FakeResponse(200, self.search_payload)
        if url.endswith("/v1/delete"):
            return FakeResponse(200, self.delete_payload)
        return FakeResponse(404, {"error": "not found"})


def make_client(session_factory=None):
    from client_factory import base_client
    from client_factory.vatbrain_client import VatbrainClient

    fake = session_factory() if session_factory else FakeSession()
    base_client.requests.Session = lambda: fake  # intercept requests.Session()
    client = VatbrainClient()
    return client, fake


# ── Tests ───────────────────────────────────────────────────────────────────

class TestVatbrainClient(unittest.TestCase):
    def setUp(self):
        from client_factory import base_client

        self._orig_session = base_client.requests.Session

    def tearDown(self):
        from client_factory import base_client

        base_client.requests.Session = self._orig_session

    def test_add_posts_messages_and_user_id(self):
        client, fake = make_client()
        messages = [
            {"role": "user", "content": "one"},
            {"role": "user", "content": "two"},
        ]
        client.add(messages, "u1")

        self.assertEqual(len(fake.calls), 1)
        _, url, kwargs = fake.calls[0]
        self.assertTrue(url.endswith("/v1/add"))
        body = kwargs["json"]
        self.assertEqual(body["user_id"], "u1")
        self.assertEqual(len(body["messages"]), 2)
        self.assertEqual(body["messages"][0]["content"], "one")

    def test_add_batches_when_over_batch_size(self):
        client, fake = make_client()
        client._batch_size = 2
        messages = [{"role": "user", "content": f"m{i}"} for i in range(5)]
        client.add(messages, "u1")

        # 5 messages / batch 2 -> 3 requests
        self.assertEqual(len(fake.calls), 3)
        sizes = [len(c[2]["json"]["messages"]) for c in fake.calls]
        self.assertEqual(sizes, [2, 2, 1])

    def test_add_tolerates_extra_session_kwargs(self):
        client, fake = make_client()
        client.add([{"role": "user", "content": "x"}], "u1",
                   "session-1", session_key="k", timestamp=12345)
        self.assertEqual(len(fake.calls), 1)

    def test_search_returns_joined_text(self):
        client, fake = make_client()
        fake.search_payload = {
            "results": [
                {"content": "first memory", "weight": 1.0},
                {"content": "second memory", "weight": 0.5},
            ]
        }
        text = client.search("a query", "u1", 10)

        self.assertEqual(text, "first memory\nsecond memory")
        _, url, kwargs = fake.calls[0]
        self.assertTrue(url.endswith("/v1/search"))
        self.assertEqual(kwargs["json"]["top_k"], 10)

    def test_search_empty_results_returns_empty_string(self):
        client, fake = make_client()
        fake.search_payload = {"results": []}
        self.assertEqual(client.search("q", "u1", 5), "")

    def test_delete_calls_delete_endpoint(self):
        client, fake = make_client()
        client.delete("u1")
        self.assertEqual(len(fake.calls), 1)
        _, url, kwargs = fake.calls[0]
        self.assertTrue(url.endswith("/v1/delete"))
        self.assertEqual(kwargs["json"]["user_id"], "u1")

    def test_server_error_raises(self):
        # A fresh client wired to a session that 500s on add must raise.
        _, fake = make_client()
        client, _ = make_client(session_factory=lambda: _ErrorSession(fake))
        with self.assertRaises(Exception):
            client.add([{"role": "user", "content": "x"}], "u1")

    def test_sends_bearer_token_when_configured(self):
        os.environ["VATBRAIN_BENCH_API_TOKEN"] = "s3cret"
        try:
            client, fake = make_client()
        finally:
            os.environ.pop("VATBRAIN_BENCH_API_TOKEN", None)

        # The constructor must stamp the token onto the session headers.
        self.assertEqual(fake.headers.get("Authorization"), "Bearer s3cret")
        client.add([{"role": "user", "content": "x"}], "u1")
        self.assertEqual(len(fake.calls), 1)


class _ErrorSession:
    """Wraps FakeSession but returns 500 for /v1/add."""

    def __init__(self, inner):
        self.inner = inner
        self.headers = {}

    def post(self, url, **kwargs):
        if url.endswith("/v1/add"):
            return FakeResponse(500, {"error": "boom"})
        return self.inner.post(url, **kwargs)


if __name__ == "__main__":
    unittest.main()
