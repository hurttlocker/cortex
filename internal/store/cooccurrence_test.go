package store

import (
	"context"
	"testing"
)

func TestRecordCooccurrence(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	err := s.RecordCooccurrence(ctx, f1, f2)
	if err != nil {
		t.Fatalf("RecordCooccurrence: %v", err)
	}

	// Record again — count should increment
	s.RecordCooccurrence(ctx, f1, f2)
	s.RecordCooccurrence(ctx, f2, f1) // Reversed order — same pair

	pairs, _ := s.GetCooccurrencesForFact(ctx, f1, 10)
	if len(pairs) != 1 {
		t.Fatalf("Expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].Count != 3 {
		t.Fatalf("Expected count 3, got %d", pairs[0].Count)
	}
}

func TestRecordCooccurrenceSelf(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	err := s.RecordCooccurrence(ctx, 1, 1)
	if err != nil {
		t.Fatalf("Self co-occurrence should be silently ignored, got: %v", err)
	}

	count, _ := s.CountCooccurrences(ctx)
	if count != 0 {
		t.Fatalf("Expected 0 pairs after self co-occurrence, got %d", count)
	}
}

func TestRecordCooccurrenceBatch(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})
	f3, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "c", Predicate: "p", Object: "o3", FactType: "kv"})

	err := s.RecordCooccurrenceBatch(ctx, []int64{f1, f2, f3})
	if err != nil {
		t.Fatalf("RecordCooccurrenceBatch: %v", err)
	}

	// Should have 3 pairs: (f1,f2), (f1,f3), (f2,f3)
	count, _ := s.CountCooccurrences(ctx)
	if count != 3 {
		t.Fatalf("Expected 3 pairs from 3 facts, got %d", count)
	}
}

func TestGetTopCooccurrences(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})
	f3, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "c", Predicate: "p", Object: "o3", FactType: "kv"})

	// f1+f2 co-occur 5 times, f1+f3 co-occur 2 times
	for i := 0; i < 5; i++ {
		s.RecordCooccurrence(ctx, f1, f2)
	}
	s.RecordCooccurrence(ctx, f1, f3)
	s.RecordCooccurrence(ctx, f1, f3)

	top, _ := s.GetTopCooccurrences(ctx, 10)
	if len(top) != 2 {
		t.Fatalf("Expected 2 pairs, got %d", len(top))
	}
	if top[0].Count != 5 {
		t.Fatalf("Expected top pair count=5, got %d", top[0].Count)
	}
}

func TestSuggestEdgesFromCooccurrence(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})
	f3, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "c", Predicate: "p", Object: "o3", FactType: "kv"})

	// f1+f2 co-occur 6 times (above threshold)
	for i := 0; i < 6; i++ {
		s.RecordCooccurrence(ctx, f1, f2)
	}
	// f1+f3 co-occur 2 times (below threshold)
	s.RecordCooccurrence(ctx, f1, f3)
	s.RecordCooccurrence(ctx, f1, f3)

	suggestions, _ := s.SuggestEdgesFromCooccurrence(ctx, 5)
	if len(suggestions) != 1 {
		t.Fatalf("Expected 1 suggestion (f1+f2), got %d", len(suggestions))
	}

	// Now add an edge — should no longer suggest
	s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeRelatesTo})
	suggestions2, _ := s.SuggestEdgesFromCooccurrence(ctx, 5)
	if len(suggestions2) != 0 {
		t.Fatalf("Expected 0 suggestions after edge added, got %d", len(suggestions2))
	}
}

func TestGraphTraversalFollowsCooccurrence(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	// No edges, but high co-occurrence (>= 5)
	for i := 0; i < 6; i++ {
		s.RecordCooccurrence(ctx, f1, f2)
	}

	nodes, _ := s.TraverseGraph(ctx, f1, 1, 0)
	if len(nodes) != 2 {
		t.Fatalf("Expected 2 nodes (root + co-occurred neighbor), got %d", len(nodes))
	}
}

func TestRecordCooccurrenceBatchDuplicateIDs(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	// Input has duplicate IDs — should deduplicate, no self-pairs
	err := s.RecordCooccurrenceBatch(ctx, []int64{f1, f1, f2, f2})
	if err != nil {
		t.Fatalf("RecordCooccurrenceBatch with dupes: %v", err)
	}

	// Should have exactly 1 pair (f1,f2) with count 1
	count, _ := s.CountCooccurrences(ctx)
	if count != 1 {
		t.Fatalf("Expected 1 pair after dedup, got %d", count)
	}

	pairs, _ := s.GetCooccurrencesForFact(ctx, f1, 10)
	if len(pairs) != 1 || pairs[0].Count != 1 {
		t.Fatalf("Expected 1 pair with count=1, got %d pairs", len(pairs))
	}
}

func TestRecordCooccurrenceBatchSingleID(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	// Single ID (even repeated) should be a no-op
	err := s.RecordCooccurrenceBatch(ctx, []int64{1, 1, 1})
	if err != nil {
		t.Fatalf("Expected no error for single unique ID: %v", err)
	}

	count, _ := s.CountCooccurrences(ctx)
	if count != 0 {
		t.Fatalf("Expected 0 pairs, got %d", count)
	}
}

func TestRecordCooccurrenceBatchZeroAndNegativeIDs(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})

	// Zero and negative IDs should be filtered out
	err := s.RecordCooccurrenceBatch(ctx, []int64{0, -1, f1})
	if err != nil {
		t.Fatalf("Expected no error: %v", err)
	}

	count, _ := s.CountCooccurrences(ctx)
	if count != 0 {
		t.Fatalf("Expected 0 pairs (only 1 valid ID), got %d", count)
	}
}
