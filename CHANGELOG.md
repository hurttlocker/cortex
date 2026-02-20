# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

- No unreleased entries yet.

## [0.3.5-rc2] - 2026-02-20

### Fixed
- Release checklist guard no longer hard-depends on `rg`; now uses `grep` fallback when `rg` is unavailable (fixes release-runner portability).

### Added
- v0.3.5-rc2 audit handoff docs (`go-no-go`, auditor command pack, release notes).

## [0.3.5-rc1] - 2026-02-20

### Added
- Deterministic runtime connectivity smoke gate: `scripts/connectivity_smoke.sh`.
- One-command external audit preflight pack generator: `scripts/audit_preflight.sh --tag ...`.
- Adversarial audit sanity harness: `scripts/audit_break_harness.sh`.
- v0.3.5-rc1 audit handoff docs (`go-no-go`, auditor command pack, release notes).

### Changed
- CI now executes runtime connectivity smoke on PR/push.
- Release checklist now requires connectivity smoke before publish checks pass.
- RC smoke now includes runtime connectivity validation.
- Visualizer closure includes retrieval-debug deltas/reasons across bm25+semantic+hybrid and bounded provenance contract enforcement.

### Fixed
- Importer now rejects symlinked directory recursion paths (prevents symlink-loop stack overflow crashes).
- Recursive directory imports now surface unreadable walk paths as explicit import errors (no silent partial-success).
- `codex-rollout-report --warn-only=false` now fails when zero valid telemetry runs are parsed.
- `CORTEX_DB=~/...` and `--db ~/...` now expand `~` to user home before DB open.
- `cortex search --limit` now validates bounds (`1..1000`) instead of silently coercing invalid values.
- `visualizer_export.py` now rejects output paths outside workspace root by default (prevents traversal-style `../` writes unless explicitly overridden).

## [0.3.4] - 2026-02-20

### Added
- Stable release promotion from audited RC path (`v0.3.4-rc1..a62f11e`).
- Release go/no-go handoff docs and artifact revalidation notes for auditor continuity.

### Changed
- Search hardening for legacy `NULL memory_class` rows with startup backfill safeguards.
- Conflict output defaults tuned for readability (compact preview + higher-similarity prioritization).

### Validation
- Delta audit result: **GO_WITH_CONDITIONS** (no Critical/High findings in audited scope).
- Stable artifacts published for darwin/linux/windows with checksums.

## [0.3.4-rc1] - 2026-02-20

### Added
- Runtime CLI command: `cortex codex-rollout-report`.
- Shared rollout report package: `internal/codexrollout` (used by runtime command + helper binary).
- Guardrail controls for rollout report:
  - `--one-shot-p95-warn-ms`
  - `--recursive-known-cost-min-share`
  - `--warn-only` (strict mode exits non-zero on guardrail warnings)
- Runtime/help behavior hardening so `--help` exits `0` on rollout report paths.
- New tests for rollout report parsing, guardrail behavior, and strict-mode exits.

### Changed
- `scripts/codex_rollout_report.sh` now routes through runtime CLI while keeping backward-compatible positional telemetry file handling.
- Updated `README.md` usage examples for warn-only vs strict rollout gating.

### Fixed
- Audit guide fixture for concurrent import test (`tests/AUDIT.md`, test 9e) now uses substantive content to avoid low-signal hygiene false negatives.
