# Auto-Capture Reprocess Runbook + Execution Log

Date: 2026-02-20 (America/New_York)

## Objective
Safely reprocess auto-capture memories with the new 0-A/0-B extraction hygiene rules, in staged batches with backups and checkpoints.

## Tooling
- Script: `scripts/staged_reprocess_auto_capture.go`
- Mode support:
  - `--dry-run` (no mutation)
  - write mode with per-stage DB backup (`--backup`)
- Selection:
  - `source_file LIKE '%auto-capture%'`
  - ordered by `imported_at DESC`
  - `--limit/--offset` for staged batches

## Staged execution (completed)

### Stage 1
- Selection: `limit=250 offset=0`
- Backup: `~/.cortex/backups/cortex-pre-stage1-20260220-224112.db`
- Before facts: 15,683
- After facts: 209

### Stage 2
- Selection: `limit=500 offset=250`
- Backup: `~/.cortex/backups/cortex-pre-stage2-20260220-224137.db`
- Before facts: 234,649
- After facts: 752

### Stage 3
- Selection: `limit=500 offset=750`
- Backup: `~/.cortex/backups/cortex-pre-stage3-20260220-224209.db`
- Before facts: 639,866
- After facts: 739

### Stage 4
- Selection: `limit=500 offset=1250` (selected 428)
- Backup: `~/.cortex/backups/cortex-pre-stage4-20260220-224329.db`
- Before facts: 784,509
- After facts: 535

## Aggregate outcome
- Memories processed: **1,678 / 1,678**
- Failures: **0**
- Facts deleted: **1,674,707**
- Facts inserted: **2,235**
- KV facts: **1,534,507 → 1,949**
- Noisy KV predicates (scaffold set): **217,195 → 1**

## Post-run maintenance
- Ran: `cortex optimize --vacuum-only --json`
- DB size: **1,668,427,776 → 716,066,816 bytes** (~953MB reclaimed)

## Verification snapshot (post run)
- `scripts/cortex.sh stats`:
  - memories: 2,841
  - facts: 1,381,412
  - db size: 716,066,816 bytes
  - alerts: `memory_growth_spike` (expected with active capture)

## Repeatable command template
```bash
# Dry run
GOFLAGS="" go run ./scripts/staged_reprocess_auto_capture.go \
  --dry-run --limit 500 --offset 0 \
  --report /tmp/cortex-stage-dryrun.json

# Write mode with backup
ts=$(date +%Y%m%d-%H%M%S)
GOFLAGS="" go run ./scripts/staged_reprocess_auto_capture.go \
  --limit 500 --offset 0 \
  --backup "$HOME/.cortex/backups/cortex-pre-stage-$ts.db" \
  --report "/tmp/cortex-stage-write-$ts.json"
```
