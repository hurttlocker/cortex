package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaimMetaMigration_StoresPIDAndTimestamp(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)

	claimed, err := s.claimMetaMigration("test_claim_state")
	if err != nil {
		t.Fatalf("claimMetaMigration: %v", err)
	}
	if !claimed {
		t.Fatal("expected claimMetaMigration to claim empty key")
	}

	value, err := s.getMetaValue("test_claim_state")
	if err != nil {
		t.Fatalf("getMetaValue: %v", err)
	}
	if !strings.HasPrefix(value, "in_progress") {
		t.Fatalf("expected in_progress value, got %q", value)
	}

	pid, startedAt, ok := parseMetaMigrationClaim(value)
	if !ok {
		t.Fatalf("expected claim value to parse, got %q", value)
	}
	if pid != os.Getpid() {
		t.Fatalf("claimed pid = %d, want %d", pid, os.Getpid())
	}
	if startedAt.IsZero() {
		t.Fatal("expected non-zero started_at")
	}
}

func TestClaimMetaMigration_ReclaimsDeadPID(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	key := "test_claim_reclaim"

	stale := formatMetaMigrationClaim(999999999, time.Now().Add(-time.Hour))
	if _, err := s.db.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)", key, stale); err != nil {
		t.Fatalf("seed stale claim: %v", err)
	}

	claimed, err := s.claimMetaMigration(key)
	if err != nil {
		t.Fatalf("claimMetaMigration: %v", err)
	}
	if !claimed {
		t.Fatal("expected stale dead-pid claim to be reclaimed")
	}

	value, err := s.getMetaValue(key)
	if err != nil {
		t.Fatalf("getMetaValue: %v", err)
	}
	pid, _, ok := parseMetaMigrationClaim(value)
	if !ok {
		t.Fatalf("expected replacement claim to parse, got %q", value)
	}
	if pid != os.Getpid() {
		t.Fatalf("reclaimed pid = %d, want %d", pid, os.Getpid())
	}
}

func TestMigrateFTSMultiColumn_RecoverStaleInProgressMarker(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)

	stale := formatMetaMigrationClaim(999999999, time.Now().Add(-time.Hour))
	if _, err := s.db.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES ('fts_multi_column', ?)", stale); err != nil {
		t.Fatalf("seed stale fts marker: %v", err)
	}

	start := time.Now()
	if err := s.migrateFTSMultiColumn(); err != nil {
		t.Fatalf("migrateFTSMultiColumn: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("expected stale marker recovery to be quick, took %s", time.Since(start))
	}

	state, err := s.getMetaValue("fts_multi_column")
	if err != nil {
		t.Fatalf("getMetaValue: %v", err)
	}
	if state != "true" {
		t.Fatalf("expected fts_multi_column=true after recovery, got %q", state)
	}
}

func TestMigrateMemoryClassNullBackfill_RewritesLegacyNulls(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-null-class.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}

	// Simulate a legacy schema where memory_class exists and allows NULL.
	if _, err := rawDB.Exec(`
		CREATE TABLE memories (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			content        TEXT NOT NULL,
			source_file    TEXT,
			source_line    INTEGER,
			source_section TEXT,
			content_hash   TEXT UNIQUE NOT NULL,
			project        TEXT NOT NULL DEFAULT '',
			memory_class   TEXT DEFAULT NULL,
			metadata       TEXT DEFAULT NULL,
			imported_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at     DATETIME
		)
	`); err != nil {
		t.Fatalf("create legacy memories table: %v", err)
	}
	if _, err := rawDB.Exec(`
		INSERT INTO memories (content, source_file, source_line, source_section, content_hash, project, memory_class)
		VALUES ('legacy row with nullable class', 'legacy.md', 1, 'legacy', 'legacy-hash-1', '', NULL)
	`); err != nil {
		t.Fatalf("insert legacy NULL row: %v", err)
	}
	ss := &SQLiteStore{db: rawDB}
	if err := ss.migrateMemoryClassNullBackfill(); err != nil {
		t.Fatalf("migrateMemoryClassNullBackfill on legacy schema: %v", err)
	}

	var nullCount int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM memories WHERE memory_class IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("count NULL memory_class: %v", err)
	}
	if nullCount != 0 {
		t.Fatalf("expected NULL memory_class rows to be backfilled, still found %d", nullCount)
	}

	var class string
	if err := rawDB.QueryRow(`SELECT memory_class FROM memories WHERE content_hash = 'legacy-hash-1'`).Scan(&class); err != nil {
		t.Fatalf("read backfilled class: %v", err)
	}
	if class != "" {
		t.Fatalf("expected empty-string sentinel after backfill, got %q", class)
	}

	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}
}
