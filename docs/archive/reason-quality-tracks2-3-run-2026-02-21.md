# Reason Quality — Tracks 2 & 3 Activation Run

Date: 2026-02-21
Branch: `feat/104-retrieval-provenance-closure`

## Goal
Activate the remaining two tracks after the base reason-eval pack:

- **Track 2:** reliability guardrails
- **Track 3:** outcome-loop KPI rollups

## Shipped components

### Track 2 (Guardrails)
- Script: `scripts/reason_guardrail_gate.py`
- Input: JSON report from `scripts/reason_quality_eval.py`
- Output: pass/fail gate JSON with failed-check details

Checks:
- pass rate
- overall score
- grounding/actionability/usefulness floors
- error-rate ceiling
- empty-content-rate ceiling
- hard-failure-rate ceiling

### Track 3 (Outcome loop)
- Script: `scripts/reason_outcome_rollup.py`
- Input: JSONL operator feedback log
- Output: KPI summary + threshold checks

KPI focus:
1. accepted-without-edits rate
2. action-taken rate
3. useful-rate (optional)

Added template:
- `tests/fixtures/reason/outcomes-template.jsonl`

## Validation runs

### 1) Reason quality system working (live one-case smoke)
```bash
python3 scripts/reason_quality_eval.py \
  --binary ./cortex \
  --fixture tests/fixtures/reason/eval-set-v1.json \
  --max-prompts 1 \
  --model openrouter/google/gemini-3-flash-preview \
  --output /tmp/reason-eval-one.json
```

Result:
- suite passed: `true`
- pass_rate: `1.0`
- average_overall_score: `0.7834`

### 2) Track 2 guardrail gate
```bash
python3 scripts/reason_guardrail_gate.py \
  --report /tmp/reason-eval-one.json \
  --output /tmp/reason-guardrail-one.json
```

Result:
- guardrail passed: `true`

### 3) Track 3 outcome rollup
```bash
python3 scripts/reason_outcome_rollup.py \
  --input tests/fixtures/reason/outcomes-template.jsonl \
  --min-samples 1 \
  --min-accept-rate 0 \
  --min-action-rate 0 \
  --min-useful-rate 0 \
  --output /tmp/reason-outcome-rollup.json
```

Result:
- rollup ran successfully
- sample metrics surfaced:
  - accepted_without_edits_rate: `0.3333`
  - action_taken_rate: `0.6667`
  - useful_rate: `0.6667`

## Important fix applied during activation
The eval harness previously failed when no `--embed` was passed for hybrid/semantic presets.

Fix:
- Added fixture default embed: `ollama/nomic-embed-text`
- Eval runner now applies `defaults.embed` automatically when case/CLI embed is absent.

This removed runtime failure:
- `search failed: semantic search requires an embedder`

## Outcome
Tracks 2 and 3 are now operational and runnable in CI/nightly flows, with artifacts that expose both quality and product-level usefulness KPIs.
