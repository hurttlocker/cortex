# Retrieval Tuning Phase 5 — CI Gates + Nightly Quality Artifact

Date: 2026-02-20

## Objective
Lock retrieval quality so regressions cannot merge silently.

## What was added

### 1) Deterministic CI corpus + gate fixture
- Corpus: `tests/fixtures/retrieval/ci-corpus/`
  - `ops.md`, `trading.md`, `profile.md`, `spear.md`, `noisy.md`
- Gate fixture: `tests/fixtures/retrieval/ci-gate.json`
  - 7 deterministic queries
  - includes low-signal + wrapper-noise checks

### 2) Retrieval bench runner enhancements
- `scripts/retrieval_precision_bench.py`
  - supports configurable `--mode` (`keyword|semantic|hybrid|bm25`)
  - optional `--embed`
  - case-insensitive noise marker matching

### 3) New CI gate script
- `scripts/retrieval_ci_gate.py`
  - creates temp DB
  - imports deterministic corpus via `cortex reimport`
  - runs benchmark fixture
  - enforces thresholds and exits non-zero on failure
  - emits JSON report

### 4) New GitHub Actions workflow
- `.github/workflows/retrieval-quality.yml`
  - triggers: PR, push(main), schedule (nightly), manual dispatch
  - builds cortex binary
  - runs deterministic retrieval gate with strict thresholds
  - uploads report artifact (`retention-days: 30`)

## Gate thresholds (current)
- min pass rate: `1.0`
- min avg precision@k: `1.0`
- max total noisy_top3: `3`

## Local validation
Command:
```bash
python3 scripts/retrieval_ci_gate.py \
  --binary /tmp/cortex-ci-gate-bin \
  --fixture tests/fixtures/retrieval/ci-gate.json \
  --corpus tests/fixtures/retrieval/ci-corpus \
  --mode keyword \
  --min-pass-rate 1.0 \
  --min-avg-precision 1.0 \
  --max-total-noisy-top3 3
```

Result:
- pass_rate: `1.0`
- avg_precision_at_k: `1.0`
- total_noisy_top3: `2`
- gate passed ✅

## Outcome
Phase 5 makes retrieval quality *regression-proof* in CI and produces a recurring artifact trail for trend monitoring.
