## What

Brief description of changes.

## Why

What problem does this solve? Reference PRD or issue number.

**PRD:** docs/prd/XXX-feature.md  
**Issue:** #XX (if applicable)

## How

Technical approach taken. Key implementation decisions.

## Testing

- [ ] Unit tests added/updated
- [ ] Integration tests added/updated (if applicable)
- [ ] `go test ./...` passes
- [ ] `go build ./...` passes

## Checklist

- [ ] Code follows Go conventions (`gofmt`, `go vet`)
- [ ] No unrelated changes included
- [ ] Documentation updated (if applicable)
- [ ] Error messages are clear and actionable

## Visualizer Gate Checklist (required for #99-#104 scope)

If this PR touches Visualizer v1 contracts/UI/read models (`#99-#104`), complete all items:

### Contract integrity
- [ ] Contract invariants documented (units, enums, null semantics, timestamp format)
- [ ] `schema_version` and compatibility impact called out
- [ ] Pagination/sorting behavior is deterministic

### Evidence + fixtures
- [ ] Golden fixture(s) added/updated under `tests/fixtures/visualizer/` (or noted why N/A)
- [ ] Evidence links included (artifact/log/run) for behavior claims

### Reliability + UX safety
- [ ] `NO_DATA` / empty state behavior explicitly handled
- [ ] p95 target impact noted (or explicitly N/A)
- [ ] Existing CLI workflows verified as non-regressed
