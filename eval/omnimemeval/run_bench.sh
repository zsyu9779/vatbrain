#!/usr/bin/env bash
# Start the vatbrain-bench server with the credentials in .env.bench.
#
# Usage:
#   bash eval/omnimemeval/run_bench.sh --gate off --port 18080
#
# Sources .env.bench (copy from .env.bench.example) for the semantic embedding
# key, then execs the bench entrypoint. Extra args are passed through.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${VATBRAIN_BENCH_ENV_FILE:-$HERE/.env.bench}"

if [[ -f "$ENV_FILE" ]]; then
    # shellcheck disable=SC1090
    set -a && source "$ENV_FILE" && set +a
fi

exec go run "$HERE/../../cmd/vatbrain-bench" "$@"
