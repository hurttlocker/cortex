package connect

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

// newTestSyncEngine creates a SyncEngine with in-memory stores for testing.
func newTestSyncEngine(t *testing.T, provider *mockProvider) (*SyncEngine, *ConnectorStore, store.Store) {
	t.Helper()

	// Create in-memory Cortex store (with full schema)
	st, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Get the underlying SQLite store for connector store
	sqlSt, ok := st.(*store.SQLiteStore)
	if !ok {
		t.Fatal("expected SQLiteStore")
	}

	// Create connector store using the same DB
	cs := NewConnectorStore(sqlSt.GetDB())

	// Register the mock provider
	registry := NewRegistry()
	registry.Register(provider)

	engine := NewSyncEngine(registry, cs, st, false)
	return engine, cs, st
}

func TestSyncOneBasic(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		records: []Record{
			{ExternalID: "r1", Content: "Alice is the CEO of Acme Corp", Source: "doc1.md"},
			{ExternalID: "r2", Content: "Bob joined the team in 2024", Source: "doc2.md"},
		},
	}

	engine, cs, st := newTestSyncEngine(t, mock)
	ctx := context.Background()

	// Add the connector
	_, err := cs.Add(ctx, "test", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("adding connector: %v", err)
	}
	conn, err := cs.Get(ctx, "test")
	if err != nil {
		t.Fatalf("getting connector: %v", err)
	}

	// Sync without extraction
	result := engine.SyncOne(ctx, conn)

	if result.Error != "" {
		t.Fatalf("sync error: %s", result.Error)
	}
	if result.RecordsFetched != 2 {
		t.Errorf("expected 2 fetched, got %d", result.RecordsFetched)
	}
	if result.RecordsImported != 2 {
		t.Errorf("expected 2 imported, got %d", result.RecordsImported)
	}
	if result.RecordsSkipped != 0 {
		t.Errorf("expected 0 skipped, got %d", result.RecordsSkipped)
	}

	// Verify memories were stored
	mems, err := st.ListMemories(ctx, store.ListOpts{Limit: 100})
	if err != nil {
		t.Fatalf("listing memories: %v", err)
	}
	if len(mems) != 2 {
		t.Errorf("expected 2 memories, got %d", len(mems))
	}
}

func TestSyncOneDedup(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		records: []Record{
			{ExternalID: "r1", Content: "Alice is the CEO", Source: "doc1.md"},
		},
	}

	engine, cs, _ := newTestSyncEngine(t, mock)
	ctx := context.Background()

	_, _ = cs.Add(ctx, "test", json.RawMessage(`{}`))
	conn, _ := cs.Get(ctx, "test")

	// First sync
	r1 := engine.SyncOne(ctx, conn)
	if r1.RecordsImported != 1 {
		t.Fatalf("expected 1 imported, got %d", r1.RecordsImported)
	}

	// Second sync — same content should be deduplicated
	r2 := engine.SyncOne(ctx, conn)
	if r2.RecordsFetched != 1 {
		t.Errorf("expected 1 fetched, got %d", r2.RecordsFetched)
	}
	if r2.RecordsImported != 0 {
		t.Errorf("expected 0 imported (dedup), got %d", r2.RecordsImported)
	}
	if r2.RecordsSkipped != 1 {
		t.Errorf("expected 1 skipped, got %d", r2.RecordsSkipped)
	}
}

func TestSyncOneWithExtraction(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		records: []Record{
			{
				ExternalID: "r1",
				Content:    "## Team\n- **Role:** CEO of Acme Corp\n- **Name:** Alice Johnson\n- **Joined:** January 2024\n- **Project:** Cortex Memory Engine",
				Source:     "team.md",
			},
		},
	}

	engine, cs, st := newTestSyncEngine(t, mock)
	ctx := context.Background()

	_, _ = cs.Add(ctx, "test", json.RawMessage(`{}`))
	conn, _ := cs.Get(ctx, "test")

	// Sync WITH extraction (rule-based only, no LLM)
	result := engine.SyncOne(ctx, conn, SyncOptions{Extract: true})

	if result.Error != "" {
		t.Fatalf("sync error: %s", result.Error)
	}
	if result.RecordsImported != 1 {
		t.Errorf("expected 1 imported, got %d", result.RecordsImported)
	}
	if result.FactsExtracted == 0 {
		t.Errorf("expected facts to be extracted, got 0")
	}

	// Verify facts were stored
	facts, err := st.ListFacts(ctx, store.ListOpts{Limit: 100})
	if err != nil {
		t.Fatalf("listing facts: %v", err)
	}
	if len(facts) == 0 {
		t.Error("expected facts in store, got 0")
	}

	t.Logf("Extracted %d facts from sync", result.FactsExtracted)
	for _, f := range facts {
		t.Logf("  [%s] %s — %s → %s (%.2f)", f.FactType, f.Subject, f.Predicate, f.Object, f.Confidence)
	}
}

func TestSyncOneExtractWithNoInfer(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		records: []Record{
			{
				ExternalID: "r1",
				Content:    "Cortex version is 0.7.0. Database size is 333MB.",
				Source:     "status.md",
			},
		},
	}

	engine, cs, _ := newTestSyncEngine(t, mock)
	ctx := context.Background()

	_, _ = cs.Add(ctx, "test", json.RawMessage(`{}`))
	conn, _ := cs.Get(ctx, "test")

	// Sync with extract but NO inference
	result := engine.SyncOne(ctx, conn, SyncOptions{Extract: true, NoInfer: true})

	if result.Error != "" {
		t.Fatalf("sync error: %s", result.Error)
	}
	// Even if facts were extracted, inference should be 0
	if result.EdgesInferred != 0 {
		t.Errorf("expected 0 edges (--no-infer), got %d", result.EdgesInferred)
	}
}

func TestSyncAllMultipleProviders(t *testing.T) {
	mock1 := &mockProvider{
		name: "github",
		records: []Record{
			{ExternalID: "issue-1", Content: "Fix login bug", Source: "issues"},
		},
	}
	mock2 := &mockProvider{
		name: "gmail",
		records: []Record{
			{ExternalID: "msg-1", Content: "Meeting tomorrow at 3pm", Source: "inbox"},
			{ExternalID: "msg-2", Content: "Budget report attached", Source: "inbox"},
		},
	}

	// Create engine with both providers
	st, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sqlSt := st.(*store.SQLiteStore)
	cs := NewConnectorStore(sqlSt.GetDB())

	registry := NewRegistry()
	registry.Register(mock1)
	registry.Register(mock2)

	engine := NewSyncEngine(registry, cs, st, false)
	ctx := context.Background()

	// Add both connectors
	_, _ = cs.Add(ctx, "github", json.RawMessage(`{}`))
	_, _ = cs.Add(ctx, "gmail", json.RawMessage(`{}`))

	results, err := engine.SyncAll(ctx)
	if err != nil {
		t.Fatalf("SyncAll error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	total := 0
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("provider %s error: %s", r.Provider, r.Error)
		}
		total += r.RecordsImported
	}
	if total != 3 {
		t.Errorf("expected 3 total imported, got %d", total)
	}
}

func TestSyncProviderDisabled(t *testing.T) {
	mock := &mockProvider{name: "test", records: []Record{}}

	engine, cs, _ := newTestSyncEngine(t, mock)
	ctx := context.Background()

	_, _ = cs.Add(ctx, "test", json.RawMessage(`{}`))
	_ = cs.SetEnabled(ctx, "test", false)

	_, err := engine.SyncProvider(ctx, "test")
	if err == nil {
		t.Error("expected error syncing disabled connector")
	}
}

func TestSyncProviderNotFound(t *testing.T) {
	mock := &mockProvider{name: "test", records: []Record{}}

	engine, _, _ := newTestSyncEngine(t, mock)
	ctx := context.Background()

	_, err := engine.SyncProvider(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}
