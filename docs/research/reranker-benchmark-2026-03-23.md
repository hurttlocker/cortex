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

### Same-Run Off vs On

| Mode | Questions | F1 | Exact match | Avg latency | Median latency | Degraded | Avg packed tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ask --rerank off` | 81 | `10.53%` | `0.00%` | `5452.20 ms` | `5520.53 ms` | `12` | `450.22` |
| `ask --rerank on` | 81 | `1.14%` | `0.00%` | `10650.59 ms` | `9798.32 ms` | `1` | `434.74` |

Delta:

- F1: `-9.39`
- average latency: `+5198.39 ms`
- degraded count: `-11`

### Category Breakdown

`ask --rerank off`

- category `1`: `14.98%` F1
- category `2`: `12.19%` F1
- category `4`: `8.43%` F1

`ask --rerank on`

- category `1`: `0.00%` F1
- category `2`: `0.00%` F1
- category `4`: `2.10%` F1

### Historical Comparison

Previously recorded `cortex ask` baseline on March 22, 2026:

- `15.77%` F1

This rerun without reranking measured `10.53%` F1, so the historical `15.77%` number and this branch-local rerun should not be compared directly as if they were a controlled A/B. The trustworthy comparison for this work is the same-run delta:

- `10.53%` without rerank
- `1.14%` with rerank

## Interpretation

The first shipped reranker does not help the real `cortex ask` path yet.

What went wrong:

- the `base` int8 reranker is too weak for these long conversational evidence chunks
- the reranker reorders the packed evidence aggressively enough that the reader often falls back to weaker context and outputs low-information answers
- citation integrity improved numerically because the model often answered with fewer or no factual claims, but that was not a quality win

What I tested during debugging:

- switched rerank input construction away from raw HTML-marked snippets and toward richer section/content text
- spot-checked the stronger `m3` reranker

What I found:

- the richer text construction did not recover the `base` model enough
- `m3` looks directionally better on a spot check, but it is too slow for the current per-query CLI process model

## Conclusion

The infrastructure is working:

- optional local model setup
- graceful `auto|on|off` flagging
- rerank insertion in the hybrid/RRF hot path
- real ONNX inference in Go

But the current default model/configuration is not merge-ready as a retrieval improvement. On the measured `conv-30` slice it regressed badly:

- `10.53%` F1 without rerank
- `1.14%` F1 with rerank

The likely next step is not another small tuning pass on `bge-reranker-base:int8`. The better path is:

1. move reranking into a long-lived process or daemon so `m3` is feasible
2. benchmark `m3` or another stronger reranker on the same 81-question slice
3. only merge once the reranked path is at least neutral against the same-run no-rerank baseline
