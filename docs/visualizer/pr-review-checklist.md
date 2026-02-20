# Visualizer v1 PR Review Checklist

Applies to work in epic #99 and child issues #100-#104.

## Purpose
Use this checklist to enforce decision-speed quality without contract churn.

## 1) Contract integrity
- [ ] Contract envelope present (`schema_version`, `generated_at`, `window`, `data`)
- [ ] Units are explicit (`ms`, `usd`, ratio scale definition)
- [ ] Enum domain defined with fallback (`PASS|WARN|FAIL|NO_DATA` or module equivalent)
- [ ] Nullability semantics are documented (`missing`, `not_applicable`, `not_collected`)
- [ ] Timestamp format is RFC3339 UTC
- [ ] Sorting + pagination are deterministic

## 2) Backward compatibility
- [ ] Schema evolution impact called out (non-breaking vs breaking)
- [ ] Existing consumers remain compatible or migration path documented
- [ ] Breaking changes include explicit version bump rationale

## 3) Evidence and fixtures
- [ ] Golden fixture(s) added or updated under `tests/fixtures/visualizer/`
- [ ] Fixture diff reviewed for intentional semantic changes
- [ ] PR includes links to evidence artifacts/logs/runs for key claims

## 4) Reliability and UX safety
- [ ] Empty and missing-data states are handled and demonstrated
- [ ] p95 impact is stated (or `N/A` with reason)
- [ ] Failure and partial data paths are tested

## 5) Non-regression
- [ ] Existing CLI workflows remain unchanged (or change is explicitly documented)
- [ ] No shadow state introduced in UI layer
- [ ] Source-of-truth producer map remains accurate

## Suggested reviewer decision labels
- **APPROVE**: all required gates pass
- **REQUEST_CHANGES**: contract ambiguity, missing fixtures, or unproven reliability paths
