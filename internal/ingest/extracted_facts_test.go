package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestStoreExtractedFact_AutoSupersedesChangedObject(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	memoryA, err := s.AddMemory(ctx, &store.Memory{Content: "status: running", SourceFile: "a.md"})
	if err != nil {
		t.Fatalf("AddMemory A: %v", err)
	}
	oldFactID, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memoryA,
		Subject:    "service-status",
		Predicate:  "status",
		Object:     "running",
		FactType:   "state",
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("AddFact old: %v", err)
	}

	memoryB, err := s.AddMemory(ctx, &store.Memory{Content: "status: stopped", SourceFile: "b.md"})
	if err != nil {
		t.Fatalf("AddMemory B: %v", err)
	}
	newFactID, stored, err := StoreExtractedFact(ctx, s, &store.Fact{
		MemoryID:   memoryB,
		Subject:    "service-status",
		Predicate:  "status",
		Object:     "stopped",
		FactType:   "state",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("StoreExtractedFact: %v", err)
	}
	if !stored {
		t.Fatalf("expected changed object to store a new fact")
	}
	if newFactID <= 0 {
		t.Fatalf("expected new fact id, got %d", newFactID)
	}

	oldFact, err := s.GetFact(ctx, oldFactID)
	if err != nil {
		t.Fatalf("GetFact old: %v", err)
	}
	if oldFact == nil || oldFact.SupersededBy == nil || *oldFact.SupersededBy != newFactID {
		t.Fatalf("expected old fact to be superseded by %d, got %+v", newFactID, oldFact)
	}
	if !strings.EqualFold(oldFact.State, store.FactStateSuperseded) {
		t.Fatalf("expected old fact state=%q, got %q", store.FactStateSuperseded, oldFact.State)
	}
}

func TestStoreExtractedFact_SkipsDuplicateObjectAndSupersedesConflicts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	memoryA, err := s.AddMemory(ctx, &store.Memory{Content: "status: running", SourceFile: "a.md"})
	if err != nil {
		t.Fatalf("AddMemory A: %v", err)
	}
	winnerID, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memoryA,
		Subject:    "service-status",
		Predicate:  "status",
		Object:     "running",
		FactType:   "state",
		Confidence: 0.95,
	})
	if err != nil {
		t.Fatalf("AddFact winner: %v", err)
	}

	memoryB, err := s.AddMemory(ctx, &store.Memory{Content: "status: stopped", SourceFile: "b.md"})
	if err != nil {
		t.Fatalf("AddMemory B: %v", err)
	}
	loserID, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memoryB,
		Subject:    "service-status",
		Predicate:  "status",
		Object:     "stopped",
		FactType:   "state",
		Confidence: 0.7,
	})
	if err != nil {
		t.Fatalf("AddFact loser: %v", err)
	}

	memoryC, err := s.AddMemory(ctx, &store.Memory{Content: "status: running updated import", SourceFile: "c.md"})
	if err != nil {
		t.Fatalf("AddMemory C: %v", err)
	}
	factID, stored, err := StoreExtractedFact(ctx, s, &store.Fact{
		MemoryID:   memoryC,
		Subject:    "service-status",
		Predicate:  "status",
		Object:     "running",
		FactType:   "state",
		Confidence: 0.92,
	})
	if err != nil {
		t.Fatalf("StoreExtractedFact: %v", err)
	}
	if stored {
		t.Fatalf("expected identical object to skip insert")
	}
	if factID != winnerID {
		t.Fatalf("expected existing matching fact %d to remain winner, got %d", winnerID, factID)
	}

	loserFact, err := s.GetFact(ctx, loserID)
	if err != nil {
		t.Fatalf("GetFact loser: %v", err)
	}
	if loserFact == nil || loserFact.SupersededBy == nil || *loserFact.SupersededBy != winnerID {
		t.Fatalf("expected conflicting loser to be superseded by %d, got %+v", winnerID, loserFact)
	}

	allFacts, err := s.ListFacts(ctx, store.ListOpts{Limit: 50, IncludeSuperseded: true})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	activeRunning := 0
	for _, f := range allFacts {
		if f.SupersededBy == nil && strings.EqualFold(strings.TrimSpace(f.Object), "running") {
			activeRunning++
		}
	}
	if activeRunning != 1 {
		t.Fatalf("expected exactly one active 'running' fact, got %d", activeRunning)
	}
}

func TestStoreExtractedFact_DropsDeniedTemporalSubject(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "Current time: Friday", SourceFile: "clock.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	factID, stored, err := StoreExtractedFact(ctx, s, &store.Fact{
		MemoryID:   memoryID,
		Subject:    "Current time",
		Predicate:  "value",
		Object:     "Friday, March 20th, 2026 — 8:48 PM",
		FactType:   "temporal",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("StoreExtractedFact: %v", err)
	}
	if stored {
		t.Fatalf("expected denied temporal subject to be skipped, got fact id=%d", factID)
	}

	facts, err := s.ListFacts(ctx, store.ListOpts{Limit: 10, IncludeSuperseded: true})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("expected no stored facts, got %+v", facts)
	}
}
