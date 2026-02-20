# DB Growth Guardrails & Maintenance Runbook

This runbook defines when to intervene on Cortex DB growth and how to do it safely.

## Why this exists
As memory/fact volume grows, output and operator workflows can degrade before correctness fails.
This guide tracks thresholds and a repeatable maintenance path.

## Daily / Weekly Checks

### Daily (quick)
```bash
cortex stats
```
Review:
- `storage_bytes`
- 24h growth in memories/facts
- `alerts` (if present)

### Weekly (operator)
```bash
cortex stats --json
cortex stale 7
```
Confirm whether growth is expected (imports, captures) vs noise churn.

## Thresholds

Treat these as intervention triggers:

1. **DB size notice**: `storage_bytes > 1.0 GB`
   - Action: schedule weekly review and confirm expected growth source.

2. **DB size warning**: `storage_bytes > 1.5 GB`
   - Action: run maintenance window (below) and verify post-maintenance deltas.

3. **Fact growth spike alert** (24h)
   - Action: inspect recent imports/capture sources and conflict/stale outputs for noise.

4. **Memory growth spike alert** (24h)
   - Action: validate capture hygiene and source dedupe behavior.

## Maintenance Window (Safe)

Run during low-traffic windows.

1) Backup DB file:
```bash
cp ~/.cortex/cortex.db ~/.cortex/cortex.db.backup.$(date +%Y%m%d%H%M%S)
```

2) Run integrity check:
```bash
sqlite3 ~/.cortex/cortex.db "PRAGMA integrity_check;"
```
Expected output: `ok`

3) Reclaim space:
```bash
sqlite3 ~/.cortex/cortex.db "VACUUM;"
```

4) Refresh planner stats:
```bash
sqlite3 ~/.cortex/cortex.db "ANALYZE;"
```

5) Verify post-state:
```bash
cortex stats --json
```
Compare size and growth metrics to pre-maintenance snapshot.

## Output Scaling Guidance

For large conflict sets:
- default to compact output:
  ```bash
  cortex conflicts
  ```
- use `--verbose` only when deep triage is required:
  ```bash
  cortex conflicts --verbose
  ```
- for machine workflows, prefer JSON + downstream filtering:
  ```bash
  cortex conflicts --json
  ```

## SLO Checkpoints (Operator)

Track these checkpoints during growth reviews:

- `cortex stats`: completes under **3s** on current production-scale DBs.
- `cortex search "<common query>" --mode hybrid --limit 10`: under **5s** baseline on warmed local DB.
- `cortex conflicts` (default compact mode): returns summary output without terminal spam or hangs.

If checkpoints regress materially, file/track under #64 and attach command output + DB size context.

## Related Tracking
- #64 — DB growth guardrails follow-through
- #74 — post-v0.3.4 reliability wave
