# Retrieval Parity Foundation Benchmark — 2026-03-23

## Scope

This note benchmarks the `feat/retrieval-parity-foundation` branch against the same public LoCoMo `conv-30` answerable slice used in the `v1.4.0` note.

This branch adds:

- query strategy classification
- strategy-aware RRF weights
- a temporal retrieval channel in RRF
- bounded scene expansion
- grouped evidence rendering in `ask` / `answer`
- `ask` and `answer` defaulting to `rrf`

## Setup

- repo branch: `feat/retrieval-parity-foundation`
- binary: `/tmp/cortex-retrieval-parity-bench`
- benchmark DB: `/tmp/cortex-v140-benchmark-2026-03-23/cortex.db`
- corpus source: `/tmp/cortex-locomo-combined-2026-03-22/corpus`
- question source: `/tmp/cortex-locomo-run-fast/checkpoints/hybrid.json`
- raw artifact: `/tmp/retrieval-parity-results-2026-03-23.json`
- reader model: `google/gemini-2.5-flash`
- query embedder: `openrouter/text-embedding-3-small`

The DB was the same fresh LoCoMo-only DB used for the existing `v1.4.0` benchmark. I did not re-import or re-embed it.

## Modes

I measured two product-path variants through `cortex ask`:

- `rrf_default`
  - `--mode rrf --rerank off`
- `rrf_rerank_daemon`
  - `--mode rrf --rerank on`
  - warm local reranker daemon on `localhost:9720`

## Results

| Mode | F1 | Avg latency | Median latency | Degraded |
| --- | ---: | ---: | ---: | ---: |
| `rrf_default` | `11.90%` | `6461.61 ms` | `6328.81 ms` | `0` |
| `rrf_rerank_daemon` | `15.03%` | `9706.82 ms` | `9910.43 ms` | `0` |

Important note:

- normalized exact accuracy on raw answer strings stayed `0.00%` in both modes
- this is not a retrieval failure signal; it reflects that the current answer path still returns cited short-form sentences instead of exact normalized gold strings
- the meaningful comparison metric here remains the same LoCoMo-style token F1 used in the prior Cortex notes

## Comparison To v1.4.0

Reference numbers from `docs/research/v140-benchmark-2026-03-23.md`:

- `A` baseline: `11.81%` F1
- `C` reranker daemon only: `13.26%` F1
- prior historical best single-feature benchmark: `15.77%` F1

Delta from `A` baseline:

- `rrf_default`: `+0.09`
- `rrf_rerank_daemon`: `+3.22`

Delta from `C` reranker daemon only:

- `rrf_rerank_daemon`: `+1.77`

Delta from prior `15.77%` reference:

- `rrf_rerank_daemon`: `-0.74`

The key read:

- the retrieval-parity foundation work is basically neutral on the non-reranked path
- the same work is a real win on the daemon-backed best path
- the branch closes most of the remaining gap to the previous `15.77%` best reference

## Category Breakdown

### `rrf_default`

- category `1`: `18.70%` F1 on `11` questions
- category `2`: `10.12%` F1 on `26` questions
- category `4`: `11.26%` F1 on `44` questions

### `rrf_rerank_daemon`

- category `1`: `27.46%` F1 on `11` questions
- category `2`: `11.22%` F1 on `26` questions
- category `4`: `14.18%` F1 on `44` questions

Interpretation:

- the biggest gain is category `1`
- category `2` is roughly flat relative to the prior reranker-backed path
- category `4` stays roughly flat to slightly down relative to the prior reranker-backed path

That profile is consistent with what this branch changed:

- better routing
- better evidence-set construction
- better scene-level packing

These help compositional / scene-sensitive questions more than pure date normalization.

## 3-Question Diagnostic

### `conv-30:27` `Did Jon and Gina both participate in dance competitions?`

- `rrf_default` answer:
  - `Jon participates in dance competitions [1]. There is no evidence that Gina participates in dance competitions.`
  - F1: `0.00`
- `rrf_rerank_daemon` answer:
  - `Yes, both Jon and Gina participated in dance competitions...`
  - F1: `1.00`

This is the clearest branch win.

### `conv-30:62` `How does Gina stay confident in her business?`

- `rrf_default` F1: `0.6667`
- `rrf_rerank_daemon` F1: `0.6977`

Small improvement, same evidence family.

### `conv-30:11` `When did Gina get her tattoo?`

- `rrf_default` answer:
  - `Gina got her tattoo a few years ago [4].`
  - F1: `0.5455`
- `rrf_rerank_daemon` answer:
  - `Gina got her tattoo a few years ago [1]. This statement was made on February 8, 2023 [1].`
  - F1: `0.3000`

This is a useful warning:

- grouped evidence and scene expansion help composition
- but the reader still sometimes appends extra temporal framing that hurts strict F1

## Bottom Line

This branch is a real retrieval improvement when paired with the reranker daemon:

- `13.26%` -> `15.03%` F1 on the best path
- `0` degraded responses maintained

Without the daemon, the branch is roughly flat:

- `11.81%` -> `11.90%`

So the honest conclusion is:

1. the strategy classifier + temporal channel + scene expansion stack is directionally correct
2. the gain shows up most clearly when the reranker is present
3. answer-form discipline is now the main blocker to converting these retrieval wins into normalized exact-answer wins
