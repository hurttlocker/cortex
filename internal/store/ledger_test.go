package store

import (
	"context"
	"testing"
	"time"
)

func TestMigrateSessionLedgerTable_FreshAndIdempotent(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)

	// migrate() already ran once via NewStore; running the migration again
	// directly must be a no-op (idempotent).
	if err := s.migrateSessionLedgerTable(); err != nil {
		t.Fatalf("second migrateSessionLedgerTable call failed: %v", err)
	}

	var tableName string
	err := s.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='session_ledger'",
	).Scan(&tableName)
	if err != nil {
		t.Fatalf("session_ledger table not found: %v", err)
	}

	flag, err := s.isMetaFlagEnabled("session_ledger_v1")
	if err != nil {
		t.Fatalf("checking session_ledger_v1 flag: %v", err)
	}
	if !flag {
		t.Fatal("expected session_ledger_v1 meta flag to be set after migration")
	}
}

func TestRecordLedgerEntry_PersistsAllFields(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t).(*SQLiteStore)

	entry := &LedgerEntry{
		SessionID:    "sess-1",
		TaskSummary:  "Fixed nil pointer in worker dispatch",
		Outcome:      "success",
		FilesTouched: []string{"internal/worker/dispatch.go", "internal/worker/dispatch_test.go"},
		FixPattern:   "add nil check before dereference",
		AgentID:      "codex",
		Project:      "cortex",
	}

	id, err := s.RecordLedgerEntry(ctx, entry)
	if err != nil {
		t.Fatalf("RecordLedgerEntry: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	entries, err := s.ListLedgerEntries(ctx, time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("ListLedgerEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	got := entries[0]
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", got.SessionID)
	}
	if got.TaskSummary != entry.TaskSummary {
		t.Errorf("TaskSummary = %q, want %q", got.TaskSummary, entry.TaskSummary)
	}
	if got.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", got.Outcome)
	}
	if len(got.FilesTouched) != 2 || got.FilesTouched[0] != "internal/worker/dispatch.go" {
		t.Errorf("FilesTouched = %v, want 2 entries starting with dispatch.go", got.FilesTouched)
	}
	if got.FixPattern != entry.FixPattern {
		t.Errorf("FixPattern = %q, want %q", got.FixPattern, entry.FixPattern)
	}
	if got.AgentID != "codex" {
		t.Errorf("AgentID = %q, want codex", got.AgentID)
	}
	if got.Project != "cortex" {
		t.Errorf("Project = %q, want cortex", got.Project)
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestRecordLedgerEntry_RejectsInvalidOutcome(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t).(*SQLiteStore)

	_, err := s.RecordLedgerEntry(ctx, &LedgerEntry{
		TaskSummary: "some task",
		Outcome:     "bogus",
	})
	if err == nil {
		t.Fatal("expected error for invalid outcome, got nil")
	}
}

func TestRecordLedgerEntry_RejectsEmptySummary(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t).(*SQLiteStore)

	_, err := s.RecordLedgerEntry(ctx, &LedgerEntry{
		TaskSummary: "   ",
		Outcome:     "success",
	})
	if err == nil {
		t.Fatal("expected error for empty task_summary, got nil")
	}
}

func TestListLedgerEntries_FiltersBySinceAndProject(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t).(*SQLiteStore)

	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)

	seed := func(createdAt time.Time, project, summary string) int64 {
		t.Helper()
		id, err := s.RecordLedgerEntry(ctx, &LedgerEntry{
			TaskSummary: summary,
			Outcome:     "success",
			Project:     project,
		})
		if err != nil {
			t.Fatalf("seeding ledger entry: %v", err)
		}
		// Backdate created_at directly since RecordLedgerEntry always stamps now().
		if _, err := s.db.ExecContext(ctx, `UPDATE session_ledger SET created_at = ? WHERE id = ?`, createdAt, id); err != nil {
			t.Fatalf("backdating ledger entry: %v", err)
		}
		return id
	}

	seed(old, "cortex", "old cortex task")
	seed(recent, "cortex", "recent cortex task")
	seed(recent, "spear", "recent spear task")

	// since=14d ago should exclude the 30-day-old row.
	since := time.Now().UTC().Add(-14 * 24 * time.Hour)
	entries, err := s.ListLedgerEntries(ctx, since, "", 0)
	if err != nil {
		t.Fatalf("ListLedgerEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries within 14d window, got %d", len(entries))
	}

	// project filter narrows further.
	entries, err = s.ListLedgerEntries(ctx, since, "cortex", 0)
	if err != nil {
		t.Fatalf("ListLedgerEntries with project filter: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cortex entry within window, got %d", len(entries))
	}
	if entries[0].Project != "cortex" {
		t.Errorf("Project = %q, want cortex", entries[0].Project)
	}

	// no since filter returns all 3.
	entries, err = s.ListLedgerEntries(ctx, time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("ListLedgerEntries unfiltered: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries total, got %d", len(entries))
	}
}

func TestLedgerEntriesByPattern_OnlyNonEmptyPatternsWithinWindow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t).(*SQLiteStore)

	old := time.Now().UTC().Add(-30 * 24 * time.Hour)

	mustRecord := func(pattern string) int64 {
		id, err := s.RecordLedgerEntry(ctx, &LedgerEntry{
			TaskSummary: "task",
			Outcome:     "success",
			FixPattern:  pattern,
		})
		if err != nil {
			t.Fatalf("RecordLedgerEntry: %v", err)
		}
		return id
	}

	mustRecord("recurring fix A") // recent, has pattern
	mustRecord("")                // recent, no pattern -> excluded

	oldID := mustRecord("recurring fix A") // will be backdated out of window
	if _, err := s.db.ExecContext(ctx, `UPDATE session_ledger SET created_at = ? WHERE id = ?`, old, oldID); err != nil {
		t.Fatalf("backdating: %v", err)
	}

	entries, err := s.LedgerEntriesByPattern(ctx, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("LedgerEntriesByPattern: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (recent, non-empty pattern), got %d", len(entries))
	}
	if entries[0].FixPattern != "recurring fix A" {
		t.Errorf("FixPattern = %q, want %q", entries[0].FixPattern, "recurring fix A")
	}
}
