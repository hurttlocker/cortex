# Cortex v0.9.0 — LLM-Augmented Intelligence

**Release Date:** February 24, 2026

The intelligence layer for Cortex. Every feature is optional (`--expand`, `--enrich`, `--resolve llm`), zero-cost by default, and benchmarked across 6 models before shipping.

## What's New

### 🧠 Query Expansion (#216)
- `cortex search "vague query" --expand` → LLM rewrites query into precise search terms before hitting BM25/semantic
- LRU cache prevents duplicate API calls for repeated queries
- Default: Gemini 2.0 Flash (free, 686ms avg, 10/10 success rate)
- Graceful fallback: if LLM fails, original query runs unchanged

### 🔬 Smarter Fact Extraction (#218)
- `cortex extract --enrich <file>` → Grok 4.1 Fast finds facts rule-based extraction misses
- Additive-only: never removes or modifies rule-extracted facts, tagged as `llm-enrich`
- Benchmarked: Grok +26 facts across 3 test files vs ≤9 for all other models
- `--enrich` implies `--extract` — no need to specify both

### 🏷️ Auto-Classification (#219)
- `cortex classify` → reclassifies generic `kv` facts into semantic types (decision, config, state, temporal, etc.)
- Default: DeepSeek V3.2 (76% reclassification, 0 errors, $0.25/M tokens)
- Concurrent batch processing: `--concurrency 5` (default), `--batch-size 20` (default)
- New fact type: `config` added to schema (technical settings, parameters)

### ⚔️ Conflict Auto-Resolution (#217)
- `cortex conflicts --resolve llm` → LLM evaluates contradictory facts using recency, source authority, and context
- Three actions: **supersede** (clear winner), **merge** (combine complementary info), **flag-human** (ambiguous)
- Confidence-gated: below 0.7 → automatically flagged for human review
- Concurrent with `--dry-run` support for safe preview

### 📊 Fact Clustering & Summarization (#220)
- `cortex summarize --llm <model>` → consolidates redundant facts within topic clusters
- Leverages v0.8.0 clustering infrastructure
- Creates summary facts with combined provenance, supersedes originals (audit trail preserved)
- Designed for monthly maintenance runs

## Performance & Cost

| Feature | Default Model | Latency | Cost |
|---|---|---|---|
| Query Expansion | Gemini 2.0 Flash | 686ms | **Free** |
| Enrichment | Grok 4.1 Fast | ~50s/file | ~$0.01/file |
| Classification | DeepSeek V3.2 | ~14s/100 facts | ~$0.50/20K facts |
| Conflict Resolution | DeepSeek V3.2 | ~8s/5 pairs | ~$0.005/conflict |
| Summarization | User-specified | ~10s/cluster | ~$0.05/cluster |

**Estimated ongoing cost: <$1/month** for a personal knowledge base.

## Breaking Changes
- None. All LLM features are opt-in via flags. Existing behavior unchanged.

## Technical Details
- New packages: `internal/extract/resolve.go`, `internal/extract/enrich.go`, `internal/extract/classify.go`, `internal/extract/summarize.go`, `internal/llm/` (provider adapter), `internal/search/expand.go`
- New store methods: `UpdateFactType()`, `config` in fact_type CHECK constraint
- 18 resolve tests, 21 enrich tests, 15 classify tests, 9 summarize tests, 12 expand tests, 11 LLM adapter tests
- All 15 test packages green
- LLM Provider interface: Google AI + OpenRouter (supports any model on either platform)

## Full Changelog
- `dbd0807` feat(search): LLM-powered query expansion (#216)
- `abf718d` feat(extract): LLM enrichment + auto-classification + benchmarked defaults (#218, #219)
- `66969ff` fix(extract): tighten rule extractor — kill 88% kv garbage (#227)
- `17412d6` fix(schema): add 'config' to valid fact_type enum
- `9c810bb` fix(enrich): bump MaxTokens 1024→8192 for large file enrichment
- `020a1ff` feat(classify): add --concurrency flag for parallel batch processing
- `9687c96` chore: update DefaultClassifyBatchSize 10→20
- `1402b6e` feat(resolve): LLM-powered conflict auto-resolution (#217)
