# Architecture

> This document describes the high-level architecture of Cortex.  
> It will evolve as the implementation progresses.

## Overview

Cortex is organized as a pipeline:

```
Input Files → Importers → Fact Extraction → Storage → Search/Observe
```

Each stage is a clean interface that can be extended independently.

## Directory Structure

```
cortex/
├── cmd/cortex/          # CLI entry point (cobra commands)
├── internal/
│   ├── store/           # SQLite + FTS5 storage layer
│   ├── ingest/          # Import engine — format-specific parsers
│   ├── extract/         # Fact extraction — NLP pipeline
│   ├── search/          # Dual search — BM25 + semantic
│   └── observe/         # Observability — stats, stale, conflicts
├── docs/                # Documentation
└── go.mod               # Go module definition
```

## Key Design Decisions

### Single SQLite Database

All data lives in one SQLite file (`~/.cortex/cortex.db` by default):
- Raw imported content
- Extracted facts with provenance
- FTS5 full-text index
- Embedding vectors (stored as BLOBs)

**Why:** Zero configuration, trivially portable (it's just a file), battle-tested concurrency with WAL mode.

### Import-First Architecture

The import engine is the front door. Every memory enters through an importer that:
1. Parses the source format
2. Chunks content into memory units
3. Preserves provenance (file, line, timestamp)
4. Hands off to the extraction pipeline

Adding a new format means implementing one interface:

```go
type Importer interface {
    CanHandle(path string) bool
    Import(ctx context.Context, path string) ([]RawMemory, error)
}
```

### Local-Only Processing

No network calls in the critical path. Fact extraction uses rule-based NLP (dependency parsing, NER via the `prose` library). Embeddings use ONNX Runtime with a bundled model.

This is a philosophical choice, not just a technical one: your memory should work without an internet connection.

### Dual Search Strategy

- **BM25 (FTS5):** For when users know what they're looking for. Exact keyword matching, boolean operators, fast.
- **Semantic (ONNX):** For when users know the concept but not the words. Finds related memories even without keyword overlap.
- **Hybrid (default):** Combines both with reciprocal rank fusion.

### Two-Tier Extraction Pipeline

```
Input Document
     │
     ├─── Structured? (MD with headers, JSON, YAML, CSV)
     │         │
     │         └──▶ Tier 1: Rule-Based Extraction
     │              • Pattern matching on structure
     │              • Regex for dates, emails, URLs
     │              • Basic NER via prose
     │              • Zero dependencies
     │
     └─── Unstructured? (Raw text, conversation logs)
               │
               ├──▶ Tier 1: Best-effort local extraction
               │    (may miss relationships, coreference)
               │
               └──▶ Tier 2: LLM-Assist (if configured)
                    • Sends doc + extraction schema to LLM
                    • Any OpenAI-compatible API
                    • Returns typed JSON (validated by Cortex)
                    • Never sends existing memory to LLM
```

Both tiers produce the same output format: `[]ExtractedFact`. The storage layer doesn't know or care which tier produced the fact.

## Data Model

```sql
-- Core memory table
CREATE TABLE memories (
    id          INTEGER PRIMARY KEY,
    content     TEXT NOT NULL,
    source_file TEXT,
    source_line INTEGER,
    imported_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Full-text search index
CREATE VIRTUAL TABLE memories_fts USING fts5(content, content=memories, content_rowid=id);

-- Extracted facts
CREATE TABLE facts (
    id        INTEGER PRIMARY KEY,
    memory_id INTEGER REFERENCES memories(id),
    key       TEXT,
    value     TEXT,
    fact_type TEXT,  -- 'kv', 'relationship', 'preference', 'temporal'
    confidence REAL DEFAULT 1.0
);

-- Embeddings for semantic search
CREATE TABLE embeddings (
    memory_id INTEGER PRIMARY KEY REFERENCES memories(id),
    vector    BLOB NOT NULL  -- float32 array, 384 dimensions for MiniLM
);
```

## Future Architecture Notes

- **MCP Server (Phase 2):** Will wrap the search and import APIs as MCP tools
- **Web Dashboard (Phase 2):** Read-only view of stats/search, served from the same binary
- **Plugin System (Phase 3):** Custom importers and extractors as Go plugins or subprocess pipes

## Extended Data Model (Novel Features)

### Recall Logging (Provenance Chains)

```sql
-- Track every time a fact is recalled
CREATE TABLE recall_log (
    id          INTEGER PRIMARY KEY,
    fact_id     INTEGER REFERENCES facts(id),
    recalled_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    context     TEXT,       -- query or conversation that triggered recall
    session_id  TEXT,       -- which agent session used this fact
    lens        TEXT        -- which lens was active during recall
);
```

### Confidence Decay

```sql
-- Extended facts table with decay fields
ALTER TABLE facts ADD COLUMN decay_rate REAL DEFAULT 0.01;
ALTER TABLE facts ADD COLUMN last_reinforced DATETIME DEFAULT CURRENT_TIMESTAMP;
-- confidence already exists, recalculated on query:
-- effective_confidence = confidence * exp(-decay_rate * days_since_reinforced)
```

Decay rates by fact type:
| Type | Decay/Day | Half-Life |
|------|-----------|-----------|
| identity | 0.001 | 693 days |
| decision | 0.002 | 347 days |
| relationship | 0.003 | 231 days |
| location | 0.005 | 139 days |
| preference | 0.01 | 69 days |
| state | 0.05 | 14 days |
| temporal | 0.1 | 7 days |

### Memory Event Log (Differential Memory)

```sql
-- Append-only event log for git-style history
CREATE TABLE memory_events (
    id         INTEGER PRIMARY KEY,
    event_type TEXT NOT NULL,  -- 'add', 'update', 'merge', 'decay', 'delete', 'reinforce'
    fact_id    INTEGER,
    old_value  TEXT,
    new_value  TEXT,
    source     TEXT,           -- what triggered this event
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Named snapshots for point-in-time restore
CREATE TABLE snapshots (
    id         INTEGER PRIMARY KEY,
    tag        TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    event_id   INTEGER REFERENCES memory_events(id)
);
```

### Memory Lenses

```sql
-- Lens definitions
CREATE TABLE lenses (
    id           INTEGER PRIMARY KEY,
    name         TEXT UNIQUE NOT NULL,
    include_tags TEXT,  -- JSON array of tags to include
    exclude_tags TEXT,  -- JSON array of tags to exclude
    boost_rules  TEXT,  -- JSON: boost/demote rules
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## Architecture Principles

1. **Every novel feature maps to SQL.** No magic. If you can query it, you can debug it.
2. **All tables are additive.** New features add tables/columns, never break existing ones.
3. **Single SQLite file.** Everything in `~/.cortex/cortex.db`. Trivially portable.
4. **Interfaces first.** Every layer is an interface that can be swapped without touching others.
5. **Local by default, cloud by choice.** Nothing phones home. Ever. Unless you ask it to.
