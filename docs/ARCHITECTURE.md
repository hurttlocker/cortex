# Architecture

> Current as of v0.9.0 (62,300+ lines, 15 packages, 1,081 tests).

## Overview

Cortex is a pipeline:

```
Files/Sources → Import → Chunk → Extract → [Enrich] → [Classify] → Store → Search
```

Each stage is a clean interface. LLM stages (in brackets) are optional — the pipeline works without them.

## Directory Structure

```
cortex/
├── cmd/cortex/           # CLI entry point + MCP server bootstrap
│   ├── main.go           # All commands (7,756 lines)
│   └── main_test.go      # CLI integration tests
├── internal/
│   ├── store/            # SQLite + FTS5 + WAL storage layer
│   ├── ingest/           # Format-specific importers + chunking
│   ├── extract/          # Rule-based fact extraction + governor
│   ├── llm/              # LLM provider abstraction (multi-provider)
│   ├── search/           # BM25, semantic, hybrid, RRF retrieval
│   ├── embed/            # Embedding providers (ollama, OpenAI-compat)
│   ├── ann/              # HNSW approximate nearest neighbor index
│   ├── graph/            # Knowledge graph, clustering, 2D explorer
│   ├── connect/          # 8 source connectors + sync engine
│   ├── mcp/              # MCP server (17 tools, 4 resources)
│   ├── observe/          # Stats, stale, conflicts, alerts, doctor
│   └── reason/           # One-shot + recursive reasoning engine
├── docs/                 # Documentation
├── scripts/              # Benchmarks, utilities
└── go.mod
```

## Package Details

### `store/` — Storage Layer (7,088 lines)

The foundation. Everything goes through the store.

- **SQLite + FTS5**: Single file, WAL mode, no external database server
- **Tables**: `memories`, `facts`, `fts_memories` (virtual), `connectors`, `sync_log`
- **Migrations**: Automatic schema evolution on startup
- **Dedup**: Content-hash (`SHA256(sourcePath + \0 + content)`) prevents duplicate memories
- **Agent scoping**: `agent_id` column on facts, `json_extract(metadata, '$.agent_id')` on memories
- **Interfaces**: `Store` interface with `ListMemories`, `ListFacts`, `GetMemory`, `SaveMemory`, `SaveFact`, `DeleteMemory`, `ListOpts` (agent filter, pagination, type filter)

### `ingest/` — Import Engine (1,985 lines)

The front door. Every memory enters through an importer.

- **Formats**: Markdown, JSON, YAML, CSV, plain text
- **Chunking**: 500 char max, paragraph → line → word boundary splitting
- **Provenance**: Every chunk tracks source file, line range, import timestamp
- **Recursive**: `--recursive` imports entire directory trees
- **Extension filter**: `--ext md,txt,yaml` to limit file types
- **Interface**:
  ```go
  type Importer interface {
      CanHandle(path string) bool
      Import(ctx context.Context, path string) ([]RawMemory, error)
  }
  ```

### `extract/` — Fact Extraction (3,782 lines)

Turns unstructured text into structured facts.

- **Rule-based pipeline**: Pattern matching, regex (dates, emails, URLs, amounts), NER via `prose`
- **Governor**: Caps facts per memory. Default: 10 (auto-capture: 5). Filters headers, bold text, file paths, long objects, checkboxes. `MaxSubjectLength=50`.
- **Fact types**: 9 valid types — `kv`, `relationship`, `preference`, `temporal`, `identity`, `location`, `decision`, `state`, `config`
- **LLM enrichment** (v0.9.0): Optional second pass via `llm/` package. Additive only — never removes rule-extracted facts. Tagged `llm-enrich`.
- **LLM classification** (v0.9.0): Optional reclassification of fact types. Batch: 20 facts/request, 5 concurrent.

### `llm/` — LLM Provider Abstraction (391 lines)

Thin wrapper over OpenAI-compatible APIs.

- **Providers**: Any OpenAI-compatible endpoint (Grok, DeepSeek, Gemini, OpenRouter, local)
- **Config**: `~/.cortex/config.yaml` or environment variables
- **Used by**: Enrichment, classification, query expansion, conflict resolution, cluster summarization
- **Not used by**: Search (no LLM in the hot path)

### `search/` — Retrieval Engine (2,294 lines)

Three search modes, composable.

- **BM25**: FTS5 keyword matching with boolean operators
- **Semantic**: Cosine similarity on embedding vectors (requires embedder)
- **Hybrid**: BM25 + semantic with reciprocal rank fusion (RRF)
- **Query expansion**: Pre-search LLM call to broaden query (Gemini free tier). Runs *before* retrieval — no LLM in the search path itself.
- **Graceful degradation**: Hybrid without embedder → silent fallback to BM25. Semantic without embedder → hard error with remediation hint.
- **Confidence decay**: Results weighted by Ebbinghaus decay factor per fact type
- **Agent scoping**: `--agent` filters to single agent's memories

### `embed/` — Embedding Providers (477 lines)

Generates and stores vector embeddings for semantic search.

- **Providers**: `ollama/<model>` (local, free), `openai/<model>`, any OpenAI-compatible endpoint
- **Default**: `ollama/nomic-embed-text` (local, zero cost)
- **Batch processing**: `cortex embed <provider/model> --batch-size 10`
- **Storage**: Vectors stored as BLOBs in the memories table

### `ann/` — Approximate Nearest Neighbor (647 lines)

HNSW index for fast vector similarity search.

- **In-memory**: Built from stored embeddings on search startup
- **Parameters**: Configurable M, efConstruction, efSearch
- **Used by**: Semantic and hybrid search modes

### `graph/` — Knowledge Graph (3,069 lines)

Builds and serves a knowledge graph from extracted facts.

- **Graph construction**: Subject → predicate → object triples from facts table
- **Cluster detection**: Groups related facts by subject proximity
- **2D Explorer**: Interactive browser-based visualization (shadcn UI)
  - Force-directed layout with zoom, pan, click-to-inspect
  - 5 view modes: graph, table, subjects, clusters, search
  - Stale state clearing, loading indicators, empty state handlers
- **HTTP API**: `/api/graph`, `/api/subjects`, `/api/clusters`, `/api/facts`
- **Pagination**: `offset` + `limit` on all endpoints, rank metadata in responses
- **Agent filter**: Middleware injects `?agent=` for scoped views

### `connect/` — Source Connectors (4,273 lines)

Imports data from external services into the standard pipeline.

- **8 providers**: GitHub, Gmail, Calendar, Drive, Slack, Discord, Telegram, Notion
- **Sync engine**: Handles cursor tracking, incremental sync, error recovery
- **Record model**: `Record{ID, Title, Content, Source, Timestamp, AgentID}`
- **Auto-scheduling**: OS-native (launchd on macOS, systemd on Linux)
- **Agent tagging**: `--agent` on `connect sync` tags all imported memories
- **MCP integration**: 4 connector tools exposed via MCP server
- **Interface**:
  ```go
  type Provider interface {
      Name() string
      Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error)
  }
  ```

### `mcp/` — MCP Server (2,284 lines)

Model Context Protocol server for agent integration.

- **Transport**: stdio (default) or HTTP+SSE (`--port`)
- **17 tools**: Search, import, facts, stats, stale, reinforce, reason, edge-add, graph (4 tools), clusters, connect (4 tools)
- **4 resources**: stats, recent memories, graph subjects, graph clusters
- **Agent scoping**: `--agent` sets default agent for all tool invocations; per-request `agent_id` overrides
- **Tested with**: Claude Code, Cursor, Windsurf

### `observe/` — Observability (823 lines)

Health monitoring and diagnostics.

- **Stats**: Memory/fact counts, storage size, confidence distribution, freshness breakdown
- **Stale**: Facts not reinforced within configurable window
- **Conflicts**: Contradictory facts (same subject+predicate, different object)
- **Alerts**: Proactive notifications (DB size, growth spikes, staleness)
- **Doctor**: Diagnostic suite (DB health, WAL mode, FTS5, providers)

### `reason/` — Reasoning Engine (2,304 lines)

Synthesizes answers from memory.

- **One-shot**: Search → gather context → generate answer
- **Recursive**: Multi-hop reasoning with configurable depth
- **Provider**: Any configured LLM via `llm/` package

## Data Flow

### Import Path
```
File → Ingest (parse + chunk) → Store (save memories)
     → Extract (rule-based facts) → Store (save facts)
     → [LLM Enrich (find missed facts)] → Store (save enriched facts)
     → [LLM Classify (reclassify types)] → Store (update fact types)
```

### Search Path
```
Query → [Query Expansion (LLM, pre-search)]
      → BM25 (FTS5) ──┐
      → Semantic (ANN) ─┤─→ RRF Fusion → Confidence Decay → Ranked Results
                         │
                    (or BM25-only fallback if no embedder)
```

### Connector Path
```
External API → Provider.Fetch() → []Record
             → SyncEngine.importRecord() → Store (save memories)
             → Extract → [Enrich] → [Classify] → Store (save facts)
             → Update sync cursor
```

## Configuration

```yaml
# ~/.cortex/config.yaml
db_path: ~/.cortex/cortex.db
embed:
  provider: ollama/nomic-embed-text
llm:
  provider: openrouter/grok-4.1-fast   # For enrichment
  api_key: or-...
  enrich_provider: xai/grok-4.1-fast   # Override for enrichment specifically
  classify_provider: deepseek/v3.2     # Override for classification
  expand_provider: google/gemini-2.0-flash  # Override for query expansion
```

Environment variables work too: `CORTEX_DB_PATH`, `OPENROUTER_API_KEY`, `XAI_API_KEY`, etc.

## Testing

- **1,081 test functions** across 14 packages (+ 1 script package)
- **No external dependencies in tests**: All tests use in-memory SQLite
- **CI**: GitHub Actions, `go test ./...`, `go vet ./...`
- **Benchmark suite**: `scripts/bench/` for search quality and performance

```bash
go test ./...                    # Run all tests
go test ./internal/store/...     # Run one package
go test -run TestSearch ./...    # Run matching tests
go test -count=1 ./...           # Skip cache
```
