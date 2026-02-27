package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestRunLifecycle_DryRunJSON(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "cortex.db")
	si, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, _ := si.AddMemory(ctx, &store.Memory{Content: "lifecycle test", SourceFile: "memory/2026-02-27.md"})
	factID, _ := si.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "test", Predicate: "status", Object: "active", FactType: "state", Confidence: 0.9})
	if err := si.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })
	t.Setenv("HOME", t.TempDir())

	var runErr error
	out := captureStdout(func() {
		runErr = runLifecycle([]string{"run", "--dry-run", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runLifecycle dry-run: %v\nout=%s", runErr, out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode lifecycle json: %v\nout=%s", err, out)
	}
	if v, ok := payload["dry_run"].(bool); !ok || !v {
		t.Fatalf("expected dry_run=true payload, got %v", payload["dry_run"])
	}

	s2, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	defer s2.Close()
	f, err := s2.GetFact(context.Background(), factID)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	if f.State != store.FactStateActive {
		t.Fatalf("dry-run should not mutate fact state, got %s", f.State)
	}
}
