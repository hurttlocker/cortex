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

		// Fix #10: Recreate memories_au to skip FTS reinsert on soft-delete.
		// DROP+CREATE is idempotent — fixes existing databases without a version gate.
		`DROP TRIGGER IF EXISTS memories_au`,
		`CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.id, old.content);
			INSERT INTO memories_fts(rowid, content)
				SELECT new.id, new.content WHERE new.deleted_at IS NULL;
		END`,

		// One-time cleanup: purge FTS ghosts for already-soft-deleted memories.
		`DELETE FROM memories_fts WHERE rowid IN (SELECT id FROM memories WHERE deleted_at IS NOT NULL)`,

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
		`CREATE INDEX IF NOT EXISTS idx_facts_subject_predicate ON facts(subject, predicate)`,

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

	// Schema evolution: add project column (v0.2.0 — Issue #29)
	// Uses ALTER TABLE which can't be inside CREATE TABLE IF NOT EXISTS.
	// We check for column existence first to make it idempotent.
	if err := s.migrateProjectColumn(); err != nil {
		return fmt.Errorf("migrating project column: %w", err)
	}

	// Schema evolution: add metadata column (v0.2.0 — Issue #30)
	if err := s.migrateMetadataColumn(); err != nil {
		return fmt.Errorf("migrating metadata column: %w", err)
	}

	// Schema evolution: add memory_class column (v0.3.0 — Issue #34)
	if err := s.migrateMemoryClassColumn(); err != nil {
		return fmt.Errorf("migrating memory_class column: %w", err)
	}

	// Schema evolution: multi-column FTS5 with source context (v0.2.0 — Issue #26)
	if err := s.migrateFTSMultiColumn(); err != nil {
		return fmt.Errorf("migrating FTS multi-column: %w", err)
	}

	return nil
}

// migrateProjectColumn adds the project column to memories if it doesn't exist.
// This is a safe, idempotent migration for Issue #29 (thread/project tagging).
func (s *SQLiteStore) migrateProjectColumn() error {
	// Check if column already exists
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name='project'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("checking for project column: %w", err)
	}
	if count > 0 {
		return nil // Already migrated
	}

	// Add column + index in a transaction
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning project migration: %w", err)
	}
	defer tx.Rollback()

	stmts := []string{
		`ALTER TABLE memories ADD COLUMN project TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_memories_project ON memories(project)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("executing %q: %w", truncate(stmt, 60), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing project migration: %w", err)
	}
	return nil
}

// migrateMemoryClassColumn adds memory_class to memories for class-aware retrieval (Issue #34).
// NULL means "unclassified" for backward compatibility.
func (s *SQLiteStore) migrateMemoryClassColumn() error {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name='memory_class'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("checking for memory_class column: %w", err)
	}
	if count > 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning memory_class migration: %w", err)
	}
	defer tx.Rollback()

	stms := []string{
		`ALTER TABLE memories ADD COLUMN memory_class TEXT DEFAULT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_memory_class ON memories(memory_class)`,
	}
	for _, stmt := range stms {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("executing %q: %w", truncate(stmt, 60), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing memory_class migration: %w", err)
	}
	return nil
}

// migrateFTSMultiColumn upgrades FTS5 from single-column (content only) to
// multi-column (content + source_file + source_section) for Issue #26.
//
// This enables BM25 to match against section headers and filenames,
// dramatically improving search quality for domain-specific queries.
// Example: "cortex conflicts timeout" now matches chunks where "cortex"
// appears in the section header even if the chunk body only says "timeout".
//
// The migration is idempotent: checks a meta key to avoid re-running.
// After upgrading, existing FTS data is rebuilt from the memories table.
func (s *SQLiteStore) migrateFTSMultiColumn() error {
	// Check if already migrated
	var val string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = 'fts_multi_column'").Scan(&val)
	if err == nil && val == "true" {
		return nil // Already migrated
	}

	// Drop old triggers + FTS table, recreate with multi-column schema
	stmts := []string{
		// Drop old triggers
		`DROP TRIGGER IF EXISTS memories_ai`,
		`DROP TRIGGER IF EXISTS memories_ad`,
		`DROP TRIGGER IF EXISTS memories_au`,

		// Drop old single-column FTS table
		`DROP TABLE IF EXISTS memories_fts`,

		// Create new multi-column FTS table
		// content=memories + content_rowid=id makes this a content-synced table
		// Three columns: content (main text), source_file (path), source_section (header)
		`CREATE VIRTUAL TABLE memories_fts USING fts5(
			content,
			source_file,
			source_section,
			content=memories,
			content_rowid=id,
			tokenize='porter unicode61'
		)`,

		// New triggers that populate all 3 FTS columns
		`CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, content, source_file, source_section)
			VALUES (new.id, new.content, COALESCE(new.source_file, ''), COALESCE(new.source_section, ''));
		END`,

		`CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content, source_file, source_section)
			VALUES('delete', old.id, old.content, COALESCE(old.source_file, ''), COALESCE(old.source_section, ''));
		END`,

		// Update trigger: delete old entry, insert new only if not soft-deleted
		`CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content, source_file, source_section)
			VALUES('delete', old.id, old.content, COALESCE(old.source_file, ''), COALESCE(old.source_section, ''));
			INSERT INTO memories_fts(rowid, content, source_file, source_section)
				SELECT new.id, new.content, COALESCE(new.source_file, ''), COALESCE(new.source_section, '')
				WHERE new.deleted_at IS NULL;
		END`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("executing FTS migration %q: %w", truncate(stmt, 80), err)
		}
	}

	// Rebuild FTS index from existing memories
	result, err := s.db.Exec(`
		INSERT INTO memories_fts(rowid, content, source_file, source_section)
		SELECT id, content, COALESCE(source_file, ''), COALESCE(source_section, '')
		FROM memories
		WHERE deleted_at IS NULL
	`)
	if err != nil {
		return fmt.Errorf("rebuilding FTS index: %w", err)
	}
	rebuilt, _ := result.RowsAffected()

	// Mark migration as done
	if _, err := s.db.Exec(
		"INSERT OR REPLACE INTO meta (key, value) VALUES ('fts_multi_column', 'true')",
	); err != nil {
		return fmt.Errorf("marking FTS migration complete: %w", err)
	}

	// Also remove the stale FTS ghost cleanup since we just rebuilt from scratch
	if _, err := s.db.Exec(
		"DELETE FROM memories_fts WHERE rowid IN (SELECT id FROM memories WHERE deleted_at IS NOT NULL)",
	); err != nil {
		// Non-fatal: ghost cleanup is best-effort
		_ = err
	}

	fmt.Printf("  FTS multi-column migration complete: %d memories indexed with source context\n", rebuilt)
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
