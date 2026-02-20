# Local LLM Performance Guide (`cortex reason`)

This guide helps you choose local models based on hardware, expected speed, and use case.

## TL;DR

- **Intel CPU-only**: use local models for scheduled/cron workloads, not interactive chat.
- **Apple Silicon (Metal)**: local interactive use is viable for 4B–8B models.
- **GPU Linux/NVIDIA**: best local throughput; suitable for larger models and recursive runs.
- If latency matters more than privacy: use cloud models via OpenRouter.

## Known Baseline (Intel i7-10700K, CPU-only)

| Model | Approx tok/s | Typical latency | Notes |
|---|---:|---:|---|
| `phi4-mini` (3.8B) | ~7.5 | ~60s (263 toks) | Best local Intel pick |
| `qwen3:4b` | ~7.1 | ~44s (short outputs) | May need no-think setting |
| `gemma2:9b` | ~4.9 | 36s short / 90s+ long | Better quality, slower |

**Recommendation:** Intel CPU-only should default to cron/scheduled usage for `cortex reason`.

## Hardware Tiers & Model Recommendations

| Tier | Recommended Models | Best Use |
|---|---|---|
| Intel Mac / CPU-only | `phi4-mini`, `qwen3:4b` | Nightly digests, audits, batched jobs |
| Apple Silicon 8GB | `phi4-mini`, `qwen3:4b` | Light interactive + cron |
| Apple Silicon 16GB+ | `qwen3:8b`, `deepseek-r1:8b`, `gemma2:9b` | Interactive + recursive runs |
| Apple Silicon Pro/Max | 8B–14B class models | Deeper recursive analysis |
| Linux + NVIDIA GPU | 8B+ and larger | Highest throughput local inference |

## Decision Rule

Use this quick routing rule:

1. **Need privacy + can tolerate latency** → local model
2. **Need fast interactive answers** → cloud model (`--model google/gemini-2.5-flash` etc.)
3. **Need deep recursive reliability** → cloud recursive by default, local recursive when GPU-class hardware is available

## Benchmark Your Own Machine

Run this sequence with your real memory set:

```bash
# 1) Ensure embeddings are available
cortex embed ollama/nomic-embed-text --batch-size 10

# 2) Compare local models on presets
cortex bench --local --embed ollama/nomic-embed-text --output local-bench.md

# 3) Compare one local model vs cloud
cortex bench --compare "phi4-mini,google/gemini-2.5-flash" \
  --embed ollama/nomic-embed-text \
  --output local-vs-cloud.md

# 4) Test recursive mode explicitly
cortex bench --recursive --max-iterations 6 --max-depth 1 --output recursive-local.md
```

## Ops Notes

- Prefer `nomic-embed-text` for local hybrid/semantic search embeddings.
- Long recursive runs should be scheduled off hot paths if local CPU-only.
- Track quality and latency with telemetry (`~/.cortex/reason-telemetry.jsonl`) and `cortex codex-rollout-report`.

## Privacy Note

Local inference keeps model execution on your machine. This is useful for sensitive data and compliance-constrained workflows.
