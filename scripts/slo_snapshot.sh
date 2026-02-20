#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
slo_snapshot.sh — Capture Cortex SLO checkpoint timings and emit a report

Usage:
  scripts/slo_snapshot.sh [options]

Options:
  --db <path>              Cortex DB path (optional)
  --cortex-bin <path>      Cortex binary/command (default: cortex)
  --query <text>           Search query for checkpoint (default: "memory")
  --mode <mode>            Search mode: keyword|semantic|hybrid (default: keyword)
  --embed <provider/model> Embed model for semantic/hybrid search (optional)
  --limit <n>              Search result limit (default: 10)
  --conflict-limit <n>     Conflict limit for checkpoint (default: 100)
  --output <file>          JSON report output path
  --markdown <file>        Optional markdown summary output path
  -h, --help               Show this help

Examples:
  scripts/slo_snapshot.sh
  scripts/slo_snapshot.sh --db ~/.cortex/cortex.db --query "deployment" --mode hybrid --embed ollama/nomic-embed-text
  scripts/slo_snapshot.sh --output /tmp/slo.json --markdown /tmp/slo.md
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
  local name="$1" cmd="$2" rc="$3" duration="$4"
  python3 - "$steps_file" "$name" "$cmd" "$rc" "$duration" <<'PY'
import json, sys
path, name, cmd, rc, dur = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5])
with open(path, "a", encoding="utf-8") as f:
    f.write(json.dumps({
        "name": name,
        "command": cmd,
        "exit_code": rc,
        "duration_ms": dur,
        "ok": rc == 0,
    }) + "\n")
PY
}

run_checkpoint() {
  local name="$1"; shift
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

  append_step "$name" "$cmd_str" "$rc" "$dur"
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

run_checkpoint "stats" "${stats_cmd[@]}"
run_checkpoint "search" "${search_cmd[@]}"
run_checkpoint "conflicts" "${conflicts_cmd[@]}"

python3 - "$steps_file" "$OUTPUT_PATH" "$DB_PATH" "$CORTEX_BIN" "$QUERY" "$MODE" "$LIMIT" "$CONFLICT_LIMIT" <<'PY'
import json, sys, datetime
steps_path, out_path, db_path, cortex_bin, query, mode, limit, conflict_limit = sys.argv[1:9]
steps = []
with open(steps_path, "r", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if line:
            steps.append(json.loads(line))

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
    "checkpoints": steps,
    "overall_ok": all(step.get("ok") for step in steps),
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
lines.append("| Checkpoint | Exit | Duration (ms) |")
lines.append("|---|---:|---:|")
for step in report["checkpoints"]:
    lines.append(f"| {step['name']} | {step['exit_code']} | {step['duration_ms']} |")
lines.append("")
lines.append(f"Overall: **{'PASS' if report['overall_ok'] else 'FAIL'}**")

with open(dst, "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + "\n")
PY
fi

python3 - "$OUTPUT_PATH" <<'PY'
import json, sys
with open(sys.argv[1], 'r', encoding='utf-8') as f:
    report = json.load(f)
status = "PASS" if report.get("overall_ok") else "FAIL"
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
