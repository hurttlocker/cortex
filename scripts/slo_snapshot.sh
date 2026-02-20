#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
slo_snapshot.sh — Capture Cortex SLO checkpoint timings and emit a report

Usage:
  scripts/slo_snapshot.sh [options]

Options:
  --db <path>                  Cortex DB path (optional)
  --cortex-bin <path>          Cortex binary/command (default: cortex)
  --query <text>               Search query for checkpoint (default: "memory")
  --mode <mode>                Search mode: keyword|semantic|hybrid (default: keyword)
  --embed <provider/model>     Embed model for semantic/hybrid search (optional)
  --limit <n>                  Search result limit (default: 10)
  --conflict-limit <n>         Conflict limit for checkpoint (default: 100)
  --warn-stats-ms <n>          Warn threshold for stats duration (ms, 0=disabled)
  --warn-search-ms <n>         Warn threshold for search duration (ms, 0=disabled)
  --warn-conflicts-ms <n>      Warn threshold for conflicts duration (ms, 0=disabled)
  --fail-stats-ms <n>          Fail threshold for stats duration (ms, 0=disabled)
  --fail-search-ms <n>         Fail threshold for search duration (ms, 0=disabled)
  --fail-conflicts-ms <n>      Fail threshold for conflicts duration (ms, 0=disabled)
  --warn-only-thresholds       Do not fail exit code on threshold fail breaches
  --output <file>              JSON report output path
  --markdown <file>            Optional markdown summary output path
  -h, --help                   Show this help

Examples:
  scripts/slo_snapshot.sh
  scripts/slo_snapshot.sh --db ~/.cortex/cortex.db --query "deployment" --mode hybrid --embed ollama/nomic-embed-text
  scripts/slo_snapshot.sh --output /tmp/slo.json --markdown /tmp/slo.md
  scripts/slo_snapshot.sh --warn-stats-ms 3000 --warn-search-ms 5000 --warn-conflicts-ms 5000 \
    --fail-stats-ms 7000 --fail-search-ms 10000 --fail-conflicts-ms 12000
EOF
}

now_ms() {
  python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
}

DB_PATH=""
CORTEX_BIN="${CORTEX_BIN:-}"
QUERY="memory"
MODE="keyword"
EMBED=""
LIMIT="10"
CONFLICT_LIMIT="100"
WARN_STATS_MS="0"
WARN_SEARCH_MS="0"
WARN_CONFLICTS_MS="0"
FAIL_STATS_MS="0"
FAIL_SEARCH_MS="0"
FAIL_CONFLICTS_MS="0"
WARN_ONLY_THRESHOLDS=0
OUTPUT_PATH=""
MARKDOWN_PATH=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db)
      DB_PATH="${2:-}"; shift 2 ;;
    --cortex-bin)
      CORTEX_BIN="${2:-}"; shift 2 ;;
    --query)
      QUERY="${2:-}"; shift 2 ;;
    --mode)
      MODE="${2:-}"; shift 2 ;;
    --embed)
      EMBED="${2:-}"; shift 2 ;;
    --limit)
      LIMIT="${2:-}"; shift 2 ;;
    --conflict-limit)
      CONFLICT_LIMIT="${2:-}"; shift 2 ;;
    --warn-stats-ms)
      WARN_STATS_MS="${2:-}"; shift 2 ;;
    --warn-search-ms)
      WARN_SEARCH_MS="${2:-}"; shift 2 ;;
    --warn-conflicts-ms)
      WARN_CONFLICTS_MS="${2:-}"; shift 2 ;;
    --fail-stats-ms)
      FAIL_STATS_MS="${2:-}"; shift 2 ;;
    --fail-search-ms)
      FAIL_SEARCH_MS="${2:-}"; shift 2 ;;
    --fail-conflicts-ms)
      FAIL_CONFLICTS_MS="${2:-}"; shift 2 ;;
    --warn-only-thresholds)
      WARN_ONLY_THRESHOLDS=1; shift ;;
    --output)
      OUTPUT_PATH="${2:-}"; shift 2 ;;
    --markdown)
      MARKDOWN_PATH="${2:-}"; shift 2 ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1 ;;
  esac
done

case "$MODE" in
  keyword|semantic|hybrid) ;;
  *)
    echo "Invalid --mode: $MODE (expected keyword|semantic|hybrid)" >&2
    exit 1 ;;
esac

if ! [[ "$LIMIT" =~ ^[0-9]+$ ]] || [[ "$LIMIT" -le 0 ]]; then
  echo "Invalid --limit: $LIMIT" >&2
  exit 1
fi
if ! [[ "$CONFLICT_LIMIT" =~ ^[0-9]+$ ]] || [[ "$CONFLICT_LIMIT" -le 0 ]]; then
  echo "Invalid --conflict-limit: $CONFLICT_LIMIT" >&2
  exit 1
fi

for threshold_name in WARN_STATS_MS WARN_SEARCH_MS WARN_CONFLICTS_MS FAIL_STATS_MS FAIL_SEARCH_MS FAIL_CONFLICTS_MS; do
  threshold_val="${!threshold_name}"
  if ! [[ "$threshold_val" =~ ^[0-9]+$ ]]; then
    echo "Invalid threshold value for $threshold_name: $threshold_val" >&2
    exit 1
  fi
  if [[ "$threshold_val" -lt 0 ]]; then
    echo "Threshold must be >= 0 for $threshold_name" >&2
    exit 1
  fi
done

if [[ "$FAIL_STATS_MS" -gt 0 && "$WARN_STATS_MS" -gt "$FAIL_STATS_MS" ]]; then
  echo "Invalid thresholds: WARN_STATS_MS > FAIL_STATS_MS" >&2
  exit 1
fi
if [[ "$FAIL_SEARCH_MS" -gt 0 && "$WARN_SEARCH_MS" -gt "$FAIL_SEARCH_MS" ]]; then
  echo "Invalid thresholds: WARN_SEARCH_MS > FAIL_SEARCH_MS" >&2
  exit 1
fi
if [[ "$FAIL_CONFLICTS_MS" -gt 0 && "$WARN_CONFLICTS_MS" -gt "$FAIL_CONFLICTS_MS" ]]; then
  echo "Invalid thresholds: WARN_CONFLICTS_MS > FAIL_CONFLICTS_MS" >&2
  exit 1
fi

if [[ -z "$OUTPUT_PATH" ]]; then
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  OUTPUT_PATH="slo-snapshot-${stamp}.json"
fi

# Resolve cortex binary AFTER args parsing so --cortex-bin takes effect.
if [[ -z "$CORTEX_BIN" ]]; then
  if command -v cortex >/dev/null 2>&1; then
    CORTEX_BIN="cortex"
  elif [[ -x "$HOME/bin/cortex" ]]; then
    CORTEX_BIN="$HOME/bin/cortex"
  else
    echo "Could not find cortex binary. Set --cortex-bin or CORTEX_BIN." >&2
    exit 1
  fi
fi

if [[ "$CORTEX_BIN" == */* ]]; then
  if [[ ! -x "$CORTEX_BIN" ]]; then
    echo "Cortex binary path is not executable: $CORTEX_BIN" >&2
    exit 1
  fi
else
  if ! command -v "$CORTEX_BIN" >/dev/null 2>&1; then
    echo "Cortex command not found in PATH: $CORTEX_BIN" >&2
    exit 1
  fi
fi

steps_file="$(mktemp)"
cleanup() {
  rm -f "$steps_file"
}
trap cleanup EXIT

append_step() {
  local name="$1" cmd="$2" rc="$3" duration="$4" warn_ms="$5" fail_ms="$6" warn_only="$7"
  python3 - "$steps_file" "$name" "$cmd" "$rc" "$duration" "$warn_ms" "$fail_ms" "$warn_only" <<'PY'
import json, sys
path = sys.argv[1]
name = sys.argv[2]
cmd = sys.argv[3]
rc = int(sys.argv[4])
dur = int(sys.argv[5])
warn_ms = int(sys.argv[6])
fail_ms = int(sys.argv[7])
warn_only = bool(int(sys.argv[8]))

threshold_status = "none"
if fail_ms > 0 and dur > fail_ms:
    threshold_status = "fail"
elif warn_ms > 0 and dur > warn_ms:
    threshold_status = "warn"

ok = (rc == 0) and (threshold_status != "fail" or warn_only)

with open(path, "a", encoding="utf-8") as f:
    f.write(json.dumps({
        "name": name,
        "command": cmd,
        "exit_code": rc,
        "duration_ms": dur,
        "threshold_warn_ms": warn_ms,
        "threshold_fail_ms": fail_ms,
        "threshold_status": threshold_status,
        "ok": ok,
    }) + "\n")
PY
}

run_checkpoint() {
  local name="$1" warn_ms="$2" fail_ms="$3"; shift 3
  local -a cmd=("$@")
  local cmd_str
  cmd_str="$(printf '%q ' "${cmd[@]}")"

  local start end rc dur
  start="$(now_ms)"
  set +e
  "${cmd[@]}" >/dev/null 2>&1
  rc=$?
  set -e
  end="$(now_ms)"
  dur=$((end - start))

  append_step "$name" "$cmd_str" "$rc" "$dur" "$warn_ms" "$fail_ms" "$WARN_ONLY_THRESHOLDS"
}

common_prefix=("$CORTEX_BIN")
if [[ -n "$DB_PATH" ]]; then
  common_prefix+=(--db "$DB_PATH")
fi

stats_cmd=("${common_prefix[@]}" stats --json)
search_cmd=("${common_prefix[@]}" search "$QUERY" --mode "$MODE" --limit "$LIMIT" --json)
if [[ -n "$EMBED" ]]; then
  search_cmd+=(--embed "$EMBED")
fi
conflicts_cmd=("${common_prefix[@]}" conflicts --limit "$CONFLICT_LIMIT" --json)

run_checkpoint "stats" "$WARN_STATS_MS" "$FAIL_STATS_MS" "${stats_cmd[@]}"
run_checkpoint "search" "$WARN_SEARCH_MS" "$FAIL_SEARCH_MS" "${search_cmd[@]}"
run_checkpoint "conflicts" "$WARN_CONFLICTS_MS" "$FAIL_CONFLICTS_MS" "${conflicts_cmd[@]}"

python3 - "$steps_file" "$OUTPUT_PATH" "$DB_PATH" "$CORTEX_BIN" "$QUERY" "$MODE" "$LIMIT" "$CONFLICT_LIMIT" "$WARN_ONLY_THRESHOLDS" <<'PY'
import json, sys, datetime
steps_path, out_path, db_path, cortex_bin, query, mode, limit, conflict_limit, warn_only_raw = sys.argv[1:10]
warn_only = bool(int(warn_only_raw))
steps = []
with open(steps_path, "r", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if line:
            steps.append(json.loads(line))

threshold_warn_count = sum(1 for s in steps if s.get("threshold_status") == "warn")
threshold_fail_count = sum(1 for s in steps if s.get("threshold_status") == "fail")
command_fail_count = sum(1 for s in steps if int(s.get("exit_code", 1)) != 0)
overall_ok = all(bool(s.get("ok")) for s in steps)

if command_fail_count > 0:
    overall_status = "FAIL"
elif threshold_fail_count > 0 and warn_only:
    overall_status = "WARN"
elif threshold_fail_count > 0:
    overall_status = "FAIL"
elif threshold_warn_count > 0:
    overall_status = "WARN"
else:
    overall_status = "PASS"

report = {
    "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "db_path": db_path or "(default)",
    "cortex_bin": cortex_bin,
    "search": {
        "query": query,
        "mode": mode,
        "limit": int(limit),
    },
    "conflicts": {
        "limit": int(conflict_limit),
    },
    "thresholds_warn_only": warn_only,
    "checkpoints": steps,
    "threshold_warn_count": threshold_warn_count,
    "threshold_fail_count": threshold_fail_count,
    "command_fail_count": command_fail_count,
    "overall_status": overall_status,
    "overall_ok": overall_ok,
}

with open(out_path, "w", encoding="utf-8") as f:
    json.dump(report, f, indent=2)
    f.write("\n")
PY

if [[ -n "$MARKDOWN_PATH" ]]; then
  python3 - "$OUTPUT_PATH" "$MARKDOWN_PATH" <<'PY'
import json, sys
src, dst = sys.argv[1], sys.argv[2]
with open(src, "r", encoding="utf-8") as f:
    report = json.load(f)

lines = []
lines.append("# Cortex SLO Snapshot")
lines.append("")
lines.append(f"- Generated: `{report['generated_at']}`")
lines.append(f"- DB: `{report['db_path']}`")
lines.append(f"- Binary: `{report['cortex_bin']}`")
lines.append(f"- Search: `{report['search']['query']}` (mode={report['search']['mode']}, limit={report['search']['limit']})")
lines.append(f"- Conflicts limit: `{report['conflicts']['limit']}`")
lines.append("")
lines.append("| Checkpoint | Exit | Duration (ms) | Threshold (warn/fail) | Threshold status |")
lines.append("|---|---:|---:|---:|---:|")
for step in report["checkpoints"]:
    warn_ms = step.get("threshold_warn_ms", 0)
    fail_ms = step.get("threshold_fail_ms", 0)
    lines.append(f"| {step['name']} | {step['exit_code']} | {step['duration_ms']} | {warn_ms}/{fail_ms} | {step.get('threshold_status', 'none')} |")
lines.append("")
lines.append(f"- Threshold warn count: `{report.get('threshold_warn_count', 0)}`")
lines.append(f"- Threshold fail count: `{report.get('threshold_fail_count', 0)}`")
lines.append(f"- Command fail count: `{report.get('command_fail_count', 0)}`")
lines.append(f"- Warn-only thresholds: `{report.get('thresholds_warn_only', False)}`")
lines.append("")
lines.append(f"Overall: **{report.get('overall_status', 'FAIL')}**")

with open(dst, "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + "\n")
PY
fi

python3 - "$OUTPUT_PATH" <<'PY'
import json, sys
with open(sys.argv[1], 'r', encoding='utf-8') as f:
    report = json.load(f)
status = report.get("overall_status", "FAIL")
print(f"SLO snapshot: {status}")
print(f"JSON: {sys.argv[1]}")
PY

if [[ -n "$MARKDOWN_PATH" ]]; then
  echo "Markdown: $MARKDOWN_PATH"
fi

python3 - "$OUTPUT_PATH" <<'PY'
import json, sys
with open(sys.argv[1], 'r', encoding='utf-8') as f:
    report = json.load(f)
raise SystemExit(0 if report.get('overall_ok') else 1)
PY
