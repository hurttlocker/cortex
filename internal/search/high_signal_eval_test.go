package search

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestHighSignalSearch_PrefersCuratedFactOverPromptEcho(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _ = s.AddMemory(ctx, &store.Memory{
		Content:    "Q prefers green for additions and blue for deletions in code diffs.",
		SourceFile: "memory/2026-03-18.md",
	})
	_, _ = s.AddMemory(ctx, &store.Memory{
		Content:    "Run these test queries and verify: Q preferences for code diffs should mention green/blue.",
		SourceFile: "/tmp/cortex-capture-abc/auto-capture.md",
	})

	engine := NewEngine(s)
	results, err := engine.Search(ctx, "Q preferences for code diffs", Options{Mode: ModeKeyword, Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].SourceFile != "memory/2026-03-18.md" {
		t.Fatalf("expected curated memory result first, got %+v", results[0])
	}
}

func TestHighSignalSearchFacts_PrefersDirectPreferenceFact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &store.Memory{
		Content:    "Q prefers green for additions and blue for deletions in code diffs.",
		SourceFile: "memory/2026-03-18.md",
	})
	_, _ = s.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Q",
		Predicate:  "prefers",
		Object:     "green for additions and blue for deletions in code diffs",
		FactType:   "preference",
		Confidence: 0.95,
	})

	promptID, _ := s.AddMemory(ctx, &store.Memory{
		Content:    "Run these test queries and verify: Q preferences for code diffs should mention green/blue.",
		SourceFile: "/tmp/cortex-capture-abc/auto-capture.md",
	})
	_, _ = s.AddFact(ctx, &store.Fact{
		MemoryID:   promptID,
		Subject:    "task",
		Predicate:  "query",
		Object:     "Q preferences for code diffs",
		FactType:   "kv",
		Confidence: 0.30,
	})

	engine := NewEngine(s)
	results, err := engine.SearchFacts(ctx, "Q preferences for code diffs", Options{Limit: 5})
	if err != nil {
		t.Fatalf("SearchFacts failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected fact results")
	}
	if results[0].SourceFile != "memory/2026-03-18.md" {
		t.Fatalf("expected direct preference fact first, got %+v", results[0])
	}
}
