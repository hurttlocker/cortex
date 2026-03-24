package store

import (
	"context"
	"testing"
)

func TestUpdateFactType_UpdatesDecayRate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{Content: "typed fact", SourceFile: "seed.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	factID, err := s.AddFact(ctx, &Fact{
		MemoryID:   memID,
		Subject:    "scanner",
		Predicate:  "fixed",
		Object:     "timeout bug",
		FactType:   "kv",
		Confidence: 0.9,
		DecayRate:  0.05,
	})
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}

	if err := s.UpdateFactType(ctx, factID, "event"); err != nil {
		t.Fatalf("UpdateFactType: %v", err)
	}

	fact, err := s.GetFact(ctx, factID)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	if fact.FactType != "event" {
		t.Fatalf("FactType = %q, want event", fact.FactType)
	}
	if fact.DecayRate != 0.08 {
		t.Fatalf("DecayRate = %.2f, want 0.08", fact.DecayRate)
	}
}
