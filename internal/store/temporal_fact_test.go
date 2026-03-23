package store

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/temporal"
)

func TestAddFact_TemporalNormRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{Content: "Jon went to the fair", SourceFile: "locomo.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	factID, err := s.AddFact(ctx, &Fact{
		MemoryID:   memID,
		Subject:    "Jon",
		Predicate:  "date",
		Object:     "last week",
		FactType:   "temporal",
		Confidence: 0.9,
		TemporalNorm: &temporal.Norm{
			Kind:       "date_range",
			Literal:    "last week",
			Start:      "2023-03-16",
			End:        "2023-03-22",
			Anchor:     "2023-03-23",
			Precision:  "day",
			Resolution: "resolved_from_anchor",
		},
	})
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}

	got, err := s.GetFact(ctx, factID)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	if got == nil || got.TemporalNorm == nil {
		t.Fatalf("expected temporal norm, got %+v", got)
	}
	if got.TemporalNorm.Start != "2023-03-16" || got.TemporalNorm.End != "2023-03-22" {
		t.Fatalf("unexpected temporal norm: %+v", got.TemporalNorm)
	}
}
