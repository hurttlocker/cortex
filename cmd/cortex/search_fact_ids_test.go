package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

func TestEnrichSearchResultsWithFactIDs_ActiveOnly(t *testing.T) {
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "cortex ide notes", ImportedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	secondMemoryID, err := s.AddMemory(ctx, &store.Memory{Content: "no facts", ImportedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("AddMemory (second): %v", err)
	}

	oldFactID, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "cortex ide", Predicate: "status", Object: "initial", FactType: "state", Confidence: 0.8})
	if err != nil {
		t.Fatalf("AddFact old: %v", err)
	}
	newFactID, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "cortex ide", Predicate: "status", Object: "current", FactType: "state", Confidence: 0.9})
	if err != nil {
		t.Fatalf("AddFact new: %v", err)
	}
	if err := s.SupersedeFact(ctx, oldFactID, newFactID, "updated status"); err != nil {
		t.Fatalf("SupersedeFact: %v", err)
	}

	results := []search.Result{
		{MemoryID: memoryID, Content: "result 1"},
		{MemoryID: secondMemoryID, Content: "result 2"},
	}

	enriched := enrichSearchResultsWithFactIDs(ctx, s, results, false)
	if len(enriched) != 2 {
		t.Fatalf("expected 2 results, got %d", len(enriched))
	}
	if len(enriched[0].FactIDs) != 1 || enriched[0].FactIDs[0] != newFactID {
		t.Fatalf("expected active fact id [%d], got %v", newFactID, enriched[0].FactIDs)
	}
	if enriched[1].FactIDs == nil {
		t.Fatalf("expected empty fact_ids array, got nil")
	}
	if len(enriched[1].FactIDs) != 0 {
		t.Fatalf("expected no fact ids for second memory, got %v", enriched[1].FactIDs)
	}
}

func TestEnrichSearchResultsWithFactIDs_IncludingSuperseded(t *testing.T) {
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "cortex ide notes", ImportedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	oldFactID, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "cortex ide", Predicate: "status", Object: "initial", FactType: "state", Confidence: 0.8})
	if err != nil {
		t.Fatalf("AddFact old: %v", err)
	}
	newFactID, err := s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "cortex ide", Predicate: "status", Object: "current", FactType: "state", Confidence: 0.9})
	if err != nil {
		t.Fatalf("AddFact new: %v", err)
	}
	if err := s.SupersedeFact(ctx, oldFactID, newFactID, "updated status"); err != nil {
		t.Fatalf("SupersedeFact: %v", err)
	}

	results := []search.Result{{MemoryID: memoryID, Content: "result"}}
	enriched := enrichSearchResultsWithFactIDs(ctx, s, results, true)

	if len(enriched) != 1 {
		t.Fatalf("expected 1 result, got %d", len(enriched))
	}
	if len(enriched[0].FactIDs) != 2 {
		t.Fatalf("expected 2 fact ids (active + superseded), got %v", enriched[0].FactIDs)
	}
	if enriched[0].FactIDs[0] != oldFactID || enriched[0].FactIDs[1] != newFactID {
		t.Fatalf("expected sorted fact ids [%d %d], got %v", oldFactID, newFactID, enriched[0].FactIDs)
	}
}

func TestOutputJSON_IncludesFactIDsField(t *testing.T) {
	results := []search.Result{{MemoryID: 42, FactIDs: []int64{}, Content: "cortex ide", MatchType: "bm25"}}

	out := captureStdout(func() {
		if err := outputJSON(results); err != nil {
			t.Fatalf("outputJSON: %v", err)
		}
	})

	if !strings.Contains(out, "\"fact_ids\"") {
		t.Fatalf("expected JSON output to include fact_ids field, got: %s", out)
	}

	var decoded []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 result in JSON output, got %d", len(decoded))
	}
	if _, ok := decoded[0]["fact_ids"]; !ok {
		t.Fatalf("expected fact_ids key in JSON object")
	}
}
