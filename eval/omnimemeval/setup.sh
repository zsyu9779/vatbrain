#!/usr/bin/env bash
# Install the VatBrain adapter + registration patches into an OmniMemEval
# checkout. Idempotent: safe to run repeatedly.
#
# Usage:
#   bash eval/omnimemeval/setup.sh /path/to/OmniMemEval
#
# Steps:
#   1. Copy vatbrain_client.py into scripts/client_factory/
#   2. Apply the three registration patches (registry.py + two dispatch tables)
#   3. Copy .env.vatbrain.example -> env_examples/.env.vatbrain (never overwrite)
set -euo pipefail

OE_DIR="${1:?usage: setup.sh /path/to/OmniMemEval}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ ! -d "$OE_DIR/scripts/client_factory" ]]; then
    echo "error: $OE_DIR does not look like an OmniMemEval checkout" >&2
    exit 1
fi

# 1. Adapter file.
cp "$HERE/vatbrain_client.py" "$OE_DIR/scripts/client_factory/vatbrain_client.py"
echo "copied vatbrain_client.py -> scripts/client_factory/"

# 2. Registration patches (idempotent).
python3 "$HERE/patch.py" "$OE_DIR"

# 3. Env template at the OmniMemEval ROOT (the --env flag resolves relative to
#    the repo root, not env_examples/). Never clobber a user-edited file.
if [[ -f "$OE_DIR/.env.vatbrain" ]]; then
    echo ".env.vatbrain already exists (kept)"
else
    cp "$HERE/.env.vatbrain.example" "$OE_DIR/.env.vatbrain"
    echo "created .env.vatbrain (template — fill in ANSWER/EVAL credentials)"
fi

echo "done. Now:"
echo "  1) start the bench server: go run ./cmd/vatbrain-bench --gate off"
echo "  2) cd $OE_DIR && ./scripts/run_halumem_eval.sh --lib vatbrain --env .env.vatbrain"
