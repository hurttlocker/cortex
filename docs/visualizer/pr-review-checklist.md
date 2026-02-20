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

## 6) Dual-target compatibility (Cortex UI + Obsidian graph view)
- [ ] One canonical graph read-model powers both consumers (no duplicated business logic)
- [ ] Stable graph IDs are defined (`node_id`, `edge_id`) and remain deterministic across exports
- [ ] Graph contract includes required fields for both targets (`type`, `label`, `weight/confidence`, `timestamp`, `source_ref`)
- [ ] Server-side bounds are enforced (`max_hops`, `max_nodes`, default radius) before rendering/export
- [ ] Obsidian export adapter is additive only (does not alter canonical Cortex graph semantics)
- [ ] Evidence links resolve from graph node → source context (file/line or canonical reference)

## 7) Graph performance + safety
- [ ] No global full-scale graph render path in default UX
- [ ] Focus-node first flow with explicit neighborhood expansion
- [ ] p95 graph payload + render targets stated and verified
- [ ] Privacy/redaction rules applied before any export target

## Suggested reviewer decision labels
- **APPROVE**: all required gates pass
- **REQUEST_CHANGES**: contract ambiguity, missing fixtures, unbounded graph behavior, or unproven reliability paths
