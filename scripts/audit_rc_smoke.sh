#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "==> [1/5] go test ./..."
go test ./...

echo "==> [2/5] go vet ./..."
go vet ./...

echo "==> [3/5] build runtime binary"
runtime_bin="$(mktemp -t cortex-audit-bin.XXXXXX)"
telemetry_file="$(mktemp -t cortex-audit-telemetry.XXXXXX.jsonl)"
cleanup() {
  rm -f "$runtime_bin" "$telemetry_file"
}
trap cleanup EXIT

go build -o "$runtime_bin" ./cmd/cortex

echo "==> [4/5] rollout help check"
help_output="$($runtime_bin codex-rollout-report --help 2>&1)"
printf '%s\n' "$help_output"
if ! grep -q "Usage of codex-rollout-report" <<<"$help_output"; then
  echo "ERROR: rollout help output missing expected usage text" >&2
  exit 1
fi

echo "==> [5/6] runtime connectivity smoke"
scripts/connectivity_smoke.sh --cortex-bin "$runtime_bin"

echo "==> [6/6] strict-mode fixture check"
cat > "$telemetry_file" <<'EOF'
{"mode":"one-shot","provider":"openrouter","model":"openai-codex/gpt-5.2","wall_ms":25000,"cost_known":true,"cost_usd":0.001}
{"mode":"recursive","provider":"openrouter","model":"google/gemini-2.5-flash","wall_ms":30000,"cost_known":false}
EOF

set +e
strict_output="$($runtime_bin codex-rollout-report --file "$telemetry_file" --warn-only=false 2>&1)"
strict_rc=$?
set -e
printf '%s\n' "$strict_output"

if [[ $strict_rc -ne 2 ]]; then
  echo "ERROR: expected strict-mode exit code 2, got $strict_rc" >&2
  exit 1
fi
if ! grep -q "WARN:" <<<"$strict_output"; then
  echo "ERROR: strict-mode output missing WARN lines" >&2
  exit 1
fi

echo "✅ audit RC smoke passed"
