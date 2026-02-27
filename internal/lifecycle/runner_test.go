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
