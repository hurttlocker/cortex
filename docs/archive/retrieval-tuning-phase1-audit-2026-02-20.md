# Retrieval Tuning Phase 1 — Audit (2026-02-20)

## Change shipped
Implemented capture-noise rank penalty in search engine:
- Penalizes auto-capture memories containing wrapper/noise scaffolding
- Keeps results searchable, but pushes noisy captures lower in rank

Code:
- `internal/search/search.go`
- `internal/search/search_test.go`

## Benchmark setup
Compared **pre-tuning** (`ca9fc02`) vs **post-tuning** (current) binaries on same DB (`~/.cortex/cortex.db`) with hybrid search.

Queries:
1. `Fire the test`
2. `Conversation info untrusted metadata`
3. `current time Saturday February 14th 2026 9:04 PM`
4. `HEARTBEAT_OK`
5. `Q prefers Sonnet for coding tasks`

## Observed impact
- `Fire the test`: noisy hits in top-3 improved **1 → 0** (noise pushed to positions 6-8)
- `current time ...`: slight tail improvement (noise moved lower at bottom)
- `HEARTBEAT_OK`: neutral
- `Q prefers Sonnet ...`: neutral (clean query preserved)
- `Conversation info untrusted metadata`: intentionally still returns metadata-heavy memories (query directly asks for them)

## Interpretation
Phase 1 retrieval tuning gives a measurable ranking improvement on low-signal capture queries without harming clean queries.

## Next retrieval steps
1. Query-aware suppression for low-signal intents (`fire the test`, `heartbeat_ok`) to hard-drop stale capture wrappers.
2. Metadata-aware reranking in hybrid merge (reward decision/preference + penalize wrapper-heavy captures).
3. Add `search bench` fixture for precision@k tracking on known-answer query set.
