// Package ingest provides the import engine for Cortex.
// It parses files in multiple formats, chunks them into memory units,
// preserves provenance (source file, line number, section header, timestamp),
// and feeds them into the storage layer.
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
	// CanHandle returns true if this importer supports the given file path/content.
	CanHandle(path string) bool

	// Import parses the file and returns memory chunks.
	Import(ctx context.Context, path string) ([]RawMemory, error)
}

// ImportResult summarizes an import operation.
type ImportResult struct {
	FilesScanned      int
	FilesImported     int
	FilesSkipped      int
	MemoriesNew       int
	MemoriesUpdated   int
	MemoriesUnchanged int
	Errors            []ImportError
}

// Add merges another ImportResult into this one.
func (r *ImportResult) Add(other *ImportResult) {
	r.FilesScanned += other.FilesScanned
	r.FilesImported += other.FilesImported
	r.FilesSkipped += other.FilesSkipped
	r.MemoriesNew += other.MemoriesNew
	r.MemoriesUpdated += other.MemoriesUpdated
	r.MemoriesUnchanged += other.MemoriesUnchanged
	r.Errors = append(r.Errors, other.Errors...)
}

// ImportError records a non-fatal error during import.
type ImportError struct {
	File    string
	Line    int
	Message string
}

// ImportOptions configures an import operation.
type ImportOptions struct {
	Recursive   bool
	DryRun      bool
	MaxFileSize int64       // bytes, default 10MB
	Project     string      // Project tag to assign to imported memories
	MemoryClass string      // Optional class to assign (rule, decision, preference, identity, status, scratch)
	AutoTag     bool        // Infer project from file paths using default rules
	Metadata    interface{} // *store.Metadata — stored as interface{} to avoid circular import
	ProgressFn  func(current, total int, file string)
}

// DefaultMaxFileSize is 10MB.
const DefaultMaxFileSize = 10 * 1024 * 1024
