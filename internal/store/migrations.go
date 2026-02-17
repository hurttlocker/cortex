package store

import (
	"fmt"
	"time"
)

// migrate creates all tables if they don't exist and seeds metadata.
func (s *SQLiteStore) migrate() error {
	statements := []string{
		// Core memory table
		`CREATE TABLE IF NOT EXISTS memories (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			content        TEXT NOT NULL,
			source_file    TEXT,
			source_line    INTEGER,
			source_section TEXT,
			content_hash   TEXT UNIQUE NOT NULL,
			imported_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at     DATETIME
		)`,

		// FTS5 full-text search index
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			content,
			content=memories,
			content_rowid=id,
			tokenize='porter unicode61'
		)`,

		// FTS sync triggers
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
		END`,

		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.id, old.content);
		END`,

		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.id, old.content);
			INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
		END`,

		// Extracted facts
		`CREATE TABLE IF NOT EXISTS facts (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id       INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
			subject         TEXT,
			predicate       TEXT,
			object          TEXT,
			fact_type       TEXT NOT NULL CHECK(fact_type IN ('kv','relationship','preference','temporal','identity','location','decision','state')),
			confidence      REAL DEFAULT 1.0,
			decay_rate      REAL DEFAULT 0.01,
			last_reinforced DATETIME DEFAULT CURRENT_TIMESTAMP,
			source_quote    TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		`CREATE INDEX IF NOT EXISTS idx_facts_memory_id ON facts(memory_id)`,
		`CREATE INDEX IF NOT EXISTS idx_facts_type ON facts(fact_type)`,
		`CREATE INDEX IF NOT EXISTS idx_facts_subject ON facts(subject)`,

		// Embedding vectors for semantic search
		`CREATE TABLE IF NOT EXISTS embeddings (
			memory_id  INTEGER PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
			vector     BLOB NOT NULL,
			dimensions INTEGER NOT NULL
		)`,

		// Recall log for provenance tracking
		`CREATE TABLE IF NOT EXISTS recall_log (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			fact_id     INTEGER REFERENCES facts(id) ON DELETE CASCADE,
			recalled_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			context     TEXT,
			session_id  TEXT,
			lens        TEXT
		)`,

		// Memory event log (append-only, differential memory)
		`CREATE TABLE IF NOT EXISTS memory_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL CHECK(event_type IN ('add','update','merge','decay','delete','reinforce')),
			fact_id    INTEGER,
			old_value  TEXT,
			new_value  TEXT,
			source     TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// Named snapshots for point-in-time restore
		`CREATE TABLE IF NOT EXISTS snapshots (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			tag        TEXT UNIQUE NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			event_id   INTEGER REFERENCES memory_events(id)
		)`,

		// Memory lenses
		`CREATE TABLE IF NOT EXISTS lenses (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			name         TEXT UNIQUE NOT NULL,
			include_tags TEXT,
			exclude_tags TEXT,
			boost_rules  TEXT,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// Metadata table
		`CREATE TABLE IF NOT EXISTS meta (
			key   TEXT PRIMARY KEY,
			value TEXT
		)`,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning migration transaction: %w", err)
	}
	defer tx.Rollback()

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("executing migration %q: %w", truncate(stmt, 80), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration: %w", err)
	}

	// Seed metadata (outside transaction — meta table now exists)
	if err := s.seedMeta(); err != nil {
		return fmt.Errorf("seeding metadata: %w", err)
	}

	return nil
}

// seedMeta initializes the meta table with defaults if not already set.
func (s *SQLiteStore) seedMeta() error {
	defaults := map[string]string{
		"schema_version":       "1",
		"embedding_dimensions": fmt.Sprintf("%d", s.embDims),
		"created_at":           time.Now().UTC().Format(time.RFC3339),
	}

	for k, v := range defaults {
		_, err := s.db.Exec(
			"INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)", k, v,
		)
		if err != nil {
			return fmt.Errorf("seeding meta key %q: %w", k, err)
		}
	}
	return nil
}

// truncate shortens a string for error messages.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
