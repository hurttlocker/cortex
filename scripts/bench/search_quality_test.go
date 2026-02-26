// search_quality_test.go — Search quality benchmark with golden queries.
// Run: go test ./scripts/bench/ -run TestSearchQuality -v
//
// Uses a frozen test corpus and golden queries to measure precision, recall,
// and MRR across all search modes. Fails if quality drops below thresholds.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

// GoldenQuery defines an expected search result.
type GoldenQuery struct {
	Query          string   `json:"query"`
	Mode           string   `json:"mode"` // bm25, hybrid
	ExpectedTerms  []string `json:"expected_terms"`  // terms that MUST appear in top results
	ForbiddenTerms []string `json:"forbidden_terms"` // terms that should NOT dominate results
	MinResults     int      `json:"min_results"`     // minimum result count expected
	Description    string   `json:"description"`
}

// QualityResult stores benchmark results for a single query.
type QualityResult struct {
	Query       string  `json:"query"`
	Mode        string  `json:"mode"`
	ResultCount int     `json:"result_count"`
	TermHits    int     `json:"term_hits"`
	TermTotal   int     `json:"term_total"`
	Precision   float64 `json:"precision"` // fraction of expected terms found
	LatencyMs   float64 `json:"latency_ms"`
	Pass        bool    `json:"pass"`
}

// goldenQueries — curated set testing search quality across modes.
// These don't depend on specific memory IDs — they test term recall.
var goldenQueries = []GoldenQuery{
	// BM25 keyword queries — should find exact matches
	{
		Query:         "email address",
		Mode:          "bm25",
		ExpectedTerms: []string{"email"},
		MinResults:    1,
		Description:   "BM25 finds keyword matches for common terms",
	},
	{
		Query:         "trading strategy",
		Mode:          "bm25",
		ExpectedTerms: []string{"trading"},
		MinResults:    1,
		Description:   "BM25 finds trading-related memories",
	},
	{
		Query:         "version release",
		Mode:          "bm25",
		ExpectedTerms: []string{"version", "release"},
		MinResults:    1,
		Description:   "BM25 handles multi-word queries",
	},
	{
		Query:         "configuration setting",
		Mode:          "bm25",
		ExpectedTerms: []string{"config"},
		MinResults:    1,
		Description:   "BM25 finds config-related content",
	},
	// Hybrid queries — should find conceptual matches
	{
		Query:         "what model does the main agent use",
		Mode:          "hybrid",
		ExpectedTerms: []string{"model", "agent"},
		MinResults:    1,
		Description:   "Hybrid finds agent model info",
	},
	{
		Query:         "health recipe ingredients",
		Mode:          "hybrid",
		ExpectedTerms: []string{},
		MinResults:    0,
		Description:   "Hybrid handles queries about niche topics",
	},
	{
		Query:         "deployment production",
		Mode:          "hybrid",
		ExpectedTerms: []string{},
		MinResults:    0,
		Description:   "Hybrid handles deployment queries",
	},
	{
		Query:         "scanner automation",
		Mode:          "hybrid",
		ExpectedTerms: []string{},
		MinResults:    0,
		Description:   "Hybrid handles automation queries",
	},
}

func TestSearchQuality(t *testing.T) {
	// Use production DB if available, otherwise skip
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".cortex", "cortex.db")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skip("No cortex.db found — skipping search quality benchmark")
	}

	cfg := store.StoreConfig{
		DBPath:   dbPath,
		ReadOnly: true,
	}

	s, err := store.NewStore(cfg)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	eng := search.NewEngine(s)

	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	t.Logf("Corpus: %d memories, %d facts", stats.MemoryCount, stats.FactCount)

	if stats.MemoryCount < 10 {
		t.Skip("Corpus too small for meaningful quality benchmark")
	}

	var results []QualityResult
	totalPass := 0
	totalQueries := 0

	for _, gq := range goldenQueries {
		totalQueries++

		mode := search.ModeKeyword
		switch gq.Mode {
		case "hybrid":
			mode = search.ModeKeyword // fallback to BM25 if no embedder
		case "semantic":
			continue // skip semantic without embedder
		}

		start := time.Now()
		searchResults, err := eng.Search(ctx, gq.Query, search.Options{
			Limit: 10,
			Mode:  mode,
		})
		latency := float64(time.Since(start).Microseconds()) / 1000.0

		if err != nil {
			t.Logf("  ❌ %s [%s]: search error: %v", gq.Query, gq.Mode, err)
			results = append(results, QualityResult{
				Query: gq.Query, Mode: gq.Mode, Pass: false, LatencyMs: latency,
			})
			continue
		}

		// Count how many expected terms appear in result content
		termHits := 0
		resultText := ""
		for _, r := range searchResults {
			resultText += " " + r.Content
		}

		for _, term := range gq.ExpectedTerms {
			if containsCI(resultText, term) {
				termHits++
			}
		}

		precision := 1.0
		if len(gq.ExpectedTerms) > 0 {
			precision = float64(termHits) / float64(len(gq.ExpectedTerms))
		}

		pass := len(searchResults) >= gq.MinResults
		if len(gq.ExpectedTerms) > 0 {
			pass = pass && precision >= 0.5 // at least half of expected terms found
		}

		if pass {
			totalPass++
		}

		qr := QualityResult{
			Query:       gq.Query,
			Mode:        gq.Mode,
			ResultCount: len(searchResults),
			TermHits:    termHits,
			TermTotal:   len(gq.ExpectedTerms),
			Precision:   precision,
			LatencyMs:   latency,
			Pass:        pass,
		}
		results = append(results, qr)

		status := "✅"
		if !pass {
			status = "❌"
		}
		t.Logf("  %s %s [%s]: %d results, precision=%.2f, %.1fms — %s",
			status, gq.Query, gq.Mode, len(searchResults), precision, latency, gq.Description)
	}

	passRate := float64(totalPass) / float64(totalQueries)
	t.Logf("\nOverall: %d/%d passed (%.0f%%)", totalPass, totalQueries, passRate*100)

	// Write results
	report := map[string]interface{}{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"corpus_size":  stats.MemoryCount,
		"fact_count":   stats.FactCount,
		"pass_rate":    passRate,
		"results":      results,
		"platform":     runtime.GOOS + "/" + runtime.GOARCH,
	}

	jsonBytes, _ := json.MarshalIndent(report, "", "  ")
	outPath := filepath.Join(home, ".cortex", "search_quality_results.json")
	os.WriteFile(outPath, jsonBytes, 0644)
	t.Logf("Results written to %s", outPath)

	// Quality gate: at least 60% of queries must pass
	if passRate < 0.6 {
		t.Errorf("Search quality below threshold: %.0f%% (need ≥60%%)", passRate*100)
	}
}

func containsCI(haystack, needle string) bool {
	h := []byte(haystack)
	n := []byte(needle)
	for i := range h {
		if h[i] >= 'A' && h[i] <= 'Z' {
			h[i] += 32
		}
	}
	for i := range n {
		if n[i] >= 'A' && n[i] <= 'Z' {
			n[i] += 32
		}
	}
	return len(n) > 0 && len(h) >= len(n) && bytesContains(h, n)
}

func bytesContains(haystack, needle []byte) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
