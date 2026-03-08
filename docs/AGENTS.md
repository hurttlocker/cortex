# Multi-Agent Coordination

> **Active contract:** The repo-root `AGENTS.md` is the current operating contract for all agent work in Cortex. If this file conflicts with the root `AGENTS.md`, the root file wins.

This repo is developed by multiple AI agents working in parallel. This document defines coordination conventions to prevent conflicts, maintain quality, and keep development velocity high.

---

## Package Ownership

| Package | Owner | Status | Notes |
|---------|-------|--------|-------|
| `internal/store/` | Unassigned | 🔴 Not started | Storage layer — **foundation, build first** |
| `internal/ingest/` | Unassigned | 🔴 Not started | Import engine — depends on store |
| `internal/extract/` | Unassigned | 🔴 Not started | Extraction pipeline — depends on ingest |
| `internal/search/` | Unassigned | 🔴 Not started | Search — depends on store |
| `internal/observe/` | Unassigned | 🔴 Not started | Observability — depends on store + search |
| `cmd/cortex/` | Unassigned | 🟡 Scaffold only | CLI — depends on all internal packages |
| `docs/` | Any agent | 🟢 Active | PRDs, architecture, decisions |

**To claim a package:** Open a PR that adds your agent name to this table. First come, first served.

---

## Dependency Order

Build in this order to avoid blocking:

```
1. internal/store/     ← no dependencies (build FIRST)
2. internal/search/    ← depends on store
3. internal/ingest/    ← depends on store
4. internal/extract/   ← depends on ingest
5. internal/observe/   ← depends on store + search
6. cmd/cortex/         ← depends on everything
```

---

## Conventions

### Before Starting Work

1. **Read the relevant PRD** in `docs/prd/` — it contains requirements, interfaces, and test strategy
2. **Check for open PRs** on the same feature (avoid conflicts)
3. **Create a feature branch** from `main`: `feat/<feature-name>`
4. **If the PRD is unclear**, open a GitHub issue asking for clarification — don't guess

### During Work

- **Commit frequently** with descriptive messages
- **Don't modify files outside your assigned package** without coordination
- **If you need an interface change** in another package, open an issue first
- **Write tests alongside code** (not after) — see test strategy in the PRD
- **Follow Go conventions** — `gofmt`, `go vet`, error wrapping

### Submitting Work

1. Open a PR against `main`
2. Fill out the PR template completely (`.github/PULL_REQUEST_TEMPLATE.md`)
3. Reference the PRD number in the PR description
4. Wait for CI to pass
5. Request review — **Q merges all PRs**

### Communication

- **GitHub Issues** for async questions and feature requests
- **PR comments** for code-specific discussion
- **Don't modify `docs/DECISIONS.md`** without discussion — ADRs are project-level decisions
- **`docs/prd/` files are READ-ONLY for agents** — Q modifies PRDs

---

## File Locking

No formal file locking. Instead:

- **Check `git log <file>`** before modifying shared files
- **Interfaces in `internal/store/store.go`** are the API contract — changing them requires an ADR
- **`docs/prd/` files are READ-ONLY** for agents (Q modifies PRDs)
- **`go.mod` and `go.sum`** — coordinate if adding dependencies (open an issue first)

---

## Shared Interfaces

The store package defines the core interfaces. All other packages depend on these:

```go
// These interfaces are the API contract.
// Changing them requires an ADR (Architecture Decision Record).
// Do NOT modify without coordination.

type Importer interface {
    CanHandle(path string) bool
    Import(ctx context.Context, path string) ([]RawMemory, error)
}

type Searcher interface {
    Search(ctx context.Context, query string, opts SearchOpts) ([]Result, error)
}

type Store interface {
    Create(ctx context.Context, memory Memory) (int64, error)
    Read(ctx context.Context, id int64) (*Memory, error)
    Update(ctx context.Context, memory Memory) error
    Delete(ctx context.Context, id int64) error
    Search(ctx context.Context, query string) ([]Memory, error)
}
```

---

## Testing

- Each package has its own `_test.go` files
- `tests/testdata/` contains shared test fixtures:
  - `sample-memory.md` — sample MEMORY.md for import testing
  - `sample-data.json` — sample JSON data for import testing
- Run full suite: `go test ./...`
- **Aim for >80% coverage** on core packages (`store`, `search`, `extract`)
- Use in-memory SQLite (`:memory:`) for unit tests
- Use temp files for integration tests

---

## CI/CD

- Every PR triggers CI (`.github/workflows/ci.yml`)
- CI runs: `go build`, `go test`, `go vet`
- **All checks must pass** before merge
- **Q merges all PRs** — do not merge your own

---

## Quick Reference

| Action | How |
|--------|-----|
| Start work on a feature | Read PRD → branch from `main` → claim in this file |
| Need a new dependency | Open an issue, discuss, then add to `go.mod` |
| Need an interface change | Open an issue with proposed change + rationale |
| Found a bug in another package | Open an issue, tag the package owner |
| PRD is unclear | Open an issue asking for clarification |
| Finished a feature | Open PR → fill template → wait for CI → Q merges |
