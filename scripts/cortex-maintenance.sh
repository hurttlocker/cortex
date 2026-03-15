#!/bin/bash
# cortex-maintenance.sh — Daily maintenance: lifecycle + dedup + stats.
#
# Designed to run as a launchd/systemd timer (daily, e.g., 3:30 AM).
# Runs lifecycle policies first, then dedup sweep, then logs summary.
#
# Usage:
#   ./cortex-maintenance.sh
#
# Environment:
#   CORTEX_DB      — path to cortex.db (default: ~/.cortex/cortex.db)
#   CORTEX_BIN     — path to cortex binary (default: cortex in PATH)
#   CORTEX_LOGDIR  — log directory (default: ~/.cortex/logs)

set -euo pipefail

CORTEX_BIN="${CORTEX_BIN:-cortex}"
LOGDIR="${CORTEX_LOGDIR:-$HOME/.cortex/logs}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

mkdir -p "$LOGDIR"
LOG="$LOGDIR/maintenance.log"

echo "=== Cortex maintenance: $TIMESTAMP ===" >> "$LOG"

# Step 1: Lifecycle policies (decay, supersede, promote)
echo "[$TIMESTAMP] Running lifecycle policies..." >> "$LOG"
$CORTEX_BIN lifecycle run >> "$LOG" 2>&1 || echo "[$TIMESTAMP] WARNING: lifecycle run failed" >> "$LOG"

# Step 2: Dedup sweep (keep newest per subject)
echo "[$TIMESTAMP] Running dedup sweep..." >> "$LOG"
"$SCRIPT_DIR/cortex-dedup.sh" >> "$LOG" 2>&1 || echo "[$TIMESTAMP] WARNING: dedup failed" >> "$LOG"

# Step 3: Summary stats
ACTIVE=$(sqlite3 "${CORTEX_DB:-$HOME/.cortex/cortex.db}" "SELECT COUNT(*) FROM facts WHERE state NOT IN ('retired','superseded');")
CONFLICTS=$($CORTEX_BIN conflicts --json 2>/dev/null | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "?")
echo "[$TIMESTAMP] Post-maintenance: $ACTIVE active facts, $CONFLICTS conflicts" >> "$LOG"

echo "cortex-maintenance: done — $ACTIVE active facts, $CONFLICTS conflicts"
