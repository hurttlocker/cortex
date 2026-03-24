# Import Quality Student Baseline — 2026-03-24

## Goal

Test whether the first tiny import keep/drop model is viable enough to wire into Cortex behind an experimental flag.

## Artifacts

- dataset builder:
  `/Users/marquisehurtt/clawd/repos/cortex/scripts/bench/build_import_quality_dataset.py`
- student trainer:
  `/Users/marquisehurtt/clawd/repos/cortex/scripts/bench/import_quality_student.py`
- teacher helper:
  `/Users/marquisehurtt/clawd/repos/cortex/scripts/bench/import_quality_teacher.py`
- exported runtime model:
  `/Users/marquisehurtt/clawd/repos/cortex/internal/ingest/import_keepdrop_model.json`

Temporary run artifacts:

- dataset: `/tmp/import_quality_dataset.jsonl`
- review pack: `/tmp/import_quality_review.jsonl`
- report: `/tmp/import_quality_report.json`
- student report: `/tmp/import_quality_student_report.json`
- student review pack: `/tmp/import_quality_student_review.jsonl`
- exported model source: `/tmp/import_quality_keepdrop_model.json`

## Dataset

The first useful dataset came from the live `~/.cortex/cortex.db` and used balanced proxy sampling.

- samples: `400`
- proxy kept: `200`
- proxy dropped: `200`

Common positive signals:

- path/doc shape
- source sections
- healthy content length
- line count

Common negative signals:

- protocol noise
- short chunks with no facts and no access
- high punctuation ratio
- scratch memory class

## Student Result

Training/eval setup:

- TF-IDF
- logistic regression
- stratified `80/20` holdout
- balanced class weight
- `max_features=4000`
- `min_df=2`

Result:

- F1: `0.6914`
- precision: `0.6829`
- recall: `0.7000`

This is not yet “ship default-on” quality.
But it is good enough for an experimental import flag.

Teacher-seeded update:

- added a small curated override set from the review pack
- switched the trainer to runtime-aligned metadata tokens
- retrained holdout result:
  - F1: `0.9091`
  - precision: `0.8333`
  - recall: `1.0000`

This cleared the original `>0.85` target on the teacher-seeded holdout.

## Product Hook

The runtime gate is now wired behind:

```bash
cortex import <path> --import-quality-gate
```

Integration points:

- `internal/ingest/import_keepdrop_gate.go`
- `internal/ingest/ingest.go`
- `cmd/cortex/main.go`

## Dry-Run Behavior

### Negative example

Input:

```text
heartbeat ok
session started
connected
status ok
```

Dry run:

- denied: yes
- score: `0.2107`

### Positive example

Input:

```text
## Decision
We should keep SQLite + provenance as the default memory substrate for Cortex because it preserves exportability and local-first debugging.
```

Dry run:

- denied: no
- imported as new memory in dry-run output

## Threshold Policy

The raw exported threshold from the first trainer run was slightly too aggressive for the positive dry-run example.

For the experimental runtime model, the threshold was lowered to:

- `0.45`

That keeps obvious protocol noise out while allowing a durable design-note chunk through.

This is a product-policy choice favoring recall over over-pruning.

## Recommendation

Next step:

1. expand teacher labels specifically around short ops / gateway knowledge chunks
2. rerun the fixture eval
3. only move to LoCoMo gate-on benchmarks after the retrieval fixture is non-regressing

## Current blocker

The gate still regresses the retrieval fixture corpus:

- baseline pass rate: `1.00`
- gated pass rate: `0.8571`

Specific miss:

- `OpenClaw gateway restart command`

Interpretation:

- the model still over-drops short operational knowledge despite the stronger holdout score
- this is a real product regression and should block shipping for now
