# PRD-001: Storage Layer

**Status:** Draft  
**Priority:** P0  
**Phase:** 1  
**Depends On:** None  
**Package:** `internal/store/`

---

## Overview

The storage layer is the foundation of Cortex. It provides a SQLite database with FTS5 full-text search, embedding storage, and CRUD operations for all memory data. Every other package depends on this — it must be built first.

## Problem

AI agents need persistent, searchable memory that works offline, requires zero configuration, and is trivially portable (copy a single file). Existing solutions require Postgres, Docker, or cloud services. We need an embedded storage layer that "just works."

---

## Requirements

### Must Have (P0)

- **Database initialization**
  - Create `~/.cortex/cortex.db` on first run (with parent directory)
  - Configurable via `CORTEX_DB` environment variable
  - Configurable via `--db <path>` CLI flag
  - Priority: CLI flag > env var > default path
  - Run migrations on startup (create tables if not exist)
  - Enable WAL mode for concurrent read access

- **Memories table — CRUD operations**
  - `Create(ctx, memory) → (id, error)` — insert a new memory
  - `Read(ctx, id) → (*Memory, error)` — get a memory by ID
  - `Update(ctx, memory) → error` — update an existing memory
  - `Delete(ctx, id) → error` — soft delete (mark as deleted, don't remove)
  - `List(ctx, opts) → ([]Memory, error)` — list with pagination, sorting, filtering
  - Each memory stores: `id`, `content`, `source_file`, `source_line`, `source_section`, `content_hash`, `imported_at`, `updated_at`

- **FTS5 full-text search index**
  - Create `memories_fts` virtual table using FTS5 on `content` column
  - Keep FTS index in sync with memories table (triggers or manual sync)
  - Support standard FTS5 query syntax: AND, OR, NOT, quoted phrases
  - Return BM25 rank scores with results
  - Support snippet generation with highlighted matches

- **Facts table**
  - Store extracted facts linked to source memories
  - Fields: `id`, `memory_id` (FK), `subject`, `predicate`, `object`, `fact_type`, `confidence`, `decay_rate`, `last_reinforced`, `source_quote`
  - `fact_type` enum: `kv`, `relationship`, `preference`, `temporal`, `identity`, `location`, `decision`, `state`
  - CRUD operations for facts

- **Embeddings table**
  - Store embedding vectors as BLOBs (float32 arrays, 384 dimensions for MiniLM)
  - `memory_id` (PK, FK), `vector` (BLOB)
  - Insert and retrieve embeddings by memory ID
  - Brute-force cosine similarity search (scan all embeddings, return top-K)

- **Content deduplication**
  - SHA-256 hash of content stored in `content_hash` column
  - On import: check hash — if exists, skip; if source matches but hash changed, update
  - UNIQUE constraint on `content_hash`

### Should Have (P1)

- **Recall logging table**
  - `recall_log`: `id`, `fact_id` (FK), `recalled_at`, `context`, `session_id`, `lens`
  - Log every time a fact is retrieved via search
  - Used for provenance chains and confidence reinforcement

- **Memory events table** (for differential memory)
  - `memory_events`: `id`, `event_type`, `fact_id`, `old_value`, `new_value`, `source`, `created_at`
  - Append-only log of all changes
  - Event types: `add`, `update`, `merge`, `decay`, `delete`, `reinforce`

- **Batch operations**
  - `CreateBatch(ctx, []Memory) → ([]int64, error)` — bulk insert within a transaction
  - Important for large imports (1000+ memories)

### Future (P2)

- **Snapshots table** for point-in-time restore
- **Lenses table** for context-dependent views
- **Migration versioning** — numbered migrations with up/down
- **Connection pooling** for concurrent access patterns

---

## Technical Design

### Database Schema

```sql
-- Enable WAL mode for concurrent reads
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- Core memory table
CREATE TABLE IF NOT EXISTS memories (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    content      TEXT NOT NULL,
    source_file  TEXT,
    source_line  INTEGER,
    source_section TEXT,
    content_hash TEXT UNIQUE NOT NULL,
    imported_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at   DATETIME  -- soft delete
);

-- Full-text search index
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content,
    content=memories,
    content_rowid=id,
    tokenize='porter unicode61'
);

-- Keep FTS in sync
CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.id, old.content);
END;
CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.id, old.content);
    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
END;

-- Extracted facts
CREATE TABLE IF NOT EXISTS facts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id       INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    subject         TEXT,
    predicate       TEXT,
    object          TEXT,
    fact_type       TEXT NOT NULL CHECK(fact_type IN ('kv','relationship','preference','temporal','identity','location','decision','state')),
    confidence      REAL DEFAULT 1.0,
    decay_rate      REAL DEFAULT 0.01,
    last_reinforced DATETIME DEFAULT CURRENT_TIMESTAMP,
    source_quote    TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_facts_memory_id ON facts(memory_id);
CREATE INDEX IF NOT EXISTS idx_facts_type ON facts(fact_type);
CREATE INDEX IF NOT EXISTS idx_facts_subject ON facts(subject);

-- Embedding vectors for semantic search
CREATE TABLE IF NOT EXISTS embeddings (
    memory_id INTEGER PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    vector    BLOB NOT NULL
);

-- Recall log for provenance tracking
CREATE TABLE IF NOT EXISTS recall_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fact_id     INTEGER REFERENCES facts(id) ON DELETE CASCADE,
    recalled_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    context     TEXT,
    session_id  TEXT,
    lens        TEXT
);

-- Memory event log for differential memory
CREATE TABLE IF NOT EXISTS memory_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL CHECK(event_type IN ('add','update','merge','decay','delete','reinforce')),
    fact_id    INTEGER,
    old_value  TEXT,
    new_value  TEXT,
    source     TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Go Interfaces

```go
package store

import (
    "context"
    "time"
)

// Memory represents a single imported memory unit.
type Memory struct {
    ID            int64
    Content       string
    SourceFile    string
    SourceLine    int
    SourceSection string
    ContentHash   string
    ImportedAt    time.Time
    UpdatedAt     time.Time
}

// Fact represents an extracted fact from a memory.
type Fact struct {
    ID             int64
    MemoryID       int64
    Subject        string
    Predicate      string
    Object         string
    FactType       string
    Confidence     float64
    DecayRate      float64
    LastReinforced time.Time
    SourceQuote    string
    CreatedAt      time.Time
}

// ListOpts controls pagination and filtering for List operations.
type ListOpts struct {
    Limit    int
    Offset   int
    SortBy   string // "date", "confidence", "recalls"
    FactType string // filter by fact type
}

// SearchResult holds a BM25 search result with score and snippet.
type SearchResult struct {
    Memory  Memory
    Score   float64
    Snippet string
}

// Store defines the core storage interface.
type Store interface {
    // Memory CRUD
    Create(ctx context.Context, m Memory) (int64, error)
    Read(ctx context.Context, id int64) (*Memory, error)
    Update(ctx context.Context, m Memory) error
    Delete(ctx context.Context, id int64) error
    List(ctx context.Context, opts ListOpts) ([]Memory, error)

    // Batch operations
    CreateBatch(ctx context.Context, memories []Memory) ([]int64, error)

    // FTS5 search
    SearchFTS(ctx context.Context, query string, limit int) ([]SearchResult, error)

    // Facts
    CreateFact(ctx context.Context, f Fact) (int64, error)
    GetFactsByMemory(ctx context.Context, memoryID int64) ([]Fact, error)
    UpdateFactConfidence(ctx context.Context, factID int64, confidence float64) error

    // Embeddings
    StoreEmbedding(ctx context.Context, memoryID int64, vector []float32) error
    GetEmbedding(ctx context.Context, memoryID int64) ([]float32, error)
    SearchEmbeddings(ctx context.Context, query []float32, limit int) ([]SearchResult, error)

    // Deduplication
    FindByHash(ctx context.Context, hash string) (*Memory, error)

    // Lifecycle
    Close() error
}

// New creates a new Store backed by SQLite at the given path.
// Pass ":memory:" for in-memory databases (testing).
func New(dbPath string) (Store, error) {
    // Implementation here
}
```

### Key Implementation Details

1. **Database path resolution:**
   ```
   CLI --db flag → CORTEX_DB env var → ~/.cortex/cortex.db
   ```
   Create `~/.cortex/` directory if it doesn't exist.

2. **WAL mode:** Set `PRAGMA journal_mode=WAL` immediately after opening the connection. This allows concurrent reads while writing.

3. **Embedding search:** Brute-force cosine similarity. Load all embeddings into memory, compute cosine similarity against query vector, return top-K. This is O(n) but fast enough for <100K entries. If scale becomes an issue, migrate to LanceDB (see ADR-002).

4. **Content hashing:** SHA-256 of the content string. Used for deduplication on re-import.

5. **Soft deletes:** `Delete()` sets `deleted_at` timestamp. All queries filter out `deleted_at IS NOT NULL` by default. Hard delete available as separate method.

6. **Pure Go SQLite:** Use `modernc.org/sqlite` — pure Go, no CGO, cross-compiles everywhere.

---

## Test Strategy

### Unit Tests (`internal/store/store_test.go`)

- **TestNew** — creates database, runs migrations, verifies tables exist
- **TestCreate** — insert a memory, verify ID returned and content stored
- **TestRead** — insert then read, verify all fields
- **TestUpdate** — insert, update content, verify updated_at changes
- **TestDelete** — insert, soft delete, verify not returned by List but still in DB
- **TestList** — insert multiple, test pagination (limit/offset), sorting
- **TestCreateBatch** — insert 100 memories in one call, verify all created
- **TestSearchFTS** — insert memories, search with keyword, verify ranked results
- **TestSearchFTS_BooleanQuery** — test AND, OR, NOT, quoted phrases
- **TestSearchFTS_Snippets** — verify snippet generation with highlights
- **TestCreateFact** — insert fact linked to memory, verify FK constraint
- **TestStoreEmbedding** — store float32 vector, retrieve, verify identical
- **TestSearchEmbeddings** — store multiple embeddings, search by cosine similarity, verify ranking
- **TestFindByHash** — test deduplication lookup
- **TestDuplicateHash** — inserting same content twice returns error or skips
- **TestWALMode** — verify WAL mode is enabled after init

### Integration Tests

- **TestConcurrentReads** — multiple goroutines reading while one writes
- **TestLargeImport** — insert 10,000 memories, verify performance
- **TestDatabasePortability** — create DB, close, copy file, reopen from new path

### Test Setup

```go
func newTestStore(t *testing.T) Store {
    t.Helper()
    s, err := New(":memory:")
    if err != nil {
        t.Fatalf("failed to create test store: %v", err)
    }
    t.Cleanup(func() { s.Close() })
    return s
}
```

---

## Open Questions

1. **Embedding dimensions:** Hardcode 384 (MiniLM) or make configurable for future models?
2. **FTS5 tokenizer:** `porter unicode61` is a good default — should we support configurable tokenizers?
3. **Batch size:** What's the optimal transaction batch size for large imports? (Test 100, 500, 1000)
4. **Vacuum strategy:** When should we run `VACUUM`? After large deletes? On a schedule?
