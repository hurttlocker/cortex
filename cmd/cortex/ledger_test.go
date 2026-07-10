package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

func withLedgerTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cortex-ledger.db")
	globalDBPath = ""
	t.Cleanup(func() { globalDBPath = "" })
	t.Setenv("CORTEX_DB", dbPath)
	return dbPath
}

func TestRunLedger_RecordAndList_RealPathJSONRoundTrip(t *testing.T) {
	withLedgerTestDB(t)

	if err := runLedger([]string{"record",
		"--summary", "Fixed flaky worktree merge test",
		"--outcome", "success",
		"--files", "internal/lane/worktree_side_merge.go,internal/lane/worktree_side_merge_test.go",
		"--pattern", "serialize with keyed-promise-chain",
		"--session", "sess-1",
		"--agent", "codex",
		"--project", "cortex",
	}); err != nil {
		t.Fatalf("runLedger record: %v", err)
	}

	if err := runLedger([]string{"record",
		"--summary", "Partial fix for dispatch retry budget",
		"--outcome", "partial",
	}); err != nil {
		t.Fatalf("runLedger record (2nd): %v", err)
	}

	out := captureStdout(func() {
		if err := runLedger([]string{"list", "--json"}); err != nil {
			t.Fatalf("runLedger list: %v", err)
		}
	})

	var entries []store.LedgerEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("unmarshal ledger list output: %v\noutput=%s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 ledger entries, got %d", len(entries))
	}

	// Newest first.
	first := entries[0]
	if first.TaskSummary != "Partial fix for dispatch retry budget" {
		t.Errorf("expected newest entry first, got %q", first.TaskSummary)
	}
	if first.Outcome != "partial" {
		t.Errorf("Outcome = %q, want partial", first.Outcome)
	}

	second := entries[1]
	if second.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", second.Outcome)
	}
	if len(second.FilesTouched) != 2 {
		t.Errorf("expected 2 files_touched, got %v", second.FilesTouched)
	}
	if second.FixPattern != "serialize with keyed-promise-chain" {
		t.Errorf("FixPattern = %q", second.FixPattern)
	}
	if second.AgentID != "codex" || second.Project != "cortex" || second.SessionID != "sess-1" {
		t.Errorf("unexpected scope fields: %+v", second)
	}
}

func TestRunLedger_ListSinceFiltersOldRows(t *testing.T) {
	dbPath := withLedgerTestDB(t)

	if err := runLedger([]string{"record", "--summary", "recent task", "--outcome", "success"}); err != nil {
		t.Fatalf("runLedger record: %v", err)
	}
	if err := runLedger([]string{"record", "--summary", "old task", "--outcome", "failure"}); err != nil {
		t.Fatalf("runLedger record: %v", err)
	}

	// Backdate the "old task" row directly in the store.
	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sqlStore := s.(*store.SQLiteStore)
	if _, err := sqlStore.ExecContext(context.Background(),
		`UPDATE session_ledger SET created_at = ? WHERE task_summary = ?`,
		time.Now().UTC().AddDate(0, 0, -30), "old task",
	); err != nil {
		t.Fatalf("backdate row: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	out := captureStdout(func() {
		if err := runLedger([]string{"list", "--since", "14d", "--json"}); err != nil {
			t.Fatalf("runLedger list --since: %v", err)
		}
	})

	var entries []store.LedgerEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("unmarshal ledger list output: %v\noutput=%s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry within 14d window, got %d", len(entries))
	}
	if entries[0].TaskSummary != "recent task" {
		t.Errorf("expected recent task to survive the --since filter, got %q", entries[0].TaskSummary)
	}
}

func TestRunLedger_RecordRequiresSummaryAndOutcome(t *testing.T) {
	withLedgerTestDB(t)

	err := runLedger([]string{"record", "--outcome", "success"})
	if err == nil {
		t.Fatal("expected error when --summary is missing")
	}

	err = runLedger([]string{"record", "--summary", "a task"})
	if err == nil {
		t.Fatal("expected error when --outcome is missing")
	}
}

func TestRunLedger_RecordRejectsInvalidOutcome(t *testing.T) {
	withLedgerTestDB(t)

	err := runLedger([]string{"record", "--summary", "a task", "--outcome", "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid outcome")
	}
	if !strings.Contains(err.Error(), "invalid ledger outcome") {
		t.Fatalf("expected invalid ledger outcome error, got: %v", err)
	}
}

func TestRunLedger_UnknownSubcommand(t *testing.T) {
	err := runLedger([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown ledger subcommand") {
		t.Fatalf("expected unknown subcommand error, got: %v", err)
	}
}

func TestParseSinceDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"14d", 14 * 24 * time.Hour, false},
		{"2w", 2 * 7 * 24 * time.Hour, false},
		{"12h", 12 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"", 0, true},
		{"bogus", 0, true},
	}

	for _, c := range cases {
		got, err := parseSinceDuration(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSinceDuration(%q): expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSinceDuration(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseSinceDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
