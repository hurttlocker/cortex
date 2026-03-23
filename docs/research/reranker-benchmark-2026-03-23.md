# Cross-Encoder Reranker Benchmark — 2026-03-23

## Scope

This note records the first end-to-end benchmark of the local cross-encoder reranker from issue `#353`.

The implemented pipeline is:

1. hybrid search
2. weighted fusion
3. cross-encoder rerank over the top candidate set
4. `cortex ask` synthesis

The benchmark goal was to measure the real product-path delta on the public LoCoMo `conv-30` slice.

## Model Choice

The shipped default model is:

- `onnx-community/bge-reranker-base-ONNX:int8`

Why this default:

- it is ONNX-native, so no conversion step is required
- it is much smaller than the `m3` ONNX mirror
- it is usable with the current process-per-query CLI benchmark shape

I also spot-checked the intended stronger target model:

- `onnx-community/bge-reranker-v2-m3-ONNX:int8`

That model produced a better answer on the first benchmark question, but it took about `55s` for a single `ask` invocation on this machine, which makes full `conv-30` evaluation impractical with the current CLI architecture.

## Method

- binary: `/tmp/cortex-rerank`
- dataset: public LoCoMo `conv-30`
- questions scored: `81`
- categories included: `1`, `2`, and `4`
- ask model: `google/gemini-2.5-flash`
- hybrid embedder: `openrouter/text-embedding-3-small`
- ask budget: `1200`

Benchmark substrate used for the final numbers:

- existing populated DB: `/tmp/cortex-locomo-combined-2026-03-22/cortex.db`

Important caveat:

- I attempted a fresh re-import first, but the current live `main` import path produced an empty DB in this environment despite reporting imported rows. Because the user explicitly allowed using the existing benchmark DB, I used the known-good populated DB for the final reranker measurement.

Commands under test:

```bash
cortex ask "<question>" \
  --mode hybrid \
  --budget 1200 \
  --model google/gemini-2.5-flash \
  --embed openrouter/text-embedding-3-small \
  --rerank off \
  --json
```

```bash
cortex ask "<question>" \
  --mode hybrid \
  --budget 1200 \
  --model google/gemini-2.5-flash \
  --embed openrouter/text-embedding-3-small \
  --rerank on \
  --json
```

## Results

### Phase 1: Full-Memory Rerank Input

Initial shipped input format:

- query + section/date metadata + full memory content

Measured result:

| Mode | Questions | F1 | Exact match | Avg latency | Median latency | Degraded | Avg packed tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ask --rerank off` | 81 | `10.53%` | `0.00%` | `5452.20 ms` | `5520.53 ms` | `12` | `450.22` |
| `ask --rerank on` | 81 | `1.14%` | `0.00%` | `10650.59 ms` | `9798.32 ms` | `1` | `434.74` |

This was a hard regression.

### Phase 2: Evidence-Window Rerank Input

Revised input format:

- query + section/date metadata + extracted evidence window
- evidence window selection:
  - deterministic 1-3 block span
  - simple lexical/entity/temporal scoring
  - max `128` tokens

Measured result:

| Mode | Questions | F1 | Exact match | Avg latency | Median latency | Degraded | Avg packed tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ask --rerank off` | 81 | `12.48%` | `0.00%` | `5202.57 ms` | `5257.60 ms` | `9` | `450.22` |
| `ask --rerank on` | 81 | `11.93%` | `0.00%` | `11751.15 ms` | `11180.24 ms` | `6` | `441.74` |

Phase 2 delta:

- F1: `-0.55`
- average latency: `+6548.58 ms`
- degraded count: `-3`

### Category Breakdown

Phase 2 `ask --rerank off`

- category `1`: `14.98%` F1
- category `2`: `12.19%` F1
- category `4`: `8.43%` F1

Phase 2 `ask --rerank on`

- category `1`: `0.00%` F1
- category `2`: `0.00%` F1
- category `4`: `2.10%` F1

### Historical Comparison

Previously recorded `cortex ask` baseline on March 22, 2026:

- `15.77%` F1

This rerun without reranking measured `10.53%` in Phase 1 and `12.48%` in Phase 2, so the historical `15.77%` number and this branch-local rerun should not be compared directly as if they were a controlled A/B. The trustworthy comparison for this work is the same-run delta within each phase.

- `10.53%` without rerank
- `1.14%` with rerank

## Interpretation

The first shipped reranker input was wrong for Cortex’s retrieval unit.

What changed after diagnosis:

- switched from full memory blobs to extracted evidence windows before cross-encoder scoring
- preserved full memory content in the returned results and only changed reranker input text

What that fixed:

- the reranker stopped catastrophically inverting the product path
- diagnostic questions recovered:
  - `conv-30:62` stayed correct
  - `conv-30:11` kept the answer-bearing memory in the reranked set and `ask` answered correctly
  - `conv-30:27` improved enough for `ask` to answer correctly, though the ideal dual-entity memory still was not rank 1

What remains true:

- the default `base` int8 reranker is still slightly worse than no reranker on the full 81-question slice
- latency is still materially higher
- `m3` looks directionally better on a spot check, but it is too slow for the current per-query CLI process model

## Conclusion

The infrastructure is working and the input fix materially improved it:

- optional local model setup
- graceful `auto|on|off` flagging
- rerank insertion in the hybrid/RRF hot path
- real ONNX inference in Go

But the current default model/configuration is still not merge-ready as a retrieval improvement. After the evidence-window fix it is close to neutral, not positive:

- `12.48%` F1 without rerank
- `11.93%` F1 with rerank

The likely next step is not another small tuning pass on `bge-reranker-base:int8`. The better path is:

1. move reranking into a long-lived process or daemon so `m3` is feasible
2. benchmark `m3` or another stronger reranker on the same 81-question slice
3. only merge once the reranked path is at least neutral against the same-run no-rerank baseline
