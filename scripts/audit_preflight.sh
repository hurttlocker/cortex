#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
audit_preflight.sh — one-command external audit preflight with evidence artifact

Usage:
  scripts/audit_preflight.sh --tag vX.Y.Z[-rcN] [--out docs/audits/<name>.md]

Runs:
  1) scripts/release_checklist.sh --tag <tag>
  2) scripts/audit_rc_smoke.sh
  3) python3 scripts/validate_visualizer_contract.py

Output:
  - Markdown summary report (default: docs/audits/<tag>-preflight.md)
  - Per-step logs in docs/audits/<tag>-preflight-logs/

Exit codes:
  0 = all checks passed
  1 = one or more checks failed
EOF
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

TAG=""
OUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:-}"
      shift 2
      ;;
    --out)
      OUT="${2:-}"
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

if [[ -z "$TAG" ]]; then
  echo "ERROR: --tag is required" >&2
  exit 1
fi

if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$ ]]; then
  echo "ERROR: invalid --tag '$TAG' (expected v<major>.<minor>.<patch>[-rcN])" >&2
  exit 1
fi

if [[ -z "$OUT" ]]; then
  OUT="docs/audits/${TAG}-preflight.md"
fi

mkdir -p "$(dirname "$OUT")"
LOG_DIR="${OUT%.md}-logs"
mkdir -p "$LOG_DIR"

RESULTS_TSV="$(mktemp -t cortex-audit-preflight-results.XXXXXX)"
trap 'rm -f "$RESULTS_TSV"' EXIT

OVERALL_FAIL=0

run_step() {
  local name="$1"
  local cmd="$2"
  local slug
  slug="$(echo "$name" | tr ' ' '-' | tr '[:upper:]' '[:lower:]')"
  local log_file="$LOG_DIR/${slug}.log"

  echo "==> $name"
  set +e
  bash -lc "$cmd" >"$log_file" 2>&1
  local rc=$?
  set -e

  local status="PASS"
  if [[ $rc -ne 0 ]]; then
    status="FAIL"
    OVERALL_FAIL=1
  fi

  printf '%s\t%s\t%s\t%s\n' "$status" "$name" "$cmd" "$log_file" >> "$RESULTS_TSV"

  if [[ "$status" == "PASS" ]]; then
    echo "    PASS"
  else
    echo "    FAIL (see $log_file)"
  fi
}

run_step "release checklist" "scripts/release_checklist.sh --tag $TAG"
run_step "audit rc smoke" "scripts/audit_rc_smoke.sh"
run_step "visualizer contract validator" "python3 scripts/validate_visualizer_contract.py"

UTC_NOW="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
GIT_SHA="$(git rev-parse HEAD)"
GIT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"

{
  echo "# External Audit Preflight — $TAG"
  echo
  echo "- Generated at (UTC): \`$UTC_NOW\`"
  echo "- Git SHA: \`$GIT_SHA\`"
  echo "- Git branch: \`$GIT_BRANCH\`"
  echo "- Overall: **$([[ $OVERALL_FAIL -eq 0 ]] && echo PASS || echo FAIL)**"
  echo
  echo "## Step Results"
  echo
  echo "| Status | Step | Command | Log |"
  echo "|---|---|---|---|"

  while IFS=$'\t' read -r status name cmd log_file; do
    rel_log="${log_file#${ROOT_DIR}/}"
    echo "| $status | $name | \`$cmd\` | \`$rel_log\` |"
  done < "$RESULTS_TSV"

  echo
  echo "## Notes"
  echo
  if [[ $OVERALL_FAIL -eq 0 ]]; then
    echo "All preflight gates passed for \`$TAG\`."
  else
    echo "One or more gates failed. See logs above before requesting external audit."
  fi
} > "$OUT"

if [[ $OVERALL_FAIL -eq 0 ]]; then
  echo "✅ audit preflight passed"
  echo "report: $OUT"
  exit 0
fi

echo "❌ audit preflight failed"
echo "report: $OUT"
exit 1
