# ONNX Embedder Benchmark — 2026-03-24

## Scope

This note records the first local-embedder benchmark for issue `#360` / `#361`.

The target was the real live Cortex DB:

- DB: `~/.cortex/cortex.db`
- memories: `5694`
- facts: `5822`
- stored embeddings: `5694`
- stored dimensions: `384`

Important live-state note:

- `cortex embed --status` showed a pre-existing config mismatch before the benchmark:
  - configured provider: `ollama/nomic-embed-text`
  - stored corpus dimensions: `384`
- because `nomic-embed-text` is `768d`, the live DB was clearly not aligned with the current configured provider
- for that reason I benchmarked with explicit provider flags on copied DBs instead of mutating the live DB in place

## Setup

- binary under test: `/tmp/cortex-onnx-embed`
- source DB: `~/.cortex/cortex.db`
- ONNX benchmark DB copy: `/tmp/cortex-onnx-solo-2026-03-24.db`
- bounded Ollama-rate DB copy: `/tmp/cortex-ollama-rate-2026-03-24.db`
- ONNX model warmed once before timing so the measured run excludes the first-use download

Commands used:

```bash
/tmp/cortex-onnx-embed --db /tmp/cortex-onnx-solo-2026-03-24.db \
  embed onnx/all-minilm-l6-v2 --force
```

```bash
/tmp/cortex-onnx-embed --db /tmp/cortex-ollama-rate-2026-03-24.db \
  embed ollama/all-minilm --force
```

## Throughput

### ONNX full live-DB pass

Measured command result:

```text
embed memories_processed=5694 embeddings_added=5694 embeddings_skipped=0 errors=0 elapsed_ms=69199
```

Derived throughput:

- `5694 / 69.199s = 82.3 emb/s`

### Ollama live-DB bounded sample

I did not wait for a full `--force` pass to finish because the live-DB run was projecting to hours, not minutes.

Observed progress on the copied live DB:

- after `120s`: `12` embeddings persisted
- after `180s`: `18` embeddings persisted

Derived bounded-sample throughput:

- `18 / 180s = 0.10 emb/s`

Extrapolated full-pass duration at that observed rate:

- `5694 / 0.10 emb/s = 56,940s`
- about `15.8h`

## Result

| Provider | Corpus | Measurement type | Elapsed | Throughput |
| --- | ---: | --- | ---: | ---: |
| `onnx/all-minilm-l6-v2` | `5694` memories | full run | `69.2s` | `82.3 emb/s` |
| `ollama/all-minilm` | `5694` memories | bounded live sample, extrapolated | `180s` sample | `0.10 emb/s` observed |

Read plainly:

- ONNX was dramatically faster than the live Ollama path on this machine
- this does **not** match the earlier small-corpus manual reference (`~6.9 emb/s`) mentioned in issue `#361`
- the honest interpretation is that the current live Ollama setup here is underperforming badly relative to prior reference numbers

## Search Quality

I checked quality in two ways.

### 1. Existing live `384d` corpus vs fresh ONNX re-embed

I compared semantic top-5 results for five representative live-memory queries using the same ONNX query embedder against:

- the fully ONNX-reembedded DB copy
- the existing live `384d` DB

Those rankings were **not identical**.

That is consistent with the earlier config mismatch:

- the existing live corpus is `384d`
- but the configured provider is `nomic-embed-text`
- so the current live store cannot be treated as a clean runtime-to-runtime all-MiniLM baseline

### 2. Direct ONNX vs Ollama all-MiniLM runtime parity

To isolate the runtime difference from the stale live corpus, I embedded:

- `8` representative recent live-memory excerpts
- `5` representative search queries

with both providers directly:

- `onnx/all-minilm-l6-v2`
- `ollama/all-minilm`

Results:

- average candidate-vector cosine between runtimes: `0.986`
- average query-vector cosine between runtimes: `0.991`
- identical top-1 retrieval on the sampled set: `5 / 5`
- identical top-3 retrieval on the sampled set: `4 / 5`

Interpretation:

- the two runtimes are very close on the same model family
- they are not bit-identical in this environment
- for sampled retrieval they were effectively top-1 equivalent

## Bottom Line

The ONNX embedder is viable and fast on the real live corpus:

- full `5694`-memory re-embed completed in `69.2s`
- no external service dependency
- `384d` output matches the preferred all-MiniLM footprint

The live Ollama path here was too slow to finish a full fair pass during the benchmark window:

- bounded sample observed `0.10 emb/s`
- projected full pass was `~15.8h`

So the honest conclusion from this environment is:

1. local ONNX embedding is ready for zero-setup use
2. ONNX speed is more than acceptable here
3. the current live Ollama setup is the outlier, not ONNX
4. the direct provider-to-provider sample shows near-parity quality at top-1, even though the stale live corpus does not rank identically
