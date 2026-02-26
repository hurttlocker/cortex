# Reason Quality Evaluation Pack (First Pass)

Date: 2026-02-21  
Branch: `feat/104-retrieval-provenance-closure`  
Scope: close final quality gap toward issue #31 outcomes with a repeatable, low-friction evaluation harness.

## Objective

Add a practical quality gate for `cortex reason` that is:

- **Fast enough for CI/nightly** (scriptable, JSON output, thresholded exit code)
- **Focused on answer quality** (not just latency/cost)
- **Grounded in issue #31 goals** (reasoned synthesis from memory with explicit uncertainty handling)

This pack ships:

1. `tests/fixtures/reason/eval-set-v1.json` — 30 realistic prompts + expected quality signals.
2. `scripts/reason_quality_eval.py` — executes prompts via `cortex reason --json`, scores responses, emits pass/fail + summary metrics.
3. README usage snippet for CI/nightly execution.

---

## Scoring rubric (0-3 per dimension)

Each response is scored on four dimensions, then combined into a weighted overall score.

### 1) Actionability

**Question:** Does the answer produce concrete next moves?

| Score | Description |
|---|---|
| 0 | No actionable guidance; generic narrative only |
| 1 | Some implied action, but missing prioritization/ownership |
| 2 | Clear actions with prioritization OR sequencing |
| 3 | Specific, prioritized actions with owner/timeline-style cues |

**Signals used (heuristic):** next-step verbs, priority language, owner/timeline markers, structured bullets.

---

### 2) Factual grounding / citation behavior

**Question:** Does the answer ground claims in memory-derived evidence?

| Score | Description |
|---|---|
| 0 | Unsupported assertions; no evidence language |
| 1 | Sparse grounding cues (mentions “source/fact” without clear linkage) |
| 2 | Repeated evidence/provenance cues; confidence-aware framing appears |
| 3 | Strong grounding, explicit evidence/citation-style references, calibrated confidence |

**Signals used (heuristic):** source/fact/memory/provenance terms, confidence tags (`[0.xx]`), file/line-like references, uncertainty calibration language.

---

### 3) Contradiction handling

**Question:** When conflicts/uncertainty exist, does the answer identify and resolve them responsibly?

| Score | Description |
|---|---|
| 0 | Ignores obvious conflict or uncertainty |
| 1 | Mentions conflict but gives no reconciliation path |
| 2 | Identifies conflict + partial reconciliation/verification plan |
| 3 | Explicit conflict framing + concrete reconcile/verify/supersede path |

**Signals used (heuristic):** conflict/inconsistency markers, “however/trade-off” language, and explicit reconcile/verify actions.

> Note: For prompts where contradiction handling is not required, the script marks this dimension as not-required and does not hard-fail the case on it.

---

### 4) Usefulness

**Question:** Is the answer decision-useful (not just plausible)?

| Score | Description |
|---|---|
| 0 | Too vague/short to be useful |
| 1 | Partially useful but lacks structure or specifics |
| 2 | Useful, structured response with practical context |
| 3 | High decision utility: structured, specific, and risk/impact aware |

**Signals used (heuristic):** minimum word count, sections/bullets, specificity (numbers/thresholds), and presence of summary-impact-risk/recommendation language.

---

## Aggregation + pass criteria

Default weights (v1):

- Actionability: **0.30**
- Factual grounding: **0.30**
- Contradiction handling: **0.15**
- Usefulness: **0.25**

Default thresholds (fixture-controlled):

- **Per-case overall minimum:** `0.65`
- **Suite minimum pass rate:** `0.70`
- **Suite dimension minimum averages:**
  - actionability `>= 0.60`
  - factual_grounding `>= 0.55`
  - contradiction_handling `>= 0.50`
  - usefulness `>= 0.65`

A case fails if:

- overall score is below case threshold, or
- any **required** dimension is below its dimension minimum.

---

## Fixture design notes (v1)

- 30 prompts across built-in reason presets (`daily-digest`, `fact-audit`, `conflict-check`, `weekly-dive`, `agent-review`)
- Each case defines expected signals (keywords, minimum signal hits, min word count, optional per-dimension min score)
- Mixes concise and deep-analysis tasks to cover one-shot and recursive behavior

This is intentionally a **first-pass heuristic harness** (not a ground-truth semantic judge). It is meant to catch quality drift early and provide trendable signals for iterative tuning.

---

## How to run

```bash
python3 scripts/reason_quality_eval.py \
  --binary ./cortex \
  --fixture tests/fixtures/reason/eval-set-v1.json \
  --model google/gemini-3-flash-preview \
  --embed ollama/nomic-embed-text \
  --output /tmp/reason-quality-report.json
```

Exit code is non-zero when suite thresholds fail (CI-friendly).

For nightly/local models, swap `--model` to an Ollama model (e.g., `phi4-mini`) and keep the same fixture.

---

## Track 2 — Reliability guardrails (post-eval gate)

Use a stricter guardrail gate on top of the eval report:

```bash
python3 scripts/reason_guardrail_gate.py \
  --report /tmp/reason-quality-report.json \
  --min-pass-rate 0.80 \
  --min-overall 0.72 \
  --min-grounding 0.62 \
  --min-actionability 0.65 \
  --min-usefulness 0.68 \
  --max-error-rate 0.05 \
  --max-empty-content-rate 0.02 \
  --max-hard-failure-rate 0.10
```

This catches failure modes that can slip past average pass-rate checks:
- too many empty/blank model responses
- too many hard per-dimension failures
- grounding/actionability quality drift

---

## Track 3 — Outcome loop (product KPI layer)

Log human outcome feedback as JSONL and roll up weekly KPI metrics:

- Template: `tests/fixtures/reason/outcomes-template.jsonl`
- Required fields: `prompt_id`, `accepted_without_edits`, `action_taken`

```bash
python3 scripts/reason_outcome_rollup.py \
  --input tests/fixtures/reason/outcomes-template.jsonl \
  --min-samples 20 \
  --min-accept-rate 0.70 \
  --min-action-rate 0.55 \
  --min-useful-rate 0.65
```

Primary KPI targets toward Issue #31 closure:
1. **Accepted-without-edits rate**
2. **Action-taken rate**
3. Useful-rate (optional but recommended)
