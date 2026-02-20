#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
connectivity_smoke.sh — deterministic end-to-end Cortex runtime smoke

Usage:
  scripts/connectivity_smoke.sh [--cortex-bin /path/to/cortex]

Checks:
1) Build or use cortex binary
2) Import sample corpus with --extract into temp DB
3) Verify stats --json has memories>0 and facts>0
4) Verify keyword search returns expected hit
5) Verify list --facts returns at least one fact
6) Verify optimize --check-only --json returns integrity_check=ok
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

TMP_DIR="$(mktemp -d -t cortex-connectivity.XXXXXX)"
BUILT_BIN=""
cleanup() {
  if [[ -n "$BUILT_BIN" && -f "$BUILT_BIN" ]]; then
    rm -f "$BUILT_BIN"
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

if [[ -z "$CORTEX_BIN" ]]; then
  BUILT_BIN="$(mktemp -t cortex-connectivity-bin.XXXXXX)"
  echo "==> [1/6] building runtime binary"
  go build -o "$BUILT_BIN" ./cmd/cortex
  CORTEX_BIN="$BUILT_BIN"
else
  if [[ ! -x "$CORTEX_BIN" ]]; then
    echo "ERROR: --cortex-bin is not executable: $CORTEX_BIN" >&2
    exit 1
  fi
  echo "==> [1/6] using provided binary: $CORTEX_BIN"
fi

DB_PATH="$TMP_DIR/connectivity.db"
CORPUS_PATH="$TMP_DIR/corpus.md"
cat > "$CORPUS_PATH" <<'EOF'
# Cortex Connectivity Smoke Corpus

Release policy: canary-first rollout with reversible guardrails.
The ops owner validates PASS/WARN/FAIL budgets before production cutover.
Optimization routines must report integrity check status as ok.
EOF

echo "==> [2/6] import sample corpus with extraction"
IMPORT_LOG="$TMP_DIR/import.log"
if ! "$CORTEX_BIN" --db "$DB_PATH" import "$CORPUS_PATH" --extract >"$IMPORT_LOG" 2>&1; then
  echo "ERROR: import --extract failed" >&2
  cat "$IMPORT_LOG" >&2
  exit 1
fi


echo "==> [3/6] validate stats JSON"
STATS_JSON="$TMP_DIR/stats.json"
"$CORTEX_BIN" --db "$DB_PATH" stats --json > "$STATS_JSON"
python3 - "$STATS_JSON" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))
memories = int(data.get("memories", 0) or 0)
facts = int(data.get("facts", 0) or 0)
if memories <= 0:
    raise SystemExit(f"ERROR: expected memories>0, got {memories}")
if facts <= 0:
    raise SystemExit(f"ERROR: expected facts>0, got {facts}")
print(f"stats ok: memories={memories}, facts={facts}")
PY


echo "==> [4/6] validate keyword search hit"
SEARCH_JSON="$TMP_DIR/search.json"
"$CORTEX_BIN" --db "$DB_PATH" search "canary-first" --json --limit 5 > "$SEARCH_JSON"
python3 - "$SEARCH_JSON" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
rows = json.loads(path.read_text(encoding="utf-8"))
if not isinstance(rows, list) or not rows:
    raise SystemExit("ERROR: search returned no rows")
needle = "canary-first"
joined = "\n".join(str(r.get("content", "")) + "\n" + str(r.get("snippet", "")) for r in rows).lower()
if needle not in joined:
    raise SystemExit("ERROR: search results missing expected marker 'canary-first'")
print(f"search ok: rows={len(rows)}")
PY


echo "==> [5/6] validate list --facts output"
FACTS_JSON="$TMP_DIR/facts.json"
"$CORTEX_BIN" --db "$DB_PATH" list --facts --limit 5 --json > "$FACTS_JSON"
python3 - "$FACTS_JSON" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
rows = json.loads(path.read_text(encoding="utf-8"))
if not isinstance(rows, list) or not rows:
    raise SystemExit("ERROR: list --facts returned no rows")
print(f"facts ok: rows={len(rows)}")
PY


echo "==> [6/6] validate optimize integrity"
OPT_JSON="$TMP_DIR/optimize.json"
"$CORTEX_BIN" --db "$DB_PATH" optimize --check-only --json > "$OPT_JSON"
python3 - "$OPT_JSON" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
row = json.loads(path.read_text(encoding="utf-8"))
integrity = str(row.get("integrity_check", "")).lower()
if integrity != "ok":
    raise SystemExit(f"ERROR: integrity_check expected 'ok', got '{integrity}'")
print("optimize ok: integrity_check=ok")
PY

echo "✅ connectivity smoke passed"
