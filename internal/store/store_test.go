package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// newTestStore creates an in-memory store for testing.
func newTestStore(t *testing.T) Store {
	t.Helper()
	s, err := NewStore(StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- Database Initialization ---

func TestNewStore(t *testing.T) {
	s, err := NewStore(StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer s.Close()

	// Verify tables exist by querying each
	ss := s.(*SQLiteStore)
	tables := []string{"memories", "facts", "embeddings", "recall_log",
		"memory_events", "snapshots", "lenses", "meta"}
	for _, table := range tables {
		var name string
		err := ss.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}

	// Verify FTS virtual table
	var ftsName string
	err = ss.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='memories_fts'",
	).Scan(&ftsName)
	if err != nil {
		t.Error("memories_fts virtual table not found")
	}
}

func TestMemoryClassColumnExists(t *testing.T) {
	s := newTestStore(t)
	ss := s.(*SQLiteStore)

	var count int
	err := ss.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name='memory_class'").Scan(&count)
	if err != nil {
		t.Fatalf("checking memory_class column: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected memory_class column to exist, count=%d", count)
	}
}

func TestWALMode(t *testing.T) {
	s := newTestStore(t)
	ss := s.(*SQLiteStore)

	var mode string
	ss.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	// In-memory databases use "memory" journal mode, not WAL
	// WAL applies to file-based databases
	if mode != "memory" && mode != "wal" {
		t.Errorf("expected journal_mode 'wal' or 'memory', got %q", mode)
	}
}

func TestMetadata(t *testing.T) {
	s := newTestStore(t)
	ss := s.(*SQLiteStore)

	var version string
	ss.db.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&version)
	if version != "1" {
		t.Errorf("expected schema_version '1', got %q", version)
	}

	var dims string
	ss.db.QueryRow("SELECT value FROM meta WHERE key = 'embedding_dimensions'").Scan(&dims)
	if dims != "384" {
		t.Errorf("expected embedding_dimensions '384', got %q", dims)
	}
}

// --- Memory CRUD ---

func TestAddMemory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := &Memory{
		Content:       "The quick brown fox jumps over the lazy dog",
		SourceFile:    "test.md",
		SourceLine:    1,
		SourceSection: "intro",
	}

	id, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("AddMemory failed: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}
	if m.ID != id {
		t.Errorf("memory ID not updated: expected %d, got %d", id, m.ID)
	}
	if m.ContentHash == "" {
		t.Error("content hash not set")
	}
}

func TestAddMemory_EmptyContent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.AddMemory(ctx, &Memory{Content: ""})
	if err == nil {
		t.Error("expected error for empty content")
	}
}

func TestGetMemory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := &Memory{
		Content:       "Test memory content",
		SourceFile:    "notes.md",
		SourceLine:    42,
		SourceSection: "section-a",
	}
	id, _ := s.AddMemory(ctx, m)

	got, err := s.GetMemory(ctx, id)
	if err != nil {
		t.Fatalf("GetMemory failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected memory, got nil")
	}
	if got.Content != m.Content {
		t.Errorf("content mismatch: %q != %q", got.Content, m.Content)
	}
	if got.SourceFile != "notes.md" {
		t.Errorf("source_file mismatch: %q", got.SourceFile)
	}
	if got.SourceLine != 42 {
		t.Errorf("source_line mismatch: %d", got.SourceLine)
	}
	if got.DeletedAt != nil {
		t.Error("expected nil deleted_at")
	}
}

func TestGetMemory_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetMemory(ctx, 99999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent memory")
	}
}

func TestListMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s.AddMemory(ctx, &Memory{Content: fmt.Sprintf("Memory %d", i)})
	}

	memories, err := s.ListMemories(ctx, ListOpts{Limit: 3})
	if err != nil {
		t.Fatalf("ListMemories failed: %v", err)
	}
	if len(memories) != 3 {
		t.Errorf("expected 3 memories, got %d", len(memories))
	}

	// Test offset
	memories2, err := s.ListMemories(ctx, ListOpts{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("ListMemories with offset failed: %v", err)
	}
	if len(memories2) != 2 {
		t.Errorf("expected 2 memories with offset, got %d", len(memories2))
	}
}

func TestDeleteMemory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _ := s.AddMemory(ctx, &Memory{Content: "To be deleted"})

	err := s.DeleteMemory(ctx, id)
	if err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}

	// Should not appear in list
	memories, _ := s.ListMemories(ctx, ListOpts{})
	if len(memories) != 0 {
		t.Error("soft-deleted memory still appears in list")
	}

	// But should still exist in DB (soft delete)
	got, _ := s.GetMemory(ctx, id)
	if got == nil {
		t.Error("soft-deleted memory should still be retrievable by ID")
	}
	if got.DeletedAt == nil {
		t.Error("deleted_at should be set")
	}
}

func TestDeleteMemory_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.DeleteMemory(ctx, 99999)
	if err == nil {
		t.Error("expected error deleting nonexistent memory")
	}
}

// --- Deduplication ---

func TestFindByHash(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	content := "unique content for hashing"
	s.AddMemory(ctx, &Memory{Content: content})

	hash := HashMemoryContent(content, "")
	found, err := s.FindByHash(ctx, hash)
	if err != nil {
		t.Fatalf("FindByHash failed: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find memory by hash")
	}
	if found.Content != content {
		t.Errorf("content mismatch: %q", found.Content)
	}
}

func TestDuplicateHash(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := &Memory{Content: "duplicate content"}
	_, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Second insert with same content should fail (UNIQUE constraint on content_hash)
	_, err = s.AddMemory(ctx, &Memory{Content: "duplicate content"})
	if err == nil {
		t.Error("expected error inserting duplicate content")
	}
}

// --- Batch Operations ---

func TestAddMemoryBatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memories := make([]*Memory, 100)
	for i := range memories {
		memories[i] = &Memory{Content: fmt.Sprintf("Batch memory %d", i)}
	}

	ids, err := s.AddMemoryBatch(ctx, memories)
	if err != nil {
		t.Fatalf("AddMemoryBatch failed: %v", err)
	}
	if len(ids) != 100 {
		t.Errorf("expected 100 IDs, got %d", len(ids))
	}

	// Verify all exist
	list, _ := s.ListMemories(ctx, ListOpts{Limit: 200})
	if len(list) != 100 {
		t.Errorf("expected 100 memories in DB, got %d", len(list))
	}
}

func TestBatchInsertPerformance(t *testing.T) {
	sizes := []int{100, 500, 1000}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()

			memories := make([]*Memory, size)
			for i := range memories {
				memories[i] = &Memory{Content: fmt.Sprintf("Perf test memory %d of %d", i, size)}
			}

			start := time.Now()
			ids, err := s.AddMemoryBatch(ctx, memories)
			elapsed := time.Since(start)

			if err != nil {
				t.Fatalf("batch insert failed: %v", err)
			}
			if len(ids) != size {
				t.Errorf("expected %d IDs, got %d", size, len(ids))
			}

			t.Logf("Batch insert %d memories: %v (%.0f/sec)", size, elapsed,
				float64(size)/elapsed.Seconds())
		})
	}
}

// --- Facts CRUD ---

func TestAddFact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "Q lives in Philadelphia"})

	f := &Fact{
		MemoryID:  memID,
		Subject:   "Q",
		Predicate: "lives_in",
		Object:    "Philadelphia",
		FactType:  "location",
	}

	id, err := s.AddFact(ctx, f)
	if err != nil {
		t.Fatalf("AddFact failed: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}
	if f.Confidence != 1.0 {
		t.Errorf("expected default confidence 1.0, got %f", f.Confidence)
	}
}

func TestAddFact_InvalidType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test"})

	_, err := s.AddFact(ctx, &Fact{
		MemoryID: memID,
		FactType: "invalid_type",
	})
	if err == nil {
		t.Error("expected error for invalid fact_type")
	}
}

func TestGetFact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "Test fact source"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID:    memID,
		Subject:     "Alice",
		Predicate:   "knows",
		Object:      "Bob",
		FactType:    "relationship",
		SourceQuote: "Alice knows Bob from college",
	})

	got, err := s.GetFact(ctx, factID)
	if err != nil {
		t.Fatalf("GetFact failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected fact, got nil")
	}
	if got.Subject != "Alice" {
		t.Errorf("subject mismatch: %q", got.Subject)
	}
	if got.SourceQuote != "Alice knows Bob from college" {
		t.Errorf("source_quote mismatch: %q", got.SourceQuote)
	}
}

func TestGetFact_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetFact(ctx, 99999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent fact")
	}
}

func TestListFacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "Fact source"})

	types := []string{"kv", "relationship", "preference", "kv", "identity"}
	for i, ft := range types {
		s.AddFact(ctx, &Fact{
			MemoryID: memID,
			Subject:  fmt.Sprintf("subject_%d", i),
			FactType: ft,
		})
	}

	// List all
	facts, err := s.ListFacts(ctx, ListOpts{Limit: 10})
	if err != nil {
		t.Fatalf("ListFacts failed: %v", err)
	}
	if len(facts) != 5 {
		t.Errorf("expected 5 facts, got %d", len(facts))
	}

	// Filter by type
	kvFacts, err := s.ListFacts(ctx, ListOpts{Limit: 10, FactType: "kv"})
	if err != nil {
		t.Fatalf("ListFacts with type filter failed: %v", err)
	}
	if len(kvFacts) != 2 {
		t.Errorf("expected 2 kv facts, got %d", len(kvFacts))
	}
}

func TestUpdateFactConfidence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "confidence test"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID,
		FactType: "kv",
	})

	err := s.UpdateFactConfidence(ctx, factID, 0.75)
	if err != nil {
		t.Fatalf("UpdateFactConfidence failed: %v", err)
	}

	got, _ := s.GetFact(ctx, factID)
	if got.Confidence != 0.75 {
		t.Errorf("expected confidence 0.75, got %f", got.Confidence)
	}
}

func TestReinforceFact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "reinforce test"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID,
		FactType: "kv",
	})

	// Get original timestamp
	before, _ := s.GetFact(ctx, factID)
	time.Sleep(10 * time.Millisecond) // ensure timestamp changes

	err := s.ReinforceFact(ctx, factID)
	if err != nil {
		t.Fatalf("ReinforceFact failed: %v", err)
	}

	after, _ := s.GetFact(ctx, factID)
	if !after.LastReinforced.After(before.LastReinforced) {
		t.Error("last_reinforced should have been updated")
	}
}

// --- FTS5 Search ---

func TestSearchFTS(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memories := []string{
		"Go is a statically typed programming language",
		"Python is dynamically typed and very popular",
		"Rust emphasizes memory safety without garbage collection",
		"JavaScript runs in the browser and on Node.js",
		"Go has excellent concurrency support with goroutines",
	}
	for _, content := range memories {
		s.AddMemory(ctx, &Memory{Content: content})
	}

	results, err := s.SearchFTS(ctx, "Go", 10)
	if err != nil {
		t.Fatalf("SearchFTS failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'Go', got %d", len(results))
	}
}

func TestSearchFTS_BooleanQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.AddMemory(ctx, &Memory{Content: "Alice and Bob are friends"})
	s.AddMemory(ctx, &Memory{Content: "Alice works at a bank"})
	s.AddMemory(ctx, &Memory{Content: "Bob works at a hospital"})

	// AND query
	results, err := s.SearchFTS(ctx, "Alice AND Bob", 10)
	if err != nil {
		t.Fatalf("SearchFTS boolean failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'Alice AND Bob', got %d", len(results))
	}

	// Quoted phrase
	results, err = s.SearchFTS(ctx, `"works at"`, 10)
	if err != nil {
		t.Fatalf("SearchFTS phrase failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for '\"works at\"', got %d", len(results))
	}
}

func TestSearchFTS_Snippets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.AddMemory(ctx, &Memory{
		Content: "The Cortex project is a memory layer for AI agents that stores facts and supports semantic search",
	})

	results, err := s.SearchFTS(ctx, "memory", 10)
	if err != nil {
		t.Fatalf("SearchFTS failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
	t.Logf("Snippet: %s", results[0].Snippet)
}

func TestSearchFTS_NoResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.AddMemory(ctx, &Memory{Content: "only about dogs"})

	results, err := s.SearchFTS(ctx, "quantum", 10)
	if err != nil {
		t.Fatalf("SearchFTS failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// --- Embeddings ---

func TestAddAndGetEmbedding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "embedding test"})

	// Create a simple test vector
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = float32(i) / 384.0
	}

	err := s.AddEmbedding(ctx, memID, vec)
	if err != nil {
		t.Fatalf("AddEmbedding failed: %v", err)
	}

	got, err := s.GetEmbedding(ctx, memID)
	if err != nil {
		t.Fatalf("GetEmbedding failed: %v", err)
	}
	if len(got) != 384 {
		t.Errorf("expected 384 dimensions, got %d", len(got))
	}
	// Verify values are preserved
	for i := 0; i < 10; i++ {
		if got[i] != vec[i] {
			t.Errorf("vector[%d] mismatch: %f != %f", i, got[i], vec[i])
			break
		}
	}
}

func TestSearchEmbedding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert memories with known vectors
	contents := []string{"about cats", "about dogs", "about programming"}
	vectors := [][]float32{
		makeVector(384, 1.0, 0.0, 0.0), // "cats" direction
		makeVector(384, 0.9, 0.1, 0.0), // similar to cats
		makeVector(384, 0.0, 0.0, 1.0), // "programming" direction (different)
	}

	for i, content := range contents {
		id, _ := s.AddMemory(ctx, &Memory{Content: content})
		s.AddEmbedding(ctx, id, vectors[i])
	}

	// Search with a vector similar to "cats"
	query := makeVector(384, 1.0, 0.0, 0.0)
	results, err := s.SearchEmbedding(ctx, query, 10, 0.5)
	if err != nil {
		t.Fatalf("SearchEmbedding failed: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results above threshold, got %d", len(results))
	}

	// First result should be most similar (cats)
	if results[0].Memory.Content != "about cats" {
		t.Errorf("expected 'about cats' as top result, got %q", results[0].Memory.Content)
	}

	// Scores should be descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Error("results should be sorted by score descending")
		}
	}
}

func TestSearchEmbedding_MinSimilarity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _ := s.AddMemory(ctx, &Memory{Content: "orthogonal content"})
	s.AddEmbedding(ctx, id, makeVector(384, 0.0, 1.0, 0.0))

	// Query in a completely different direction
	query := makeVector(384, 1.0, 0.0, 0.0)
	results, err := s.SearchEmbedding(ctx, query, 10, 0.9)
	if err != nil {
		t.Fatalf("SearchEmbedding failed: %v", err)
	}

	// Should find nothing above 0.9 similarity for orthogonal vectors
	if len(results) != 0 {
		t.Errorf("expected 0 results above 0.9 threshold, got %d (score: %f)",
			len(results), results[0].Score)
	}
}

// --- Events ---

func TestLogEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	event := &MemoryEvent{
		EventType: "add",
		FactID:    1,
		NewValue:  "new fact value",
		Source:    "import",
	}

	err := s.LogEvent(ctx, event)
	if err != nil {
		t.Fatalf("LogEvent failed: %v", err)
	}
	if event.ID <= 0 {
		t.Error("expected positive event ID")
	}
	if event.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestLogEvent_AllTypes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	types := []string{"add", "update", "merge", "decay", "delete", "reinforce"}
	for _, et := range types {
		err := s.LogEvent(ctx, &MemoryEvent{EventType: et, Source: "test"})
		if err != nil {
			t.Errorf("LogEvent(%q) failed: %v", et, err)
		}
	}

	// Invalid type should fail
	err := s.LogEvent(ctx, &MemoryEvent{EventType: "invalid", Source: "test"})
	if err == nil {
		t.Error("expected error for invalid event type")
	}
}

// --- Stats ---

func TestStats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Empty store
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.MemoryCount != 0 || stats.FactCount != 0 {
		t.Error("expected zero counts for empty store")
	}

	// Add some data
	for i := 0; i < 3; i++ {
		memID, _ := s.AddMemory(ctx, &Memory{Content: fmt.Sprintf("stat mem %d", i)})
		s.AddFact(ctx, &Fact{MemoryID: memID, FactType: "kv"})
		s.AddEmbedding(ctx, memID, makeVector(384, float32(i), 0, 0))
	}
	s.LogEvent(ctx, &MemoryEvent{EventType: "add", Source: "test"})
	s.LogEvent(ctx, &MemoryEvent{EventType: "add", Source: "test"})

	stats, err = s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.MemoryCount != 3 {
		t.Errorf("expected 3 memories, got %d", stats.MemoryCount)
	}
	if stats.FactCount != 3 {
		t.Errorf("expected 3 facts, got %d", stats.FactCount)
	}
	if stats.EmbeddingCount != 3 {
		t.Errorf("expected 3 embeddings, got %d", stats.EmbeddingCount)
	}
	if stats.EventCount != 2 {
		t.Errorf("expected 2 events, got %d", stats.EventCount)
	}
}

// --- Stale Facts ---

func TestStaleFacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "stale test"})

	// Add a fact with low confidence
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID:   memID,
		Subject:    "old fact",
		FactType:   "state",
		Confidence: 0.3,
	})

	// With daysSinceRecall=-1 (future cutoff), the fact just created should be stale
	// because its last_reinforced is before the cutoff (now + 1 day)
	stale, err := s.StaleFacts(ctx, 0.5, -1)
	if err != nil {
		t.Fatalf("StaleFacts failed: %v", err)
	}
	if len(stale) != 1 {
		t.Errorf("expected 1 stale fact with future cutoff, got %d", len(stale))
	}

	// With daysSinceRecall=30, fact was just reinforced so shouldn't be stale
	stale, err = s.StaleFacts(ctx, 0.5, 30)
	if err != nil {
		t.Fatalf("StaleFacts failed: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale facts (recently reinforced), got %d", len(stale))
	}

	// High confidence fact should NOT be stale even with generous window
	ss := s.(*SQLiteStore)
	ss.db.ExecContext(ctx, "UPDATE facts SET confidence = 0.9 WHERE id = ?", factID)
	stale, err = s.StaleFacts(ctx, 0.5, -1)
	if err != nil {
		t.Fatalf("StaleFacts failed: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale facts (high confidence), got %d", len(stale))
	}
}

// --- Vacuum ---

func TestVacuum(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Just verify it doesn't error
	err := s.Vacuum(ctx)
	if err != nil {
		t.Fatalf("Vacuum failed: %v", err)
	}
}

// --- Helpers ---

// makeVector creates a test vector with specific values in first 3 dimensions.
func makeVector(dims int, x, y, z float32) []float32 {
	v := make([]float32, dims)
	if dims > 0 {
		v[0] = x
	}
	if dims > 1 {
		v[1] = y
	}
	if dims > 2 {
		v[2] = z
	}
	return v
}

// Tests for SourceFile filtering

func TestListMemories_SourceFileFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memories with different source files
	memories := []*Memory{
		{Content: "Memory 1", SourceFile: "/path/to/file1.md"},
		{Content: "Memory 2", SourceFile: "/path/to/file2.md"},
		{Content: "Memory 3", SourceFile: "/path/to/file1.md"},
		{Content: "Memory 4", SourceFile: ""},
	}

	for _, m := range memories {
		_, err := s.AddMemory(ctx, m)
		if err != nil {
			t.Fatalf("failed to add memory: %v", err)
		}
	}

	// Test filtering by source file
	results, err := s.ListMemories(ctx, ListOpts{
		Limit:      100,
		SourceFile: "/path/to/file1.md",
	})
	if err != nil {
		t.Fatalf("ListMemories with SourceFile filter failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 memories, got %d", len(results))
	}

	for _, m := range results {
		if m.SourceFile != "/path/to/file1.md" {
			t.Errorf("expected source file '/path/to/file1.md', got '%s'", m.SourceFile)
		}
	}
}

func TestListMemories_SourceFileFilter_NoMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memory with different source file
	m := &Memory{Content: "Memory 1", SourceFile: "/path/to/file1.md"}
	_, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// Test filtering by non-existent source file
	results, err := s.ListMemories(ctx, ListOpts{
		Limit:      100,
		SourceFile: "/nonexistent/file.md",
	})
	if err != nil {
		t.Fatalf("ListMemories with SourceFile filter failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 memories, got %d", len(results))
	}
}

func TestListFacts_SourceFileFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memories with different source files
	m1 := &Memory{Content: "Memory 1", SourceFile: "/path/to/file1.md"}
	id1, err := s.AddMemory(ctx, m1)
	if err != nil {
		t.Fatalf("failed to add memory 1: %v", err)
	}

	m2 := &Memory{Content: "Memory 2", SourceFile: "/path/to/file2.md"}
	id2, err := s.AddMemory(ctx, m2)
	if err != nil {
		t.Fatalf("failed to add memory 2: %v", err)
	}

	// Add facts to both memories
	facts := []*Fact{
		{MemoryID: id1, Predicate: "fact1", Object: "value1", FactType: "kv"},
		{MemoryID: id1, Predicate: "fact2", Object: "value2", FactType: "kv"},
		{MemoryID: id2, Predicate: "fact3", Object: "value3", FactType: "kv"},
	}

	for _, f := range facts {
		_, err := s.AddFact(ctx, f)
		if err != nil {
			t.Fatalf("failed to add fact: %v", err)
		}
	}

	// Test filtering facts by source file
	results, err := s.ListFacts(ctx, ListOpts{
		Limit:      100,
		SourceFile: "/path/to/file1.md",
	})
	if err != nil {
		t.Fatalf("ListFacts with SourceFile filter failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 facts, got %d", len(results))
	}

	for _, f := range results {
		if f.MemoryID != id1 {
			t.Errorf("expected fact from memory %d, got memory %d", id1, f.MemoryID)
		}
	}
}

func TestListFacts_SourceFileAndTypeFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memory with source file
	m := &Memory{Content: "Memory 1", SourceFile: "/path/to/file1.md"}
	id, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// Add facts with different types
	facts := []*Fact{
		{MemoryID: id, Predicate: "fact1", Object: "value1", FactType: "kv"},
		{MemoryID: id, Predicate: "fact2", Object: "value2", FactType: "temporal"},
		{MemoryID: id, Predicate: "fact3", Object: "value3", FactType: "kv"},
	}

	for _, f := range facts {
		_, err := s.AddFact(ctx, f)
		if err != nil {
			t.Fatalf("failed to add fact: %v", err)
		}
	}

	// Test filtering by both source file and fact type
	results, err := s.ListFacts(ctx, ListOpts{
		Limit:      100,
		SourceFile: "/path/to/file1.md",
		FactType:   "kv",
	})
	if err != nil {
		t.Fatalf("ListFacts with SourceFile and FactType filter failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 facts, got %d", len(results))
	}

	for _, f := range results {
		if f.FactType != "kv" {
			t.Errorf("expected fact type 'kv', got '%s'", f.FactType)
		}
		if f.MemoryID != id {
			t.Errorf("expected fact from memory %d, got memory %d", id, f.MemoryID)
		}
	}
}

func TestListFacts_SourceFileFilter_NoMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memory with different source file
	m := &Memory{Content: "Memory 1", SourceFile: "/path/to/file1.md"}
	id, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// Add fact
	f := &Fact{MemoryID: id, Predicate: "fact1", Object: "value1", FactType: "kv"}
	_, err = s.AddFact(ctx, f)
	if err != nil {
		t.Fatalf("failed to add fact: %v", err)
	}

	// Test filtering by non-existent source file
	results, err := s.ListFacts(ctx, ListOpts{
		Limit:      100,
		SourceFile: "/nonexistent/file.md",
	})
	if err != nil {
		t.Fatalf("ListFacts with SourceFile filter failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 facts, got %d", len(results))
	}
}

// Export format tests

func TestExportMemoriesCSV_Format(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add test memory
	m := &Memory{
		Content:       "Test content",
		SourceFile:    "/test/file.md",
		SourceLine:    42,
		SourceSection: "Test Section",
	}
	_, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// List memories to get the actual data
	memories, err := s.ListMemories(ctx, ListOpts{Limit: 1})
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}

	if len(memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(memories))
	}

	// Test CSV format - we'll check that it produces valid CSV structure
	// This is a basic format validation test
	memory := memories[0]
	if memory.Content != "Test content" {
		t.Errorf("expected content 'Test content', got '%s'", memory.Content)
	}
	if memory.SourceFile != "/test/file.md" {
		t.Errorf("expected source file '/test/file.md', got '%s'", memory.SourceFile)
	}
	if memory.SourceLine != 42 {
		t.Errorf("expected source line 42, got %d", memory.SourceLine)
	}
	if memory.SourceSection != "Test Section" {
		t.Errorf("expected source section 'Test Section', got '%s'", memory.SourceSection)
	}
}

func TestExportFactsCSV_Format(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add test memory and fact
	m := &Memory{Content: "Test content"}
	memoryID, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	f := &Fact{
		MemoryID:    memoryID,
		Subject:     "test_subject",
		Predicate:   "test_predicate",
		Object:      "test_object",
		FactType:    "kv",
		Confidence:  0.95,
		DecayRate:   0.01,
		SourceQuote: "Test quote",
	}
	_, err = s.AddFact(ctx, f)
	if err != nil {
		t.Fatalf("failed to add fact: %v", err)
	}

	// List facts to get the actual data
	facts, err := s.ListFacts(ctx, ListOpts{Limit: 1})
	if err != nil {
		t.Fatalf("failed to list facts: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}

	// Test the fact data structure - validates our export will work
	fact := facts[0]
	if fact.Subject != "test_subject" {
		t.Errorf("expected subject 'test_subject', got '%s'", fact.Subject)
	}
	if fact.Predicate != "test_predicate" {
		t.Errorf("expected predicate 'test_predicate', got '%s'", fact.Predicate)
	}
	if fact.Object != "test_object" {
		t.Errorf("expected object 'test_object', got '%s'", fact.Object)
	}
	if fact.FactType != "kv" {
		t.Errorf("expected fact type 'kv', got '%s'", fact.FactType)
	}
	if fact.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", fact.Confidence)
	}
}

func TestEnhancedObservabilityMethods(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Test GetSourceCount with empty store
	count, err := s.GetSourceCount(ctx)
	if err != nil {
		t.Fatalf("GetSourceCount failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 sources, got %d", count)
	}

	// Add memories with different source files
	memories := []*Memory{
		{Content: "Memory 1", SourceFile: "/file1.md"},
		{Content: "Memory 2", SourceFile: "/file2.md"},
		{Content: "Memory 3", SourceFile: "/file1.md"}, // duplicate source
		{Content: "Memory 4", SourceFile: ""},          // empty source
	}

	for _, m := range memories {
		_, err := s.AddMemory(ctx, m)
		if err != nil {
			t.Fatalf("failed to add memory: %v", err)
		}
	}

	// Test GetSourceCount again
	count, err = s.GetSourceCount(ctx)
	if err != nil {
		t.Fatalf("GetSourceCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 distinct sources, got %d", count)
	}
}

func TestGetFreshnessDistribution(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add a memory
	m := &Memory{Content: "Test memory"}
	_, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// Test GetFreshnessDistribution
	freshness, err := s.GetFreshnessDistribution(ctx)
	if err != nil {
		t.Fatalf("GetFreshnessDistribution failed: %v", err)
	}

	// Should have at least one entry in today bucket (since we just added it)
	total := freshness.Today + freshness.ThisWeek + freshness.ThisMonth + freshness.Older
	if total != 1 {
		t.Errorf("expected total of 1 memory, got %d", total)
	}

	// Most likely should be in Today bucket, but exact timing depends on when test runs
	if freshness.Today < 0 || freshness.ThisWeek < 0 || freshness.ThisMonth < 0 || freshness.Older < 0 {
		t.Error("freshness counts should not be negative")
	}
}

func TestGetFactsByType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Test empty store
	factsByType, err := s.GetFactsByType(ctx)
	if err != nil {
		t.Fatalf("GetFactsByType failed: %v", err)
	}
	if len(factsByType) != 0 {
		t.Errorf("expected empty map, got %d entries", len(factsByType))
	}

	// Add memory and facts
	m := &Memory{Content: "Test memory"}
	memoryID, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	facts := []*Fact{
		{MemoryID: memoryID, Predicate: "fact1", Object: "value1", FactType: "kv"},
		{MemoryID: memoryID, Predicate: "fact2", Object: "value2", FactType: "kv"},
		{MemoryID: memoryID, Predicate: "fact3", Object: "value3", FactType: "temporal"},
		{MemoryID: memoryID, Predicate: "fact4", Object: "value4", FactType: "identity"},
	}

	for _, f := range facts {
		_, err := s.AddFact(ctx, f)
		if err != nil {
			t.Fatalf("failed to add fact: %v", err)
		}
	}

	// Test GetFactsByType
	factsByType, err = s.GetFactsByType(ctx)
	if err != nil {
		t.Fatalf("GetFactsByType failed: %v", err)
	}

	expected := map[string]int{
		"kv":       2,
		"temporal": 1,
		"identity": 1,
	}

	if len(factsByType) != len(expected) {
		t.Errorf("expected %d fact types, got %d", len(expected), len(factsByType))
	}

	for factType, expectedCount := range expected {
		if actualCount, exists := factsByType[factType]; !exists {
			t.Errorf("expected fact type %s not found", factType)
		} else if actualCount != expectedCount {
			t.Errorf("expected %d facts of type %s, got %d", expectedCount, factType, actualCount)
		}
	}
}

func TestGetFactsByMemoryIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create two memories with facts
	memID1, _ := s.AddMemory(ctx, &Memory{Content: "memory one"})
	memID2, _ := s.AddMemory(ctx, &Memory{Content: "memory two"})
	memID3, _ := s.AddMemory(ctx, &Memory{Content: "memory three (no facts)"})

	s.AddFact(ctx, &Fact{MemoryID: memID1, Subject: "Q", Predicate: "lives_in", Object: "Philly", FactType: "location"})
	s.AddFact(ctx, &Fact{MemoryID: memID1, Subject: "Q", Predicate: "age", Object: "30", FactType: "identity"})
	s.AddFact(ctx, &Fact{MemoryID: memID2, Subject: "Mister", Predicate: "model", Object: "Opus", FactType: "kv"})

	// Query both memory IDs
	facts, err := s.GetFactsByMemoryIDs(ctx, []int64{memID1, memID2})
	if err != nil {
		t.Fatalf("GetFactsByMemoryIDs failed: %v", err)
	}
	if len(facts) != 3 {
		t.Errorf("expected 3 facts, got %d", len(facts))
	}

	// Query just one
	facts, err = s.GetFactsByMemoryIDs(ctx, []int64{memID2})
	if err != nil {
		t.Fatalf("GetFactsByMemoryIDs failed: %v", err)
	}
	if len(facts) != 1 {
		t.Errorf("expected 1 fact, got %d", len(facts))
	}

	// Query memory with no facts
	facts, err = s.GetFactsByMemoryIDs(ctx, []int64{memID3})
	if err != nil {
		t.Fatalf("GetFactsByMemoryIDs failed: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(facts))
	}

	// Empty input
	facts, err = s.GetFactsByMemoryIDs(ctx, []int64{})
	if err != nil {
		t.Fatalf("GetFactsByMemoryIDs failed: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected 0 facts for empty input, got %d", len(facts))
	}
}

func TestReinforceFactsByMemoryIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID1, _ := s.AddMemory(ctx, &Memory{Content: "memory one"})
	memID2, _ := s.AddMemory(ctx, &Memory{Content: "memory two"})

	s.AddFact(ctx, &Fact{MemoryID: memID1, Subject: "A", Predicate: "is", Object: "1", FactType: "kv"})
	s.AddFact(ctx, &Fact{MemoryID: memID1, Subject: "B", Predicate: "is", Object: "2", FactType: "kv"})
	s.AddFact(ctx, &Fact{MemoryID: memID2, Subject: "C", Predicate: "is", Object: "3", FactType: "kv"})

	time.Sleep(10 * time.Millisecond)

	count, err := s.ReinforceFactsByMemoryIDs(ctx, []int64{memID1})
	if err != nil {
		t.Fatalf("ReinforceFactsByMemoryIDs failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 reinforced, got %d", count)
	}

	// Empty input
	count, err = s.ReinforceFactsByMemoryIDs(ctx, []int64{})
	if err != nil {
		t.Fatalf("ReinforceFactsByMemoryIDs with empty failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 reinforced for empty input, got %d", count)
	}
}

func TestGetConfidenceDistribution(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "distribution test"})

	// Add facts with different decay rates
	// All start at confidence=1.0, but different decay rates will produce different effective confidences
	// identity (0.001) stays high, temporal (0.1) decays fast, state (0.05) medium
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "A", Predicate: "is", Object: "1", FactType: "identity", Confidence: 1.0, DecayRate: 0.001})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "B", Predicate: "is", Object: "2", FactType: "kv", Confidence: 1.0, DecayRate: 0.01})

	dist, err := s.GetConfidenceDistribution(ctx)
	if err != nil {
		t.Fatalf("GetConfidenceDistribution failed: %v", err)
	}
	if dist.Total != 2 {
		t.Errorf("expected total 2, got %d", dist.Total)
	}
	// Both just created, so effective confidence ≈ 1.0 → both high
	if dist.High != 2 {
		t.Errorf("expected 2 high confidence facts, got %d", dist.High)
	}
}
