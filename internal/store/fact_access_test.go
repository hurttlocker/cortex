package store

import (
	"context"
	"testing"
	"time"
)

func TestRecordFactAccess(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "test", Predicate: "value",
		Object: "hello", FactType: "kv",
	})

	err := s.RecordFactAccess(ctx, factID, "mister", AccessTypeSearch)
	if err != nil {
		t.Fatalf("RecordFactAccess: %v", err)
	}

	summary, err := s.GetFactAccessSummary(ctx, factID)
	if err != nil {
		t.Fatalf("GetFactAccessSummary: %v", err)
	}
	if summary.TotalAccess != 1 {
		t.Fatalf("Expected 1 access, got %d", summary.TotalAccess)
	}
	if summary.SearchCount != 1 {
		t.Fatalf("Expected 1 search access, got %d", summary.SearchCount)
	}
}

func TestRecordFactAccessBatch(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	id1, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "a", Predicate: "p", Object: "o1", FactType: "kv"})
	id2, _ := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "b", Predicate: "p", Object: "o2", FactType: "kv"})

	err := s.RecordFactAccessBatch(ctx, []int64{id1, id2}, "hawk", AccessTypeSearch)
	if err != nil {
		t.Fatalf("RecordFactAccessBatch: %v", err)
	}

	s1, _ := s.GetFactAccessSummary(ctx, id1)
	s2, _ := s.GetFactAccessSummary(ctx, id2)
	if s1.TotalAccess != 1 || s2.TotalAccess != 1 {
		t.Fatalf("Expected 1 access each, got %d and %d", s1.TotalAccess, s2.TotalAccess)
	}
}

func TestSearchImplicitReinforcement(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})

	// Create fact with last_reinforced set 30 days ago
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "old", Predicate: "fact",
		Object: "value", FactType: "kv",
	})

	// Manually backdate last_reinforced
	thirtyDaysAgo := time.Now().UTC().Add(-30 * 24 * time.Hour)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", thirtyDaysAgo, factID)

	// Record search access (weight 0.3)
	s.RecordFactAccess(ctx, factID, "mister", AccessTypeSearch)

	// Verify last_reinforced moved forward (but not to now)
	fact, _ := s.GetFact(ctx, factID)
	if !fact.LastReinforced.After(thirtyDaysAgo) {
		t.Fatal("Expected last_reinforced to move forward after search access")
	}
	if fact.LastReinforced.After(time.Now().UTC().Add(-1 * time.Hour)) {
		t.Fatal("Search access should not fully reset last_reinforced (weight=0.3)")
	}
}

func TestCrossAgentReinforcement(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "shared", SourceFile: "t.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "shared", Predicate: "fact",
		Object: "value", FactType: "kv",
	})

	// Only one agent
	s.RecordFactAccess(ctx, factID, "mister", AccessTypeSearch)
	amplified, _ := s.CheckCrossAgentReinforcement(ctx, factID, 30)
	if amplified {
		t.Fatal("Should not amplify with only 1 agent")
	}

	// Second agent
	s.RecordFactAccess(ctx, factID, "hawk", AccessTypeSearch)
	amplified, _ = s.CheckCrossAgentReinforcement(ctx, factID, 30)
	if !amplified {
		t.Fatal("Should amplify with 2 agents accessing same fact")
	}
}

func TestCrossAgentSummary(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "test", Predicate: "v",
		Object: "val", FactType: "kv",
	})

	s.RecordFactAccess(ctx, factID, "mister", AccessTypeSearch)
	s.RecordFactAccess(ctx, factID, "hawk", AccessTypeReinforce)
	s.RecordFactAccess(ctx, factID, "mister", AccessTypeSearch)

	summary, _ := s.GetFactAccessSummary(ctx, factID)
	if summary.UniqueAgents != 2 {
		t.Fatalf("Expected 2 unique agents, got %d", summary.UniqueAgents)
	}
	if !summary.CrossAgent {
		t.Fatal("Expected cross_agent=true")
	}
	if summary.TotalAccess != 3 {
		t.Fatalf("Expected 3 accesses, got %d", summary.TotalAccess)
	}
	if summary.SearchCount != 2 {
		t.Fatalf("Expected 2 search accesses, got %d", summary.SearchCount)
	}
}

func TestExplicitReinforceFullReset(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "test", SourceFile: "t.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "test", Predicate: "v",
		Object: "val", FactType: "kv",
	})

	// Backdate
	oldTime := time.Now().UTC().Add(-90 * 24 * time.Hour)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, factID)

	// Explicit reinforce (weight=1.0 = full reset)
	s.RecordFactAccess(ctx, factID, "mister", AccessTypeReinforce)

	fact, _ := s.GetFact(ctx, factID)
	// Should be within the last minute
	if time.Since(fact.LastReinforced) > time.Minute {
		t.Fatalf("Expected full reset to ~now, got last_reinforced=%v", fact.LastReinforced)
	}
}

func TestEffectiveConfidence(t *testing.T) {
	now := time.Now().UTC()

	// Fresh fact
	eff := EffectiveConfidence(0.9, 0.01, now)
	if eff < 0.89 || eff > 0.91 {
		t.Fatalf("Expected ~0.9 for fresh fact, got %f", eff)
	}

	// 30-day-old fact with low decay
	thirtyDaysAgo := now.Add(-30 * 24 * time.Hour)
	eff = EffectiveConfidence(0.9, 0.01, thirtyDaysAgo)
	if eff > 0.75 {
		t.Fatalf("Expected significant decay after 30 days, got %f", eff)
	}

	// Very old fact
	yearAgo := now.Add(-365 * 24 * time.Hour)
	eff = EffectiveConfidence(0.9, 0.01, yearAgo)
	if eff > 0.05 {
		t.Fatalf("Expected near-zero after a year, got %f", eff)
	}
}

func TestCrossAgentConflictDetection(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "agents disagree", SourceFile: "t.md"})

	// Mister says model is opus
	fact1 := &Fact{
		MemoryID: memID, Subject: "primary_model", Predicate: "is",
		Object: "opus", FactType: "kv", Confidence: 0.9, AgentID: "mister",
	}
	s.AddFact(ctx, fact1)

	// Niot says model is sonnet
	fact2 := &Fact{
		MemoryID: memID, Subject: "primary_model", Predicate: "is",
		Object: "sonnet", FactType: "kv", Confidence: 0.8, AgentID: "niot",
	}
	id2, _ := s.AddFact(ctx, fact2)
	fact2.ID = id2

	conflicts, err := s.CheckConflictsForFact(ctx, fact2)
	if err != nil {
		t.Fatalf("CheckConflictsForFact: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("Expected 1 conflict, got %d", len(conflicts))
	}
	if !conflicts[0].CrossAgent {
		t.Fatal("Expected cross_agent=true for mister vs niot conflict")
	}
}

func TestSameAgentConflictNotCrossAgent(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "self contradiction", SourceFile: "t.md"})

	fact1 := &Fact{
		MemoryID: memID, Subject: "strategy", Predicate: "type",
		Object: "ORB", FactType: "kv", Confidence: 0.9, AgentID: "mister",
	}
	s.AddFact(ctx, fact1)

	fact2 := &Fact{
		MemoryID: memID, Subject: "strategy", Predicate: "type",
		Object: "EMA", FactType: "kv", Confidence: 0.8, AgentID: "mister",
	}
	id2, _ := s.AddFact(ctx, fact2)
	fact2.ID = id2

	conflicts, _ := s.CheckConflictsForFact(ctx, fact2)
	if len(conflicts) != 1 {
		t.Fatalf("Expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].CrossAgent {
		t.Fatal("Same-agent conflict should NOT be cross_agent")
	}
}

func TestGetAttributeConflictsCrossAgent(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "multi-agent", SourceFile: "t.md"})

	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "db", Predicate: "engine", Object: "sqlite", FactType: "kv", AgentID: "mister"})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "db", Predicate: "engine", Object: "postgres", FactType: "kv", AgentID: "hawk"})

	conflicts, err := s.GetAttributeConflicts(ctx)
	if err != nil {
		t.Fatalf("GetAttributeConflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("Expected 1 conflict, got %d", len(conflicts))
	}
	if !conflicts[0].CrossAgent {
		t.Fatal("Expected cross_agent=true")
	}
}
