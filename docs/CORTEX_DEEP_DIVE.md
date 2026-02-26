# Cortex: Import-First Agent Memory You Actually Own

> Your agent memory should be portable, inspectable, and durable. Cortex is a single-binary, SQLite-backed memory layer with hybrid retrieval, LLM-augmented fact extraction, confidence decay, knowledge graph exploration, 8 source connectors, and a 17-tool MCP server — all in one file you can copy with `cp`.

---

## Current State

- **Version:** v0.9.0 (released 2026-02-24)
- **Codebase:** 62,300+ lines of Go across 15 packages
- **Tests:** 1,081 test functions, all green
- **Binary:** ~19MB, pure Go, zero CGO, cross-compiled for 5 platforms
- **License:** MIT
- **Repo:** [github.com/hurttlocker/cortex](https://github.com/hurttlocker/cortex)

### Production Deployment (3 machines)

| Machine | Version | Memories | Facts | DB Size |
|---------|---------|----------|-------|---------|
| iMac (primary) | v0.9.0 | 1,906 | 3,992 | 6.4 MB |
| MacBook (mobile) | v0.9.0 | 113 | 1,044 | 1.1 MB |
| Sydney's MacBook | v0.9.0 | 455 | 673 | 3.1 MB |

---

## What Cortex Does

Cortex is memory infrastructure for AI agents. You feed it files, it extracts structured facts, and your agent searches them later. Facts decay over time using Ebbinghaus forgetting curves — identity facts last years, temporal observations fade in days. When you search, stale facts rank lower. Reinforce what matters; let the rest go.

### The Pipeline

```
Files/Sources → Import → Chunk → Extract (rules) → Enrich (LLM) → Classify → Store → Search
                                                                                      ↓
                                                                              Hybrid Retrieval
                                                                          (BM25 + Semantic + RRF)
                                                                                      ↓
                                                                              Confidence Decay
                                                                          (Ebbinghaus curves)
                                                                                      ↓
                                                                              Ranked Results
```

### Core Capabilities

| Capability | Description |
|------------|-------------|
| **Import** | Markdown, JSON, YAML, CSV, plain text. Recursive directory import. Chunking at paragraph/line/word boundaries (500 char max). Content-hash dedup prevents duplicate memories. |
| **Extraction** | Rule-based NLP pipeline: pattern matching, regex (dates, emails, URLs), basic NER. Produces typed facts with provenance. |
| **LLM Enrichment** | Optional (v0.9.0). Sends chunks to an LLM (Grok, GPT, etc.) to find facts that rules miss — relationships, decisions, preferences. Additive only: never removes rule-extracted facts. Tagged as `llm-enrich`. |
| **Classification** | Auto-classifies facts into 9 types: `kv`, `relationship`, `preference`, `temporal`, `identity`, `location`, `decision`, `state`, `config`. Rules assign defaults; LLM reclassifies for accuracy. |
| **Search** | Hybrid (BM25 + semantic + RRF), BM25-only, or semantic-only. Query expansion via LLM (Gemini free tier). Graceful degradation: no embedder → falls back to BM25. |
| **Confidence Decay** | Ebbinghaus forgetting curves per fact type. Identity decays slowly (years). Temporal decays fast (days). `cortex reinforce <id>` resets the decay timer. |
| **Knowledge Graph** | Subject→predicate→object triples with cluster detection. Interactive 2D explorer at `localhost:8090`. 5 view modes: graph, table, subjects, clusters, search. |
| **Connectors** | 8 providers: GitHub, Gmail, Calendar, Drive, Slack, Discord, Telegram, Notion. Incremental sync with cursor tracking. OS-native scheduling (launchd/systemd). |
| **MCP Server** | 17 tools + 4 resources. stdio or HTTP+SSE transport. Works with Claude Code, Cursor, Windsurf, any MCP client. |
| **Multi-Agent** | `--agent` flag scopes all operations. `cortex agents` lists known agents. Memories tagged with agent ID. No cross-agent leakage. |
| **Observability** | `stats`, `stale`, `conflicts`, `alerts`, `doctor`. Proactive health monitoring. |
| **Reasoning** | One-shot and recursive chain-of-thought over your memory corpus. Search → synthesize → answer. |

---

## Architecture Overview

```
cortex/
├── cmd/cortex/           # CLI + MCP server (7,756 lines)
├── internal/
│   ├── store/            # SQLite + FTS5 + WAL (7,088 lines)
│   ├── connect/          # 8 source connectors (4,273 lines)
│   ├── extract/          # Fact extraction pipeline (3,782 lines)
│   ├── graph/            # Knowledge graph + clustering + explorer (3,069 lines)
│   ├── search/           # BM25 + semantic + hybrid + RRF (2,294 lines)
│   ├── reason/           # One-shot + recursive reasoning (2,304 lines)
│   ├── mcp/              # MCP server: 17 tools, 4 resources (2,284 lines)
│   ├── ingest/           # Format parsers + chunking (1,985 lines)
│   ├── observe/          # Stats, stale, conflicts, alerts (823 lines)
│   ├── ann/              # HNSW approximate nearest neighbor (647 lines)
│   ├── embed/            # Embedding providers (ollama, OpenAI) (477 lines)
│   └── llm/              # LLM provider abstraction (391 lines)
├── docs/                 # This documentation
├── scripts/              # Benchmarks, utilities
└── go.mod
```

### Data Model

Everything lives in one SQLite file (`~/.cortex/cortex.db`):

- **Memories**: Imported text chunks with source path, line numbers, timestamps, agent ID, and content hash
- **Facts**: Extracted triples (subject, predicate, object) with type, confidence, provenance chain back to source memory, and optional agent scoping
- **FTS5 index**: Full-text search over memory content and fact text
- **Embedding vectors**: Stored as BLOBs, generated by ollama or OpenAI-compatible providers
- **Connector state**: Sync cursors, config, error history per provider

### Key Design Decisions

**Single SQLite file.** No Postgres, no Redis, no Docker. Your memory is a file you can `cp`, `rsync`, or back up with Time Machine. WAL mode gives concurrent reads with single-writer semantics.

**Import-first.** Start with the files you already have. Every other memory tool says "start fresh." Cortex says "bring everything." Content-hash dedup (`SHA256(sourcePath + \0 + content)`) means reimporting the same file is a no-op.

**LLM is optional.** Every feature that uses an LLM has a flag to disable it. Without LLM: rule-based extraction, BM25 search, deterministic classification. With LLM: dramatically better fact quality, query expansion, conflict resolution, cluster summarization. You choose the tradeoff.

**No network in the search path.** Query expansion (LLM) runs *before* search. The actual BM25 + semantic retrieval is pure local computation. Your search never blocks on an API call.

**Ebbinghaus decay is philosophical.** Memory should work like memory. Not everything is equally important forever. Identity facts ("Q lives in Philadelphia") last years. Temporal facts ("deployed v0.8.0 today") fade in days. This isn't a bug — it's the core insight.

---

## LLM Integration (v0.9.0)

Cortex v0.9.0 introduced LLM augmentation as an optional layer. The key constraint: **LLM calls never appear in the hot search path.**

### Integration Points

| Feature | LLM Provider | When It Runs | Cost |
|---------|-------------|-------------|------|
| **Enrichment** | Grok 4.1 Fast (default) | At import time | ~$0.001/memory |
| **Classification** | DeepSeek V3.2 (default) | At import time (or batch) | ~$0.0003/fact |
| **Query Expansion** | Gemini 2.0 Flash | Before search (pre-search) | Free (Google free tier) |
| **Conflict Resolution** | Any configured provider | On `cortex conflicts --resolve llm` | Per-invocation |
| **Cluster Summarization** | Any configured provider | On `cortex summarize` | Per-invocation |

### Cost in Practice

Three-machine deployment running auto-sync every 3 hours with enrichment: **< $1/month.**

### Disabling LLM Features

```bash
# Import without enrichment
cortex import notes.md --extract --no-enrich

# Import without classification
cortex import notes.md --extract --no-classify

# Import with zero LLM calls (rules only)
cortex import notes.md --extract --no-enrich --no-classify

# Search without query expansion
cortex search "query" --no-expand
```

---

## MCP Server

The MCP server is how agents interact with Cortex. It exposes the full feature set through the Model Context Protocol.

### Tools (17)

| Tool | Description |
|------|-------------|
| `cortex_search` | Hybrid search with confidence decay |
| `cortex_import` | Import text or files into memory |
| `cortex_facts` | List/filter extracted facts |
| `cortex_stats` | Memory statistics and health |
| `cortex_stale` | Find facts not reinforced recently |
| `cortex_reinforce` | Reset decay timer on important facts |
| `cortex_reason` | Synthesize answers from memory |
| `cortex_edge_add` | Add explicit graph edges |
| `cortex_graph` | Query knowledge graph |
| `cortex_graph_export` | Export graph as JSON |
| `cortex_graph_explore` | Traverse graph from a subject |
| `cortex_graph_impact` | Analyze subject impact/centrality |
| `cortex_clusters` | List fact clusters |
| `cortex_connect_list` | List configured connectors |
| `cortex_connect_add` | Add a new connector |
| `cortex_connect_sync` | Trigger connector sync |
| `cortex_connect_status` | Check connector health |

### Resources (4)

| Resource | Description |
|----------|-------------|
| `cortex://stats` | Live memory statistics |
| `cortex://recent` | Recently imported memories |
| `cortex://graph/subjects` | All known graph subjects |
| `cortex://graph/clusters` | Detected fact clusters |

### Starting the MCP Server

```bash
# stdio (for Claude Code, Cursor, etc.)
cortex mcp

# HTTP+SSE (for web clients)
cortex mcp --port 8080

# Agent-scoped
cortex mcp --agent mister
```

### Connecting to Claude Code

```bash
claude mcp add cortex -- cortex mcp
```

That's it. Claude Code now has persistent memory with 17 tools.

---

## Connectors

Eight providers ship with Cortex. All flow through the standard import pipeline — fact extraction, confidence decay, and search work automatically on connector-imported data.

| Provider | Data Synced | Auth Method |
|----------|-------------|-------------|
| **GitHub** | Issues, PRs, comments | Personal Access Token |
| **Gmail** | Email threads | gog CLI (OAuth) |
| **Google Calendar** | Events | gog CLI (OAuth) |
| **Google Drive** | Document content | gog CLI (OAuth) |
| **Slack** | Channel messages + threads | Bot User Token |
| **Discord** | Channel messages | Bot Token |
| **Telegram** | Chat messages | Bot Token + Chat ID |
| **Notion** | Page content | Integration Token |

### Incremental Sync

After the first full sync, subsequent syncs only fetch items updated since the last sync cursor. The cursor is tracked per-provider in the connector state table.

### Auto-Scheduling

```bash
# Install OS-native auto-sync (launchd on macOS, systemd on Linux)
cortex connect schedule --every 3h --install

# Check schedule
cortex connect schedule --show

# Remove
cortex connect schedule --uninstall
```

See [connectors.md](connectors.md) for detailed setup per provider.

---

## Knowledge Graph

The graph is built from extracted facts (subject → predicate → object triples). Cortex detects clusters of related facts and provides an interactive 2D explorer.

### Explorer

```bash
cortex graph --serve --port 8090
```

Opens an interactive browser-based explorer with:
- **Graph view**: Force-directed 2D layout with zoom, pan, click-to-inspect
- **Table view**: Sortable fact list with confidence scores
- **Subjects view**: All known entities with fact counts
- **Clusters view**: Detected fact clusters with member lists
- **Search**: Filter graph by query

### API

The graph server exposes a JSON API:

```
GET /api/graph?q=<query>&limit=100&agent=<id>
GET /api/subjects?q=<query>&limit=50
GET /api/clusters
GET /api/facts?subject=<name>&limit=50
```

Pagination support via `offset` parameter. Rank metadata in responses.

---

## Multi-Agent Support

Cortex supports multiple agents sharing one database without cross-contamination.

```bash
# Import scoped to an agent
cortex import notes.md --agent mister --extract

# Search only Mister's memories
cortex search "config" --agent mister

# Stats for one agent
cortex stats --agent mister

# Each agent gets its own MCP scope
cortex mcp --agent mister
```

The `--agent` flag works on: `import`, `search`, `stats`, `stale`, `conflicts`, `classify`, `cleanup`, `graph --serve`, `connect sync`, and `mcp`.

`cortex agents` lists all known agent IDs in the database.

---

## CLI Reference (Grouped)

### Core
```bash
cortex import <path> [--recursive] [--extract] [--agent <id>]
cortex search <query> [--mode hybrid|bm25|semantic] [--agent <id>] [--limit N]
cortex reason <query> [--recursive] [--max-hops N]
cortex reinforce <fact-id>
```

### Observe
```bash
cortex stats [--agent <id>] [--json]
cortex stale [--days N] [--agent <id>]
cortex conflicts [--resolve llm] [--agent <id>]
cortex alerts
cortex doctor
cortex agents
```

### Extract & Classify
```bash
cortex extract <memory-id>
cortex classify [--limit N] [--batch-size N] [--concurrency N] [--agent <id>]
cortex cleanup [--agent <id>] [--dry-run]
```

### Graph
```bash
cortex graph [--serve] [--port N] [--agent <id>]
cortex graph --export [--format json]
cortex summarize [--cluster <id>]
```

### Connect
```bash
cortex connect init
cortex connect add <provider> --config '{...}'
cortex connect sync [--provider <name>] [--all] [--extract] [--agent <id>]
cortex connect status
cortex connect schedule --every <duration> [--install|--uninstall|--show]
```

### Server
```bash
cortex mcp [--port N] [--agent <id>]
```

### Maintenance
```bash
cortex optimize [--vacuum-only] [--analyze-only]
cortex embed <provider/model> [--batch-size N]
cortex version
cortex help [command]
```

Shell completions available for bash, zsh, fish, and PowerShell:
```bash
cortex completion bash > /usr/local/etc/bash_completion.d/cortex
cortex completion zsh > "${fpath[1]}/_cortex"
```

---

## Extraction Pipeline (Detailed)

### Tier 1: Rule-Based (always runs)

1. **Structural patterns**: Markdown headers, YAML frontmatter, JSON keys, CSV rows
2. **Regex extractors**: Dates, emails, URLs, phone numbers, monetary amounts
3. **NER**: Named entities via the `prose` library
4. **Governor**: Caps facts per memory (default: 10, auto-capture: 5). Filters: headers as subjects, bold formatting, file paths, long objects (>200 chars), checkboxes. `MaxSubjectLength=50` — anything longer is a section header, not an entity.

### Tier 2: LLM Enrichment (optional, v0.9.0+)

When `--extract` is used (enrichment is on by default since v0.9.0):
1. Sends chunk + extraction schema to configured LLM (default: Grok 4.1 Fast)
2. LLM returns typed JSON facts
3. Facts are validated against the 9-type schema
4. **Additive only**: LLM facts are merged with rule-extracted facts, never replacing them
5. Tagged with `source: llm-enrich` for provenance

### Tier 3: Classification (optional, v0.9.0+)

After extraction, facts can be reclassified by LLM (default: DeepSeek V3.2):
- Batch processing: 20 facts per request, 5 concurrent
- Benchmarked: 82.6% reclassification rate, 0 errors on 2,303 facts
- Results collected in memory, applied after all batches complete

### Dedup

Content-hash dedup: `SHA256(sourcePath + \0 + content)`. Same file reimported = no duplicate memories. Same content from different files = separate memories (intentional — provenance matters).

---

## Search Internals

### Modes

| Mode | How It Works | When to Use |
|------|-------------|-------------|
| **hybrid** (default) | BM25 + semantic + reciprocal rank fusion | General queries |
| **bm25** | FTS5 keyword matching | Known terms, exact phrases |
| **semantic** | Cosine similarity on embedding vectors | Fuzzy/conceptual queries |

### Graceful Degradation

- No embedder configured → `hybrid` silently falls back to `bm25`
- Explicit `--mode semantic` without embedder → hard error with remediation hint
- Query expansion fails → search proceeds without expansion (pre-search only)

### Ranking

Results are ranked by a composite score:
1. **Retrieval score** (BM25 rank, semantic similarity, or RRF fusion)
2. **Confidence** (base confidence × decay factor)
3. **Freshness** (more recent = slight boost)

---

## Observability

### `cortex doctor`

Runs a diagnostic suite and reports issues:
- Database exists and is readable
- WAL mode enabled
- FTS5 index healthy
- Embedding provider reachable (if configured)
- LLM provider reachable (if configured)
- Connector state valid

### `cortex alerts`

Proactive notifications for:
- Database size thresholds
- Fact growth spikes
- Stale fact accumulation
- Conflict detection
- Memory growth anomalies

### `cortex stats`

```json
{
  "memories": 1906,
  "facts": 3992,
  "sources": 186,
  "storage_bytes": 6356992,
  "avg_confidence": 0.92,
  "facts_by_type": {
    "config": 1012, "state": 1125, "kv": 653,
    "decision": 346, "identity": 239, "relationship": 190,
    "temporal": 187, "location": 153, "preference": 87
  }
}
```

---

## Release History

| Version | Date | Highlights |
|---------|------|-----------|
| v0.9.0 | 2026-02-24 | LLM-Augmented Intelligence: enrichment, classification, query expansion, conflict resolution, cluster summarization |
| v0.8.0 | 2026-02-23 | Knowledge Graph Intelligence: HNSW ANN, cluster detection, graph ranking, MCP graph tools |
| v0.7.0 | 2026-02-22 | Connectors (8 providers), auto-scheduling, MCP connector tools, agent scoping |
| v0.6.0 | 2026-02-21 | 2D graph explorer, search filters, extraction governor overhaul |
| v0.5.0 | 2026-02-20 | Interactive knowledge graph UI, fact type system, confidence decay |
| v0.4.0 | — | MCP server, reasoning engine |
| v0.3.x | — | Core stability, audit discipline, release tooling |

---

## What's Honest

### What works well
- Import + rule-based extraction is fast and reliable
- BM25 search is excellent for known-term queries
- LLM enrichment finds facts that rules consistently miss
- Ebbinghaus decay surfaces the right things at the right time
- Connectors bring in data from where you already work
- The MCP server makes agent integration trivial

### What's beta
- Semantic search quality depends heavily on embedding model choice
- Graph explorer is functional but not polished (2D only, no persistence)
- Connector burn-in is limited (GitHub and Gmail most tested)
- Query expansion occasionally broadens too aggressively
- Reasoning engine works but isn't heavily optimized

### What's not built yet
- Web dashboard (intentionally deferred — CLI + MCP is the interface)
- Cloud sync / hosted tier (intentionally local-only)
- Real-time streaming imports (batch only today)
- Cross-database federation (single DB per machine)

---

## Philosophy

**Memory should be boring infrastructure.** Import your files, search them later, let stale things fade. No ML pipeline to train, no vector database to manage, no cloud service to pay for. A binary and a SQLite file.

**LLM augmentation is a choice, not a requirement.** Everything works without it. With it, things work better. The delta is measurable and the cost is negligible (< $1/month). But the tool doesn't break when the API is down.

**Observable by default.** If you can't see what your agent knows, you can't trust it. `stats`, `stale`, `conflicts`, `alerts`, `doctor` — the observability surface is first-class, not an afterthought.

**Import-first, not API-first.** You already have files. Cortex meets you where you are.
