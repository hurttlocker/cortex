package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

// newTestStore creates an in-memory store for testing.
func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedTestData inserts a standard set of test memories for search testing.
func seedTestData(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()

	memories := []*store.Memory{
		{Content: "Trading QQQ/SPY 0DTE options with careful risk management", SourceFile: "~/clawd/MEMORY.md", SourceLine: 38, SourceSection: "Trading"},
		{Content: "Completely isolated from equities — crypto directory for ADA and BTC", SourceFile: "~/clawd/memory/2026-01-28.md", SourceLine: 41, SourceSection: "Crypto"},
		{Content: "Go is a statically typed programming language with excellent concurrency", SourceFile: "~/notes/go.md", SourceLine: 1, SourceSection: "Languages"},
		{Content: "Python is dynamically typed and very popular for machine learning", SourceFile: "~/notes/python.md", SourceLine: 1, SourceSection: "Languages"},
		{Content: "Rust emphasizes memory safety without garbage collection", SourceFile: "~/notes/rust.md", SourceLine: 1, SourceSection: "Languages"},
		{Content: "Deploy the application using Docker containers on Railway", SourceFile: "~/projects/deploy.md", SourceLine: 10, SourceSection: "DevOps"},
		{Content: "The deployment process requires running tests first", SourceFile: "~/projects/deploy.md", SourceLine: 25, SourceSection: "DevOps"},
		{Content: "JavaScript runs in the browser and on Node.js server side", SourceFile: "~/notes/js.md", SourceLine: 1, SourceSection: "Languages"},
		{Content: "Memory management in Go uses a garbage collector with low latency", SourceFile: "~/notes/go.md", SourceLine: 50, SourceSection: "Runtime"},
		{Content: "Cortex is an AI agent memory tool written in Go with SQLite storage", SourceFile: "~/cortex/README.md", SourceLine: 1, SourceSection: "About"},
	}

	for _, m := range memories {
		_, err := s.AddMemory(ctx, m)
		if err != nil {
			t.Fatalf("failed to seed memory: %v", err)
		}
	}
}

// --- Engine Creation ---

func TestNewEngine(t *testing.T) {
	s := newTestStore(t)
	engine := NewEngine(s)
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
	if engine.store == nil {
		t.Fatal("expected non-nil store in engine")
	}
}

// --- BM25 Keyword Search ---

func TestSearchBM25_SingleTerm(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "Go", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'Go', got %d", len(results))
	}

	// All results should be BM25 type with positive scores
	for _, r := range results {
		if r.MatchType != "bm25" {
			t.Errorf("expected match_type 'bm25', got %q", r.MatchType)
		}
		if r.Score <= 0 {
			t.Errorf("expected positive score, got %f", r.Score)
		}
	}
}

func TestSearchBM25_MultipleTerm(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	// FTS5 implicit AND: both terms must be present
	results, err := engine.Search(ctx, "memory Go", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'memory Go'")
	}
}

func TestSearchBM25_OR(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "trading OR crypto", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'trading OR crypto', got %d", len(results))
	}
}

func TestSearchBM25_NOT(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	// Search for deployment but not Docker
	results, err := engine.Search(ctx, "deployment NOT Docker", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	for _, r := range results {
		if strings.Contains(strings.ToLower(r.Content), "docker") {
			t.Errorf("result should not contain 'Docker': %q", r.Content)
		}
	}
}

func TestSearchBM25_Phrase(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, `"garbage collection"`, Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for '\"garbage collection\"'")
	}
}

func TestSearchBM25_Prefix(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "deploy*", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	// With porter stemmer, "deploy*" matches stemmed tokens starting with "deploy"
	if len(results) < 1 {
		t.Errorf("expected at least 1 result for 'deploy*', got %d", len(results))
	}
	t.Logf("Got %d results for 'deploy*'", len(results))
}

func TestSearchBM25_Ranking(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "Go", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatal("need at least 2 results to verify ranking")
	}

	// Scores should be descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: score[%d]=%f > score[%d]=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestSearchBM25_Snippets(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "memory", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
	t.Logf("Snippet: %s", results[0].Snippet)
}

func TestSearchBM25_NoResults(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "xylophone", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for 'xylophone', got %d", len(results))
	}
}

func TestSearchBM25_EmptyQuery(t *testing.T) {
	s := newTestStore(t)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty query, got %d", len(results))
	}
}

func TestSearchBM25_WhitespaceQuery(t *testing.T) {
	s := newTestStore(t)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "   ", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for whitespace query, got %d", len(results))
	}
}

func TestSearchBM25_Limit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert many memories with a common term
	for i := 0; i < 20; i++ {
		s.AddMemory(ctx, &store.Memory{
			Content: fmt.Sprintf("Memory item number %d about testing search limits", i),
		})
	}

	engine := NewEngine(s)

	results, err := engine.Search(ctx, "testing", Options{Mode: ModeKeyword, Limit: 5})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) > 5 {
		t.Errorf("expected at most 5 results, got %d", len(results))
	}
}

func TestSearchBM25_SourceProvenance(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	results, err := engine.Search(ctx, "trading", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 'trading'")
	}

	r := results[0]
	if r.SourceFile == "" {
		t.Error("expected non-empty source_file")
	}
	if r.SourceLine <= 0 {
		t.Error("expected positive source_line")
	}
	if r.MemoryID <= 0 {
		t.Error("expected positive memory_id")
	}
	t.Logf("Result: score=%.2f file=%s line=%d section=%s",
		r.Score, r.SourceFile, r.SourceLine, r.SourceSection)
}

func TestSearchBM25_InvalidSyntaxFallback(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	// Unbalanced quotes — should not crash, should fall back
	results, err := engine.Search(ctx, `"unclosed quote`, Options{Mode: ModeKeyword, Limit: 10})
	// We accept either: results with fallback, or a non-panic error
	if err != nil {
		t.Logf("Got error (acceptable): %v", err)
	} else {
		t.Logf("Got %d results with fallback", len(results))
	}
}

// --- Semantic Search (Stub) ---

func TestSearchSemantic_FallbackToBM25(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)

	engine := NewEngine(s)
	ctx := context.Background()

	// Without an embedder, semantic search should fall back to BM25
	results, err := engine.Search(ctx, "Go programming", Options{Mode: ModeSemantic, Limit: 10})
	if err != nil {
		t.Fatalf("semantic search without embedder should fall back to BM25: %v", err)
	}

	// Should find results using BM25 fallback
	if len(results) == 0 {
		t.Error("expected fallback BM25 search to find results")
	}

	// Results should be marked as BM25 since we fell back
	for _, result := range results {
		if result.MatchType != "bm25" {
			t.Errorf("expected match type 'bm25' for fallback, got %q", result.MatchType)
		}
	}
}

// --- Hybrid Search ---

func TestSearchHybrid_FallsBackToKeyword(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	// Hybrid should work (falls back to keyword in Phase 1)
	results, err := engine.Search(ctx, "Go", Options{Mode: ModeHybrid, Limit: 10})
	if err != nil {
		t.Fatalf("hybrid search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from hybrid search (keyword fallback)")
	}
}

// --- Default Options ---

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if opts.Mode != ModeKeyword {
		t.Errorf("expected default mode 'keyword', got %q", opts.Mode)
	}
	if opts.Limit != 10 {
		t.Errorf("expected default limit 10, got %d", opts.Limit)
	}
	if opts.MinConfidence != 0.0 {
		t.Errorf("expected default min_confidence 0.0, got %f", opts.MinConfidence)
	}
}

// --- ParseMode ---

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
		err   bool
	}{
		{"keyword", ModeKeyword, false},
		{"bm25", ModeKeyword, false},
		{"semantic", ModeSemantic, false},
		{"hybrid", ModeHybrid, false},
		{"Keyword", ModeKeyword, false},
		{"HYBRID", ModeHybrid, false},
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseMode(tt.input)
			if tt.err && err == nil {
				t.Error("expected error")
			}
			if !tt.err && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Score Normalization ---

func TestNormalizeBM25Score(t *testing.T) {
	// FTS5 rank is negative, more negative = better
	tests := []struct {
		rank   float64
		wantGt float64
		wantLt float64
	}{
		{-10.0, 0.0, 1.0}, // good match
		{-1.0, 0.0, 1.0},  // decent match
		{-0.1, 0.0, 1.0},  // weak match
		{0.0, 0.99, 1.01}, // edge: score should be 1.0
	}

	for _, tt := range tests {
		score := normalizeBM25Score(tt.rank)
		if score <= tt.wantGt || score >= tt.wantLt {
			t.Errorf("normalizeBM25Score(%f) = %f, want (%f, %f)", tt.rank, score, tt.wantGt, tt.wantLt)
		}
	}
}

// --- TruncateContent ---

func TestTruncateContent(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		short  bool // if true, input should be returned as-is
	}{
		{"short", 100, true},
		{"hello world foo bar baz", 11, false},
		{"", 10, true},
	}

	for _, tt := range tests {
		got := TruncateContent(tt.input, tt.maxLen)
		if tt.short {
			if got != tt.input {
				t.Errorf("TruncateContent(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.input)
			}
		} else {
			if !strings.HasSuffix(got, "...") {
				t.Errorf("TruncateContent(%q, %d) = %q, should end with '...'", tt.input, tt.maxLen, got)
			}
		}
	}
}

// --- JSON Output ---

func TestResultJSON(t *testing.T) {
	r := Result{
		Content:    "Test content",
		SourceFile: "test.md",
		SourceLine: 42,
		Score:      0.85,
		MatchType:  "bm25",
		MemoryID:   1,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded["content"] != "Test content" {
		t.Errorf("content mismatch: %v", decoded["content"])
	}
	if decoded["source_file"] != "test.md" {
		t.Errorf("source_file mismatch: %v", decoded["source_file"])
	}
	if decoded["source_line"].(float64) != 42 {
		t.Errorf("source_line mismatch: %v", decoded["source_line"])
	}
}

func TestResultsJSON_Array(t *testing.T) {
	results := []Result{
		{Content: "First", SourceFile: "a.md", SourceLine: 1, Score: 0.9, MatchType: "bm25"},
		{Content: "Second", SourceFile: "b.md", SourceLine: 2, Score: 0.7, MatchType: "bm25"},
	}

	data, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded []map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 results, got %d", len(decoded))
	}
}

// --- MinConfidence Filter ---

func TestSearchBM25_MinConfidence(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)
	ctx := context.Background()

	// With min confidence of 0.99, should get no results (BM25 normalized scores are < 1)
	results, err := engine.Search(ctx, "Go", Options{Mode: ModeKeyword, Limit: 10, MinConfidence: 0.99})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results with high min_confidence, got %d (first score: %f)",
			len(results), results[0].Score)
	}
}

// --- Integration: Import then Search ---

func TestSearchEndToEnd(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Import specific content
	memories := []*store.Memory{
		{Content: "Philadelphia Eagles won the Super Bowl in February 2025", SourceFile: "sports.md", SourceLine: 1},
		{Content: "The stock market reached new highs in January 2025", SourceFile: "finance.md", SourceLine: 1},
		{Content: "Eagles are large birds of prey found worldwide", SourceFile: "nature.md", SourceLine: 1},
	}
	for _, m := range memories {
		if _, err := s.AddMemory(ctx, m); err != nil {
			t.Fatalf("failed to add memory: %v", err)
		}
	}

	engine := NewEngine(s)

	// Search for "Eagles"
	results, err := engine.Search(ctx, "Eagles", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'Eagles', got %d", len(results))
	}

	// Search for "stock market"
	results, err = engine.Search(ctx, "stock market", Options{Mode: ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'stock market', got %d", len(results))
	}
	if len(results) > 0 && results[0].SourceFile != "finance.md" {
		t.Errorf("expected source_file 'finance.md', got %q", results[0].SourceFile)
	}
}

// --- Stats (via store) ---

func TestStatsViaStore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Empty store
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.MemoryCount != 0 {
		t.Errorf("expected 0 memories, got %d", stats.MemoryCount)
	}

	// Add some data
	seedTestData(t, s)
	stats, err = s.Stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.MemoryCount != 10 {
		t.Errorf("expected 10 memories, got %d", stats.MemoryCount)
	}
	if stats.FactCount != 0 {
		t.Errorf("expected 0 facts (not extracted yet), got %d", stats.FactCount)
	}
}

// Mock embedder for testing
type mockEmbedder struct {
	dimensions int
	embeddings map[string][]float32
}

func newMockEmbedder() *mockEmbedder {
	return &mockEmbedder{
		dimensions: 384,
		embeddings: make(map[string][]float32),
	}
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if embedding, ok := m.embeddings[text]; ok {
		return embedding, nil
	}

	// Generate a simple embedding based on text content
	embedding := make([]float32, m.dimensions)
	for i := range embedding {
		embedding[i] = float32(len(text)+i) * 0.001
	}

	m.embeddings[text] = embedding
	return embedding, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		embeddings[i] = emb
	}
	return embeddings, nil
}

func (m *mockEmbedder) Dimensions() int {
	return m.dimensions
}

func TestSearchSemantic_WithEmbedder(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)

	// Add embeddings for some memories
	ctx := context.Background()
	embedder := newMockEmbedder()

	// Add embedding for one of the Go-related memories
	goMemory := []float32{0.8, 0.2, 0.1}
	err := s.AddEmbedding(ctx, 3, goMemory) // Memory ID 3 should be Go-related
	if err != nil {
		t.Fatalf("Failed to add embedding: %v", err)
	}

	// Mock the query embedding to be similar to Go memory
	embedder.embeddings["Go programming"] = []float32{0.7, 0.3, 0.2}

	engine := NewEngineWithEmbedder(s, embedder)

	results, err := engine.Search(ctx, "Go programming", Options{Mode: ModeSemantic, Limit: 10})
	if err != nil {
		t.Fatalf("Semantic search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected semantic search to find results")
	}

	// Results should be marked as semantic
	for _, result := range results {
		if result.MatchType != "semantic" {
			t.Errorf("Expected match type 'semantic', got %q", result.MatchType)
		}
	}
}

func TestSearchHybrid_RRF(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)

	ctx := context.Background()
	embedder := newMockEmbedder()

	// Add embedding for Go-related memory (Memory ID 3)
	goMemory := []float32{0.8, 0.2, 0.1}
	err := s.AddEmbedding(ctx, 3, goMemory)
	if err != nil {
		t.Fatalf("Failed to add embedding: %v", err)
	}

	// Mock query embedding similar to Go memory
	embedder.embeddings["Go programming"] = []float32{0.7, 0.3, 0.2}

	engine := NewEngineWithEmbedder(s, embedder)

	results, err := engine.Search(ctx, "Go programming", Options{Mode: ModeHybrid, Limit: 10})
	if err != nil {
		t.Fatalf("Hybrid search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected hybrid search to find results")
	}

	// Results should be marked as hybrid
	for _, result := range results {
		if result.MatchType != "hybrid" {
			t.Errorf("Expected match type 'hybrid', got %q", result.MatchType)
		}
	}

	// RRF should produce non-zero scores
	for _, result := range results {
		if result.Score <= 0 {
			t.Errorf("Expected positive RRF score, got %f", result.Score)
		}
	}
}

func TestSearchHybrid_FallbackNilEmbedder(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)

	engine := NewEngine(s) // No embedder
	ctx := context.Background()

	results, err := engine.Search(ctx, "Go programming", Options{Mode: ModeHybrid, Limit: 10})
	if err != nil {
		t.Fatalf("Hybrid search without embedder should fall back to BM25: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected fallback BM25 search to find results")
	}

	// Results should be marked as BM25 since we fell back
	for _, result := range results {
		if result.MatchType != "bm25" {
			t.Errorf("Expected match type 'bm25' for fallback, got %q", result.MatchType)
		}
	}
}
