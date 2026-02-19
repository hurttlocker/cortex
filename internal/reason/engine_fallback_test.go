package reason

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestFallbackRecentResults(t *testing.T) {
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	_, err = s.AddMemory(ctx, &store.Memory{
		Content:     "Project decisions and timeline",
		SourceFile:  "notes.md",
		ContentHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("adding memory: %v", err)
	}

	engine := &Engine{store: s}
	results, err := engine.fallbackRecentResults(ctx, 10, "")
	if err != nil {
		t.Fatalf("fallbackRecentResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].MatchType != "fallback_recent" {
		t.Fatalf("expected fallback_recent match type, got %q", results[0].MatchType)
	}
	if results[0].Content == "" {
		t.Fatal("expected non-empty fallback content")
	}
}
