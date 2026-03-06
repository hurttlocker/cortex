package store

import (
	"context"
	"testing"
)

func TestDeleteMemoriesBySourceFile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Seed: two memories from source A, one from source B
	srcA := "/fake/path/a.md"
	srcB := "/fake/path/b.md"

	idA1, err := s.AddMemory(ctx, &Memory{Content: "memory A1", SourceFile: srcA, SourceLine: 1})
	if err != nil {
		t.Fatalf("AddMemory A1: %v", err)
	}
	idA2, err := s.AddMemory(ctx, &Memory{Content: "memory A2", SourceFile: srcA, SourceLine: 10})
	if err != nil {
		t.Fatalf("AddMemory A2: %v", err)
	}
	idB1, err := s.AddMemory(ctx, &Memory{Content: "memory B1", SourceFile: srcB, SourceLine: 1})
	if err != nil {
		t.Fatalf("AddMemory B1: %v", err)
	}

	// Attach facts to each memory
	if _, err := s.AddFact(ctx, &Fact{MemoryID: idA1, Subject: "a1", Predicate: "knows", Object: "go", FactType: "kv"}); err != nil {
		t.Fatalf("AddFact A1: %v", err)
	}
	if _, err := s.AddFact(ctx, &Fact{MemoryID: idA2, Subject: "a2", Predicate: "likes", Object: "rust", FactType: "kv"}); err != nil {
		t.Fatalf("AddFact A2: %v", err)
	}
	if _, err := s.AddFact(ctx, &Fact{MemoryID: idB1, Subject: "b1", Predicate: "uses", Object: "vim", FactType: "kv"}); err != nil {
		t.Fatalf("AddFact B1: %v", err)
	}

	// Delete memories for source A
	removed, err := s.DeleteMemoriesBySourceFile(ctx, srcA)
	if err != nil {
		t.Fatalf("DeleteMemoriesBySourceFile: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 memories removed, got %d", removed)
	}

	// Source A memories should be soft-deleted (not visible via ListMemories)
	remaining, err := s.ListMemories(ctx, ListOpts{SourceFile: srcA, Limit: 100})
	if err != nil {
		t.Fatalf("ListMemories A: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 active memories for srcA, got %d", len(remaining))
	}

	// Source B memory should be untouched
	bMems, err := s.ListMemories(ctx, ListOpts{SourceFile: srcB, Limit: 100})
	if err != nil {
		t.Fatalf("ListMemories B: %v", err)
	}
	if len(bMems) != 1 {
		t.Errorf("expected 1 active memory for srcB, got %d", len(bMems))
	}

	// Facts for A should be hard-deleted
	aFacts, err := s.GetFactsByMemoryIDs(ctx, []int64{idA1, idA2})
	if err != nil {
		t.Fatalf("GetFactsByMemoryIDs A: %v", err)
	}
	if len(aFacts) != 0 {
		t.Errorf("expected 0 facts for srcA memories, got %d", len(aFacts))
	}

	// Facts for B should be untouched
	bFacts, err := s.GetFactsByMemoryIDs(ctx, []int64{idB1})
	if err != nil {
		t.Fatalf("GetFactsByMemoryIDs B: %v", err)
	}
	if len(bFacts) != 1 {
		t.Errorf("expected 1 fact for srcB memory, got %d", len(bFacts))
	}

	// Calling on already-deleted source returns 0 without error
	removed2, err := s.DeleteMemoriesBySourceFile(ctx, srcA)
	if err != nil {
		t.Fatalf("DeleteMemoriesBySourceFile second call: %v", err)
	}
	if removed2 != 0 {
		t.Errorf("expected 0 removed on second call, got %d", removed2)
	}

	// Calling on unknown source returns 0 without error
	removed3, err := s.DeleteMemoriesBySourceFile(ctx, "/nonexistent/source.md")
	if err != nil {
		t.Fatalf("DeleteMemoriesBySourceFile unknown: %v", err)
	}
	if removed3 != 0 {
		t.Errorf("expected 0 removed for unknown source, got %d", removed3)
	}
}

// TestDeleteMemoriesBySourceFileReimportable verifies that after deletion,
// the same content can be reimported (i.e. FindByHash no longer blocks it).
func TestDeleteMemoriesBySourceFileReimportable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src := "/fake/path/notes.md"
	content := "hello world"

	// First import
	id, err := s.AddMemory(ctx, &Memory{
		Content:     content,
		SourceFile:  src,
		ContentHash: HashContentOnly(content),
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Hash lookup finds it before deletion
	found, err := s.FindByHash(ctx, HashContentOnly(content))
	if err != nil {
		t.Fatalf("FindByHash before deletion: %v", err)
	}
	if found == nil || found.ID != id {
		t.Fatalf("expected to find memory before deletion")
	}

	// Delete by source
	removed, err := s.DeleteMemoriesBySourceFile(ctx, src)
	if err != nil {
		t.Fatalf("DeleteMemoriesBySourceFile: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Hash lookup should return nil (soft-deleted) — reimport is unblocked
	found2, err := s.FindByHash(ctx, HashContentOnly(content))
	if err != nil {
		t.Fatalf("FindByHash after deletion: %v", err)
	}
	if found2 != nil {
		t.Errorf("expected FindByHash to return nil after deletion, got memory id=%d", found2.ID)
	}
}

func TestDeleteFactsByMemoryID_CleansDependentRows(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	mem1, err := s.AddMemory(ctx, &Memory{Content: "source a", SourceFile: "a.md"})
	if err != nil {
		t.Fatalf("AddMemory mem1: %v", err)
	}
	mem2, err := s.AddMemory(ctx, &Memory{Content: "source b", SourceFile: "b.md"})
	if err != nil {
		t.Fatalf("AddMemory mem2: %v", err)
	}

	fact1, err := s.AddFact(ctx, &Fact{MemoryID: mem1, Subject: "alpha", Predicate: "status", Object: "old", FactType: "state", Confidence: 0.7})
	if err != nil {
		t.Fatalf("AddFact fact1: %v", err)
	}
	fact2, err := s.AddFact(ctx, &Fact{MemoryID: mem1, Subject: "alpha", Predicate: "owner", Object: "q", FactType: "identity", Confidence: 0.8})
	if err != nil {
		t.Fatalf("AddFact fact2: %v", err)
	}
	fact3, err := s.AddFact(ctx, &Fact{MemoryID: mem2, Subject: "alpha", Predicate: "status", Object: "new", FactType: "state", Confidence: 0.9})
	if err != nil {
		t.Fatalf("AddFact fact3: %v", err)
	}

	if err := s.SupersedeFact(ctx, fact1, fact3, "refreshed"); err != nil {
		t.Fatalf("SupersedeFact: %v", err)
	}

	fact1Ptr := func(v int64) *int64 { return &v }(fact1)
	fact2Ptr := func(v int64) *int64 { return &v }(fact2)
	if err := s.CreateAlert(ctx, &Alert{AlertType: AlertTypeDecay, Severity: AlertSeverityWarning, FactID: fact1Ptr, RelatedFactID: fact2Ptr, Message: "test"}); err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO fact_accesses_v1 (fact_id, agent_id, access_type) VALUES (?, 'agent:main', 'read')`, fact1); err != nil {
		t.Fatalf("insert fact_access: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO fact_edges_v1 (source_fact_id, target_fact_id, edge_type, confidence, source, agent_id) VALUES (?, ?, 'supports', 0.9, 'explicit', '')`, fact1, fact3); err != nil {
		t.Fatalf("insert fact_edge: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO fact_cooccurrence_v1 (fact_id_a, fact_id_b, count, last_seen) VALUES (?, ?, 1, CURRENT_TIMESTAMP)`, fact1, fact3); err != nil {
		t.Fatalf("insert cooccurrence: %v", err)
	}

	deleted, err := s.DeleteFactsByMemoryID(ctx, mem1)
	if err != nil {
		t.Fatalf("DeleteFactsByMemoryID: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted facts, got %d", deleted)
	}

	remaining, err := s.GetFactsByMemoryIDsIncludingSuperseded(ctx, []int64{mem1})
	if err != nil {
		t.Fatalf("GetFactsByMemoryIDsIncludingSuperseded: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining facts for mem1, got %d", len(remaining))
	}

	kept, err := s.GetFact(ctx, fact3)
	if err != nil {
		t.Fatalf("GetFact fact3: %v", err)
	}
	if kept == nil {
		t.Fatalf("expected surviving fact3 to remain")
	}
	if kept.SupersededBy != nil {
		t.Fatalf("expected surviving fact superseded_by cleared, got %v", *kept.SupersededBy)
	}

	var count int
	checks := []struct {
		query string
		name  string
		args  []any
	}{
		{`SELECT COUNT(*) FROM alerts WHERE fact_id = ? OR related_fact_id = ?`, "alerts", []any{fact1, fact1}},
		{`SELECT COUNT(*) FROM fact_accesses_v1 WHERE fact_id = ?`, "fact_accesses_v1", []any{fact1}},
		{`SELECT COUNT(*) FROM fact_edges_v1 WHERE source_fact_id = ? OR target_fact_id = ?`, "fact_edges_v1", []any{fact1, fact1}},
		{`SELECT COUNT(*) FROM fact_cooccurrence_v1 WHERE fact_id_a = ? OR fact_id_b = ?`, "fact_cooccurrence_v1", []any{fact1, fact1}},
	}
	for _, check := range checks {
		if err := s.db.QueryRowContext(ctx, check.query, check.args...).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("expected %s rows referencing deleted fact to be gone, got %d", check.name, count)
		}
	}
}
