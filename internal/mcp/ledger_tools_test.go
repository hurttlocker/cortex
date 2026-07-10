package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/mark3labs/mcp-go/server"
)

func setupLedgerToolServer(t *testing.T) (store.Store, *server.MCPServer) {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}
	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})
	return s, srv
}

func TestMCPLedgerRecord_PersistsThroughRealHandler(t *testing.T) {
	s, srv := setupLedgerToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "cortex_ledger_record", map[string]interface{}{
		"task_summary":  "Fixed race in worktree merge",
		"outcome":       "success",
		"files_touched": []interface{}{"internal/lane/worktree_side_merge.go"},
		"fix_pattern":   "serialize with keyed-promise-chain",
		"session_id":    "sess-42",
		"agent_id":      "codex",
		"project":       "cortex",
	})

	text := getTextContent(t, result)
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse cortex_ledger_record result: %v", err)
	}
	if payload["outcome"] != "success" {
		t.Fatalf("expected outcome success in response, got %v", payload["outcome"])
	}

	sqlStore := s.(*store.SQLiteStore)
	entries, err := sqlStore.ListLedgerEntries(context.Background(), time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("ListLedgerEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 persisted ledger row through the real MCP handler, got %d", len(entries))
	}
	got := entries[0]
	if got.TaskSummary != "Fixed race in worktree merge" {
		t.Errorf("TaskSummary = %q", got.TaskSummary)
	}
	if got.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", got.Outcome)
	}
	if len(got.FilesTouched) != 1 || got.FilesTouched[0] != "internal/lane/worktree_side_merge.go" {
		t.Errorf("FilesTouched = %v", got.FilesTouched)
	}
	if got.FixPattern != "serialize with keyed-promise-chain" {
		t.Errorf("FixPattern = %q", got.FixPattern)
	}
	if got.SessionID != "sess-42" {
		t.Errorf("SessionID = %q", got.SessionID)
	}
	if got.AgentID != "codex" {
		t.Errorf("AgentID = %q", got.AgentID)
	}
	if got.Project != "cortex" {
		t.Errorf("Project = %q", got.Project)
	}
}

func TestMCPLedgerRecord_RejectsInvalidOutcomeThroughRealHandler(t *testing.T) {
	s, srv := setupLedgerToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "cortex_ledger_record", map[string]interface{}{
		"task_summary": "some task",
		"outcome":      "bogus-outcome",
	})

	if !result.IsError {
		t.Fatal("expected an error result for invalid outcome, got success")
	}

	sqlStore := s.(*store.SQLiteStore)
	entries, err := sqlStore.ListLedgerEntries(context.Background(), time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("ListLedgerEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no rows persisted for a rejected outcome, got %d", len(entries))
	}
}

func TestMCPLedgerRecord_RequiresTaskSummary(t *testing.T) {
	s, srv := setupLedgerToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "cortex_ledger_record", map[string]interface{}{
		"outcome": "success",
	})

	if !result.IsError {
		t.Fatal("expected an error result for missing task_summary, got success")
	}
}
