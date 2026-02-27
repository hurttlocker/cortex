package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDedupFacts_DryRunPreview(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	si, err := NewStore(StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer si.Close()
	s := si.(*SQLiteStore)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{Content: "fact dedup dry run", SourceFile: "dry.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	if _, err := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "ETH", Predicate: "is", Object: "Ethereum is layer 1", Confidence: 0.95, FactType: "state"}); err != nil {
		t.Fatalf("AddFact winner: %v", err)
	}
	if _, err := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "ETH", Predicate: "is", Object: "Ethereum is layer-1", Confidence: 0.80, FactType: "state"}); err != nil {
		t.Fatalf("AddFact loser: %v", err)
	}
	if _, err := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "ETH", Predicate: "is", Object: "Ethereum has proof of stake", Confidence: 0.70, FactType: "state"}); err != nil {
		t.Fatalf("AddFact unrelated: %v", err)
	}

	report, err := s.DedupFacts(ctx, DedupFactOptions{DryRun: true, Threshold: 0.90, MaxPreview: 10})
	if err != nil {
		t.Fatalf("DedupFacts dry run: %v", err)
	}
	if report.Merges != 1 {
		t.Fatalf("expected 1 merge candidate, got %d", report.Merges)
	}
	if len(report.Preview) != 1 {
		t.Fatalf("expected 1 preview merge, got %d", len(report.Preview))
	}
	m := report.Preview[0]
	if m.Subject != "ETH" || m.Predicate != "is" {
		t.Fatalf("unexpected preview subject/predicate: %+v", m)
	}
	if m.WinnerObject != "Ethereum is layer 1" {
		t.Fatalf("unexpected winner object: %q", m.WinnerObject)
	}
	if m.LoserObject != "Ethereum is layer-1" {
		t.Fatalf("unexpected loser object: %q", m.LoserObject)
	}
	if m.Similarity < 0.90 {
		t.Fatalf("expected similarity >= 0.90, got %.3f", m.Similarity)
	}
}

func TestDedupFacts_ExecutesSupersede(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	si, err := NewStore(StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer si.Close()
	s := si.(*SQLiteStore)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{Content: "fact dedup apply", SourceFile: "apply.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	winnerID, err := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "BTC", Predicate: "is", Object: "Bitcoin is digital gold", Confidence: 0.92, FactType: "state"})
	if err != nil {
		t.Fatalf("AddFact winner: %v", err)
	}
	loserID, err := s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "BTC", Predicate: "is", Object: "Bitcoin is digital-gold", Confidence: 0.81, FactType: "state"})
	if err != nil {
		t.Fatalf("AddFact loser: %v", err)
	}

	report, err := s.DedupFacts(ctx, DedupFactOptions{DryRun: false, Threshold: 0.90, MaxPreview: 10})
	if err != nil {
		t.Fatalf("DedupFacts execute: %v", err)
	}
	if report.Merges != 1 {
		t.Fatalf("expected 1 merge executed, got %d", report.Merges)
	}

	winner, err := s.GetFact(ctx, winnerID)
	if err != nil {
		t.Fatalf("GetFact winner: %v", err)
	}
	if winner.SupersededBy != nil {
		t.Fatalf("winner should remain active, superseded_by=%v", winner.SupersededBy)
	}

	loser, err := s.GetFact(ctx, loserID)
	if err != nil {
		t.Fatalf("GetFact loser: %v", err)
	}
	if loser.SupersededBy == nil || *loser.SupersededBy != winnerID {
		t.Fatalf("expected loser superseded by %d, got %+v", winnerID, loser.SupersededBy)
	}
	if loser.Confidence != 0.0 {
		t.Fatalf("expected loser confidence tombstoned to 0.0, got %f", loser.Confidence)
	}
}
