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
