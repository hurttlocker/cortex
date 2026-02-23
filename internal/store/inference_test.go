package store

import (
	"context"
	"fmt"
	"testing"
)

func TestInferenceDryRun(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})

	// Create facts with same subject, different predicates
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "cortex", Predicate: "language", Object: "Go", FactType: "kv"})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "cortex", Predicate: "database", Object: "SQLite", FactType: "kv"})

	opts := DefaultInferenceOpts()
	opts.DryRun = true

	result, err := s.RunInference(ctx, opts)
	if err != nil {
		t.Fatalf("RunInference dry-run: %v", err)
	}

	if result.EdgesCreated != 0 {
		t.Fatal("Dry-run should not create edges")
	}
	if len(result.Proposals) == 0 {
		t.Fatal("Expected proposals from subject clustering")
	}
}

func TestInferenceApply(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})

	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "cortex", Predicate: "language", Object: "Go", FactType: "kv"})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "cortex", Predicate: "database", Object: "SQLite", FactType: "kv"})

	opts := DefaultInferenceOpts()
	opts.DryRun = false

	result, err := s.RunInference(ctx, opts)
	if err != nil {
		t.Fatalf("RunInference apply: %v", err)
	}

	if result.EdgesCreated == 0 {
		t.Fatal("Expected at least 1 edge created")
	}

	// Verify edge exists
	edgeCount, _ := s.CountEdges(ctx)
	if edgeCount == 0 {
		t.Fatal("Expected edges in database after inference")
	}
}

func TestInferenceCooccurrence(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	// Create co-occurrence above threshold
	for i := 0; i < 7; i++ {
		s.RecordCooccurrence(ctx, f1, f2)
	}

	opts := DefaultInferenceOpts()
	opts.DryRun = true

	result, _ := s.RunInference(ctx, opts)

	found := false
	for _, p := range result.Proposals {
		if p.Rule == "cooccurrence" {
			found = true
		}
	}
	if !found {
		t.Fatal("Expected co-occurrence rule to propose an edge")
	}
}

func TestInferenceSupersession(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})

	// Same subject+predicate, different objects
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "config", Predicate: "value", Object: "old_val", FactType: "kv"})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "config", Predicate: "value", Object: "new_val", FactType: "kv"})

	opts := DefaultInferenceOpts()
	opts.DryRun = true

	result, _ := s.RunInference(ctx, opts)

	found := false
	for _, p := range result.Proposals {
		if p.Rule == "supersession" {
			found = true
		}
	}
	if !found {
		t.Fatal("Expected supersession rule to propose an edge")
	}
}

func TestInferenceMaxEdges(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})

	// Create many facts with same subject to generate many proposals
	for i := 0; i < 10; i++ {
		s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "project", Predicate: fmt.Sprintf("attr%d", i), Object: fmt.Sprintf("val%d", i), FactType: "kv"})
	}

	opts := DefaultInferenceOpts()
	opts.DryRun = false
	opts.MaxEdges = 3

	result, _ := s.RunInference(ctx, opts)

	if result.EdgesCreated > 3 {
		t.Fatalf("Expected max 3 edges, got %d", result.EdgesCreated)
	}
}

func TestInferenceMinConfidence(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})

	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "x", Predicate: "a", Object: "v1", FactType: "kv"})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "x", Predicate: "b", Object: "v2", FactType: "kv"})

	// High confidence threshold should filter out low-confidence subject clustering (0.4)
	opts := DefaultInferenceOpts()
	opts.DryRun = true
	opts.MinConfidence = 0.8

	result, _ := s.RunInference(ctx, opts)

	for _, p := range result.Proposals {
		if p.Rule == "subject_clustering" {
			t.Fatal("Subject clustering (conf=0.4) should be filtered at minConf=0.8")
		}
	}
}
