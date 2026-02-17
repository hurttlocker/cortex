# PRD-002: Import Engine

**Status:** Draft  
**Priority:** P0  
**Phase:** 1  
**Depends On:** PRD-001 (Storage Layer)  
**Package:** `internal/ingest/`

---

## Overview

The import engine is the primary entry point for Cortex — the headline feature that differentiates it from every competitor. It parses files in multiple formats, chunks them into memory units, preserves provenance (source file, line number, section header, timestamp), and feeds them into the storage layer. Re-importing is safe and idempotent.

## Problem

Users have accumulated months of AI agent memory in scattered formats: `MEMORY.md` files, JSON configs, YAML state files, conversation logs, CSV exports. No existing tool can ingest this data. They all say "start fresh." Cortex needs to make importing existing memory a 30-second operation.

---

## Requirements

### Must Have (P0)

- **Markdown importer** (`.md`, `.markdown`)
  - Split on `## ` (h2 headers) as primary chunk boundaries
  - If no headers, split on double newlines (paragraph boundaries)
  - Extract key:value patterns from bullets: `- Key: Value`, `- **Key:** Value`
  - Preserve header text as `source_section` metadata
  - Parse YAML front matter if present (store as metadata)
  - Detect date-based filenames (`2024-01-15.md`) and attach date metadata
  - Code blocks preserved as-is within the memory unit (never split mid-block)
  - Handle nested headers (h3, h4) as sub-sections within h2 chunks

- **JSON importer** (`.json`)
  - Array of objects → each object becomes one memory unit
  - Single object → each top-level key becomes one memory unit
  - Nested objects → flattened with dot notation (`user.preferences.theme`)
  - Detect conversation log format (`role`/`content` keys) and merge into conversation chunks
  - Pretty-print the JSON content for storage (human-readable in DB)

- **Provenance tracking** for every imported memory
  - `source_file`: absolute path of the source file
  - `source_line`: line number where this chunk starts (1-indexed)
  - `source_section`: section header (for Markdown) or JSON key path
  - `imported_at`: timestamp of import

- **Recursive directory import** (`--recursive` flag)
  - Walk directory tree, apply correct importer based on file extension
  - Respect `.gitignore` patterns (if present)
  - Respect `.cortexignore` patterns (Cortex-specific ignore file)
  - Skip binary files, images, and files over 10MB (configurable via `--max-size`)
  - Report progress: files scanned, files imported, files skipped

- **Format detection**
  - Primary: by file extension (`.md`, `.json`, `.yaml`, `.csv`, `.txt`)
  - Fallback: content sniffing (detect JSON by `{` or `[` prefix, YAML by `---` prefix)
  - Unknown formats: treat as plain text

- **Idempotent re-import**
  - Hash each memory unit (SHA-256 of content + source path)
  - If hash exists in DB → skip (no duplicate)
  - If source path exists but hash changed → update the existing entry
  - If new → insert
  - Report: `N new, N updated, N unchanged`

### Should Have (P1)

- **YAML importer** (`.yaml`, `.yml`)
  - Parse identically to JSON after YAML→JSON conversion
  - Multi-document YAML (separated by `---`) → one memory unit per document
  - Resolve anchors and aliases before import

- **CSV importer** (`.csv`, `.tsv`)
  - First row treated as headers (configurable with `--no-header`)
  - Each row becomes one memory unit
  - Headers become fact keys, cell values become fact values
  - Empty cells skipped
  - Tab-separated files auto-detected by extension

- **Plain text importer** (`.txt`, `.log`, and any unrecognized format)
  - Split on double newlines (paragraph boundaries)
  - If paragraphs >500 words, split on sentence boundaries
  - If no paragraph breaks, split on fixed token count (~256 tokens)
  - Detect chat log patterns (`Username: message`, `[timestamp] message`)

- **Progress reporting**
  - Progress bar for large imports (>10 files or >100 memory units)
  - Show: current file, files processed/total, memories extracted
  - Respect `--json` flag (emit JSON progress events instead of progress bar)

### Future (P2)

- PDF, DOCX, HTML importers (via Docling or Unstructured.io)
- Obsidian vault-specific handling (wikilinks, tags, dataview)
- Streaming import from stdin
- Watch mode (`cortex import --watch <dir>`)

---

## Technical Design

### Importer Interface

```go
package ingest

import "context"

// RawMemory is a parsed chunk of content ready for storage.
type RawMemory struct {
    Content       string            // The text content
    SourceFile    string            // Absolute path to source file
    SourceLine    int               // Starting line number (1-indexed)
    SourceSection string            // Section header or key path
    Metadata      map[string]string // Additional metadata (dates, front matter, etc.)
}

// Importer handles a specific file format.
type Importer interface {
    // CanHandle returns true if this importer supports the given file path.
    CanHandle(path string) bool

    // Import parses the file and returns memory chunks.
    Import(ctx context.Context, path string) ([]RawMemory, error)
}

// ImportResult summarizes an import operation.
type ImportResult struct {
    FilesScanned  int
    FilesImported int
    FilesSkipped  int
    MemoriesNew   int
    MemoriesUpdated int
    MemoriesUnchanged int
    Errors        []ImportError
}

// ImportError records a non-fatal error during import.
type ImportError struct {
    File    string
    Line    int
    Message string
}

// Engine orchestrates the import process.
type Engine struct {
    store     store.Store
    importers []Importer
}

// NewEngine creates an import engine with all registered importers.
func NewEngine(s store.Store) *Engine {
    return &Engine{
        store: s,
        importers: []Importer{
            &MarkdownImporter{},
            &JSONImporter{},
            &YAMLImporter{},
            &CSVImporter{},
            &PlainTextImporter{},
        },
    }
}

// ImportFile imports a single file using the appropriate importer.
func (e *Engine) ImportFile(ctx context.Context, path string) (*ImportResult, error) {
    // 1. Detect format → select importer
    // 2. Parse file → []RawMemory
    // 3. For each chunk: hash → check dedup → create/update in store
    // 4. Return result summary
}

// ImportDir recursively imports all files in a directory.
func (e *Engine) ImportDir(ctx context.Context, dir string, opts ImportDirOpts) (*ImportResult, error) {
    // Walk directory, respect ignore patterns, call ImportFile for each
}

// ImportDirOpts configures directory import behavior.
type ImportDirOpts struct {
    Recursive bool
    MaxFileSize int64 // bytes, default 10MB
    IgnorePatterns []string
    ProgressFn func(current, total int, file string) // progress callback
}
```

### Markdown Importer — Detailed Design

```go
type MarkdownImporter struct{}

func (m *MarkdownImporter) CanHandle(path string) bool {
    ext := filepath.Ext(path)
    return ext == ".md" || ext == ".markdown"
}

func (m *MarkdownImporter) Import(ctx context.Context, path string) ([]RawMemory, error) {
    // 1. Read file content
    // 2. Parse and strip YAML front matter (if present)
    // 3. Split on ## headers
    // 4. For each section:
    //    a. Record section header name
    //    b. Record starting line number
    //    c. Store content as-is (including sub-headers, bullets, code blocks)
    // 5. If no headers found, split on double newlines
    // 6. Return []RawMemory with provenance
}
```

### Chunking Rules

| Scenario | Strategy |
|----------|----------|
| Markdown with `##` headers | Split on `## `, each section = 1 chunk |
| Markdown without headers | Split on `\n\n` (paragraphs) |
| JSON array | Each array element = 1 chunk |
| JSON object | Each top-level key = 1 chunk |
| YAML multi-doc | Each document = 1 chunk |
| CSV | Each row = 1 chunk |
| Plain text | Paragraphs, then sentences if >500 words |

### Directory Walking

```go
func (e *Engine) ImportDir(ctx context.Context, dir string, opts ImportDirOpts) (*ImportResult, error) {
    // 1. Load .gitignore patterns (if exists)
    // 2. Load .cortexignore patterns (if exists)
    // 3. Walk directory tree
    // 4. For each file:
    //    a. Check against ignore patterns → skip if matched
    //    b. Check file size → skip if > maxFileSize
    //    c. Check if binary (first 512 bytes) → skip if binary
    //    d. Find matching importer → use PlainText as fallback
    //    e. Call ImportFile
    //    f. Aggregate results
    //    g. Call progressFn if provided
}
```

### Content Hashing

```go
func hashContent(content, sourcePath string) string {
    h := sha256.New()
    h.Write([]byte(sourcePath))
    h.Write([]byte{0}) // separator
    h.Write([]byte(content))
    return hex.EncodeToString(h.Sum(nil))
}
```

The hash includes the source path so the same content from two different files creates two separate memories (different provenance).

---

## Test Strategy

### Unit Tests (`internal/ingest/*_test.go`)

**Markdown Importer:**
- **TestMarkdownImport_WithHeaders** — file with h2 headers, verify correct splitting and section names
- **TestMarkdownImport_WithoutHeaders** — file with only paragraphs, verify paragraph splitting
- **TestMarkdownImport_KeyValue** — `- Key: Value` patterns extracted correctly
- **TestMarkdownImport_BoldKeyValue** — `- **Key:** Value` patterns extracted correctly
- **TestMarkdownImport_FrontMatter** — YAML front matter parsed and stored as metadata
- **TestMarkdownImport_CodeBlocks** — code blocks preserved intact, not split
- **TestMarkdownImport_NestedHeaders** — h3/h4 within h2 sections kept together
- **TestMarkdownImport_DateFilename** — `2024-01-15.md` attaches date metadata
- **TestMarkdownImport_EmptyFile** — returns empty slice, no error
- **TestMarkdownImport_LargeFile** — file with 100+ sections, verify all parsed

**JSON Importer:**
- **TestJSONImport_Array** — array of objects, each becomes a memory
- **TestJSONImport_Object** — single object, each key becomes a memory
- **TestJSONImport_Nested** — nested objects flattened with dot notation
- **TestJSONImport_ConversationLog** — `role`/`content` format merged into conversation chunks
- **TestJSONImport_InvalidJSON** — returns descriptive error
- **TestJSONImport_EmptyFile** — returns empty slice, no error

**Engine:**
- **TestEngine_FormatDetection** — correct importer selected by extension
- **TestEngine_ContentSniffing** — JSON detected when extension is wrong
- **TestEngine_Dedup_NewContent** — new content creates new memory
- **TestEngine_Dedup_SameContent** — same content skipped (unchanged count)
- **TestEngine_Dedup_UpdatedContent** — same path, different content updates
- **TestEngine_ImportDir_Recursive** — walks subdirectories
- **TestEngine_ImportDir_IgnorePatterns** — `.gitignore` patterns respected
- **TestEngine_ImportDir_SkipBinary** — binary files skipped
- **TestEngine_ImportDir_SkipLargeFiles** — files over limit skipped
- **TestEngine_ImportDir_Progress** — progress callback called with correct counts

### Integration Tests

- **TestImportSampleMemory** — import `tests/testdata/sample-memory.md`, verify all sections parsed
- **TestImportSampleJSON** — import `tests/testdata/sample-data.json`, verify structure preserved
- **TestReimportIdempotent** — import same file twice, verify no duplicates

### Test Fixtures

- `tests/testdata/sample-memory.md` — realistic MEMORY.md with sections, key:values, decisions
- `tests/testdata/sample-data.json` — nested JSON with user data, projects, decisions

---

## Open Questions

1. **Max chunk size:** Should we enforce a maximum chunk size? If a Markdown section is 10,000 words, should we split it further?
2. **Encoding detection:** Should we handle non-UTF-8 files? (Detect encoding, convert to UTF-8)
3. **Symlink handling:** Follow symlinks during directory walk? (Default: no, for safety)
4. **Import metadata:** Should we store import-level metadata (total files, duration, errors) in a separate table?
