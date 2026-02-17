package store

import (
	"context"
	"testing"
	"time"
)

// TestGoTimestampWithSubstrPattern verifies that Go UTC timestamps work correctly
// with SQLite queries using SUBSTR pattern instead of DATE() function.
// This addresses Issue #12: SQLite DATE() incompatible with Go UTC timestamp format.
func TestGoTimestampWithSubstrPattern(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Insert a memory with Go-format timestamp
	now := time.Now().UTC()
	m := &Memory{
		Content:    "Test memory with Go timestamp",
		SourceFile: "/test.md",
		ImportedAt: now,
	}
	
	id, err := store.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// Test that SUBSTR(imported_at, 1, 10) extracts the date correctly
	var extractedDate string
	err = store.(*SQLiteStore).db.QueryRowContext(ctx,
		"SELECT SUBSTR(imported_at, 1, 10) FROM memories WHERE id = ?", id,
	).Scan(&extractedDate)
	
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	expectedDate := now.Format("2006-01-02")
	if extractedDate != expectedDate {
		t.Errorf("expected date %s, got %s", expectedDate, extractedDate)
	}

	// Test that SUBSTR(imported_at, 1, 19) extracts the datetime correctly  
	var extractedDateTime string
	err = store.(*SQLiteStore).db.QueryRowContext(ctx,
		"SELECT SUBSTR(imported_at, 1, 19) FROM memories WHERE id = ?", id,
	).Scan(&extractedDateTime)
	
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	expectedDateTime := now.Format("2006-01-02 15:04:05")
	if extractedDateTime != expectedDateTime {
		t.Errorf("expected datetime %s, got %s", expectedDateTime, extractedDateTime)
	}
}

// TestSQLiteDateFunctionBreaks demonstrates that SQL DATE() function returns NULL 
// for Go's UTC timestamp format, which is why we use SUBSTR pattern.
func TestSQLiteDateFunctionBreaks(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Insert a memory with Go timestamp
	m := &Memory{
		Content:    "Test memory",
		SourceFile: "/test.md",
		ImportedAt: time.Now().UTC(),
	}
	
	if _, err := store.AddMemory(ctx, m); err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// Count records where DATE() returns NULL vs SUBSTR() returns valid dates
	var nullDateCount, validSubstrCount int
	
	err := store.(*SQLiteStore).db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memories WHERE DATE(imported_at) IS NULL",
	).Scan(&nullDateCount)
	if err != nil {
		t.Fatalf("query for null dates failed: %v", err)
	}

	err = store.(*SQLiteStore).db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memories WHERE SUBSTR(imported_at, 1, 10) IS NOT NULL AND SUBSTR(imported_at, 1, 10) != ''",
	).Scan(&validSubstrCount)
	if err != nil {
		t.Fatalf("query for valid substr dates failed: %v", err)
	}

	// This demonstrates the bug: DATE() returns NULL for Go timestamps but SUBSTR works
	if nullDateCount == 0 {
		t.Skip("DATE() unexpectedly parsed Go timestamp - this may indicate SQLite version differences")
	}
	
	if validSubstrCount != 1 {
		t.Errorf("expected SUBSTR to work for 1 timestamp, got %d", validSubstrCount)
	}
	
	t.Logf("DATE() returns NULL for %d Go timestamps (demonstrates the bug)", nullDateCount)
	t.Logf("SUBSTR() works correctly for %d Go timestamps", validSubstrCount)
}

// TestFreshnessDistributionUsesCorrectPattern verifies that GetFreshnessDistribution
// uses the SUBSTR pattern and doesn't break with Go timestamps.
func TestFreshnessDistributionUsesCorrectPattern(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Add a memory with Go timestamp
	m := &Memory{
		Content:    "Test memory for freshness",
		SourceFile: "/test.md", 
		ImportedAt: time.Now().UTC(),
	}
	
	if _, err := store.AddMemory(ctx, m); err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}

	// This should not fail - if it used DATE() on Go timestamps, it would return 0 for everything
	freshness, err := store.GetFreshnessDistribution(ctx)
	if err != nil {
		t.Fatalf("GetFreshnessDistribution failed: %v", err)
	}

	// At least one memory should be counted (total should be > 0)
	total := freshness.Today + freshness.ThisWeek + freshness.ThisMonth + freshness.Older
	if total == 0 {
		t.Errorf("GetFreshnessDistribution returned all zeros - this suggests DATE() function is being used instead of SUBSTR")
	}
	
	t.Logf("Freshness distribution: Today=%d, ThisWeek=%d, ThisMonth=%d, Older=%d", 
		freshness.Today, freshness.ThisWeek, freshness.ThisMonth, freshness.Older)
}