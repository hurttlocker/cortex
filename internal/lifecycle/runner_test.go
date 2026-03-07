package lifecycle

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	cfgresolver "github.com/hurttlocker/cortex/internal/config"
	"github.com/hurttlocker/cortex/internal/store"
)

func seedLifecycleData(t *testing.T, s *store.SQLiteStore) (promoteID, decayID, conflictWinnerID, conflictLoserID int64) {
	t.Helper()
	ctx := context.Background()

	m1, _ := s.AddMemory(ctx, &store.Memory{Content: "promote primary", SourceFile: "memory/2026-01-01.md"})
	m2, _ := s.AddMemory(ctx, &store.Memory{Content: "promote corroboration", SourceFile: "knowledge/corroboration.md"})
	promoteID, _ = s.AddFact(ctx, &store.Fact{MemoryID: m1, Subject: "cortex", Predicate: "mode", Object: "agent cognition", FactType: "state", Confidence: 0.82})
	_, _ = s.AddFact(ctx, &store.Fact{MemoryID: m2, Subject: "cortex", Predicate: "mode", Object: "agent cognition", FactType: "state", Confidence: 0.70})
	for i := 0; i < 3; i++ {
		_ = s.RecordFactAccess(ctx, promoteID, "xmate", store.AccessTypeReinforce)
	}

	m3, _ := s.AddMemory(ctx, &store.Memory{Content: "old fading", SourceFile: "memory/2025-10-01.md"})
	decayID, _ = s.AddFact(ctx, &store.Fact{MemoryID: m3, Subject: "legacy", Predicate: "status", Object: "old", FactType: "state", Confidence: 0.20})
	old := time.Now().UTC().AddDate(0, 0, -120)
	if _, err := s.ExecContext(ctx, `UPDATE facts SET last_reinforced=? WHERE id=?`, old, decayID); err != nil {
		t.Fatalf("update decay last_reinforced: %v", err)
	}

	m4, _ := s.AddMemory(ctx, &store.Memory{Content: "conflict old", SourceFile: "github:issues/1"})
	m5, _ := s.AddMemory(ctx, &store.Memory{Content: "conflict new", SourceFile: "github:issues/2"})
	conflictLoserID, _ = s.AddFact(ctx, &store.Fact{MemoryID: m4, Subject: "user", Predicate: "email", Object: "old@example.com", FactType: "identity", Confidence: 0.60})
	conflictWinnerID, _ = s.AddFact(ctx, &store.Fact{MemoryID: m5, Subject: "user", Predicate: "email", Object: "new@example.com", FactType: "identity", Confidence: 0.90})
	older := time.Now().UTC().AddDate(0, 0, -20)
	newer := time.Now().UTC().AddDate(0, 0, -5)
	if _, err := s.ExecContext(ctx, `UPDATE facts SET created_at=?, last_reinforced=? WHERE id=?`, older, older, conflictLoserID); err != nil {
		t.Fatalf("update conflict loser time: %v", err)
	}
	if _, err := s.ExecContext(ctx, `UPDATE facts SET created_at=?, last_reinforced=? WHERE id=?`, newer, newer, conflictWinnerID); err != nil {
		t.Fatalf("update conflict winner time: %v", err)
	}

	return
}

func testPolicies() cfgresolver.PolicyConfig {
	p := cfgresolver.DefaultPolicyConfig()
	p.ReinforcePromote.MinReinforcements = 3
	p.ReinforcePromote.MinSources = 2
	p.DecayRetire.InactiveDays = 30
	p.DecayRetire.ConfidenceBelow = 0.30
	p.ConflictSupersede.MinConfidenceDelta = 0.15
	p.ConflictSupersede.RequireStrictlyNewer = true
	return p
}

func TestRunner_DryRun_NoWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	si, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer si.Close()
	s := si.(*store.SQLiteStore)
	promoteID, decayID, winnerID, loserID := seedLifecycleData(t, s)

	r, err := NewRunner(s, testPolicies())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	report, err := r.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	if len(report.Actions) == 0 {
		t.Fatalf("expected planned actions, got none")
	}
	if report.Applied != 0 {
		t.Fatalf("expected 0 applied in dry-run, got %d", report.Applied)
	}

	fPromote, _ := s.GetFact(context.Background(), promoteID)
	fDecay, _ := s.GetFact(context.Background(), decayID)
	fWinner, _ := s.GetFact(context.Background(), winnerID)
	fLoser, _ := s.GetFact(context.Background(), loserID)
	if fPromote.State != store.FactStateActive || fDecay.State != store.FactStateActive {
		t.Fatalf("dry-run should not change states: promote=%s decay=%s", fPromote.State, fDecay.State)
	}
	if fLoser.SupersededBy != nil || fLoser.State == store.FactStateSuperseded {
		t.Fatalf("dry-run should not supersede loser; state=%s superseded=%v", fLoser.State, fLoser.SupersededBy)
	}
	if fWinner.State == store.FactStateSuperseded {
		t.Fatalf("winner should remain non-superseded")
	}
}

func TestRunner_Apply_Writes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	si, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer si.Close()
	s := si.(*store.SQLiteStore)
	promoteID, decayID, winnerID, loserID := seedLifecycleData(t, s)

	r, err := NewRunner(s, testPolicies())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	report, err := r.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run apply: %v", err)
	}
	if report.Applied == 0 {
		t.Fatalf("expected applied actions, got %+v", report)
	}

	fPromote, _ := s.GetFact(context.Background(), promoteID)
	fDecay, _ := s.GetFact(context.Background(), decayID)
	fWinner, _ := s.GetFact(context.Background(), winnerID)
	fLoser, _ := s.GetFact(context.Background(), loserID)

	if fPromote.State != store.FactStateCore {
		t.Fatalf("expected promote fact -> core, got %s", fPromote.State)
	}
	if fDecay.State != store.FactStateRetired {
		t.Fatalf("expected decay fact -> retired, got %s", fDecay.State)
	}
	if fLoser.SupersededBy == nil || *fLoser.SupersededBy != fWinner.ID {
		t.Fatalf("expected loser superseded by winner; loser=%+v winner=%d", fLoser, fWinner.ID)
	}
	if fLoser.State != store.FactStateSuperseded {
		t.Fatalf("expected loser state superseded, got %s", fLoser.State)
	}
}

// TestRunner_SkipStats_ZeroAction verifies that when no facts meet policy thresholds,
// the skip stats explain why (scanned > 0, acted = 0, skipped > 0 with reason keys).
func TestRunner_SkipStats_ZeroAction(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	si, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer si.Close()
	s := si.(*store.SQLiteStore)
	ctx := context.Background()

	// Add a single fresh fact with high confidence — should be skipped by both
	// reinforce-promote (insufficient reinforcements) and decay-retire (too fresh / too confident).
	m1, _ := s.AddMemory(ctx, &store.Memory{Content: "fresh", SourceFile: "fresh.md"})
	_, _ = s.AddFact(ctx, &store.Fact{
		MemoryID: m1, Subject: "server", Predicate: "status", Object: "online",
		FactType: "state", Confidence: 0.9,
	})

	p := cfgresolver.DefaultPolicyConfig()
	// Require high reinforcement so it definitely won't fire.
	p.ReinforcePromote.MinReinforcements = 10
	p.ReinforcePromote.MinSources = 5
	// Require old + low confidence for decay.
	p.DecayRetire.InactiveDays = 90
	p.DecayRetire.ConfidenceBelow = 0.20
	p.ConflictSupersede.Enabled = false

	r, err := NewRunner(s, p)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	report, err := r.Run(ctx, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(report.Actions) != 0 {
		t.Fatalf("expected 0 actions, got %d: %+v", len(report.Actions), report.Actions)
	}

	// reinforce-promote skip stats: should have scanned=1, acted=0, skipped=1.
	rp := report.SkipStats.ReinforcePromote
	if rp.Scanned == 0 {
		t.Errorf("reinforce-promote: expected scanned>0, got 0")
	}
	if rp.Acted != 0 {
		t.Errorf("reinforce-promote: expected acted=0, got %d", rp.Acted)
	}
	if rp.Skipped == 0 {
		t.Errorf("reinforce-promote: expected skipped>0, got 0")
	}
	if len(rp.SkipReasons) == 0 {
		t.Errorf("reinforce-promote: expected non-empty skip reasons, got none")
	}

	// decay-retire skip stats: should have scanned=1, acted=0, reason=too_fresh or confidence_too_high.
	dr := report.SkipStats.DecayRetire
	if dr.Scanned == 0 {
		t.Errorf("decay-retire: expected scanned>0, got 0")
	}
	if dr.Acted != 0 {
		t.Errorf("decay-retire: expected acted=0, got %d", dr.Acted)
	}
	if dr.Skipped == 0 {
		t.Errorf("decay-retire: expected skipped>0, got 0")
	}
	if len(dr.SkipReasons) == 0 {
		t.Errorf("decay-retire: expected non-empty skip reasons, got none")
	}
}

// TestRunner_SkipStats_ActedCount verifies skip stats are populated when actions fire.
func TestRunner_SkipStats_ActedCount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	si, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer si.Close()
	s := si.(*store.SQLiteStore)
	_, decayID, _, _ := seedLifecycleData(t, s)
	_ = decayID

	r, err := NewRunner(s, testPolicies())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	report, err := r.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}

	// At least one policy should have acted on something.
	totalActed := report.SkipStats.ReinforcePromote.Acted +
		report.SkipStats.DecayRetire.Acted +
		report.SkipStats.ConflictSupersede.Acted
	if totalActed == 0 {
		t.Errorf("expected at least one acted entry across all policies, got 0; report=%+v", report)
	}
	// SkipStats.Scanned should match report.Scanned.
	totalScanned := report.SkipStats.ReinforcePromote.Scanned +
		report.SkipStats.DecayRetire.Scanned +
		report.SkipStats.ConflictSupersede.Scanned
	if totalScanned != report.Scanned {
		t.Errorf("total skip-stats scanned (%d) != report.Scanned (%d)", totalScanned, report.Scanned)
	}
}
