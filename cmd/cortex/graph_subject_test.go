package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestResolveGraphSeedFactIDsBySubject_CaseInsensitiveActiveOnly(t *testing.T) {
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("expected SQLiteStore")
	}

	ctx := context.Background()
	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "graph seed memory", ImportedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	oldFactID, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "Cortex IDE", Predicate: "status", Object: "legacy", FactType: "state", Confidence: 0.7})
	if err != nil {
		t.Fatalf("AddFact old: %v", err)
	}
	newFactID, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "cortex ide", Predicate: "status", Object: "current", FactType: "state", Confidence: 0.9})
	if err != nil {
		t.Fatalf("AddFact new: %v", err)
	}
	if err := s.SupersedeFact(ctx, oldFactID, newFactID, "updated"); err != nil {
		t.Fatalf("SupersedeFact: %v", err)
	}

	seedIDs, err := resolveGraphSeedFactIDsBySubject(ctx, sqlStore.GetDB(), "CoRtEx IdE", 0.0)
	if err != nil {
		t.Fatalf("resolveGraphSeedFactIDsBySubject: %v", err)
	}
	if len(seedIDs) != 1 || seedIDs[0] != newFactID {
		t.Fatalf("expected only active fact id [%d], got %v", newFactID, seedIDs)
	}
}

func TestRunGraph_SubjectJSONIncludesSeedFactIDs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "graph-subject.db")
	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("expected SQLiteStore")
	}

	ctx := context.Background()
	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "graph run memory", ImportedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	f1, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "Cortex IDE", Predicate: "uses", Object: "cortex", FactType: "relationship", Confidence: 0.8})
	if err != nil {
		t.Fatalf("AddFact f1: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "cortex ide", Predicate: "needs", Object: "fact ids", FactType: "relationship", Confidence: 0.9}); err != nil {
		t.Fatalf("AddFact f2: %v", err)
	}
	f3, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "Cortex Memory", Predicate: "links", Object: "IDE", FactType: "relationship", Confidence: 0.85})
	if err != nil {
		t.Fatalf("AddFact f3: %v", err)
	}
	if err := sqlStore.AddEdge(ctx, &store.FactEdge{SourceFactID: f1, TargetFactID: f3, EdgeType: store.EdgeTypeRelatesTo, Confidence: 0.7, Source: store.EdgeSourceInferred}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	globalDBPath = ""
	t.Cleanup(func() { globalDBPath = "" })
	t.Setenv("CORTEX_DB", dbPath)

	out := captureStdout(func() {
		err := runGraph([]string{"--subject", "cortex ide", "--depth", "1", "--export", "json"})
		if err != nil {
			t.Fatalf("runGraph: %v", err)
		}
	})

	var payload struct {
		Nodes []struct {
			ID int64 `json:"id"`
		} `json:"nodes"`
		Meta map[string]interface{} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal graph output: %v\noutput=%s", err, out)
	}

	if got, _ := payload.Meta["root_subject"].(string); got != "cortex ide" {
		t.Fatalf("expected root_subject=cortex ide, got %v", payload.Meta["root_subject"])
	}
	seedRaw, ok := payload.Meta["seed_fact_ids"].([]interface{})
	if !ok {
		t.Fatalf("expected seed_fact_ids array in meta, got %T", payload.Meta["seed_fact_ids"])
	}
	if len(seedRaw) != 2 {
		t.Fatalf("expected 2 subject seed fact ids, got %d (%v)", len(seedRaw), seedRaw)
	}
	if len(payload.Nodes) < 2 {
		t.Fatalf("expected at least 2 nodes for subject graph, got %d", len(payload.Nodes))
	}
}

func TestRunGraph_SubjectAndFactIDMutuallyExclusive(t *testing.T) {
	err := runGraph([]string{"123", "--subject", "cortex ide"})
	if err == nil {
		t.Fatalf("expected argument conflict error")
	}
	if !strings.Contains(err.Error(), "either <fact_id> or --subject") {
		t.Fatalf("unexpected error: %v", err)
	}
}
