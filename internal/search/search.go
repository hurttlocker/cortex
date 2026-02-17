// Package search provides search capabilities for Cortex.
//
// Two search modes, both fully local:
// - BM25 keyword search via SQLite FTS5
// - Semantic search via local ONNX embeddings (all-MiniLM-L6-v2) [Phase 2]
//
// The default hybrid mode combines both using reciprocal rank fusion,
// giving users the best of exact keyword matching and conceptual similarity.
package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
)

// Mode specifies the search strategy.
type Mode string

const (
	ModeKeyword  Mode = "keyword"
	ModeSemantic Mode = "semantic"
	ModeHybrid   Mode = "hybrid"
)

// ParseMode converts a string to a Mode, returning an error for invalid values.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(s) {
	case "keyword", "bm25":
		return ModeKeyword, nil
	case "semantic":
		return ModeSemantic, nil
	case "hybrid":
		return ModeHybrid, nil
	default:
		return "", fmt.Errorf("invalid search mode %q (valid: keyword, semantic, hybrid)", s)
	}
}

// Options configures a search query.
type Options struct {
	Mode          Mode    // Search mode (default: keyword)
	Limit         int     // Max results (default: 10)
	MinConfidence float64 // Minimum confidence threshold (default: 0.0)
}

// DefaultOptions returns sensible defaults for Phase 1 (keyword-only).
func DefaultOptions() Options {
	return Options{
		Mode:          ModeKeyword,
		Limit:         10,
		MinConfidence: 0.0,
	}
}

// Result represents a single search result.
type Result struct {
	Content       string  `json:"content"`
	SourceFile    string  `json:"source_file"`
	SourceLine    int     `json:"source_line"`
	SourceSection string  `json:"source_section,omitempty"`
	Score         float64 `json:"score"`
	Snippet       string  `json:"snippet,omitempty"`
	MatchType     string  `json:"match_type"` // "bm25", "semantic", "hybrid"
	MemoryID      int64   `json:"memory_id"`
}

// Searcher performs searches across the memory store.
type Searcher interface {
	Search(ctx context.Context, query string, opts Options) ([]Result, error)
}

// Engine implements Searcher with BM25 search (and semantic stub for Phase 2).
type Engine struct {
	store store.Store
}

// NewEngine creates a search engine backed by the given store.
func NewEngine(s store.Store) *Engine {
	return &Engine{store: s}
}

// Search performs a search using the specified mode.
func (e *Engine) Search(ctx context.Context, query string, opts Options) ([]Result, error) {
	if query == "" {
		return nil, nil
	}

	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	switch opts.Mode {
	case ModeKeyword, "":
		return e.searchBM25(ctx, query, opts)
	case ModeSemantic:
		return nil, fmt.Errorf("semantic search requires ONNX embeddings (coming in Phase 2)")
	case ModeHybrid:
		// Phase 1: hybrid falls back to keyword-only
		results, err := e.searchBM25(ctx, query, opts)
		if err != nil {
			return nil, err
		}
		return results, nil
	default:
		return nil, fmt.Errorf("unknown search mode: %q", opts.Mode)
	}
}

// searchBM25 performs keyword search using the store's FTS5 capability.
func (e *Engine) searchBM25(ctx context.Context, query string, opts Options) ([]Result, error) {
	// Sanitize query to prevent FTS5 syntax errors from crashing
	sanitized := sanitizeFTSQuery(query)
	if sanitized == "" {
		return nil, nil
	}

	storeResults, err := e.store.SearchFTS(ctx, sanitized, opts.Limit)
	if err != nil {
		// If the query has bad FTS5 syntax, try a simpler fallback
		if isFTSSyntaxError(err) {
			// Escape the query as a simple term search
			escaped := escapeFTSQuery(query)
			storeResults, err = e.store.SearchFTS(ctx, escaped, opts.Limit)
			if err != nil {
				return nil, fmt.Errorf("search failed: %w", err)
			}
		} else {
			return nil, fmt.Errorf("search failed: %w", err)
		}
	}

	results := make([]Result, 0, len(storeResults))
	for _, sr := range storeResults {
		// FTS5 rank is negative (more negative = better match).
		// Convert to positive score where higher = better.
		score := normalizeBM25Score(sr.Score)

		if score < opts.MinConfidence {
			continue
		}

		r := Result{
			Content:       sr.Memory.Content,
			SourceFile:    sr.Memory.SourceFile,
			SourceLine:    sr.Memory.SourceLine,
			SourceSection: sr.Memory.SourceSection,
			Score:         score,
			Snippet:       sr.Snippet,
			MatchType:     "bm25",
			MemoryID:      sr.Memory.ID,
		}
		results = append(results, r)
	}

	// Sort by score descending (should already be sorted from FTS5, but ensure it)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// normalizeBM25Score converts FTS5's negative rank to a positive 0-1 score.
// FTS5 rank values are negative, with more negative being more relevant.
// We use: score = 1 / (1 + |rank|) which maps to (0, 1] range.
func normalizeBM25Score(rank float64) float64 {
	return 1.0 / (1.0 + math.Abs(rank))
}

// sanitizeFTSQuery performs basic sanitization of an FTS5 query.
// It trims whitespace and returns empty string for empty/whitespace-only queries.
func sanitizeFTSQuery(query string) string {
	return strings.TrimSpace(query)
}

// escapeFTSQuery wraps each word in double quotes to treat them as literal terms,
// used as a fallback when the original query has invalid FTS5 syntax.
func escapeFTSQuery(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return ""
	}

	escaped := make([]string, 0, len(words))
	for _, w := range words {
		// Strip any existing quotes and FTS5 operators
		w = strings.Trim(w, `"`)
		w = strings.TrimSpace(w)
		if w == "" || strings.EqualFold(w, "AND") || strings.EqualFold(w, "OR") || strings.EqualFold(w, "NOT") {
			continue
		}
		escaped = append(escaped, `"`+w+`"`)
	}
	return strings.Join(escaped, " ")
}

// isFTSSyntaxError checks if an error is likely an FTS5 syntax error.
func isFTSSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "fts5: syntax error") ||
		strings.Contains(msg, "FTS") ||
		strings.Contains(msg, "fts5")
}

// TruncateContent truncates content to approximately maxLen characters,
// breaking at a word boundary and appending "..." if truncated.
func TruncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// Find the last space before maxLen
	truncated := content[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}
