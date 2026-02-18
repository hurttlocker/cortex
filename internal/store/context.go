package store

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ContextPrefix generates a human-readable context prefix for embedding enrichment.
// This prefix is prepended to memory content before generating embeddings, giving
// the embedding model topic/source signal that the raw chunk text may lack.
//
// Format: "[filename > Section Header] " or "[filename] " if no section.
// Example: "[2026-02-18 > Cortex v0.1.3 — Tier 1 Audit Fixes] "
func ContextPrefix(sourceFile, sourceSection string) string {
	stem := FilenameStem(sourceFile)

	if sourceSection != "" && stem != "" {
		return fmt.Sprintf("[%s > %s] ", stem, sourceSection)
	}
	if sourceSection != "" {
		return fmt.Sprintf("[%s] ", sourceSection)
	}
	if stem != "" {
		return fmt.Sprintf("[%s] ", stem)
	}
	return ""
}

// EnrichedContent returns memory content with context prefix prepended.
// Used for embedding generation to carry source/section signal in the vector.
func EnrichedContent(content, sourceFile, sourceSection string) string {
	prefix := ContextPrefix(sourceFile, sourceSection)
	if prefix == "" {
		return content
	}
	return prefix + content
}

// FilenameStem extracts the filename without extension from an absolute or relative path.
// Returns empty string for empty input.
//
// Examples:
//
//	"/Users/q/memory/2026-02-18.md" → "2026-02-18"
//	"docs/setup-guide.md"           → "setup-guide"
//	"MEMORY.md"                     → "MEMORY"
//	""                              → ""
func FilenameStem(path string) string {
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == "/" {
		return ""
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		return ""
	}
	return stem
}
