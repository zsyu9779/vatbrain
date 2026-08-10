#!/usr/bin/env python3
"""Apply the three registration patches that make a "vatbrain" lib work in an
OmniMemEval checkout. Idempotent: safe to run repeatedly.

Patches:
  1. scripts/client_factory/registry.py      — register the lib in the registry
  2. scripts/locomo/locomo_search.py         — LoCoMo dual-speaker dispatch
  3. scripts/utils/search_helpers.py         — single-user benchmark dispatch

Usage: python3 patch.py /path/to/OmniMemEval
"""

from __future__ import annotations

import pathlib
import sys

REGISTRY_MARKER = '    "mem9":        ("mem9_client",        "Mem9Client"),\n'
REGISTRY_ADD = REGISTRY_MARKER + '    "vatbrain":    ("vatbrain_client",    "VatbrainClient"),\n'

LOCOMO_MARKER = '        "mem9": generic_text_search,\n'
LOCOMO_ADD = LOCOMO_MARKER + '        "vatbrain": generic_text_search,\n'

SEARCH_HELPERS_MARKER = '    "mem9": generic_text_search,\n'
SEARCH_HELPERS_ADD = SEARCH_HELPERS_MARKER + '    "vatbrain": generic_text_search,\n'


def patch_file(path: pathlib.Path, marker: str, replacement: str) -> bool:
    """Insert *replacement* after *marker* unless the lib is already present."""
    text = path.read_text(encoding="utf-8")
    if '"vatbrain"' in text:
        return False
    if marker not in text:
        raise SystemExit(
            f"patch failed: marker not found in {path}. "
            f"OmniMemEval layout changed?"
        )
    path.write_text(text.replace(marker, replacement, 1), encoding="utf-8")
    return True


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: python3 patch.py /path/to/OmniMemEval")
    root = pathlib.Path(sys.argv[1])
    if not (root / "scripts" / "client_factory").is_dir():
        raise SystemExit(f"{root} does not look like an OmniMemEval checkout")

    changed = 0
    changed += patch_file(root / "scripts/client_factory/registry.py",
                          REGISTRY_MARKER, REGISTRY_ADD)
    changed += patch_file(root / "scripts/locomo/locomo_search.py",
                          LOCOMO_MARKER, LOCOMO_ADD)
    changed += patch_file(root / "scripts/utils/search_helpers.py",
                          SEARCH_HELPERS_MARKER, SEARCH_HELPERS_ADD)
    print("vatbrain registration:", "applied" if changed else "already present")


if __name__ == "__main__":
    main()
