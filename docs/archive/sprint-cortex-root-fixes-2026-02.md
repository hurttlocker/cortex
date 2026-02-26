# Cortex Sprint — Root Fixes (Kickoff: 2026-02-20)

## Why this sprint
Live 0.3.5 checks show Cortex is up, but quality is being dragged by ingestion noise + over-extraction:

- DB size: ~1.66GB (high)
- Fact growth spike: ~728K/day
- Memory growth spike: ~674/day
- Fact mix skew: ~93% `kv`
- Retrieval quality misses on short intent probes (e.g. "Fire the test")

## Sprint goals (root-level)
1. Reduce junk captures at ingestion (before they become facts)
2. Improve extraction precision (especially `kv` over-production)
3. Improve retrieval precision for short intent queries
4. Keep growth healthy with maintenance guardrails

## Workstreams

### A) Capture hygiene (P0)
- [x] Strip boilerplate wrappers before capture filtering/ranking inputs
  - `<cortex-memories>` / `<relevant-memories>` blocks
  - `Conversation info (untrusted metadata)` JSON blocks
  - `Sender (untrusted metadata)` JSON blocks
  - queued-envelope scaffolding lines
  - Commit: `806740f`
- [ ] Add suppression for queued backlog wrappers that contain no substantive user request
- [ ] Add regression fixtures from Discord/Telegram envelopes

### B) Extraction precision (P0)
- [ ] Audit extractor rules producing `kv` inflation
- [ ] Add guardrails to avoid metadata JSON becoming high-volume facts
- [ ] Re-balance classifier thresholds and rerun on sample corpus

### C) Retrieval precision (P1)
- [ ] Tune ranking for low-information short queries
- [ ] Add class-aware penalties for noisy capture classes
- [ ] Add benchmark suite for 25 known-answer queries

### D) Ops + scale hygiene (P1)
- [ ] Schedule maintenance routine (`optimize`/VACUUM cadence)
- [ ] Add growth guardrail alerts by slope (facts/day, memories/day)
- [ ] Add quality dashboard: precision@k + noise rate + kv ratio trend

## Exit criteria
- Fact growth reduced by >=40% from current baseline (without recall collapse)
- `kv` ratio reduced from ~93% to <=75%
- Known-answer retrieval precision@5 >=85%
- No integrity regressions; full `go test ./...` green

## Current baseline snapshot
- Version: `cortex 0.3.5`
- Stats sample: 2,828 memories / 3,043,341 facts / 774 sources / 1.66GB
- Alerts: `db_size_high`, `fact_growth_spike`, `memory_growth_spike`
