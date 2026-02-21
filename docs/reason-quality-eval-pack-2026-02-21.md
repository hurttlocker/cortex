# Reason Quality Eval Pack (v1)

Date: 2026-02-21  
Branch target: `feat/104-retrieval-provenance-closure`

## Purpose

Close the final quality gap after retrieval improvements with a repeatable, deterministic evaluation harness for `cortex reason`.

This pack ships:

1. `tests/fixtures/reason/eval-set-v1.json` (40 prompts)
2. `scripts/reason_quality_eval.py`
3. Optional nightly workflow for trend artifacts

---

## Fixture schema (v1)

Path: `tests/fixtures/reason/eval-set-v1.json`

- **40 prompts** across:
  - `daily-digest` (8)
  - `weekly-dive` (8)
  - `fact-audit` (8)
  - `conflict-check` (8)
  - `agent-review` (8)
- Each prompt contains `expected_signals` for all four metrics (signal-based, not strict exact text).

Top-level fields:

- `thresholds.overall_pass_score`
- `thresholds.pass_rate_min`
- `thresholds.metric_minimums`
- `weights`
- `defaults.reason_args`
- `prompts[]`

---

## Scoring rubric

All scoring is deterministic and heuristic-based (no secondary model grader).

### 1) `grounding_score`

Checks two things:

- **Evidence presence**: grounding terms, source/citation-like references, and non-zero memory/fact usage
- **Relevance**: lexical overlap between prompt keywords and response content

### 2) `actionability_score`

Checks whether answer provides clear next moves:

- action-oriented signal hits (next step / owner / timeline / priority)
- structured checklist/bullets
- ownership or time cues

### 3) `contradiction_handling_score`

When required by prompt (or inferred from prompt wording), checks for:

- explicit conflict/uncertainty recognition
- resolve/verify/reconcile language

If not required, this metric is marked non-blocking for that prompt.

### 4) `concise_clarity_score`

Checks response quality for operator readability:

- target length window (`min_words`/`max_words`)
- structure (headings + bullets)
- sentence-length sanity
- concise/summary signal terms

---

## Pass/fail thresholds

Defaults (fixture-controlled):

- **Overall prompt pass floor:** `0.70`
- **Suite pass rate floor:** `0.75`
- **Metric minimums:**
  - grounding: `0.60`
  - actionability: `0.62`
  - contradiction handling: `0.58`
  - concise clarity: `0.65`

A prompt fails if:

- overall score below floor, or
- any required metric is below minimum.

A suite fails if:

- pass rate is below threshold, or
- average overall score is below threshold, or
- any metric average is below minimum, or
- `--fail-on-errors` and any prompt command errors.

---

## How to run

```bash
python3 scripts/reason_quality_eval.py \
  --binary ./cortex \
  --fixture tests/fixtures/reason/eval-set-v1.json \
  --output-dir tests/output/reason-eval \
  --model openrouter/google/gemini-3-flash-preview \
  --timeout-sec 300
```

Useful flags:

- `--max-prompts 10` (quick smoke run)
- `--preset weekly-dive` (force one preset)
- `--project <name>`
- `--embed ollama/nomic-embed-text`
- `--print-json`
- `--fail-on-errors`

Outputs:

- JSON report: `tests/output/reason-eval/reason-quality-eval-<timestamp>.json`
- Markdown report: `tests/output/reason-eval/reason-quality-eval-<timestamp>.md`

---

## Baseline results template

Use this section to capture first accepted baseline:

```md
## Baseline Run — YYYY-MM-DD HH:MM UTC

- Command:
  - `python3 scripts/reason_quality_eval.py ...`
- Fixture version:
  - `reason-quality-eval-pack-v1`
- Model:
  - `<provider/model>`
- Prompt count:
  - `40`

### Summary
- Suite passed: `<true|false>`
- Pass rate: `<x.xx>`
- Average overall score: `<x.xx>`

### Metric averages
- grounding_score: `<x.xx>`
- actionability_score: `<x.xx>`
- contradiction_handling_score: `<x.xx>`
- concise_clarity_score: `<x.xx>`

### Notes
- `<major misses / regressions>`
- `<follow-up actions>`

### Artifacts
- JSON: `<path or artifact link>`
- Markdown: `<path or artifact link>`
```

---

## Optional CI/nightly trend tracking

Workflow file: `.github/workflows/reason-quality-eval-nightly.yml`

Behavior:

- Runs on nightly schedule + manual dispatch
- Builds `cortex`
- Executes eval pack (or dry-run when model key is unavailable)
- Uploads JSON/Markdown artifacts for trend history
