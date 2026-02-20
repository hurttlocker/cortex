#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
audit_break_harness.sh — adversarial sanity checks for external break-audit prep

Usage:
  scripts/audit_break_harness.sh [--cortex-bin /path/to/cortex]

What it verifies (deterministic):
1) Missing telemetry file path is handled gracefully (codex-rollout-report exits 0 with Runs parsed: 0)
2) Missing import path fails cleanly (non-zero, no crash)
3) Targeted concurrency/recovery regression tests pass:
   - concurrent identical imports
   - malformed/zero PID embed lock reclaim
   - stale migration claim reclaim

This script is intentionally conservative: it checks crash resistance + known reliability regressions.
EOF
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

CORTEX_BIN=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --cortex-bin)
      CORTEX_BIN="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

TMP_DIR="$(mktemp -d -t cortex-break-harness.XXXXXX)"
BUILT_BIN=""
cleanup() {
  if [[ -n "$BUILT_BIN" && -f "$BUILT_BIN" ]]; then
    rm -f "$BUILT_BIN"
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

if [[ -z "$CORTEX_BIN" ]]; then
  BUILT_BIN="$(mktemp -t cortex-break-bin.XXXXXX)"
  echo "==> [1/4] build runtime binary"
  go build -o "$BUILT_BIN" ./cmd/cortex
  CORTEX_BIN="$BUILT_BIN"
else
  if [[ ! -x "$CORTEX_BIN" ]]; then
    echo "ERROR: --cortex-bin not executable: $CORTEX_BIN" >&2
    exit 1
  fi
  echo "==> [1/4] use provided binary: $CORTEX_BIN"
fi

echo "==> [2/4] missing telemetry should not crash"
MISSING_TELEMETRY="$TMP_DIR/does-not-exist.jsonl"
ROLL_LOG="$TMP_DIR/rollout_missing.log"
"$CORTEX_BIN" codex-rollout-report --file "$MISSING_TELEMETRY" >"$ROLL_LOG" 2>&1
if ! rg -q "Runs parsed:\s*0" "$ROLL_LOG"; then
  echo "ERROR: expected 'Runs parsed: 0' for missing telemetry file" >&2
  cat "$ROLL_LOG" >&2
  exit 1
fi

echo "==> [3/4] missing import path should fail cleanly"
MISSING_IMPORT="$TMP_DIR/missing-input.md"
IMPORT_LOG="$TMP_DIR/import_missing.log"
set +e
"$CORTEX_BIN" import "$MISSING_IMPORT" >"$IMPORT_LOG" 2>&1
import_rc=$?
set -e
if [[ $import_rc -eq 0 ]]; then
  echo "ERROR: expected non-zero exit for missing import path" >&2
  cat "$IMPORT_LOG" >&2
  exit 1
fi
if ! rg -qi "no such file|cannot|error" "$IMPORT_LOG"; then
  echo "ERROR: missing import failure did not include expected error text" >&2
  cat "$IMPORT_LOG" >&2
  exit 1
fi

echo "==> [4/4] targeted regression tests"
go test ./internal/ingest -run TestProcessMemory_ConcurrentIdenticalImports_NoUniqueErrors -v
go test ./cmd/cortex -run 'TestAcquireEmbedRunLock_Reclaims(MalformedPIDLock|ZeroPIDLock)' -v
go test ./internal/store -run 'Test(ClaimMetaMigration_ReclaimsDeadPID|MigrateFTSMultiColumn_RecoverStaleInProgressMarker)' -v

echo "✅ audit break harness passed"
