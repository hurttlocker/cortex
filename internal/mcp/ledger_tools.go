package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerLedgerRecordTool exposes the end-of-task session ledger write — the
// implicit memory layer agents call when finishing a unit of work. This is
// the only ledger tool: rows are append-only, so there is no corresponding
// update/delete tool.
func registerLedgerRecordTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_ledger_record",
		mcp.WithDescription("Record a session outcome at the end of a task: what was done, whether it succeeded, which files changed, and any recurring fix pattern. Append-only — never updates or deletes prior entries. A later process scans recurring fix_pattern values to propose directives."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("task_summary",
			mcp.Required(),
			mcp.Description("Short summary of the task performed"),
		),
		mcp.WithString("outcome",
			mcp.Required(),
			mcp.Description("Result of the task"),
			mcp.Enum("success", "partial", "failure"),
		),
		mcp.WithArray("files_touched",
			mcp.Description("File paths changed during the task"),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithString("fix_pattern",
			mcp.Description("A recurring fix pattern this task exemplifies, if any (e.g. 'add missing nil check before X'). Leave empty if not applicable."),
		),
		mcp.WithString("session_id",
			mcp.Description("Session identifier for this task run"),
		),
		mcp.WithString("agent_id",
			mcp.Description("Agent that performed the task"),
		),
		mcp.WithString("project",
			mcp.Description("Project/repo this task was performed in"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("session ledger requires SQLiteStore"), nil
		}

		summary, err := req.RequireString("task_summary")
		if err != nil || strings.TrimSpace(summary) == "" {
			return mcp.NewToolResultError("task_summary is required"), nil
		}

		outcome, err := req.RequireString("outcome")
		if err != nil || strings.TrimSpace(outcome) == "" {
			return mcp.NewToolResultError("outcome is required"), nil
		}

		entry := &store.LedgerEntry{
			TaskSummary:  summary,
			Outcome:      outcome,
			FilesTouched: req.GetStringSlice("files_touched", nil),
		}
		if v, err := req.RequireString("fix_pattern"); err == nil {
			entry.FixPattern = v
		}
		if v, err := req.RequireString("session_id"); err == nil {
			entry.SessionID = v
		}
		if v, err := req.RequireString("agent_id"); err == nil {
			entry.AgentID = v
		}
		if v, err := req.RequireString("project"); err == nil {
			entry.Project = v
		}

		id, err := sqlStore.RecordLedgerEntry(ctx, entry)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("recording ledger entry: %v", err)), nil
		}

		result := map[string]interface{}{
			"id":      id,
			"outcome": entry.Outcome,
			"message": fmt.Sprintf("Recorded session ledger entry #%d (%s)", id, entry.Outcome),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}
