# LoCoMo conv-30 Temporal Phase A+B — 2026-03-22

## Scope

This note captures the `conv-30` 81-question LoCoMo slice after landing:

- Phase A: session anchor propagation
- Phase B: fact-level temporal normalization, extraction/enrichment schema updates, query-time temporal parsing/boost, and answer-context temporal rendering

This run was executed on top of the quick-win foundation so it is directly comparable to the earlier `7.61%` product-path baseline.

## Branch

- branch: `feat/temporal-normalization`
- binary under test: `/tmp/cortex-temporal-phaseb`

## Method

- imported the full public 10-conversation corpus
- scored only the `conv-30` answerable slice, 81 questions
- kept the same reader model and embedder as the quick-wins baseline:
  - embedder: `openrouter/text-embedding-3-small`
  - answer model: `openrouter/google/gemini-2.0-flash-001`

## Results

| Mode | Questions | Top-1 hit | Top-5 hit | Evidence recall | Answer hit | F1 | Exact match | Avg latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| BM25 retrieval | 81 | 50.62% | 69.14% | 65.60% | 30.86% | — | — | 61 ms |
| Hybrid retrieval | 81 | 28.40% | 60.49% | 56.73% | 27.16% | — | — | 2.17 s |
| `cortex answer --mode hybrid --embed ...` | 81 | — | — | — | — | 8.93% | 0.00% | 3.28 s |

Degraded responses from the answer path: `0`

## Delta vs Quick Wins Baseline

Quick-wins baseline:

- hybrid answer F1: `7.61%`
- hybrid answer exact match: `0.00%`
- degraded responses: `1`

Temporal Phase A+B:

- hybrid answer F1: `8.93%`
- hybrid answer exact match: `0.00%`
- degraded responses: `0`

Absolute change:

- F1: `+1.32`
- exact match: `+0.00`
- degraded responses: `-1`

## Interpretation

- The temporal work helped, but only modestly on the real `cortex answer` path.
- The main visible product gain in this slice is that hybrid answer no longer degraded at all.
- The answer-context temporal anchors and normalized temporal facts were not enough by themselves to close the benchmark gap.
- This is consistent with the benchmark diagnosis: temporal normalization matters, but the answer path is still constrained by one-shot retrieval and weak composition.

## Conclusion

Phase A+B moved the honest product-path baseline from `7.61%` F1 to `8.93%` F1 on the `conv-30` slice.

That is real progress, but it is not enough on its own. The next material step should be the multi-hop planner and a stronger answer-path composition strategy.
