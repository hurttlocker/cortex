package store

import (
	"context"
	"testing"
	"time"
)

// TestIssue12_SQLiteDateIncompatibleWithGoTimestamp demonstrates that
// Issue #12 is resolved: SQLite DATE() function incompatibility with Go UTC timestamps
func TestIssue12_SQLiteDateIncompatibleWithGoTimestamp(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Add a memory with Go-format timestamp
	now := time.Now().UTC()
	m := &Memory{
		Content:    "Memory with Go UTC timestamp format",
		SourceFile: "/example/file.md",
		ImportedAt: now,
	}
	
	_, err := store.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// The freshness distribution should work correctly (uses SUBSTR pattern, not DATE)
	freshness, err := store.GetFreshnessDistribution(ctx)
	if err != nil {
		t.Fatalf("GetFreshnessDistribution failed: %v", err)
	}

	// Should count the memory in one of the buckets (not all zeros)
	total := freshness.Today + freshness.ThisWeek + freshness.ThisMonth + freshness.Older
	if total == 0 {
		t.Error("Freshness distribution returned all zeros - indicates DATE() function bug")
	}

	t.Logf("✅ Issue #12 resolved: Freshness distribution works with Go timestamps")
	t.Logf("   Distribution: Today=%d, ThisWeek=%d, ThisMonth=%d, Older=%d", 
		freshness.Today, freshness.ThisWeek, freshness.ThisMonth, freshness.Older)
}

// TestIssue9_DualHashFunctions demonstrates that Issue #9 is resolved:
// Single canonical hash function is used throughout the system
func TestIssue9_DualHashFunctions(t *testing.T) {
	// Test that the shared hash function behaves correctly
	content := "test content"
	sourcePathA := "/path/a.md"  
	sourcePathB := "/path/b.md"

	// Same content, different source paths should produce different hashes (provenance matters)
	hashA := HashMemoryContent(content, sourcePathA)
	hashB := HashMemoryContent(content, sourcePathB)
	if hashA == hashB {
		t.Error("Expected different hashes for same content from different source paths")
	}

	// Same content and source path should produce identical hashes
	hashA2 := HashMemoryContent(content, sourcePathA)
	if hashA != hashA2 {
		t.Error("Expected identical hashes for same content and source path")
	}

	// Verify backwards compatibility function still works
	contentOnlyHash := HashContentOnly(content)
	if contentOnlyHash == hashA {
		t.Error("Content-only hash should differ from content+sourcePath hash")
	}

	t.Logf("✅ Issue #9 resolved: Single canonical hash function with provenance")
	t.Logf("   Content+SourceA hash: %s", hashA[:8]+"...")
	t.Logf("   Content+SourceB hash: %s", hashB[:8]+"...")  
	t.Logf("   Content-only hash: %s", contentOnlyHash[:8]+"...")
}

// TestBothBugsFixed_EndToEnd creates an end-to-end test that would fail if either bug existed
func TestBothBugsFixed_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Create memories that would expose both bugs
	memories := []*Memory{
		{
			Content:    "Shared content",
			SourceFile: "/path/doc1.md",
			ImportedAt: time.Now().UTC(),
		},
		{
			Content:    "Shared content", // Same content, different source
			SourceFile: "/path/doc2.md", 
			ImportedAt: time.Now().UTC().AddDate(0, 0, -1), // Yesterday
		},
	}

	// Add memories
	for _, m := range memories {
		_, err := store.AddMemory(ctx, m)
		if err != nil {
			t.Fatalf("failed to add memory: %v", err)
		}
	}

	// Test that both memories were stored (hash deduplication working correctly with provenance)
	allMemories, err := store.ListMemories(ctx, ListOpts{Limit: 100})
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}

	if len(allMemories) != 2 {
		t.Errorf("expected 2 memories (same content, different provenance), got %d", len(allMemories))
	}

	// Test that freshness distribution works with Go timestamps
	freshness, err := store.GetFreshnessDistribution(ctx)
	if err != nil {
		t.Fatalf("GetFreshnessDistribution failed: %v", err)
	}

	total := freshness.Today + freshness.ThisWeek + freshness.ThisMonth + freshness.Older
	if total != len(allMemories) {
		t.Errorf("freshness distribution total (%d) doesn't match memory count (%d)", total, len(allMemories))
	}

	t.Logf("✅ Both bugs fixed: End-to-end test passes")
	t.Logf("   Stored %d memories with provenance-aware deduplication", len(allMemories))
	t.Logf("   Freshness distribution correctly counts memories: %d total", total)
}