#!/bin/bash
# cortex-dedup.sh — Keep only the newest fact per subject, retire older duplicates.
#
# Prevents unbounded fact growth from slightly-different content being
# re-imported and generating new facts on the same subject.
#
# Run after `cortex lifecycle run` as part of daily maintenance, or on-demand.
#
# Usage:
#   ./cortex-dedup.sh              # retire duplicates
#   ./cortex-dedup.sh --dry-run    # show count without changing anything
#
# Environment:
#   CORTEX_DB  — path to cortex.db (default: ~/.cortex/cortex.db)

set -euo pipefail

DB="${CORTEX_DB:-$HOME/.cortex/cortex.db}"
DRY_RUN=false

if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

if [ ! -f "$DB" ]; then
  echo "ERROR: Cortex DB not found at $DB"
  exit 1
fi

# Count duplicates (active facts that aren't the newest per subject)
DUPE_COUNT=$(sqlite3 "$DB" "
SELECT COUNT(*) FROM facts 
WHERE state NOT IN ('retired', 'superseded') 
AND id NOT IN (
    SELECT id FROM (
        SELECT id, subject, ROW_NUMBER() OVER (PARTITION BY subject ORDER BY created_at DESC) as rn
        FROM facts 
        WHERE state NOT IN ('retired', 'superseded')
    ) WHERE rn = 1
);")

if [ "$DUPE_COUNT" -eq 0 ]; then
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) cortex-dedup: clean — 0 duplicates found"
  exit 0
fi

if [ "$DRY_RUN" = true ]; then
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) cortex-dedup: DRY RUN — would retire $DUPE_COUNT duplicate facts"
  exit 0
fi

# Retire all duplicates in one SQL statement (keep newest per subject)
RETIRED=$(sqlite3 "$DB" "
UPDATE facts SET state = 'retired'
WHERE state NOT IN ('retired', 'superseded') 
AND id NOT IN (
    SELECT id FROM (
        SELECT id, subject, ROW_NUMBER() OVER (PARTITION BY subject ORDER BY created_at DESC) as rn
        FROM facts 
        WHERE state NOT IN ('retired', 'superseded')
    ) WHERE rn = 1
);
SELECT changes();")

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) cortex-dedup: retired $RETIRED duplicate facts (kept newest per subject)"
