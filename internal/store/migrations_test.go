package store

import (
	"os"
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
