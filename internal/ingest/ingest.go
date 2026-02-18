package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hurttlocker/cortex/internal/store"
)

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
func (e *Engine) ImportFile(ctx context.Context, path string, opts ImportOptions) (*ImportResult, error) {
	result := &ImportResult{FilesScanned: 1}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	// Check file exists and is not a directory
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return e.ImportDir(ctx, absPath, opts)
	}

	// Check file size
	maxSize := opts.MaxFileSize
	if maxSize <= 0 {
		maxSize = DefaultMaxFileSize
	}
	if info.Size() > maxSize {
		result.FilesSkipped++
		result.Errors = append(result.Errors, ImportError{
			File:    absPath,
			Message: fmt.Sprintf("file too large (%d bytes, max %d)", info.Size(), maxSize),
		})
		return result, nil
	}

	// Reject binary files
	if isBinaryFile(absPath) {
		result.FilesSkipped++
		result.Errors = append(result.Errors, ImportError{
			File:    absPath,
			Message: "binary file detected — skipping (only text files are supported)",
		})
		return result, nil
	}

	// Find appropriate importer
	importer := e.detectImporter(absPath)
	if importer == nil {
		// Fallback: try content sniffing
		importer = e.sniffFormat(absPath)
	}
	if importer == nil {
		result.FilesSkipped++
		result.Errors = append(result.Errors, ImportError{
			File:    absPath,
			Message: "no importer found for this format",
		})
		return result, nil
	}

	// Parse file into memory chunks
	rawMemories, err := importer.Import(ctx, absPath)
	if err != nil {
		result.FilesSkipped++
		result.Errors = append(result.Errors, ImportError{
			File:    absPath,
			Message: fmt.Sprintf("import error: %v", err),
		})
		return result, nil
	}

	if len(rawMemories) == 0 {
		result.FilesSkipped++
		return result, nil
	}

	result.FilesImported++

	// Process each memory chunk: dedup + store
	for _, raw := range rawMemories {
		err := e.processMemory(ctx, raw, opts, result)
		if err != nil {
			result.Errors = append(result.Errors, ImportError{
				File:    raw.SourceFile,
				Line:    raw.SourceLine,
				Message: fmt.Sprintf("storage error: %v", err),
			})
		}
	}

	return result, nil
}

// ImportDir imports all files in a directory.
func (e *Engine) ImportDir(ctx context.Context, dir string, opts ImportOptions) (*ImportResult, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving dir: %w", err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return e.ImportFile(ctx, absDir, opts)
	}

	// Collect files to import
	var files []string
	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Skip hidden files and directories
		name := d.Name()
		if strings.HasPrefix(name, ".") && path != absDir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// If not recursive, skip subdirectories
		if !opts.Recursive && d.IsDir() && path != absDir {
			return filepath.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		files = append(files, path)
		return nil
	}

	if err := filepath.WalkDir(absDir, walkFn); err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	result := &ImportResult{}
	total := len(files)

	for i, file := range files {
		if opts.ProgressFn != nil {
			opts.ProgressFn(i+1, total, file)
		}

		// Check if binary
		if isBinaryFile(file) {
			result.FilesScanned++
			result.FilesSkipped++
			continue
		}

		fileResult, err := e.ImportFile(ctx, file, opts)
		if err != nil {
			result.FilesScanned++
			result.FilesSkipped++
			result.Errors = append(result.Errors, ImportError{
				File:    file,
				Message: fmt.Sprintf("import error: %v", err),
			})
			continue
		}

		result.Add(fileResult)
	}

	return result, nil
}

// processMemory handles dedup and storage for a single memory chunk.
func (e *Engine) processMemory(ctx context.Context, raw RawMemory, opts ImportOptions, result *ImportResult) error {
	hash := store.HashMemoryContent(raw.Content, raw.SourceFile)

	// Check for existing memory with same hash (dedup)
	existing, err := e.store.FindByHash(ctx, hash)
	if err != nil {
		return err
	}

	if existing != nil {
		result.MemoriesUnchanged++
		return nil
	}

	if opts.DryRun {
		result.MemoriesNew++
		return nil
	}

	// Build store Memory
	mem := &store.Memory{
		Content:       raw.Content,
		SourceFile:    raw.SourceFile,
		SourceLine:    raw.SourceLine,
		SourceSection: raw.SourceSection,
		ContentHash:   hash,
	}

	_, err = e.store.AddMemory(ctx, mem)
	if err != nil {
		return err
	}

	result.MemoriesNew++
	return nil
}

// detectImporter finds an importer by file extension.
func (e *Engine) detectImporter(path string) Importer {
	for _, imp := range e.importers {
		if imp.CanHandle(path) {
			return imp
		}
	}
	return nil
}

// sniffFormat attempts to detect the format by reading file content.
func (e *Engine) sniffFormat(path string) Importer {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}

	// Try JSON
	if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[") {
		var js json.RawMessage
		if json.Unmarshal(data, &js) == nil {
			return &JSONImporter{}
		}
	}

	// Check for Markdown indicators
	if strings.Contains(content, "\n## ") || strings.Contains(content, "\n# ") ||
		strings.HasPrefix(content, "# ") || strings.HasPrefix(content, "## ") {
		return &MarkdownImporter{}
	}

	// Check for YAML indicators
	if strings.HasPrefix(content, "---\n") {
		return &YAMLImporter{}
	}

	// Fallback to plain text
	return &PlainTextImporter{}
}

// Hash function moved to store.HashMemoryContent for shared usage.

// isBinaryFile checks if a file appears to be binary by reading the first 512 bytes.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return false
	}

	buf = buf[:n]

	// Check if content is valid UTF-8 and doesn't contain null bytes
	if !utf8.Valid(buf) {
		return true
	}
	for _, b := range buf {
		if b == 0 {
			return true
		}
	}

	return false
}

// FormatImportResult returns a human-readable summary of an import result.
func FormatImportResult(r *ImportResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Import complete (%s)\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("  Files:    %d scanned, %d imported, %d skipped\n",
		r.FilesScanned, r.FilesImported, r.FilesSkipped))
	sb.WriteString(fmt.Sprintf("  Memories: %d new, %d updated, %d unchanged\n",
		r.MemoriesNew, r.MemoriesUpdated, r.MemoriesUnchanged))

	if len(r.Errors) > 0 {
		sb.WriteString(fmt.Sprintf("  Errors:   %d\n", len(r.Errors)))
		for _, e := range r.Errors {
			if e.Line > 0 {
				sb.WriteString(fmt.Sprintf("    - %s:%d: %s\n", e.File, e.Line, e.Message))
			} else {
				sb.WriteString(fmt.Sprintf("    - %s: %s\n", e.File, e.Message))
			}
		}
	}

	return sb.String()
}
