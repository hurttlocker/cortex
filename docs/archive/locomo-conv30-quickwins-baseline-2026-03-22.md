# LoCoMo conv-30 Quick Wins Baseline — 2026-03-22

## Scope

This note records the baseline after landing the two retrieval foundations on branch `feat/locomo-quick-wins`:

1. `cortex answer` hybrid embedder wiring
2. FTS query sanitization

This is the clean baseline to use before any temporal normalization or multi-hop planner work.

## What Changed

### Quick Win B

`cortex answer` now accepts `--embed <provider/model>` and uses the same embedder auto-resolution path as `cortex search` for `semantic`, `hybrid`, and `rrf` modes.

That unblocks real product-path evaluation of hybrid answer mode.

### Quick Win A

FTS sanitization now strips natural-language punctuation and handles quoted questions more safely before sending the query to SQLite FTS5.

This is primarily a correctness/stability fix. On the `conv-30` slice it had little visible score impact, which is expected because the worst quoted-query failures were outside this slice.

## Method

- Branch under test: `feat/locomo-quick-wins`
- Binary under test: `/tmp/cortex-locomo-quickwins-clean`
- Corpus imported: full public LoCoMo corpus, all 10 conversations
- Slice scored: `conv-30`, categories 1/2/4 only, 81 questions
- Import settings:

```bash
cortex --db /tmp/cortex-locomo-conv30-quickwins-full-clean/cortex.db \
  import /tmp/cortex-locomo-run-fast/corpus \
  --recursive \
  --extract \
  --no-enrich \
  --no-classify
```

- Hybrid embedder:

```bash
openrouter/text-embedding-3-small
```

- Reader model for `cortex answer`:

```bash
openrouter/google/gemini-2.0-flash-001
```

## Results

| Mode | Questions | Top-1 hit | Top-5 hit | Evidence recall | Answer hit | F1 | Exact match | Avg latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| BM25 retrieval | 81 | 54.32% | 70.37% | 66.83% | 33.33% | — | — | 64 ms |
| Hybrid retrieval | 81 | 28.40% | 61.73% | 57.96% | 27.16% | — | — | 2.15 s |
| `cortex answer --mode hybrid --embed ...` | 81 | — | — | — | — | 7.61% | 0.00% | 5.52 s |

Degraded responses from the answer path: `1`

## Interpretation

- Quick Win B succeeded technically: hybrid answer mode now runs through the real `cortex answer` path instead of silently requiring a custom harness.
- That real answer path benchmarks much worse than the earlier custom reader harness. This is useful because it gives us the true product baseline before temporal or planner work.
- Quick Win A is still worth keeping because it removes parser failures, even though the score impact on this specific 81-question slice is negligible.
- Hybrid retrieval still does not beat BM25 on this slice.

## Conclusion

Before starting temporal normalization or multi-hop planning, the numbers to beat are:

- BM25 top-5 evidence hit: `70.37%`
- Hybrid top-5 evidence hit: `61.73%`
- Hybrid evidence recall: `57.96%`
- Hybrid answer F1: `7.61%`

This is the clean post-quick-wins baseline.
