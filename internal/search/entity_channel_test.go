package search

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestSearchEntityProfilesInjectsKnownEntityFacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:       "General meeting notes without the entity name in the body.",
		SourceFile:    "entity-profile.md",
		SourceSection: "notes",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:    memID,
		Subject:     "Alice",
		Predicate:   "role",
		Object:      "project manager",
		FactType:    "relationship",
		Confidence:  0.95,
		SourceQuote: "Alice is the project manager.",
	}); err != nil {
		t.Fatalf("add fact: %v", err)
	}

	engine := NewEngine(s)

	withoutEntityGraph, err := engine.Search(ctx, "What does Alice do?", Options{
		Mode:  ModeKeyword,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("search without entity graph: %v", err)
	}
	if len(withoutEntityGraph) != 0 {
		t.Fatalf("expected no baseline keyword hits, got %d", len(withoutEntityGraph))
	}

	withEntityGraph, err := engine.Search(ctx, "What does Alice do?", Options{
		Mode:        ModeKeyword,
		Limit:       5,
		EntityGraph: true,
	})
	if err != nil {
		t.Fatalf("search with entity graph flag: %v", err)
	}
	if len(withEntityGraph) == 0 {
		t.Fatal("expected profile injection to surface the Alice memory")
	}
	if withEntityGraph[0].MemoryID != memID {
		t.Fatalf("top result memory_id = %d, want %d", withEntityGraph[0].MemoryID, memID)
	}
	if withEntityGraph[0].MatchType != "profile" {
		t.Fatalf("match_type = %q, want profile", withEntityGraph[0].MatchType)
	}
	if len(withEntityGraph[0].FactIDs) == 0 {
		t.Fatal("expected injected result to carry fact ids")
	}
}

func TestSearchRRFEntityGraphFindsMultiEntityResultsWithoutEmbedder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memA, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Unrelated body text A.",
		SourceFile: "alice.md",
	})
	if err != nil {
		t.Fatalf("add Alice memory: %v", err)
	}
	memB, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Unrelated body text B.",
		SourceFile: "bob.md",
	})
	if err != nil {
		t.Fatalf("add Bob memory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memA,
		Subject:    "Alice",
		Predicate:  "uses",
		Object:     "Cortex",
		FactType:   "relationship",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("add Alice fact: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memB,
		Subject:    "Bob",
		Predicate:  "uses",
		Object:     "Cortex",
		FactType:   "relationship",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("add Bob fact: %v", err)
	}

	engine := NewEngine(s)

	results, err := engine.Search(ctx, "What do Alice and Bob have in common?", Options{
		Mode:        ModeRRF,
		Limit:       5,
		EntityGraph: true,
	})
	if err != nil {
		t.Fatalf("rrf search with entity graph: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected entity graph channel to surface both entity memories, got %d results", len(results))
	}

	foundA := false
	foundB := false
	for _, result := range results {
		if result.MemoryID == memA {
			foundA = true
		}
		if result.MemoryID == memB {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatalf("expected both entity memories in results, foundA=%v foundB=%v", foundA, foundB)
	}
}
