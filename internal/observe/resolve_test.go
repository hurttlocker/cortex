package observe

import (
	"context"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

func setupResolverTest(t *testing.T) (store.Store, *Engine, *Resolver) {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	engine := NewEngine(s, ":memory:")
	resolver := NewResolver(s, engine)
	return s, engine, resolver
}

// createConflictingFacts inserts two facts with the same subject+predicate but different objects.
func createConflictingFacts(t *testing.T, ctx context.Context, s store.Store,
	subject, predicate, obj1, obj2 string) (int64, int64) {
	t.Helper()

	// Create a memory to attach facts to
	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:     subject + " test memory",
		SourceFile:  "test.md",
		ContentHash: subject + "-" + predicate + "-hash",
	})
	if err != nil {
		t.Fatalf("adding memory: %v", err)
	}

	id1, err := s.AddFact(ctx, &store.Fact{
		MemoryID:  memID,
		Subject:   subject,
		Predicate: predicate,
		Object:    obj1,
		FactType:  "kv",
	})
	if err != nil {
		t.Fatalf("adding fact1: %v", err)
	}

	// Small delay to ensure different CreatedAt
	time.Sleep(10 * time.Millisecond)

	id2, err := s.AddFact(ctx, &store.Fact{
		MemoryID:  memID,
		Subject:   subject,
		Predicate: predicate,
		Object:    obj2,
		FactType:  "kv",
	})
	if err != nil {
		t.Fatalf("adding fact2: %v", err)
	}

	return id1, id2
}

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		input   string
		want    Strategy
		wantErr bool
	}{
		{"last-write-wins", StrategyLastWrite, false},
		{"lww", StrategyLastWrite, false},
		{"highest-confidence", StrategyHighestConfidence, false},
		{"confidence", StrategyHighestConfidence, false},
		{"hcw", StrategyHighestConfidence, false},
		{"newest", StrategyNewest, false},
		{"manual", StrategyManual, false},
		{"review", StrategyManual, false},
		{"bogus", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseStrategy(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLastWriteWins(t *testing.T) {
	s, _, resolver := setupResolverTest(t)
	defer s.Close()
	ctx := context.Background()

	id1, id2 := createConflictingFacts(t, ctx, s, "Q", "timezone", "EST", "PST")

	// id2 was created after id1, so it should win
	batch, err := resolver.DetectAndResolve(ctx, StrategyLastWrite, false)
	if err != nil {
		t.Fatalf("DetectAndResolve: %v", err)
	}

	if batch.Total == 0 {
		t.Fatal("expected at least 1 conflict")
	}

	if batch.Resolved == 0 {
		t.Fatal("expected at least 1 resolution")
	}

	// Winner should be id2 (newer)
	found := false
	for _, r := range batch.Results {
		if r.WinnerID == id2 && r.LoserID == id1 {
			found = true
			if !r.Applied {
				t.Error("resolution not applied")
			}
		}
	}
	if !found {
		t.Errorf("expected id2 (%d) to win over id1 (%d)", id2, id1)
	}

	// Verify loser confidence is 0
	loser, err := s.GetFact(ctx, id1)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	if loser.Confidence != 0 {
		t.Errorf("loser confidence = %.2f, want 0", loser.Confidence)
	}
}

func TestResolveHighestConfidence(t *testing.T) {
	s, _, resolver := setupResolverTest(t)
	defer s.Close()
	ctx := context.Background()

	id1, id2 := createConflictingFacts(t, ctx, s, "app", "language", "Go", "Rust")

	// Boost fact1's confidence to ensure it wins
	if err := s.UpdateFactConfidence(ctx, id1, 0.99); err != nil {
		t.Fatalf("updating confidence: %v", err)
	}
	if err := s.UpdateFactConfidence(ctx, id2, 0.3); err != nil {
		t.Fatalf("updating confidence: %v", err)
	}

	batch, err := resolver.DetectAndResolve(ctx, StrategyHighestConfidence, false)
	if err != nil {
		t.Fatalf("DetectAndResolve: %v", err)
	}

	if batch.Total == 0 {
		t.Fatal("expected at least 1 conflict")
	}

	// Winner should be id1 (higher confidence)
	found := false
	for _, r := range batch.Results {
		if r.WinnerID == id1 && r.LoserID == id2 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected id1 (%d) to win over id2 (%d)", id1, id2)
	}
}

func TestResolveManual(t *testing.T) {
	s, _, resolver := setupResolverTest(t)
	defer s.Close()
	ctx := context.Background()

	createConflictingFacts(t, ctx, s, "server", "port", "8080", "3000")

	batch, err := resolver.DetectAndResolve(ctx, StrategyManual, false)
	if err != nil {
		t.Fatalf("DetectAndResolve: %v", err)
	}

	if batch.Skipped == 0 {
		t.Error("expected manual strategy to skip (not auto-resolve)")
	}
	if batch.Resolved != 0 {
		t.Errorf("expected 0 resolved, got %d", batch.Resolved)
	}
}

func TestResolveDryRun(t *testing.T) {
	s, _, resolver := setupResolverTest(t)
	defer s.Close()
	ctx := context.Background()

	id1, _ := createConflictingFacts(t, ctx, s, "db", "engine", "sqlite", "postgres")

	batch, err := resolver.DetectAndResolve(ctx, StrategyLastWrite, true /* dryRun */)
	if err != nil {
		t.Fatalf("DetectAndResolve: %v", err)
	}

	// Should not apply
	for _, r := range batch.Results {
		if r.Applied {
			t.Error("dry run should not apply resolutions")
		}
	}

	// Verify facts unchanged
	fact1, err := s.GetFact(ctx, id1)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	if fact1.Confidence == 0 {
		t.Error("fact1 confidence should not be 0 in dry run")
	}
}

func TestResolveByID(t *testing.T) {
	s, _, resolver := setupResolverTest(t)
	defer s.Close()
	ctx := context.Background()

	id1, id2 := createConflictingFacts(t, ctx, s, "user", "email", "old@test.com", "new@test.com")

	res, err := resolver.ResolveByID(ctx, id2, id1)
	if err != nil {
		t.Fatalf("ResolveByID: %v", err)
	}

	if res.WinnerID != id2 {
		t.Errorf("winner = %d, want %d", res.WinnerID, id2)
	}
	if !res.Applied {
		t.Error("resolution not applied")
	}

	// Verify
	loser, _ := s.GetFact(ctx, id1)
	if loser.Confidence != 0 {
		t.Errorf("loser confidence = %.2f, want 0", loser.Confidence)
	}
}

func TestEffectiveConfidence(t *testing.T) {
	now := time.Now().UTC()
	fact := store.Fact{
		Confidence:     1.0,
		DecayRate:      0.01,
		LastReinforced: now.Add(-30 * 24 * time.Hour), // 30 days ago
	}

	eff := effectiveConfidence(fact, now)
	if eff >= 1.0 {
		t.Errorf("effective confidence should be < 1.0 after 30 days decay, got %.4f", eff)
	}
	if eff < 0.5 {
		t.Errorf("effective confidence too low (decay rate 0.01, 30 days), got %.4f", eff)
	}

	// Fresh fact should be near initial confidence
	freshFact := store.Fact{
		Confidence:     1.0,
		DecayRate:      0.01,
		LastReinforced: now,
	}
	freshEff := effectiveConfidence(freshFact, now)
	if freshEff != 1.0 {
		t.Errorf("fresh fact should have confidence 1.0, got %.4f", freshEff)
	}
}

func TestNoConflictsAfterResolution(t *testing.T) {
	s, engine, resolver := setupResolverTest(t)
	defer s.Close()
	ctx := context.Background()

	createConflictingFacts(t, ctx, s, "app", "version", "1.0", "2.0")

	// Resolve
	_, err := resolver.DetectAndResolve(ctx, StrategyLastWrite, false)
	if err != nil {
		t.Fatalf("DetectAndResolve: %v", err)
	}

	// Re-detect: should find no conflicts (suppressed facts have confidence=0)
	conflicts, err := engine.GetConflicts(ctx)
	if err != nil {
		t.Fatalf("GetConflicts: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts after resolution, got %d", len(conflicts))
	}
}
