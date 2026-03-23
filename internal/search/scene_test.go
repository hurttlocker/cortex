package search

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestSearchRRFSceneExpansionAddsNearbySessionNeighbor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedID, err := s.AddMemory(ctx, &store.Memory{
		Content:       "Jon talked about opening a dance studio after losing his banking job.",
		SourceFile:    "conv-30.md",
		SourceLine:    10,
		SourceSection: "Session 9",
		Metadata:      &store.Metadata{SessionKey: "conv-30:session-9"},
	})
	if err != nil {
		t.Fatalf("add seed memory: %v", err)
	}
	neighborID, err := s.AddMemory(ctx, &store.Memory{
		Content:       "He said the official opening night is June 20, 2023.",
		SourceFile:    "conv-30.md",
		SourceLine:    18,
		SourceSection: "Session 9",
		Metadata:      &store.Metadata{SessionKey: "conv-30:session-9"},
	})
	if err != nil {
		t.Fatalf("add neighbor memory: %v", err)
	}

	engine := NewEngineWithEmbedder(s, newMockEmbedder())
	results, err := engine.Search(ctx, "How did Jon's studio come together after banking?", Options{
		Mode:  ModeRRF,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	foundSeed := false
	foundNeighbor := false
	for _, result := range results {
		if result.MemoryID == seedID {
			foundSeed = true
		}
		if result.MemoryID == neighborID {
			foundNeighbor = true
		}
	}
	if !foundSeed || !foundNeighbor {
		t.Fatalf("expected scene expansion to keep seed and add neighbor, foundSeed=%v foundNeighbor=%v", foundSeed, foundNeighbor)
	}
}

func TestSceneLabelForResultPrefersSessionKey(t *testing.T) {
	result := Result{
		SourceFile:    "conv-30.md",
		SourceSection: "Session 9",
		Metadata:      &store.Metadata{SessionKey: "conv-30:session-9"},
	}
	if got := SceneLabelForResult(result); got != "session:conv-30:session-9" {
		t.Fatalf("scene label = %q", got)
	}
}
