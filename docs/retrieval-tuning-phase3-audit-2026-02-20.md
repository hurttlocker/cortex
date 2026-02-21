# Retrieval Tuning Phase 3 — Metadata-Aware Hybrid Reranking + 25-Query Precision Suite

Date: 2026-02-20

## Scope
Phase 3 delivered two things:
1. **Metadata-aware hybrid reranking prior** in `mergeWeightedScores`
2. **Precision harness (25-query suite)** with precision@k + noise checks

## Code changes
- `internal/search/search.go`
  - Added hybrid metadata prior multipliers:
    - curated source boost (`/clawd/memory/`, `MEMORY.md`, `USER.md`)
    - auto-capture baseline penalty
    - stronger penalty for wrapper/noise capture content
    - low-signal capture content penalty
  - Added explainability reason string for prior application.
- `internal/search/search_test.go`
  - Added tests for hybrid prior and merge reordering.
- `scripts/retrieval_precision_bench.py`
  - Added precision@k support (`expected_contains_any`, `k`, `min_precision_at_k`).
- `tests/fixtures/retrieval/phase3-precision-25.json`
  - New 25-query benchmark fixture.

## Validation
- `go test ./...` ✅

## Benchmark (same DB, pre vs post phase3 binaries)
DB: `~/.cortex/cortex.db`

### Low-signal fixture (`phase2-low-signal.json`)
- Pre: 4/4 passed
- Post: 4/4 passed
- Maintained low-signal suppression behavior.

### 25-query precision fixture (`phase3-precision-25.json`)
- Pre: 21/25 passed, avg precision@k **0.664**
- Post: 21/25 passed, avg precision@k **0.680**

## Interpretation
- Phase 3 improved overall precision@k while preserving low-signal guardrails.
- Remaining misses are concentrated in ambiguous/noisy intents and fixture strictness for low-signal historical queries.

## Next step candidates
1. Add query-intent buckets (ops/trading/personal) with source priors.
2. Add per-query expected-source constraints to reduce fixture ambiguity.
3. Add precision trend reporting over time in CI.
