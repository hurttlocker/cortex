# Retrieval Tuning Phase 2 — Query-Aware Low-Signal Suppression

Date: 2026-02-20

## What changed
Implemented query-aware suppression for low-signal intents in search ranking flow.

### Behavior
When the incoming query is a known low-signal intent (e.g. `Fire the test`, `HEARTBEAT_OK`, `run test`):
- Suppress wrapper-heavy auto-capture results
- Suppress low-signal auto-capture content from result set
- Keep non-auto-capture results untouched
- Fail-safe fallback: if suppression would empty the set, return original results

### Code
- `internal/search/search.go`
- `internal/search/search_test.go`

## New benchmark fixture + runner
- Fixture: `tests/fixtures/retrieval/phase2-low-signal.json`
- Runner: `scripts/retrieval_precision_bench.py`

## Before/After benchmark
Compared binaries:
- **Before phase2**: commit `534015c`
- **After phase2**: current HEAD
- DB: `~/.cortex/cortex.db`

### Summary
- Both before and after passed fixture guardrails (no noisy top-3)
- Phase2 improved low-signal query pruning by reducing noisy tail for `Fire the test`

### Notable delta
- Query `Fire the test`
  - Before: 8 hits, noisy positions `[5,6,7,8]`
  - After: 4 hits, noisy positions `[]`

Other fixture queries remained stable (no regressions in top ranks).

## Interpretation
Phase 1 pushed noise down. Phase 2 now adds intent-aware suppression so low-signal triggers return cleaner, tighter sets without wrapper tail pollution.
