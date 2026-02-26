# Stability Contract — v1.0

Cortex v1.0 is a stability promise. This document defines what that means.

## Will NOT Break in 1.x

These are stable. We won't remove them or change their behavior in any 1.x release.

- **CLI commands**: `import`, `search`, `stats`, `stale`, `conflicts`, `reinforce`, `cleanup`, `optimize`, `doctor`, `mcp`, `connect`, `graph`, `version`, `help`
- **Core flags**: `--recursive`, `--extract`, `--mode`, `--limit`, `--agent`, `--json`, `--days`
- **SQLite schema**: Migrations only, never destructive. Your `cortex.db` from v1.0 will work with v1.9.
- **MCP tool names and parameters**: All 17 tools keep their names and required parameters
- **Config file format**: `~/.cortex/config.yaml` structure is stable
- **Default database path**: `~/.cortex/cortex.db`
- **Search modes**: `bm25`, `semantic`, `hybrid`, `rrf`
- **Fact types**: `kv`, `relationship`, `preference`, `temporal`, `identity`, `location`, `decision`, `state`, `config`
- **Exit codes**: 0 = success, 1 = error

## May Change in 1.x (With Deprecation Warnings)

These may evolve. Changes will include deprecation warnings for at least one minor version before removal.

- MCP resource URIs (`cortex://stats`, etc.)
- Graph API response shapes
- Default search tuning parameters (score thresholds, boost weights)
- `cortex reason` preset names and behavior
- `cortex bench` output format
- Connector config JSON shapes (per-provider)

## No Guarantees

- Internal Go package APIs — Cortex is a CLI tool, not a library. Don't import `internal/`.
- Performance characteristics — they may improve.
- LLM provider defaults — models change, defaults will track the best option.
- Exact fact extraction output — rules and LLM behavior evolve to improve quality.
- `docs/` content — documentation is living and will be updated.

## Versioning

Cortex follows [Semantic Versioning](https://semver.org/):
- **MAJOR** (2.0): Breaking changes to stable interfaces
- **MINOR** (1.1, 1.2): New features, non-breaking
- **PATCH** (1.0.1): Bug fixes only

## Reporting Breaking Changes

If you believe a 1.x release broke a stable interface, file an issue with the `breaking` label. We treat these as P0 bugs.
