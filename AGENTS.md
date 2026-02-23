# Repository Guidelines

## Project Structure & Module Organization
- `cmd/cortex/` contains the main CLI entrypoint; `cmd/codex-rollout-report/` holds an auxiliary command.
- `internal/` is the core Go codebase (`store`, `ingest`, `extract`, `search`, `observe`, `connect`, `graph`, etc.).
- `tests/testdata/` and `tests/fixtures/` store shared fixtures (including retrieval/reason/visualizer cases).
- `docs/` contains architecture notes, PRDs, release notes, and operational runbooks.
- `plugin/` contains the OpenClaw TypeScript plugin; `npm/` contains the `@cortex-ai/mcp` package wrapper.
- `scripts/` contains CI, audit, and validation utilities.

## Build, Test, and Development Commands
- `go build ./cmd/cortex/` builds the main CLI binary locally.
- `go test ./... -v` runs the full Go test suite.
- `go test ./... -cover` reports package coverage.
- `go vet ./...` runs static checks expected by CI.
- `python3 scripts/validate_visualizer_contract.py` validates visualizer fixtures/contracts when visualizer data or APIs change.
- `npm --prefix npm test` runs the Node-side smoke test for the MCP package.

## Coding Style & Naming Conventions
- Target Go `1.24+`; run `gofmt -w` before commit.
- Keep package names short, lowercase, and singular (`store`, `search`).
- Prefer descriptive, wrapped errors (for example, `fmt.Errorf("opening %s: %w", path, err)`).
- Follow standard Go test layout: `_test.go`, table-driven tests where appropriate.

## Testing Guidelines
- Add or update tests with every behavior change.
- Prefer in-memory SQLite (`:memory:`) for unit tests; use temp dirs/files for integration tests.
- Keep fixtures deterministic; update `tests/fixtures/visualizer/` when schema/output changes.
- Core packages (`internal/store`, `internal/search`, `internal/extract`) should stay near or above 80% coverage.

## Commit & Pull Request Guidelines
- Use Conventional Commit style seen in history: `feat(scope): ...`, `fix: ...`, `docs: ...`, `test: ...`.
- Keep commits focused and reference related issue/PRD IDs when relevant (example: `feat(graph): ... #186`).
- Complete `.github/PULL_REQUEST_TEMPLATE.md` sections (`What`, `Why`, `How`, `Testing`, checklist).
- PR baseline: `go build ./...`, `go test ./...`, and `go vet ./...` must pass; update docs for behavior changes.

## Security & Configuration Tips
- Use `.env.example` as a template and never commit real secrets or local database artifacts.
- Coordinate major interface or dependency changes through issues/ADRs before implementation.
