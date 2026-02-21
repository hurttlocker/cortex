# Reason Quality Full-30 Baseline — 2026-02-21

Branch: `feat/104-retrieval-provenance-closure`
Model: `openrouter/google/gemini-3-flash-preview`
Embed: `ollama/nomic-embed-text`
Fixture: `tests/fixtures/reason/eval-set-v1.json` (30 cases)

## Run method
A single 30-case run was unstable in this runtime (long-running process kill), so execution was completed in 3 deterministic fixture chunks (10+10+10) and merged:

- `/tmp/reason-eval-full/part1.json`
- `/tmp/reason-eval-full/part2.json`
- `/tmp/reason-eval-full/part3.json`
- Combined report: `/tmp/reason-eval-full/full-combined.json`

## Full-suite results (combined)
- Total cases: **30**
- Passed: **9**
- Failed: **21**
- Pass rate: **0.3000**
- Avg overall score: **0.6906**

Dimension averages:
- Actionability: **0.6340**
- Factual grounding: **0.7096**
- Contradiction handling: **0.7347**
- Usefulness: **0.7094**

Eval harness thresholds:
- Case minimum overall: **0.65**
- Suite pass-rate minimum: **0.70**
- Dimension minimums: actionability 0.60 / grounding 0.55 / contradiction 0.50 / usefulness 0.65

Status:
- **Eval suite FAILED** (pass-rate gate)

## Track 2 guardrail gate on full-30
Report: `/tmp/reason-eval-full/full-guardrail.json`

Failed checks:
- pass_rate (0.30 < 0.80)
- overall_score (0.6906 < 0.72)
- actionability (0.634 < 0.65)
- hard_failure_rate (0.70 > 0.10)

## Failure concentration
By preset (failed cases):
- conflict-check: 5
- agent-review: 5
- daily-digest: 4
- weekly-dive: 4
- fact-audit: 3

By hard-failure dimension:
- actionability: 13
- usefulness: 12
- contradiction_handling: 8
- factual_grounding: 4

## Interpretation
The system is stable and grounded, but still underperforms on **actionability/decision-utility consistency** under this strict rubric. This confirms Track 2 is doing its job (not letting weak outputs pass).

## Next patch focus (high leverage)
1. **Prompt contract tightening in reason engine presets**
   - Require fixed output sections: `Evidence`, `Conflicts/Uncertainty`, `Decisions`, `Next Actions (owner + time)`.
2. **Case-level signal tuning for conflict/agent-review prompts**
   - Reduce lexical brittleness and add broader accepted action/decision synonyms.
3. **Re-run full 30-case baseline and require Track 2 pass**
   - Goal: pass_rate >= 0.80, overall >= 0.72, hard_failure_rate <= 0.10.
