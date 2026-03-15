package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/observe"
	"github.com/hurttlocker/cortex/internal/store"
)

func seedStaleFactsDB(t *testing.T, factCount int) string {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "cortex-stale.db")
	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	ctx := context.Background()
	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "stale test memory", ImportedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	for i := 0; i < factCount; i++ {
		_, err := s.AddFact(ctx, &store.Fact{
			MemoryID:   memoryID,
			Subject:    fmt.Sprintf("subject-%d", i),
			Predicate:  "status",
			Object:     "value",
			FactType:   "state",
			Confidence: 0.4,
			DecayRate:  0.01,
		})
		if err != nil {
			t.Fatalf("AddFact %d: %v", i, err)
		}
	}

	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("expected SQLiteStore")
	}
	if _, err := sqlStore.ExecContext(ctx, "UPDATE facts SET last_reinforced = ?", time.Now().UTC().AddDate(0, 0, -90)); err != nil {
		t.Fatalf("backdate facts: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
	return dbPath
}

func TestRunStale_Flags_JSONAndLimitAccepted(t *testing.T) {
	err := runStale([]string{"--json", "--limit", "10"})
	if err != nil && strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected --json/--limit flags to parse, got: %v", err)
	}
}

func TestRunStale_DefaultLimitReturnsAll(t *testing.T) {
	dbPath := seedStaleFactsDB(t, 60)
	globalDBPath = ""
	t.Cleanup(func() { globalDBPath = "" })
	t.Setenv("CORTEX_DB", dbPath)

	out := captureStdout(func() {
		err := runStale([]string{"--json", "--days", "1", "--min-confidence", "1.0"})
		if err != nil {
			t.Fatalf("runStale: %v", err)
		}
	})

	var stale []observe.StaleFact
	if err := json.Unmarshal([]byte(out), &stale); err != nil {
		t.Fatalf("unmarshal stale output: %v\noutput=%s", err, out)
	}
	if len(stale) != 60 {
		t.Fatalf("expected default stale limit to return all 60 facts, got %d", len(stale))
	}
}

func TestRunStale_LimitFlagRestrictsResults(t *testing.T) {
	dbPath := seedStaleFactsDB(t, 60)
	globalDBPath = ""
	t.Cleanup(func() { globalDBPath = "" })
	t.Setenv("CORTEX_DB", dbPath)

	out := captureStdout(func() {
		err := runStale([]string{"--json", "--days", "1", "--min-confidence", "1.0", "--limit", "7"})
		if err != nil {
			t.Fatalf("runStale: %v", err)
		}
	})

	var stale []observe.StaleFact
	if err := json.Unmarshal([]byte(out), &stale); err != nil {
		t.Fatalf("unmarshal stale output: %v\noutput=%s", err, out)
	}
	if len(stale) != 7 {
		t.Fatalf("expected --limit 7 to return 7 facts, got %d", len(stale))
	}
}

func TestRunStale_LimitFlagRejectsNonPositive(t *testing.T) {
	err := runStale([]string{"--limit", "0"})
	if err == nil {
		t.Fatalf("expected error for --limit 0")
	}
	if !strings.Contains(err.Error(), "--limit must be >= 1") {
		t.Fatalf("expected non-positive limit error, got: %v", err)
	}
}
