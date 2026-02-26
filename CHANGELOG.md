# Changelog

All notable changes to this project will be documented in this file.

## [1.0.0] - 2026-02-26

### 🎉 v1.0 — Production Ready

Cortex is stable. This release represents 6 months of development: 62,300+ lines of Go, 1,081 tests across 15 packages, deployed on 3 machines with thousands of memories in production use.

**What v1.0 means:** CLI commands, MCP tool names, SQLite schema, and config format are now covered by a [stability contract](docs/STABILITY.md). We won't break your workflow in any 1.x release.

### Since v0.9.0

#### Added
- **`cortex doctor`** — Health check command: validates DB, embeddings, connectors, LLM keys. Prints pass/warn/fail summary with remediation hints.
- **Grouped CLI help** — Commands organized by category (Core, Observe, Graph, Connectors, Maintenance). Shell completions for bash, zsh, fish, PowerShell.
- **`--agent` everywhere** — Multi-agent scoping on `classify`, `cleanup`, `graph --serve`, `connect sync`, and `mcp`. `cortex agents` lists all known agent IDs.
- **Search quality benchmark** — 8 golden queries with precision measurement. CI-gated at 60% pass rate.
- **Scale benchmark** — Synthetic data generator + performance tests at 1K/10K memories. Import: 2,894/sec at 10K. BM25 P99: 41.7ms.
- **Stability contract** — `docs/STABILITY.md` defines what v1.0 guarantees.
- **Documentation overhaul** — Complete rewrite of deep dive, architecture, connectors (5→8 providers). New getting-started guide and migration guide.
- **MCP tool descriptions rewritten** — All 17 tools have clear trigger conditions, output shapes, and disambiguation. Example system prompts in `docs/mcp-prompts.md`.
- **Connector auto-sync** — GitHub connector running in production with 3-hour auto-sync via launchd.

#### Fixed
- **Hybrid/RRF search gracefully degrades to BM25** when no embedder configured (was: hard error).
- **Search JSON output strips HTML `<b>` tags** from FTS5 snippets.
- **Empty results show human-friendly messages** instead of bare `[]`.
- **`--days` validates positive values** (was: silently accepted negatives).
- **`--no-enrich` warning reworded** — removed confusing double-negative.
- **Error handling hardened** — `exitWithError()` wraps all 30+ commands. `remediationHint()` covers 12+ error patterns. Fact type validation on save.

#### Changed
- MCP tool count: 19 → 17 (removed deprecated alert tools in v0.6.0, count was stale in docs).
- Help text now lists all 17 MCP tools + 4 resources.

### Full Feature Inventory (v1.0)

| Category | Features |
|----------|----------|
| **Import** | File/directory import, recursive, extension filter, content-hash dedup, chunking |
| **Extraction** | Rule-based NLP, LLM enrichment (optional), auto-classification, 9 fact types |
| **Search** | BM25, semantic, hybrid, RRF. Query expansion. Confidence decay ranking. |
| **Graph** | Knowledge graph, cluster detection, 2D interactive explorer, impact analysis |
| **Connectors** | GitHub, Gmail, Calendar, Drive, Slack, Discord, Telegram, Notion |
| **MCP** | 17 tools, 4 resources, stdio + HTTP/SSE, multi-agent scoping |
| **Observe** | Stats, stale, conflicts, alerts, doctor. Proactive health monitoring. |
| **Reasoning** | One-shot + recursive chain-of-thought over memory corpus |
| **Multi-Agent** | `--agent` flag on all operations, `cortex agents`, scoped MCP |
| **Maintenance** | Cleanup, optimize, embed, doctor. Shell completions. |

### Stats
- **62,300+ lines** of Go
- **1,081 tests** across 15 packages
- **17 MCP tools**, 4 resources
- **8 connectors**
- **9 fact types** with Ebbinghaus decay
- **5 platform binaries** (darwin arm64/amd64, linux arm64/amd64, windows amd64)
- **< $1/month** LLM cost with enrichment enabled

## [0.9.0] - 2026-02-25

### Fixed
- **CRITICAL: Extraction/enrichment re-processed all recent memories on every import** — `runExtractionOnImportedMemories` and `runEnrichmentOnImportedMemories` were calling `ListMemories(limit:1000)` instead of targeting only newly imported memories. This caused duplicate facts to accumulate on every sync (observed: 74K facts from 1.6K memories, expected: ~2.5K). Fixed by tracking `NewMemoryIDs` during import and passing them to extraction/enrichment/classification. (#228)
- **Graceful degradation without API keys** — `cortex import --extract` and `cortex extract` no longer crash when `OPENROUTER_API_KEY` is missing. Instead, they skip LLM enrichment/classification with a one-line notice and continue with rule-only extraction.
- **`reimport` now supports `--no-enrich` and `--no-classify` flags** for offline/low-cost bulk imports.

### Added
- **Query expansion** — `--expand` flag pre-processes vague queries through an LLM (Gemini 2.0 Flash, free) to generate better search terms. LRU cache prevents duplicate calls. Graceful fallback to original query on LLM failure. (#216)
- **LLM enrichment** — `--enrich` (now default with `--extract`) sends rule-extracted facts + source text to Grok 4.1 Fast to find what rules missed: decisions, relationships, implicit connections. Additive-only — never removes rule facts. Tagged as `llm-enrich`. (#218)
- **Auto-classification** — `cortex classify` reclassifies generic `kv` facts into proper types (decision, config, state, temporal, etc.) using DeepSeek V3.2. Batch processing (20/batch), concurrent (5 goroutines), live progress logging. (#219, #227)
- **Classify-on-import** — New kv facts are auto-classified at import time when enrichment is enabled. `--no-classify` to skip. (#227)
- **Conflict auto-resolution** — `cortex conflicts --resolve llm` uses LLM to analyze contradictory facts and recommend supersede/merge/keep actions. Confidence gating (0.7), batch processing, dry-run support. (#217)
- **Fact clustering & summarization** — `cortex summarize` consolidates clusters of related facts into concise summaries using LLM. Reduces fact count while preserving knowledge. (#220)
- **Progress logging** — `cortex classify` now prints batch-by-batch progress to stderr for observability during long sweeps.
- **New CLI flags** — `--no-enrich` (skip LLM enrichment), `--no-classify` (skip auto-classification), `--concurrency N` (parallel LLM batches).

### Changed
- **Enrichment is now default** — `--extract` implies LLM enrichment (Grok 4.1 Fast) and auto-classification (DeepSeek V3.2). Use `--no-enrich` for offline/rule-only mode. (#227)
- **Governor tightened** — DefaultGovernorConfig: MaxFacts 20→10, MinObject 2→3, MinPredicate 4→5. AutoCapture: MaxFacts 15→5, MinObject 3→4, MinPredicate 3→5. Cuts rule-based kv output ≥50%.
- **6 new noise filters** — Section header subjects, bold-formatted subjects, file path predicates, long objects (>200 chars), checkbox subjects.
- **`connect sync --extract`** now implies enrichment by default (`--no-enrich` to skip).
- **`config` fact type** — Added to DB CHECK constraint. 9 valid types: kv, relationship, preference, temporal, identity, location, decision, state, config.
- **Enrichment MaxTokens** — Bumped 1024→8192 to handle large files (MEMORY.md 22K chars).
- **Classification batch-size** — Default 20 (was 10). Optimal for DeepSeek V3.2 on OpenRouter.
- CLI version now reports **`0.9.0`**.

### New Packages
- `internal/llm/` — LLM provider abstraction (Google AI + OpenRouter). `Provider.Complete()` interface.
- `internal/search/expand.go` — Query expansion with LRU cache.
- `internal/extract/enrich.go` — LLM-powered fact enrichment.
- `internal/extract/classify.go` — Batch fact classification with concurrency.
- `internal/extract/resolve.go` — LLM-powered conflict resolution.
- `internal/extract/summarize.go` — Cluster consolidation.

### Cost
- All LLM features optional (flags). <$1/month ongoing at typical usage.
- Enrichment: Grok 4.1 Fast ($0.20/$0.50 per M tokens)
- Classification: DeepSeek V3.2 ($0.25/$0.40 per M tokens)
- Query expansion: Gemini 2.0 Flash (free tier)

### Validation
- Full suite passing: `go test ./... -count=1` (15 packages, 300+ tests).
- Build clean: `go build ./cmd/cortex/`.
- Classification sweep: 20K facts in 53 min, 77.6% reclassification, 0.12% error rate.

## [0.8.0] - 2026-02-25

### Added
- **RRF search mode** — Reciprocal Rank Fusion (`--mode rrf`) combines BM25 and semantic results using rank-based scoring with configurable K parameter (default 60). Graceful fallback when embedder unavailable. (#205)
- **Topic clustering** — Subject co-occurrence analysis builds topic communities with cohesion scores at import time. Incremental updates with 10% subject delta threshold for rebuild. New `cortex cluster` CLI command with `--rebuild`, `--name`, `--export json`. API: `/api/clusters`, `/api/clusters/:id`. (#207)
- **Impact/blast-radius analysis** — `/api/impact?subject=X&depth=N` endpoint with predicate grouping, confidence distribution, and connected subjects. Visualizer adds concentric ring layout and confidence heat map. (#206)
- **Graph-aware MCP tools** — 3 new tools (`graph_explore`, `graph_impact`, `list_clusters`) and 2 new resources (`cortex://graph/subjects`, `cortex://graph/clusters`). Source prefix filtering on explore. Total: 19 MCP tools, 4 resources. (#208)
- **Temporal flow timeline** — `/api/timeline?subject=X` endpoint with day/week/month bucketing, transition detection (superseded, refined, decayed, expanded, contradicted), confidence trend tracking, and related subject expansion. Full visualizer integration with timeline mode. (#209)

### Changed
- CLI version now reports **`0.8.0`** (`cortex --version`).
- Search mode help and MCP tool descriptions updated to include `rrf` mode.
- Graph `ExportNode` now includes `cluster_id`, `cluster_color`, and `depth` fields.
- Cluster tables (`clusters`, `fact_clusters`) created automatically via migration.
- Import pipeline and connector sync now trigger cluster updates after fact extraction.
- README updated: 19 MCP tools, 4 resources.

### Validation
- Full suite passing: `go test ./... -count=1` (13 packages, 290+ tests).
- Build clean: `go build ./cmd/cortex/`.

## [0.7.0] - 2026-02-24

### Added
- **Sync pipeline in connectors** — `cortex connect sync --extract [--no-infer] [--llm <provider/model>]` runs import → extraction → inference with per-provider counts.
- **Source-aware search** — `cortex search --source <provider>` filters by connector provenance and applies source weighting (manual imports boosted over connector imports).
- **Auto-sync scheduler** — `cortex connect schedule --every <interval> [--install|--uninstall|--show]` with launchd/systemd generation.
- **New connectors** — Discord, Telegram, and Notion providers in `internal/connect/` with config validation + mock HTTP tests.
- **Graph explorer UX upgrades** — `/api/facts` endpoint, search-to-node highlighting, time-window filtering, richer fact inspector sidebar, and edge hover labels.
- **End-to-end connector integration tests** — new tests for connect → extract → infer → search workflows and multi-provider source filtering.

### Changed
- CLI version now reports **`0.7.0`** (`cortex --version`).
- Graph cluster metadata now includes per-subject `fact_count`, `last_updated`, and `source_types` for better frontend filtering and inspection.
- Graph search API now uses the search engine pipeline and returns matched node IDs alongside fact matches.

### Validation
- Full suite passing: `go test ./... -count=1` (15 packages).
- Build clean: `go build ./cmd/cortex/`.

## [0.6.0] - 2026-02-24

### Added
- **Import filters** — `--include` and `--exclude` flags on `cortex import` for file extension filtering (e.g., `--include .md,.txt` or `--exclude .go,.py`). Case-insensitive, dot-optional. 7 new tests.
- **Auto-infer edges on import** — After `import --extract`, automatically runs relationship inference to create knowledge graph edges. Skip with `--no-infer` flag. Prints "Inferred X new edges".

### Removed
- **codexrollout** — Removed `internal/codexrollout/` package and `cmd/codex-rollout-report/` binary (574 lines of internal telemetry tooling with no user value)
- **observe alerts** — Removed `watch`, `alerts`, and webhook delivery CLI commands + 3 MCP tool registrations (`cortex_alerts`, `cortex_watch_add`, `cortex_watch_list`). Core `stale` and `conflicts` commands preserved.
- **3D graph mode** — Stripped `ForceGraph3D`, `init3DGraph`, `switchTo3DSpace`, 3D button, and Three.js script from graph explorer. 2D is the production UI.
- **dist-v0.1.3** — Removed old release binaries that leaked into the repo.

### Changed
- Net change: -2,144 lines / +218 lines across 13 files
- Graph explorer is now 2D-only with cleaner, lighter codebase

## [0.5.0] - 2026-02-23

### Added
- **2D-first knowledge graph explorer** with shadcn-style dark UI, 3D toggle, and Cortex branding (#191)
- Subject graph mode — `/api/graph?subject=X` drills into a single entity's facts and edges
- Graph quality metadata panel — shows edge source, fallback counts, density per query
- Subject-cluster fallback edges with sparse fill for disconnected groups
- Edge deduplication and endpoint filtering in graph traversal
- Co-occurrence loading from `fact_cooccurrence_v1` table
- `fact_edges_v1` table support with graceful fallback to subject clustering
- New tests: subject graph API + cluster limit enforcement
- Repository contributor guide (`AGENTS.md`)
- Stats banner + `/api/stats` endpoint (#186)
- Slack connector + MCP connect tools + OpenClaw plugin wiring (#188, #140, #141)
- Webhook delivery channel for alerts (#187)
- Relationship inference engine — emergent knowledge graph (#170)
- Co-occurrence tracking + graph traversal integration (#169)
- Fact relationship edges — knowledge graph foundation (#168)
- Cross-agent conflict detection (#167)
- Shared reinforcement — implicit decay reset + cross-agent amplification (#166)
- Agent namespaces — scoped facts + multi-agent views (#165)
- Persistent watch queries with alert notifications (#164)
- Decay notifications — fading facts alert system (#163)
- Proactive fact conflict detection + alert system (#162)
- `@cortex-ai/mcp` npm package — zero-config MCP server (#159)
- Homebrew tap auto-publish via goreleaser (#160)
- Cortex Connect CLI + connectors: GitHub, Gmail, Google Calendar, Google Drive (#138, #139, #142, #143)

### Changed
- Cluster API defaults widened: 150 nodes (was 50), subject range 3–200 facts (was 3–50)
- Graph API now accepts `subject` parameter alongside `fact_id`
- Fact extraction pipeline rewritten: `normalizeSubject()` strips timestamps, section trails, emoji, markdown headers
- `MaxSubjectLength = 50` with word-boundary truncation
- Auto-capture governor: 15 facts/memory cap, min object/predicate length 3
- 92% noise reduction in fact corpus (301K → 23K facts)

### Validation
- All 15 test packages passing
- Graph visualizer manually validated across 2D/3D modes, search, and subject drill flows

## [0.3.6] - 2026-02-21

### Added
- Reason quality evaluation pack (`scripts/reason_quality_eval.py`) with 30-case fixture coverage and artifact-friendly output modes.
- Reliability guardrail gate (`scripts/reason_guardrail_gate.py`) and outcome KPI rollup (`scripts/reason_outcome_rollup.py`).
- Deterministic response quality contract enforcement for reason outputs (`internal/reason/quality_contract.go`).

### Changed
- Reason engine and recursive mode now enforce structured output sections (`Summary`, `Evidence`, `Conflicts & Trade-offs`, `Next Actions`) before returning content.
- Recursive reason initialization now uses expanded preset templates for stronger task framing and more consistent outputs.
- Duplicate search handling in recursive reason now asks for a new search angle instead of force-finalizing early.
- Eval scoring weights were rebalanced to reduce keyword brittleness and better reward actionable, structured responses.

### Validation
- Full 30-case reason quality run: **29/30 pass** (`0.9667` pass rate), avg overall `0.9395`.
- Track 2 reason guardrail: **PASS** (all checks green, hard failure rate `0.0333`).

## [0.3.5] - 2026-02-20

### Added
- Stable promotion from externally validated RC target `v0.3.5-rc2`.
- External hostile-audit evidence path (immutable artifact + fresh-clone + adversarial checks) documented for release continuity.

### Changed
- Release promotion path now depends on successful hostile audit gate outcomes before stable tag publication.

### Fixed
- Carry-forward of RC hardening into stable:
  - traversal-safe visualizer export output path guards
  - strict rollout no-valid-telemetry non-zero enforcement
  - unreadable recursive import subtree explicit failures
  - symlink-loop recursion rejection in importer
  - release checklist portability when `rg` is unavailable

### Validation
- External hostile audit verdict for `v0.3.5-rc2`: **GO** (no reproducible product findings).
- Required gates pass: `audit_break_harness`, `audit_preflight`, `release_checklist`.

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
