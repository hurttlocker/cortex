package store

import (
	"context"
	"errors"
	"testing"
)

func TestAddEdge(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	edge := &FactEdge{
		SourceFactID: f1, TargetFactID: f2,
		EdgeType: EdgeTypeSupports, Source: EdgeSourceExplicit,
	}
	if err := s.AddEdge(ctx, edge); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if edge.ID == 0 {
		t.Fatal("Expected non-zero edge ID")
	}
}

func TestAddEdgeSelfLoop(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	err := s.AddEdge(ctx, &FactEdge{
		SourceFactID: 1, TargetFactID: 1, EdgeType: EdgeTypeSupports,
	})
	if err == nil {
		t.Fatal("Expected error for self-loop edge")
	}
}

func TestAddEdgeDuplicate(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	edge := &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeSupports}
	if err := s.AddEdge(ctx, edge); err != nil {
		t.Fatalf("First edge should succeed: %v", err)
	}
	if edge.ID == 0 {
		t.Fatal("First edge should have non-zero ID")
	}

	// Duplicate should return ErrEdgeExists
	err := s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeSupports})
	if !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("Expected ErrEdgeExists, got: %v", err)
	}

	// But a different type on same facts should succeed
	err = s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeRelatesTo})
	if err != nil {
		t.Fatalf("Different edge type should succeed: %v", err)
	}

	// Verify count
	edges, _ := s.GetEdgesForFact(ctx, f1)
	if len(edges) != 2 {
		t.Fatalf("Expected 2 edges, got %d", len(edges))
	}
}

func TestAddEdgeInvalidType(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	err := s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeType("bogus")})
	if err == nil {
		t.Fatal("Expected error for invalid edge type")
	}
}

func TestGetEdgesForFact(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})
	f3, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "c", Predicate: "p", Object: "o3", FactType: "kv"})

	s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeSupports})
	s.AddEdge(ctx, &FactEdge{SourceFactID: f3, TargetFactID: f1, EdgeType: EdgeTypeContradicts})

	edges, err := s.GetEdgesForFact(ctx, f1)
	if err != nil {
		t.Fatalf("GetEdgesForFact: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("Expected 2 edges for f1 (as source and target), got %d", len(edges))
	}
}

func TestRemoveEdge(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	edge := &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeRelatesTo}
	s.AddEdge(ctx, edge)

	if err := s.RemoveEdge(ctx, edge.ID); err != nil {
		t.Fatalf("RemoveEdge: %v", err)
	}

	edges, _ := s.GetEdgesForFact(ctx, f1)
	if len(edges) != 0 {
		t.Fatal("Expected 0 edges after removal")
	}
}

func TestTraverseGraph(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "graph test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "root", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "child", Predicate: "p", Object: "o2", FactType: "kv"})
	f3, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "grandchild", Predicate: "p", Object: "o3", FactType: "kv"})
	f4, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "unrelated", Predicate: "p", Object: "o4", FactType: "kv"})
	_ = f4

	s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeSupports})
	s.AddEdge(ctx, &FactEdge{SourceFactID: f2, TargetFactID: f3, EdgeType: EdgeTypeDerivedFrom})

	// Depth 2 should find f1, f2, f3 but not f4
	nodes, err := s.TraverseGraph(ctx, f1, 2, 0)
	if err != nil {
		t.Fatalf("TraverseGraph: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("Expected 3 nodes (root + child + grandchild), got %d", len(nodes))
	}

	// Depth 1 should only find f1, f2
	nodes1, _ := s.TraverseGraph(ctx, f1, 1, 0)
	if len(nodes1) != 2 {
		t.Fatalf("Expected 2 nodes at depth 1, got %d", len(nodes1))
	}
}

func TestTraverseGraphConfidenceFilter(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})
	f3, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "c", Predicate: "p", Object: "o3", FactType: "kv"})

	s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeRelatesTo, Confidence: 0.9})
	s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f3, EdgeType: EdgeTypeRelatesTo, Confidence: 0.3})

	// High confidence filter should only follow the strong edge
	nodes, _ := s.TraverseGraph(ctx, f1, 2, 0.5)
	if len(nodes) != 2 {
		t.Fatalf("Expected 2 nodes with minConf=0.5 (root + f2), got %d", len(nodes))
	}
}

func TestSupersedeCreatesEdge(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "config", Predicate: "value", Object: "old", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "config", Predicate: "value", Object: "new", FactType: "kv"})

	s.SupersedeFact(ctx, f1, f2, "updated")

	// Should have a supersedes edge
	edges, _ := s.GetEdgesForFact(ctx, f2)
	found := false
	for _, e := range edges {
		if e.EdgeType == EdgeTypeSupersedes && e.SourceFactID == f2 && e.TargetFactID == f1 {
			found = true
		}
	}
	if !found {
		t.Fatal("Expected 'supersedes' edge from new fact to old fact")
	}
}

func TestDecayInferredEdges(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	f1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	f2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	// Add inferred edge
	s.AddEdge(ctx, &FactEdge{SourceFactID: f1, TargetFactID: f2, EdgeType: EdgeTypeRelatesTo, Source: EdgeSourceInferred})

	// Backdate it
	s.db.ExecContext(ctx, `UPDATE fact_edges_v1 SET created_at = datetime('now', '-100 days')`)

	removed, err := s.DecayInferredEdges(ctx, 90)
	if err != nil {
		t.Fatalf("DecayInferredEdges: %v", err)
	}
	if removed != 1 {
		t.Fatalf("Expected 1 removed, got %d", removed)
	}
}

func TestParseEdgeType(t *testing.T) {
	for _, valid := range ValidEdgeTypes() {
		if _, err := ParseEdgeType(valid); err != nil {
			t.Errorf("Expected %q to be valid: %v", valid, err)
		}
	}

	if _, err := ParseEdgeType("invalid_type"); err == nil {
		t.Error("Expected error for invalid edge type")
	}
}
