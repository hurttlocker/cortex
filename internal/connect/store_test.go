package connect

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDB creates an in-memory SQLite DB with the connectors table.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	// Create connectors table
	_, err = db.Exec(`CREATE TABLE connectors (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		provider        TEXT NOT NULL UNIQUE,
		config          TEXT NOT NULL DEFAULT '{}',
		enabled         INTEGER NOT NULL DEFAULT 1,
		last_sync_at    DATETIME,
		last_error      TEXT,
		records_imported INTEGER NOT NULL DEFAULT 0,
		created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}

func TestConnectorStoreAdd(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	id, err := cs.Add(ctx, "gmail", json.RawMessage(`{"token": "abc"}`))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	// Duplicate should fail
	_, err = cs.Add(ctx, "gmail", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error on duplicate add")
	}
}

func TestConnectorStoreAddEmptyProvider(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	_, err := cs.Add(ctx, "", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for empty provider")
	}
}

func TestConnectorStoreAddNilConfig(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	id, err := cs.Add(ctx, "github", nil)
	if err != nil {
		t.Fatalf("Add with nil config failed: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	// Verify it stored as "{}"
	c, err := cs.Get(ctx, "github")
	if err != nil {
		t.Fatal(err)
	}
	if string(c.Config) != "{}" {
		t.Fatalf("expected '{}' config, got %s", string(c.Config))
	}
}

func TestConnectorStoreGet(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	cs.Add(ctx, "gmail", json.RawMessage(`{"token": "xyz"}`))

	c, err := cs.Get(ctx, "gmail")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if c.Provider != "gmail" {
		t.Fatalf("expected gmail, got %s", c.Provider)
	}
	if !c.Enabled {
		t.Fatal("expected enabled by default")
	}
	if c.LastSyncAt != nil {
		t.Fatal("expected nil last sync")
	}
	if c.RecordsImported != 0 {
		t.Fatal("expected 0 records imported")
	}

	var cfg map[string]string
	json.Unmarshal(c.Config, &cfg)
	if cfg["token"] != "xyz" {
		t.Fatalf("expected token xyz, got %s", cfg["token"])
	}
}

func TestConnectorStoreGetNotFound(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	_, err := cs.Get(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent connector")
	}
}

func TestConnectorStoreList(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	cs.Add(ctx, "gmail", json.RawMessage(`{}`))
	cs.Add(ctx, "github", json.RawMessage(`{}`))
	cs.Add(ctx, "slack", json.RawMessage(`{}`))

	// Disable slack
	cs.SetEnabled(ctx, "slack", false)

	// All connectors
	all, err := cs.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}

	// Enabled only
	enabled, err := cs.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 2 {
		t.Fatalf("expected 2 enabled, got %d", len(enabled))
	}
}

func TestConnectorStoreUpdateConfig(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	cs.Add(ctx, "gmail", json.RawMessage(`{"old": true}`))
	err := cs.UpdateConfig(ctx, "gmail", json.RawMessage(`{"new": true}`))
	if err != nil {
		t.Fatal(err)
	}

	c, _ := cs.Get(ctx, "gmail")
	var cfg map[string]bool
	json.Unmarshal(c.Config, &cfg)
	if !cfg["new"] {
		t.Fatal("expected new config")
	}
}

func TestConnectorStoreUpdateConfigNotFound(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	err := cs.UpdateConfig(ctx, "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for nonexistent connector")
	}
}

func TestConnectorStoreSetEnabled(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	cs.Add(ctx, "gmail", json.RawMessage(`{}`))

	// Disable
	err := cs.SetEnabled(ctx, "gmail", false)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := cs.Get(ctx, "gmail")
	if c.Enabled {
		t.Fatal("expected disabled")
	}

	// Re-enable
	err = cs.SetEnabled(ctx, "gmail", true)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = cs.Get(ctx, "gmail")
	if !c.Enabled {
		t.Fatal("expected enabled")
	}
}

func TestConnectorStoreRecordSyncSuccess(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	cs.Add(ctx, "gmail", json.RawMessage(`{}`))

	err := cs.RecordSyncSuccess(ctx, "gmail", 42)
	if err != nil {
		t.Fatal(err)
	}

	c, _ := cs.Get(ctx, "gmail")
	if c.RecordsImported != 42 {
		t.Fatalf("expected 42 records, got %d", c.RecordsImported)
	}
	if c.LastSyncAt == nil {
		t.Fatal("expected non-nil last sync time")
	}
	if c.LastError != "" {
		t.Fatalf("expected empty error, got %s", c.LastError)
	}

	// Sync again — records should accumulate
	cs.RecordSyncSuccess(ctx, "gmail", 10)
	c, _ = cs.Get(ctx, "gmail")
	if c.RecordsImported != 52 {
		t.Fatalf("expected 52 accumulated records, got %d", c.RecordsImported)
	}
}

func TestConnectorStoreRecordSyncError(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	cs.Add(ctx, "gmail", json.RawMessage(`{}`))

	err := cs.RecordSyncError(ctx, "gmail", "auth expired")
	if err != nil {
		t.Fatal(err)
	}

	c, _ := cs.Get(ctx, "gmail")
	if c.LastError != "auth expired" {
		t.Fatalf("expected 'auth expired', got %s", c.LastError)
	}
}

func TestConnectorStoreRemove(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	cs.Add(ctx, "gmail", json.RawMessage(`{}`))

	err := cs.Remove(ctx, "gmail")
	if err != nil {
		t.Fatal(err)
	}

	// Should be gone
	_, err = cs.Get(ctx, "gmail")
	if err == nil {
		t.Fatal("expected error after removal")
	}

	// Remove again should fail
	err = cs.Remove(ctx, "gmail")
	if err == nil {
		t.Fatal("expected error on double remove")
	}
}

func TestConnectorStoreGetByID(t *testing.T) {
	db := newTestDB(t)
	cs := NewConnectorStore(db)
	ctx := context.Background()

	id, _ := cs.Add(ctx, "github", json.RawMessage(`{"repo": "test"}`))

	c, err := cs.GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "github" {
		t.Fatalf("expected github, got %s", c.Provider)
	}
}
