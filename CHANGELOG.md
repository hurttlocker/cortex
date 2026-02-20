# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- Deterministic runtime connectivity smoke gate: `scripts/connectivity_smoke.sh` (import → extract → stats → search → list facts → optimize integrity).

### Changed
- CI now runs the connectivity smoke gate on every PR/push.
- `scripts/release_checklist.sh` now requires the same connectivity smoke gate before release publish checks pass.

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
