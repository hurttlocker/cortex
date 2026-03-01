# Contributing to Cortex

Thanks for helping improve Cortex. This guide is for both human contributors and AI-assisted workflows.

## Development setup

### Prerequisites

- Go **1.22+**
- Git
- SQLite (bundled via Go driver; no separate DB server needed)

### Clone + build

```bash
git clone https://github.com/hurttlocker/cortex.git
cd cortex
go build ./cmd/cortex/
```

### Run tests locally

```bash
# Full suite
go test ./...

# With coverage
go test ./... -cover

# Target package
go test ./internal/store -v
```

> If you prefer Make-style aliases, map these commands in your local tooling (`build -> go build ./cmd/cortex/`, `test -> go test ./...`).

---

## Code style and quality bar

- Run `gofmt` on all edited Go files
- Keep changes lint-clean with `go vet ./...`
- Wrap errors with context (`fmt.Errorf("...: %w", err)`)
- Add/update tests for every behavioral change
- Keep PRs focused (one issue/feature per PR)

### Error handling example

```go
// ✅ Good
if err != nil {
    return fmt.Errorf("opening database at %s: %w", path, err)
}

// ❌ Bad
if err != nil {
    return err
}
```

---

## Pull request process

1. Fork repo
2. Create a feature branch from `main`
3. Implement + test
4. Open PR to `main` with issue link and summary

Recommended branch naming:

- `feat/<short-name>`
- `fix/<short-name>`
- `docs/<short-name>`
- `xmate/<issue-or-scope>` (used by agent lanes)

### PR checklist

- [ ] `go test ./...` passes
- [ ] Relevant tests added/updated
- [ ] Behavior changes documented (README/docs/changelog as needed)
- [ ] Issue referenced in PR body (e.g., `Closes #279`)

---

## Adding a new connector

1. Implement provider in `internal/connect/<provider>.go`
2. Satisfy connector interface methods (`Validate`, `Sync`, `Name`, config schema)
3. Register provider in connector registry/dispatch
4. Add tests under `internal/connect/*_test.go`
5. Document provider config in `docs/connectors.md`

Use existing providers (GitHub/Gmail/Discord/Obsidian) as templates.

---

## Issue labels (quick reference)

- `bug` — incorrect behavior / regression
- `enhancement` — incremental improvement
- `feature` — new capability
- `docs` — documentation-only changes
- `good-first-issue` — beginner-friendly tasks
- `breaking-change` — requires migration or behavior change awareness

If unsure, open the issue without a label and maintainers will triage.

---

## AI-assisted contributions

AI-assisted PRs are welcome. Please include:

- What was generated vs. reviewed manually
- Test evidence (`go test ./...` output)
- Any follow-up risk notes

Cortex is production-used, so review quality matters more than speed.

---

## Helpful docs

- `README.md` — install + quickstart
- `docs/ARCHITECTURE.md` — system design
- `docs/connectors.md` — provider setup
- `docs/prd/` — product/feature specs
- `docs/DECISIONS.md` — architecture decision records

Thanks for contributing 🧠
