#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TELEMETRY_FILE="${1:-$HOME/.cortex/reason-telemetry.jsonl}"

cd "$ROOT_DIR"
go run ./cmd/codex-rollout-report --file "$TELEMETRY_FILE"
