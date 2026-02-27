package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/observe"
	"github.com/hurttlocker/cortex/internal/store"
)

func seedAutoResolveConflicts(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()

	m1, err := s.AddMemory(ctx, &store.Memory{Content: "conflict set 1", SourceFile: "c1.md"})
	if err != nil {
		t.Fatalf("AddMemory1: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour).UTC()
	newTime := time.Now().Add(-1 * time.Hour).UTC()

	f1, err := s.AddFact(ctx, &store.Fact{MemoryID: m1, Subject: "user", Predicate: "email", Object: "old@example.com", Confidence: 0.80, FactType: "state"})
	if err != nil {
		t.Fatalf("AddFact f1: %v", err)
	}
	f2, err := s.AddFact(ctx, &store.Fact{MemoryID: m1, Subject: "user", Predicate: "email", Object: "new@example.com", Confidence: 0.95, FactType: "state"})
	if err != nil {
		t.Fatalf("AddFact f2: %v", err)
	}
	ss := s.(*store.SQLiteStore)
	if _, err := ss.ExecContext(ctx, `UPDATE facts SET created_at=?, last_reinforced=? WHERE id=?`, oldTime, oldTime, f1); err != nil {
		t.Fatalf("update f1 time: %v", err)
	}
	if _, err := ss.ExecContext(ctx, `UPDATE facts SET created_at=?, last_reinforced=? WHERE id=?`, newTime, newTime, f2); err != nil {
		t.Fatalf("update f2 time: %v", err)
	}

	m2, err := s.AddMemory(ctx, &store.Memory{Content: "conflict set 2", SourceFile: "c2.md"})
	if err != nil {
		t.Fatalf("AddMemory2: %v", err)
	}
	f3, err := s.AddFact(ctx, &store.Fact{MemoryID: m2, Subject: "user", Predicate: "timezone", Object: "EST", Confidence: 0.90, FactType: "state"})
	if err != nil {
		t.Fatalf("AddFact f3: %v", err)
	}
	f4, err := s.AddFact(ctx, &store.Fact{MemoryID: m2, Subject: "user", Predicate: "timezone", Object: "PST", Confidence: 0.88, FactType: "state"})
	if err != nil {
		t.Fatalf("AddFact f4: %v", err)
	}
	// Make timestamps equal so deterministic path cannot trigger for this conflict.
	if _, err := ss.ExecContext(ctx, `UPDATE facts SET created_at=?, last_reinforced=? WHERE id=?`, oldTime, oldTime, f3); err != nil {
		t.Fatalf("update f3 time: %v", err)
	}
	if _, err := ss.ExecContext(ctx, `UPDATE facts SET created_at=?, last_reinforced=? WHERE id=?`, oldTime, oldTime, f4); err != nil {
		t.Fatalf("update f4 time: %v", err)
	}
}

func TestRunAutoResolveConflicts_DryRunSplitAndNoWrite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	si, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer si.Close()
	s := si.(*store.SQLiteStore)
	seedAutoResolveConflicts(t, s)

	engine := observe.NewEngine(s, dbPath)
	conflicts, err := engine.GetConflictsLimitWithSuperseded(context.Background(), 100, false)
	if err != nil {
		t.Fatalf("GetConflicts: %v", err)
	}

	batch, err := runAutoResolveConflicts(context.Background(), s, conflicts, "openrouter/invalid-model", 0.85, true)
	if err != nil {
		t.Fatalf("runAutoResolveConflicts dry-run: %v", err)
	}
	if batch.Deterministic < 1 {
		t.Fatalf("expected at least one deterministic resolution, got %+v", batch)
	}
	if batch.LLM < 1 {
		t.Fatalf("expected at least one llm-path conflict, got %+v", batch)
	}

	// Dry-run must not write superseded_by.
	facts, err := s.ListFacts(context.Background(), store.ListOpts{Limit: 50, IncludeSuperseded: true})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	for _, f := range facts {
		if f.SupersededBy != nil {
			t.Fatalf("expected no superseded facts in dry-run, found fact %d superseded_by=%d", f.ID, *f.SupersededBy)
		}
	}
}

func TestRunConflicts_AutoResolveJSONShape(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	si, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	seedAutoResolveConflicts(t, si)
	if err := si.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var runErr error
	out := captureStdout(func() {
		runErr = runConflicts([]string{"--auto-resolve", "--dry-run", "--json", "--threshold", "0.85"})
	})
	if runErr != nil {
		t.Fatalf("runConflicts auto-resolve json: %v\nout=%s", runErr, out)
	}

	var payload autoResolveBatch
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode json: %v\nout=%s", err, out)
	}
	if payload.Total == 0 || len(payload.Results) == 0 {
		t.Fatalf("expected non-empty payload, got %+v", payload)
	}
	for _, r := range payload.Results {
		if r.Method == "" {
			t.Fatalf("expected method field populated in every result: %+v", payload.Results)
		}
	}
}
