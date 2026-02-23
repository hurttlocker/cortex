package store

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// migrate creates all tables if they don't exist and seeds metadata.
func (s *SQLiteStore) migrate() error {
	bootstrapDone, err := s.isMetaFlagEnabled("schema_bootstrap_complete")
	if err != nil {
		return fmt.Errorf("checking bootstrap state: %w", err)
	}

	if !bootstrapDone {
		if err := s.runBootstrapDDL(); err != nil {
			return err
		}
	}

	// Seed metadata (outside bootstrap transaction — meta table now exists)
	if err := s.seedMeta(); err != nil {
		return fmt.Errorf("seeding metadata: %w", err)
	}

	if !bootstrapDone {
		if err := s.setMetaFlag("schema_bootstrap_complete"); err != nil {
			return fmt.Errorf("marking bootstrap complete: %w", err)
		}
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
	// Backfill legacy NULL classes to empty-string sentinel for robust scans/filtering (#63).
	if err := s.migrateMemoryClassNullBackfill(); err != nil {
		return fmt.Errorf("backfilling memory_class NULLs: %w", err)
	}

	// Schema evolution: add superseded_by column to facts (v0.3.0 — Issue #35)
	if err := s.migrateFactSupersededColumn(); err != nil {
		return fmt.Errorf("migrating superseded_by column: %w", err)
	}

	// Schema evolution: multi-column FTS5 with source context (v0.2.0 — Issue #26)
	if err := s.migrateFTSMultiColumn(); err != nil {
		return fmt.Errorf("migrating FTS multi-column: %w", err)
	}

	// Schema evolution: SLO performance indexes (v0.4.0 — Issue #147)
	if err := s.migrateSLOIndexes(); err != nil {
		return fmt.Errorf("migrating SLO indexes: %w", err)
	}

	// Schema evolution: connectors table (v0.5.0 — Issue #138/#139)
	if err := s.migrateConnectorsTable(); err != nil {
		return fmt.Errorf("migrating connectors table: %w", err)
	}

	// Schema evolution: alerts table (v1.0 — Issue #162)
	if err := s.migrateAlertsTable(); err != nil {
		return fmt.Errorf("migrating alerts table: %w", err)
	}

	return nil
}

func (s *SQLiteStore) runBootstrapDDL() error {
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

		// FTS5 full-text search index (multi-column)
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			content,
			source_file,
			source_section,
			content=memories,
			content_rowid=id,
			tokenize='porter unicode61'
		)`,

		// FTS sync triggers
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, content, source_file, source_section)
			VALUES (new.id, new.content, COALESCE(new.source_file, ''), COALESCE(new.source_section, ''));
		END`,

		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content, source_file, source_section)
			VALUES('delete', old.id, old.content, COALESCE(old.source_file, ''), COALESCE(old.source_section, ''));
		END`,

		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content, source_file, source_section)
			VALUES('delete', old.id, old.content, COALESCE(old.source_file, ''), COALESCE(old.source_section, ''));
			INSERT INTO memories_fts(rowid, content, source_file, source_section)
				SELECT new.id, new.content, COALESCE(new.source_file, ''), COALESCE(new.source_section, '')
				WHERE new.deleted_at IS NULL;
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
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			superseded_by   INTEGER REFERENCES facts(id)
		)`,

		`CREATE INDEX IF NOT EXISTS idx_facts_memory_id ON facts(memory_id)`,
		`CREATE INDEX IF NOT EXISTS idx_facts_type ON facts(fact_type)`,
		`CREATE INDEX IF NOT EXISTS idx_facts_subject ON facts(subject)`,
		`CREATE INDEX IF NOT EXISTS idx_facts_subject_predicate ON facts(subject, predicate)`,
		// idx_facts_superseded_by is created by migrateFactSupersededColumn() for existing DBs,
		// and here for new DBs only (column exists in CREATE TABLE above).

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

	return nil
}

func (s *SQLiteStore) isMetaFlagEnabled(key string) (bool, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return false, nil
	}

	var value string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return value == "true", nil
}

func (s *SQLiteStore) setMetaFlag(key string) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES (?, 'true')", key)
	return err
}

func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
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
			if isDuplicateColumnError(err) {
				continue
			}
			return fmt.Errorf("executing %q: %w", truncate(stmt, 60), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing project migration: %w", err)
	}
	return nil
}

// migrateMemoryClassColumn adds memory_class to memories for class-aware retrieval (Issue #34).
// Column stays nullable for compatibility with historical DBs; startup backfill normalizes NULLs.
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
			if isDuplicateColumnError(err) {
				continue
			}
			return fmt.Errorf("executing %q: %w", truncate(stmt, 60), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing memory_class migration: %w", err)
	}
	return nil
}

// migrateMemoryClassNullBackfill rewrites legacy NULL memory_class rows to "" (#63).
// This keeps historical rows searchable while preventing NULL scan edge cases in older datasets.
func (s *SQLiteStore) migrateMemoryClassNullBackfill() error {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name='memory_class'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("checking for memory_class column: %w", err)
	}
	if count == 0 {
		return nil
	}

	if _, err := s.db.Exec(`UPDATE memories SET memory_class = '' WHERE memory_class IS NULL`); err != nil {
		return fmt.Errorf("backfilling NULL memory_class values: %w", err)
	}
	return nil
}

// migrateFactSupersededColumn adds superseded_by to facts for tombstone semantics (Issue #35).
func (s *SQLiteStore) migrateFactSupersededColumn() error {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('facts') WHERE name='superseded_by'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("checking for superseded_by column: %w", err)
	}
	if count > 0 {
		// Ensure index exists for existing DBs.
		if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_facts_superseded_by ON facts(superseded_by)`); err != nil {
			return fmt.Errorf("creating superseded_by index: %w", err)
		}
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning superseded_by migration: %w", err)
	}
	defer tx.Rollback()

	stms := []string{
		`ALTER TABLE facts ADD COLUMN superseded_by INTEGER REFERENCES facts(id)`,
		`CREATE INDEX IF NOT EXISTS idx_facts_superseded_by ON facts(superseded_by)`,
	}
	for _, stmt := range stms {
		if _, err := tx.Exec(stmt); err != nil {
			if isDuplicateColumnError(err) {
				continue
			}
			return fmt.Errorf("executing %q: %w", truncate(stmt, 60), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing superseded_by migration: %w", err)
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
	const key = "fts_multi_column"

	state, err := s.getMetaValue(key)
	if err != nil {
		return fmt.Errorf("checking FTS migration state: %w", err)
	}
	if state == "true" {
		return nil
	}
	if isMetaMigrationInProgress(state) {
		stale, reason := isStaleMetaMigrationClaim(state)
		if stale {
			fmt.Printf("  Clearing stale FTS migration claim (%s)\n", reason)
			if err := s.clearMetaKey(key); err != nil {
				return fmt.Errorf("clearing stale FTS migration claim: %w", err)
			}
		} else {
			if err := s.waitForMetaValue(key, "true", 30*time.Second); err != nil {
				return fmt.Errorf("waiting for concurrent FTS migration: %w", err)
			}
			return nil
		}
	}

	// Fresh DBs now bootstrap with multi-column FTS directly.
	isMulti, err := s.hasFTSMultiSchema()
	if err != nil {
		return fmt.Errorf("checking FTS schema: %w", err)
	}
	if isMulti {
		if _, err := s.db.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES ('fts_multi_column', 'true')"); err != nil {
			return fmt.Errorf("marking FTS schema state: %w", err)
		}
		return nil
	}

	claimed, err := s.claimMetaMigration(key)
	if err != nil {
		return fmt.Errorf("claiming FTS migration: %w", err)
	}
	if !claimed {
		if err := s.waitForMetaValue(key, "true", 30*time.Second); err != nil {
			return fmt.Errorf("waiting for concurrent FTS migration: %w", err)
		}
		return nil
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
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			content,
			source_file,
			source_section,
			content=memories,
			content_rowid=id,
			tokenize='porter unicode61'
		)`,

		// New triggers that populate all 3 FTS columns
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, content, source_file, source_section)
			VALUES (new.id, new.content, COALESCE(new.source_file, ''), COALESCE(new.source_section, ''));
		END`,

		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content, source_file, source_section)
			VALUES('delete', old.id, old.content, COALESCE(old.source_file, ''), COALESCE(old.source_section, ''));
		END`,

		// Update trigger: delete old entry, insert new only if not soft-deleted
		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content, source_file, source_section)
			VALUES('delete', old.id, old.content, COALESCE(old.source_file, ''), COALESCE(old.source_section, ''));
			INSERT INTO memories_fts(rowid, content, source_file, source_section)
				SELECT new.id, new.content, COALESCE(new.source_file, ''), COALESCE(new.source_section, '')
				WHERE new.deleted_at IS NULL;
		END`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			_ = s.clearMetaKey(key)
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
		_ = s.clearMetaKey(key)
		return fmt.Errorf("rebuilding FTS index: %w", err)
	}
	rebuilt, _ := result.RowsAffected()

	// Mark migration as done
	if _, err := s.db.Exec(
		"INSERT OR REPLACE INTO meta (key, value) VALUES ('fts_multi_column', 'true')",
	); err != nil {
		_ = s.clearMetaKey(key)
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

func (s *SQLiteStore) getMetaValue(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

func (s *SQLiteStore) hasFTSMultiSchema() (bool, error) {
	rows, err := s.db.Query("SELECT name FROM pragma_table_info('memories_fts')")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return false, nil
		}
		return false, err
	}
	defer rows.Close()

	hasSourceFile := false
	hasSourceSection := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		switch strings.ToLower(name) {
		case "source_file":
			hasSourceFile = true
		case "source_section":
			hasSourceSection = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return hasSourceFile && hasSourceSection, nil
}

func formatMetaMigrationClaim(pid int, startedAt time.Time) string {
	return fmt.Sprintf("in_progress;pid=%d;started_at=%s", pid, startedAt.UTC().Format(time.RFC3339))
}

func isMetaMigrationInProgress(value string) bool {
	return strings.HasPrefix(value, "in_progress")
}

func parseMetaMigrationClaim(value string) (int, time.Time, bool) {
	if !isMetaMigrationInProgress(value) {
		return 0, time.Time{}, false
	}

	parts := strings.Split(value, ";")
	var pid int
	var startedAt time.Time
	var pidFound bool
	var tsFound bool

	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "pid="):
			if _, err := fmt.Sscanf(part, "pid=%d", &pid); err != nil {
				return 0, time.Time{}, false
			}
			if pid <= 0 {
				return 0, time.Time{}, false
			}
			pidFound = true
		case strings.HasPrefix(part, "started_at="):
			ts := strings.TrimPrefix(part, "started_at=")
			parsed, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				return 0, time.Time{}, false
			}
			startedAt = parsed
			tsFound = true
		}
	}

	if !pidFound || !tsFound {
		return 0, time.Time{}, false
	}

	return pid, startedAt, true
}

func isStaleMetaMigrationClaim(value string) (bool, string) {
	if !isMetaMigrationInProgress(value) {
		return false, ""
	}

	pid, _, ok := parseMetaMigrationClaim(value)
	if !ok {
		return true, "malformed or legacy in_progress value"
	}
	if !isStoreProcessAlive(pid) {
		return true, fmt.Sprintf("pid %d is not running", pid)
	}
	return false, ""
}

func isStoreProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func (s *SQLiteStore) claimMetaMigration(key string) (bool, error) {
	claimValue := formatMetaMigrationClaim(os.Getpid(), time.Now().UTC())

	for attempt := 0; attempt < 2; attempt++ {
		result, err := s.db.Exec("INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)", key, claimValue)
		if err != nil {
			return false, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if rows == 1 {
			return true, nil
		}

		existing, err := s.getMetaValue(key)
		if err != nil {
			return false, err
		}
		if !isMetaMigrationInProgress(existing) {
			return false, nil
		}

		stale, _ := isStaleMetaMigrationClaim(existing)
		if !stale {
			return false, nil
		}

		// Compare-and-delete so we only clear the stale value we inspected.
		clearRes, err := s.db.Exec("DELETE FROM meta WHERE key = ? AND value = ?", key, existing)
		if err != nil {
			return false, err
		}
		cleared, err := clearRes.RowsAffected()
		if err != nil {
			return false, err
		}
		if cleared == 0 {
			// Lost race to another writer; retry once.
			continue
		}
	}

	return false, nil
}

func (s *SQLiteStore) waitForMetaValue(key, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		value, err := s.getMetaValue(key)
		if err != nil {
			return err
		}
		if value == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s=%q (last=%q)", key, want, value)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func (s *SQLiteStore) clearMetaKey(key string) error {
	_, err := s.db.Exec("DELETE FROM meta WHERE key = ?", key)
	return err
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

// migrateSLOIndexes adds performance indexes for search, stale, and conflicts SLOs.
func (s *SQLiteStore) migrateSLOIndexes() error {
	done, err := s.isMetaFlagEnabled("slo_indexes_v1")
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	indexes := []string{
		// Covering index for conflicts query: GROUP BY LOWER(subject), LOWER(predicate)
		// with superseded_by filter. Dramatically speeds up the self-join.
		`CREATE INDEX IF NOT EXISTS idx_facts_conflict_scan
		 ON facts(superseded_by, subject, predicate, object)`,

		// Index for stale facts query: confidence + last_reinforced scan
		`CREATE INDEX IF NOT EXISTS idx_facts_stale_scan
		 ON facts(superseded_by, confidence, last_reinforced)`,

		// Index for memory-joined fact lookups (used in governor purge and search)
		`CREATE INDEX IF NOT EXISTS idx_facts_memid_superseded
		 ON facts(memory_id, superseded_by)`,
	}

	for _, ddl := range indexes {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("creating SLO index: %w", err)
		}
	}

	if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('slo_indexes_v1', '1')`); err != nil {
		return fmt.Errorf("setting slo_indexes_v1 flag: %w", err)
	}

	return nil
}

// migrateConnectorsTable creates the connectors table for Cortex Connect (#138/#139).
func (s *SQLiteStore) migrateConnectorsTable() error {
	done, err := s.isMetaFlagEnabled("connectors_v1")
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS connectors (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			provider        TEXT NOT NULL UNIQUE,
			config          TEXT NOT NULL DEFAULT '{}',
			enabled         INTEGER NOT NULL DEFAULT 1,
			last_sync_at    DATETIME,
			last_error      TEXT,
			records_imported INTEGER NOT NULL DEFAULT 0,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("creating connectors table: %w", err)
		}
	}

	if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('connectors_v1', 'true')`); err != nil {
		return fmt.Errorf("setting connectors_v1 flag: %w", err)
	}

	return nil
}

// migrateAlertsTable creates the alerts table for proactive notifications.
func (s *SQLiteStore) migrateAlertsTable() error {
	done, err := s.isMetaFlagEnabled("alerts_v1")
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS alerts (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			alert_type      TEXT NOT NULL,
			severity        TEXT NOT NULL DEFAULT 'info',
			fact_id         INTEGER,
			related_fact_id INTEGER,
			agent_id        TEXT DEFAULT '',
			message         TEXT NOT NULL,
			details         TEXT DEFAULT '',
			acknowledged    INTEGER NOT NULL DEFAULT 0,
			acknowledged_at DATETIME,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (fact_id) REFERENCES facts(id),
			FOREIGN KEY (related_fact_id) REFERENCES facts(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_type ON alerts(alert_type)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_unacked ON alerts(acknowledged, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_fact ON alerts(fact_id)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("creating alerts table: %w", err)
		}
	}

	if _, err := s.db.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('alerts_v1', 'true')`); err != nil {
		return fmt.Errorf("setting alerts_v1 flag: %w", err)
	}

	return nil
}

// GetDB returns the underlying *sql.DB for packages that need direct access
// (e.g., internal/connect). This does NOT break encapsulation — callers still
// go through typed store methods for normal operations.
func (s *SQLiteStore) GetDB() *sql.DB {
	return s.db
}

// truncate shortens a string for error messages.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
