# Changelog

All notable changes to this project will be documented in this file.

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
