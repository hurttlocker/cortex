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

	"github.com/hurttlocker/cortex/internal/extract"
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

	// Check path metadata and guard against symlinked directories.
	pathInfo, err := os.Lstat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	info := pathInfo
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		targetInfo, targetErr := os.Stat(absPath)
		if targetErr != nil {
			return nil, fmt.Errorf("stat symlink target %s: %w", path, targetErr)
		}
		if targetInfo.IsDir() {
			return nil, fmt.Errorf("symlinked directory import is not supported: %s", absPath)
		}
		info = targetInfo
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
		result.Errors = append(result.Errors, ImportError{
			File:    absPath,
			Message: "empty or whitespace-only content — nothing to import",
		})
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

	result := &ImportResult{}

	// Collect files to import
	var files []string
	walkFn := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Errors = append(result.Errors, ImportError{
				File:    path,
				Message: fmt.Sprintf("walk error: %v", walkErr),
			})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d == nil {
			return nil
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

		// Guard against symlinked directories to prevent recursive cycles.
		if d.Type()&os.ModeSymlink != 0 {
			targetInfo, statErr := os.Stat(path)
			if statErr != nil {
				result.Errors = append(result.Errors, ImportError{
					File:    path,
					Message: fmt.Sprintf("symlink target stat error: %v", statErr),
				})
				return nil
			}
			if targetInfo.IsDir() {
				result.Errors = append(result.Errors, ImportError{
					File:    path,
					Message: "symlinked directory skipped (potential recursion cycle)",
				})
				return nil
			}
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

	// Apply include/exclude extension filters
	if len(opts.Include) > 0 || len(opts.Exclude) > 0 {
		files = filterByExtension(files, opts.Include, opts.Exclude)
	}

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

// normalizeExt ensures an extension starts with "." and is lowercase.
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

// filterByExtension filters file paths by include/exclude extension lists.
// If include is non-empty, only files whose extension matches one in include are kept.
// If exclude is non-empty, files whose extension matches one in exclude are removed.
// Include is applied first (allowlist), then exclude (blocklist).
func filterByExtension(files []string, include, exclude []string) []string {
	incSet := make(map[string]bool, len(include))
	for _, ext := range include {
		incSet[normalizeExt(ext)] = true
	}
	excSet := make(map[string]bool, len(exclude))
	for _, ext := range exclude {
		excSet[normalizeExt(ext)] = true
	}

	var filtered []string
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if len(incSet) > 0 && !incSet[ext] {
			continue
		}
		if excSet[ext] {
			continue
		}
		filtered = append(filtered, f)
	}
	return filtered
}

// processMemory handles dedup and storage for a single memory chunk.
func (e *Engine) processMemory(ctx context.Context, raw RawMemory, opts ImportOptions, result *ImportResult) error {
	opts.Normalize()
	hash := store.HashMemoryContent(raw.Content, raw.SourceFile)

	// Check for existing memory with same hash (dedup)
	existing, err := e.store.FindByHash(ctx, hash)
	if err != nil {
		return err
	}

	if existing != nil {
		// If new metadata is provided, update the existing memory's metadata (#53)
		if opts.Metadata != nil {
			if meta, ok := opts.Metadata.(*store.Metadata); ok && meta != nil {
				if err := e.store.UpdateMemoryMetadata(ctx, existing.ID, meta); err != nil {
					return fmt.Errorf("updating metadata on existing memory: %w", err)
				}
				result.MemoriesUpdated++
				return nil
			}
		}
		result.MemoriesUnchanged++
		return nil
	}

	if shouldSkipLowSignalCapture(raw.Content, opts) {
		result.MemoriesUnchanged++
		return nil
	}

	if opts.CaptureDedupeEnabled {
		isNearDup, _, _, err := findNearDuplicate(ctx, e.store, raw.Content, opts)
		if err != nil {
			return err
		}
		if isNearDup {
			result.MemoriesNearDuped++
			result.MemoriesUnchanged++
			return nil
		}
	}

	if opts.DryRun {
		result.MemoriesNew++
		return nil
	}

	// Determine project tag
	project := opts.Project
	if project == "" && opts.AutoTag {
		project = store.InferProject(raw.SourceFile, store.DefaultProjectRules)
	}

	// Determine memory class
	memoryClass := store.NormalizeMemoryClass(opts.MemoryClass)
	if memoryClass != "" {
		if !store.IsValidMemoryClass(memoryClass) {
			return fmt.Errorf("invalid memory class %q", opts.MemoryClass)
		}
	} else {
		memoryClass = extract.AutoClassifyMemoryClass(raw.Content, raw.SourceSection)
	}

	// Build store Memory
	mem := &store.Memory{
		Content:       raw.Content,
		SourceFile:    raw.SourceFile,
		SourceLine:    raw.SourceLine,
		SourceSection: raw.SourceSection,
		ContentHash:   hash,
		Project:       project,
		MemoryClass:   memoryClass,
	}

	// Attach metadata if provided (Issue #30)
	if opts.Metadata != nil {
		if meta, ok := opts.Metadata.(*store.Metadata); ok {
			mem.Metadata = meta
		}
	}

	_, err = e.store.AddMemory(ctx, mem)
	if err != nil {
		if isDuplicateMemoryInsert(err) {
			existing, findErr := e.store.FindByHash(ctx, hash)
			if findErr != nil {
				return findErr
			}
			if existing != nil && opts.Metadata != nil {
				if meta, ok := opts.Metadata.(*store.Metadata); ok && meta != nil {
					if err := e.store.UpdateMemoryMetadata(ctx, existing.ID, meta); err != nil {
						return fmt.Errorf("updating metadata on existing memory: %w", err)
					}
					result.MemoriesUpdated++
					return nil
				}
			}
			result.MemoriesUnchanged++
			return nil
		}
		return err
	}

	result.MemoriesNew++
	return nil
}

func isDuplicateMemoryInsert(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "constraint") {
		return false
	}
	if strings.Contains(msg, "unique") && strings.Contains(msg, "memories.content_hash") {
		return true
	}
	if strings.Contains(msg, "constraint failed") && strings.Contains(msg, "memories.content_hash") {
		return true
	}
	return false
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
	if r.MemoriesNearDuped > 0 {
		sb.WriteString(fmt.Sprintf("  Hygiene:  %d near-duplicates suppressed\n", r.MemoriesNearDuped))
	}

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
