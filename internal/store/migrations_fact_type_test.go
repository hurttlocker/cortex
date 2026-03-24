package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateFactTypeEnum_ExpandsConstraintAndPreservesRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-fact-type.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}

	if _, err := rawDB.Exec(`
		CREATE TABLE memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			content TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create memories table: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO memories (content) VALUES ('seed')`); err != nil {
		t.Fatalf("insert memory: %v", err)
	}
	if _, err := rawDB.Exec(`
		CREATE TABLE facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
			entity_id INTEGER,
			subject TEXT,
			predicate TEXT,
			object TEXT,
			fact_type TEXT NOT NULL CHECK(fact_type IN ('kv','relationship','preference','temporal','identity','location','decision','state','config')),
			confidence REAL DEFAULT 1.0,
			decay_rate REAL DEFAULT 0.01,
			last_reinforced DATETIME DEFAULT CURRENT_TIMESTAMP,
			source_quote TEXT,
			temporal_norm TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			state TEXT NOT NULL DEFAULT 'active',
			superseded_by INTEGER,
			agent_id TEXT NOT NULL DEFAULT '',
			observer_agent TEXT NOT NULL DEFAULT '',
			observed_entity TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			project_id TEXT NOT NULL DEFAULT '',
			token_estimate INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		t.Fatalf("create legacy facts table: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO facts (memory_id, subject, predicate, object, fact_type) VALUES (1,'repo','branch','main','kv')`); err != nil {
		t.Fatalf("insert fact: %v", err)
	}

	ss := &SQLiteStore{db: rawDB}
	if err := ss.migrateFactTypeEnum(); err != nil {
		t.Fatalf("migrateFactTypeEnum: %v", err)
	}

	var createSQL string
	if err := rawDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='facts'`).Scan(&createSQL); err != nil {
		t.Fatalf("read facts schema: %v", err)
	}
	if !strings.Contains(createSQL, "'event'") || !strings.Contains(createSQL, "'rule'") {
		t.Fatalf("expected updated facts schema to include event/rule, got %s", createSQL)
	}

	var count int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM facts`).Scan(&count); err != nil {
		t.Fatalf("count facts after migration: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 fact after migration, got %d", count)
	}

	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}
}
