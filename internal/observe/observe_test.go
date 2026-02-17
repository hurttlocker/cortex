package observe

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

// newTestEngine creates an observe engine with in-memory store for testing.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewEngine(s, ":memory:")
}

// addTestMemory adds a test memory to the store.
func addTestMemory(t *testing.T, engine *Engine, content, sourceFile string) int64 {
	t.Helper()
	ctx := context.Background()

	memory := &store.Memory{
		Content:    content,
		SourceFile: sourceFile,
	}

	id, err := engine.store.AddMemory(ctx, memory)
	if err != nil {
		t.Fatalf("failed to add test memory: %v", err)
	}
	return id
}

// addTestFact adds a test fact to the store.
func addTestFact(t *testing.T, engine *Engine, memoryID int64, subject, predicate, object, factType string, confidence float64) int64 {
	t.Helper()
	ctx := context.Background()

	fact := &store.Fact{
		MemoryID:   memoryID,
		Subject:    subject,
		Predicate:  predicate,
		Object:     object,
		FactType:   factType,
		Confidence: confidence,
		DecayRate:  0.01,
	}

	id, err := engine.store.AddFact(ctx, fact)
	if err != nil {
		t.Fatalf("failed to add test fact: %v", err)
	}
	return id
}

// --- Stats Tests ---

func TestGetStats_Empty(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	stats, err := engine.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	if stats.TotalMemories != 0 {
		t.Errorf("expected 0 memories, got %d", stats.TotalMemories)
	}
	if stats.TotalFacts != 0 {
		t.Errorf("expected 0 facts, got %d", stats.TotalFacts)
	}
	if stats.TotalSources != 0 {
		t.Errorf("expected 0 sources, got %d", stats.TotalSources)
	}
	if stats.AvgConfidence != 0.0 {
		t.Errorf("expected 0.0 avg confidence, got %.2f", stats.AvgConfidence)
	}
}

func TestGetStats_WithData(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	// Add test data
	m1 := addTestMemory(t, engine, "Test content 1", "file1.md")
	m2 := addTestMemory(t, engine, "Test content 2", "file2.md")

	addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.9)
	addTestFact(t, engine, m1, "user", "age", "25", "kv", 0.8)
	addTestFact(t, engine, m2, "user", "city", "New York", "location", 0.7)

	stats, err := engine.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	if stats.TotalMemories != 2 {
		t.Errorf("expected 2 memories, got %d", stats.TotalMemories)
	}
	if stats.TotalFacts != 3 {
		t.Errorf("expected 3 facts, got %d", stats.TotalFacts)
	}
	if stats.TotalSources != 2 {
		t.Errorf("expected 2 sources, got %d", stats.TotalSources)
	}

	expectedAvg := (0.9 + 0.8 + 0.7) / 3.0
	if math.Abs(stats.AvgConfidence-expectedAvg) > 0.01 {
		t.Errorf("expected avg confidence %.2f, got %.2f", expectedAvg, stats.AvgConfidence)
	}
}

func TestGetStats_FactsByType(t *testing.T) {
	engine := newTestEngine(t)

	m1 := addTestMemory(t, engine, "Test content", "file1.md")

	addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.9)
	addTestFact(t, engine, m1, "user", "age", "25", "kv", 0.8)
	addTestFact(t, engine, m1, "user", "city", "NYC", "location", 0.7)
	addTestFact(t, engine, m1, "user", "hobby", "reading", "kv", 0.6)

	stats, err := engine.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	expected := map[string]int{
		"identity": 1,
		"kv":       2,
		"location": 1,
	}

	for factType, expectedCount := range expected {
		if stats.FactsByType[factType] != expectedCount {
			t.Errorf("expected %d facts of type %s, got %d", expectedCount, factType, stats.FactsByType[factType])
		}
	}
}

func TestGetStats_Freshness(t *testing.T) {
	engine := newTestEngine(t)

	// This test is challenging because we can't easily control import timestamps
	// We'll add some data and verify that freshness totals match memory count
	m1 := addTestMemory(t, engine, "Today content", "today.md")
	m2 := addTestMemory(t, engine, "Also today", "today2.md")

	stats, err := engine.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	totalFreshness := stats.Freshness.Today + stats.Freshness.ThisWeek + stats.Freshness.ThisMonth + stats.Freshness.Older
	if totalFreshness != stats.TotalMemories {
		t.Errorf("freshness totals (%d) don't match memory count (%d)", totalFreshness, stats.TotalMemories)
	}

	// New memories should appear in "today" bucket
	if stats.Freshness.Today == 0 {
		t.Error("expected some memories in 'today' bucket")
	}

	// Avoid unused variable warnings
	_ = m1
	_ = m2
}

func TestGetStats_StorageSize(t *testing.T) {
	engine := newTestEngine(t)

	// Add some data
	m1 := addTestMemory(t, engine, "Test content", "file1.md")
	addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.9)

	stats, err := engine.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	// In-memory databases may not report size, but it shouldn't be negative
	if stats.StorageBytes < 0 {
		t.Errorf("storage bytes should not be negative, got %d", stats.StorageBytes)
	}
}

// --- Stale Detection Tests ---

func TestGetStaleFacts_NoStale(t *testing.T) {
	engine := newTestEngine(t)

	m1 := addTestMemory(t, engine, "Fresh content", "fresh.md")
	addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.9)

	opts := StaleOpts{
		MaxConfidence: 0.5,
		MaxDays:       30,
		Limit:         50,
	}

	staleFacts, err := engine.GetStaleFacts(context.Background(), opts)
	if err != nil {
		t.Fatalf("GetStaleFacts failed: %v", err)
	}

	if len(staleFacts) != 0 {
		t.Errorf("expected no stale facts, got %d", len(staleFacts))
	}
}

func TestGetStaleFacts_DecayedFacts(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	m1 := addTestMemory(t, engine, "Old content", "old.md")
	factID := addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.8)

	// Manually set last_reinforced to old date to simulate staleness
	// We need to access the SQLite store directly for this
	sqliteStore := engine.store.(*store.SQLiteStore)
	oldTime := time.Now().UTC().AddDate(0, 0, -60) // 60 days ago

	_, err := sqliteStore.ExecContext(ctx,
		"UPDATE facts SET last_reinforced = ? WHERE id = ?",
		oldTime, factID)
	if err != nil {
		t.Fatalf("failed to update last_reinforced: %v", err)
	}

	opts := StaleOpts{
		MaxConfidence: 0.5,
		MaxDays:       30,
		Limit:         50,
	}

	staleFacts, err := engine.GetStaleFacts(ctx, opts)
	if err != nil {
		t.Fatalf("GetStaleFacts failed: %v", err)
	}

	if len(staleFacts) == 0 {
		t.Error("expected to find stale facts")
	}

	if staleFacts[0].DaysSinceReinforced < 30 {
		t.Errorf("expected days since reinforced >= 30, got %d", staleFacts[0].DaysSinceReinforced)
	}

	// Effective confidence should be less than original due to decay
	if staleFacts[0].EffectiveConfidence >= staleFacts[0].Fact.Confidence {
		t.Error("effective confidence should be less than original confidence due to decay")
	}
}

func TestGetStaleFacts_EffectiveConfidenceCalculation(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	m1 := addTestMemory(t, engine, "Test content", "test.md")
	factID := addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.8)

	// Set last_reinforced to 30 days ago
	sqliteStore := engine.store.(*store.SQLiteStore)
	oldTime := time.Now().UTC().AddDate(0, 0, -30)
	_, err := sqliteStore.ExecContext(ctx,
		"UPDATE facts SET last_reinforced = ?, decay_rate = ? WHERE id = ?",
		oldTime, 0.01, factID)
	if err != nil {
		t.Fatalf("failed to update fact: %v", err)
	}

	opts := StaleOpts{
		MaxConfidence: 0.9, // High threshold to catch the decayed fact
		MaxDays:       29,  // Just under 30 days
		Limit:         50,
	}

	staleFacts, err := engine.GetStaleFacts(ctx, opts)
	if err != nil {
		t.Fatalf("GetStaleFacts failed: %v", err)
	}

	if len(staleFacts) == 0 {
		t.Error("expected to find stale facts")
		return
	}

	// Calculate expected effective confidence: 0.8 * exp(-0.01 * 30)
	expectedEffective := 0.8 * math.Exp(-0.01*30)
	actualEffective := staleFacts[0].EffectiveConfidence

	if math.Abs(actualEffective-expectedEffective) > 0.01 {
		t.Errorf("expected effective confidence %.3f, got %.3f", expectedEffective, actualEffective)
	}
}

func TestGetStaleFacts_SortedByStaleness(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	m1 := addTestMemory(t, engine, "Test content", "test.md")

	// Add facts with different staleness levels
	f1 := addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.9)
	f2 := addTestFact(t, engine, m1, "user", "age", "25", "kv", 0.5)

	sqliteStore := engine.store.(*store.SQLiteStore)

	// Make f1 more stale (older)
	_, err := sqliteStore.ExecContext(ctx,
		"UPDATE facts SET last_reinforced = ? WHERE id = ?",
		time.Now().UTC().AddDate(0, 0, -60), f1)
	if err != nil {
		t.Fatalf("failed to update f1: %v", err)
	}

	// Make f2 less stale
	_, err = sqliteStore.ExecContext(ctx,
		"UPDATE facts SET last_reinforced = ? WHERE id = ?",
		time.Now().UTC().AddDate(0, 0, -40), f2)
	if err != nil {
		t.Fatalf("failed to update f2: %v", err)
	}

	opts := StaleOpts{
		MaxConfidence: 0.9,
		MaxDays:       30,
		Limit:         50,
	}

	staleFacts, err := engine.GetStaleFacts(ctx, opts)
	if err != nil {
		t.Fatalf("GetStaleFacts failed: %v", err)
	}

	if len(staleFacts) < 2 {
		t.Fatalf("expected at least 2 stale facts, got %d", len(staleFacts))
	}

	// Should be sorted by effective confidence (ascending - stalest first)
	if staleFacts[0].EffectiveConfidence > staleFacts[1].EffectiveConfidence {
		t.Error("stale facts should be sorted by effective confidence (lowest first)")
	}
}

func TestGetStaleFacts_Limit(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	m1 := addTestMemory(t, engine, "Test content", "test.md")

	// Add multiple old facts
	for i := 0; i < 5; i++ {
		factID := addTestFact(t, engine, m1, "user", "fact", "value", "kv", 0.5)

		sqliteStore := engine.store.(*store.SQLiteStore)
		_, err := sqliteStore.ExecContext(ctx,
			"UPDATE facts SET last_reinforced = ? WHERE id = ?",
			time.Now().UTC().AddDate(0, 0, -60), factID)
		if err != nil {
			t.Fatalf("failed to update fact %d: %v", i, err)
		}
	}

	opts := StaleOpts{
		MaxConfidence: 0.9,
		MaxDays:       30,
		Limit:         3,
	}

	staleFacts, err := engine.GetStaleFacts(ctx, opts)
	if err != nil {
		t.Fatalf("GetStaleFacts failed: %v", err)
	}

	if len(staleFacts) != 3 {
		t.Errorf("expected 3 stale facts due to limit, got %d", len(staleFacts))
	}
}

// --- Conflict Detection Tests ---

func TestGetConflicts_AttributeConflict(t *testing.T) {
	engine := newTestEngine(t)

	m1 := addTestMemory(t, engine, "Test content", "test.md")

	// Add conflicting facts: same subject+predicate, different objects
	addTestFact(t, engine, m1, "user", "timezone", "EST", "preference", 0.8)
	addTestFact(t, engine, m1, "user", "timezone", "PST", "preference", 0.7)

	conflicts, err := engine.GetConflicts(context.Background())
	if err != nil {
		t.Fatalf("GetConflicts failed: %v", err)
	}

	if len(conflicts) != 1 {
		t.Errorf("expected 1 conflict, got %d", len(conflicts))
	}

	if len(conflicts) > 0 {
		c := conflicts[0]
		if c.ConflictType != "attribute" {
			t.Errorf("expected conflict type 'attribute', got '%s'", c.ConflictType)
		}
		if c.Similarity != 1.0 {
			t.Errorf("expected similarity 1.0, got %.2f", c.Similarity)
		}
	}
}

func TestGetConflicts_NoConflicts(t *testing.T) {
	engine := newTestEngine(t)

	m1 := addTestMemory(t, engine, "Clean content", "clean.md")

	// Add non-conflicting facts
	addTestFact(t, engine, m1, "user", "name", "Alice", "identity", 0.9)
	addTestFact(t, engine, m1, "user", "age", "25", "kv", 0.8)
	addTestFact(t, engine, m1, "system", "version", "1.0", "kv", 0.7)

	conflicts, err := engine.GetConflicts(context.Background())
	if err != nil {
		t.Fatalf("GetConflicts failed: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts, got %d", len(conflicts))
	}
}

func TestGetConflicts_CaseInsensitiveMatching(t *testing.T) {
	engine := newTestEngine(t)

	m1 := addTestMemory(t, engine, "Test content", "test.md")

	// Add facts with different case but same subject+predicate
	addTestFact(t, engine, m1, "USER", "TIMEZONE", "EST", "preference", 0.8)
	addTestFact(t, engine, m1, "user", "timezone", "PST", "preference", 0.7)

	conflicts, err := engine.GetConflicts(context.Background())
	if err != nil {
		t.Fatalf("GetConflicts failed: %v", err)
	}

	if len(conflicts) != 1 {
		t.Errorf("expected 1 conflict (case-insensitive), got %d", len(conflicts))
	}
}

func TestGetConflicts_SameObjectNoConflict(t *testing.T) {
	engine := newTestEngine(t)

	m1 := addTestMemory(t, engine, "Test content", "test.md")

	// Add facts with same subject+predicate+object (reinforcement, not conflict)
	addTestFact(t, engine, m1, "user", "timezone", "EST", "preference", 0.8)
	addTestFact(t, engine, m1, "user", "timezone", "EST", "preference", 0.9)

	conflicts, err := engine.GetConflicts(context.Background())
	if err != nil {
		t.Fatalf("GetConflicts failed: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts for same object values, got %d", len(conflicts))
	}
}

// --- Edge Cases ---

func TestStaleOpts_Defaults(t *testing.T) {
	engine := newTestEngine(t)

	// Empty opts should use defaults
	staleFacts, err := engine.GetStaleFacts(context.Background(), StaleOpts{})
	if err != nil {
		t.Fatalf("GetStaleFacts with empty opts failed: %v", err)
	}

	// Should not error and should return slice (even if empty)
	if staleFacts == nil {
		t.Error("staleFacts should not be nil")
	}
}

func TestGetStats_EmptyStrings(t *testing.T) {
	engine := newTestEngine(t)

	m1 := addTestMemory(t, engine, "Test content", "") // Empty source file
	addTestFact(t, engine, m1, "", "", "", "kv", 0.0)  // Empty fact fields

	stats, err := engine.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats with empty strings failed: %v", err)
	}

	// Should handle empty data gracefully
	if stats.TotalSources < 0 {
		t.Error("total sources should not be negative")
	}
}

func TestNewEngine(t *testing.T) {
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	engine := NewEngine(s, ":memory:")
	if engine == nil {
		t.Fatal("NewEngine returned nil")
	}
	if engine.store != s {
		t.Error("engine store not set correctly")
	}
	if engine.dbPath != ":memory:" {
		t.Error("engine dbPath not set correctly")
	}
}
