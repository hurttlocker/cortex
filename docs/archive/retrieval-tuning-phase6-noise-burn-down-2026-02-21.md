# Retrieval Tuning Phase 6 — Residual Noise Debt Burn-Down

Date: 2026-02-21
Branch: `feat/104-retrieval-provenance-closure`

## Objective
Reduce remaining transcript/envelope KV noise in production while preserving retrieval quality and guardrails.

## Scope shipped

### 1) Extractor hardening for transcript-like captures
Files:
- `internal/extract/extract.go`
- `internal/extract/extract_test.go`

What changed:
- Added transcript-like content detection for non-auto-capture memories when content includes:
  - `<cortex-memories>`
  - `(untrusted metadata)` blocks
  - queued-message envelope markers
  - repeated role-prefixed lines (`assistant:`, `user:`, `system:`)
- In transcript-like content, KV extraction now suppresses metadata-envelope predicates and role-line KV noise.
- Preserved non-transcript behavior (ordinary non-auto-capture `Key: Value` still extracts).

Validation:
- New test: `TestExtractKeyValues_TranscriptLikeNonAutoSkipsEnvelopeNoise`
- Full suite: `go test ./...` ✅

### 2) Safe staged reprocess tool for transcript-noise candidates
File:
- `scripts/transcript_noise/main.go`

Tool behavior:
- Finds candidate memories with noisy transcript/envelope predicates.
- Supports dry-run metrics + write mode with backup.
- Re-extracts selected memories with the hardened extractor.
- Emits before/after report JSON.

## Production run (safe)

Dry-run command:
```bash
go run ./scripts/transcript_noise \
  --db ~/.cortex/cortex.db \
  --dry-run \
  --limit 500 \
  --offset 0 \
  --report /tmp/cortex-transcript-noise-dryrun-20260221.json
```

Write command:
```bash
go run ./scripts/transcript_noise \
  --db ~/.cortex/cortex.db \
  --limit 500 \
  --offset 0 \
  --backup ~/.cortex/backups/cortex-pre-phase6-20260221-001155.db \
  --report /tmp/cortex-transcript-noise-write-20260221.json
```

## Results

### Candidate selection
- Selected memories: **130**
- Selected noisy KV facts: **70,559**

### Global (production DB)
- Facts total: **1,419,790 → 1,272,075**
- KV facts: **1,341,571 → 1,207,005**
- Noisy KV facts: **76,434 → 5,876**
- Noisy KV ratio: **5.70% → 0.49%**

### Selected subset (130 memories)
- Facts total: **148,224 → 509**
- KV facts: **134,933 → 367**
- Noisy KV facts: **70,559 → 1**
- Noisy KV ratio: **52.29% → 0.27%**

### Safety / reliability
- Processed: **130**
- Failed: **0**
- Backup created: `~/.cortex/backups/cortex-pre-phase6-20260221-001155.db`

## Regression checks after cleanup

### Deterministic CI gate
- Command: `python3 scripts/retrieval_ci_gate.py ...`
- Result: **pass_rate=1.0**, **avg_precision@k=1.0**, **total_noisy_top3=2** ✅

### 25-query precision fixture (live DB)
- Fixture: `tests/fixtures/retrieval/phase3-precision-25.json`
- Result: **25/25 pass**, avg precision@k **0.744** (no regression) ✅

## Outcome
Phase 6 removed the remaining high-volume transcript/envelope KV pollution from production facts while preserving benchmarked retrieval quality.
