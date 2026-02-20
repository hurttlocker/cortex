#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TELEMETRY_FILE="$HOME/.cortex/reason-telemetry.jsonl"

# First non-flag positional arg (if provided) is treated as telemetry file path.
if [[ $# -gt 0 && "$1" != --* ]]; then
  TELEMETRY_FILE="$1"
  shift
fi

cd "$ROOT_DIR"
go run ./cmd/cortex codex-rollout-report --file "$TELEMETRY_FILE" "$@"
