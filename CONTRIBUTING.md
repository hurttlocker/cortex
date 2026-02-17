# Contributing to Cortex

Thanks for your interest in Cortex! This project is built by both humans and AI agents working in parallel. Whether you're a person or a machine, these guidelines will help you contribute effectively.

---

## For AI Agents

- **Always** read the relevant PRD in [`docs/prd/`](docs/prd/) before starting work
- Create a feature branch: `feat/<feature-name>` or `fix/<bug-name>`
- **Never** push to `main` directly
- PRs must include: description, what PRD it implements, test coverage
- Keep PRs focused — one feature per PR
- Read [`docs/AGENTS.md`](docs/AGENTS.md) for coordination conventions

## For Humans

- Issues welcome! Feature requests, bug reports, questions — all appreciated
- PRs welcome for any open issue tagged `good-first-issue`
- See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for system overview
- See [`docs/prd/`](docs/prd/) for feature specifications

---

## Getting Started

```bash
git clone https://github.com/hurttlocker/cortex.git
cd cortex
go build ./cmd/cortex/
go test ./...
```

---

## Branch Conventions

| Branch | Purpose |
|--------|---------|
| `main` | Stable — always builds, always passes tests |
| `feat/<name>` | Feature branches |
| `fix/<name>` | Bug fix branches |
| `docs/<name>` | Documentation-only changes |

---

## PR Requirements

Every pull request must:

1. **Build:** `go build ./...`
2. **Pass tests:** `go test ./...`
3. **Include tests:** Add or update tests for any code changes
4. **Reference a PRD or issue:** Link to the relevant spec or bug report
5. **Use the PR template:** Fill out [`.github/PULL_REQUEST_TEMPLATE.md`](.github/PULL_REQUEST_TEMPLATE.md) completely

---

## Code Style

### Go Conventions

- Run `gofmt` and `go vet` before committing
- Follow standard Go conventions — if in doubt, check [Effective Go](https://go.dev/doc/effective_go)

### Error Handling

Always wrap errors with context:

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

### Comments

- Explain **why**, not **what**
- Package comments go in a `doc.go` file or the main package file
- Exported functions need doc comments

### Package Names

- Short, lowercase, no underscores
- Singular (`store`, not `stores`)
- Avoid stuttering (`store.New()`, not `store.NewStore()`)

---

## Commit Messages

Use conventional commit style:

```
<type>: <description>

<optional body>
```

**Types:** `feat`, `fix`, `docs`, `test`, `refactor`, `ci`, `chore`

**Examples:**
```
feat: add markdown importer with provenance tracking
fix: handle empty FTS5 results without panic
docs: add PRD-004 search specification
test: add integration tests for SQLite WAL mode
```

---

## Testing

- Each package has its own `_test.go` files
- Shared test fixtures live in [`tests/testdata/`](tests/testdata/)
- Use in-memory SQLite (`:memory:`) for unit tests
- Use temp files for integration tests
- Aim for **>80% coverage** on core packages (`store`, `search`, `extract`)

```bash
# Run all tests
go test ./...

# Run with coverage
go test ./... -cover

# Run a specific package
go test ./internal/store/ -v
```

---

## Architecture & Specs

Before writing code, read the relevant documentation:

| Doc | Purpose |
|-----|---------|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System design, data model, design principles |
| [`docs/prd/`](docs/prd/) | Feature specifications (7 PRDs) |
| [`docs/AGENTS.md`](docs/AGENTS.md) | Multi-agent coordination |
| [`docs/DECISIONS.md`](docs/DECISIONS.md) | Architecture Decision Records |
| [`docs/NOVEL-IDEAS.md`](docs/NOVEL-IDEAS.md) | Novel features and vision |

---

## Questions?

- Open a [GitHub Issue](https://github.com/hurttlocker/cortex/issues) for questions, bugs, or feature requests
- Check existing issues before creating a new one
- Tag your issue with the appropriate label

We're glad you're here. Let's build something great. 🧠
