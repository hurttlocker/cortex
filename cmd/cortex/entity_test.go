package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestRunEntityListAndProfile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "entity-cli.db")
	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Alice is the project manager for Cortex.",
		SourceFile: "entity-cli.md",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:    memID,
		Subject:     "Alice",
		Predicate:   "role",
		Object:      "project manager",
		FactType:    "relationship",
		Confidence:  0.95,
		SourceQuote: "Alice is the project manager for Cortex.",
	}); err != nil {
		t.Fatalf("add fact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seeded store: %v", err)
	}

	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = ""
		globalReadOnly = false
	})

	listOut := captureStdout(func() {
		if err := runEntity([]string{"list"}); err != nil {
			t.Fatalf("runEntity list: %v", err)
		}
	})
	if !strings.Contains(listOut, "Alice") {
		t.Fatalf("expected entity list to mention Alice, got:\n%s", listOut)
	}

	profileOut := captureStdout(func() {
		if err := runEntity([]string{"profile", "Alice"}); err != nil {
			t.Fatalf("runEntity profile: %v", err)
		}
	})
	if !strings.Contains(profileOut, "# Alice") || !strings.Contains(profileOut, "project manager") {
		t.Fatalf("expected entity profile markdown, got:\n%s", profileOut)
	}
}

func TestRunEntityMerge(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "entity-merge-cli.db")
	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Jonathan and Jon describe the same person.",
		SourceFile: "merge-cli.md",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	jonathan := &store.Fact{MemoryID: memID, Subject: "Jonathan", Predicate: "role", Object: "engineer", FactType: "identity", Confidence: 0.9}
	jon := &store.Fact{MemoryID: memID, Subject: "Jon", Predicate: "focus", Object: "memory", FactType: "state", Confidence: 0.8}
	if _, err := s.AddFact(ctx, jonathan); err != nil {
		t.Fatalf("add Jonathan fact: %v", err)
	}
	if _, err := s.AddFact(ctx, jon); err != nil {
		t.Fatalf("add Jon fact: %v", err)
	}
	if jonathan.EntityID == 0 || jon.EntityID == 0 || jonathan.EntityID == jon.EntityID {
		t.Fatalf("expected distinct entity ids before merge, got Jonathan=%d Jon=%d", jonathan.EntityID, jon.EntityID)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seeded store: %v", err)
	}

	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = ""
		globalReadOnly = false
	})

	out := captureStdout(func() {
		if err := runEntity([]string{"merge", strconv.FormatInt(jonathan.EntityID, 10), strconv.FormatInt(jon.EntityID, 10)}); err != nil {
			t.Fatalf("runEntity merge: %v", err)
		}
	})
	if !strings.Contains(out, "Merged entity") {
		t.Fatalf("expected merge confirmation, got:\n%s", out)
	}

	verify, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer verify.Close()

	facts, err := verify.GetFactsByEntityIDs(ctx, []int64{jonathan.EntityID}, false, 10)
	if err != nil {
		t.Fatalf("get facts by merged entity: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected merged entity to own 2 facts, got %d", len(facts))
	}
}
