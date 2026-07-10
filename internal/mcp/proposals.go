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

// registerProposeListTool exposes cortex_propose_list — a READ-ONLY view of the
// proposer's candidate directives.
//
// This is deliberately the ONLY proposal tool over MCP. Scanning, accepting, and
// dismissing are CLI-only: accept is the sole path from a proposal to a directive
// write and stays human-gated at the operator's terminal, never reachable by an
// agent through MCP. Input schema is flat (no oneOf/anyOf/allOf) for OpenAI
// strict-mode clients.
func registerProposeListTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_propose_list",
		mcp.WithDescription("List candidate directive proposals the proposer derived from recurring session-ledger fix patterns. Read-only: proposals are inert until a human accepts one at the CLI (`cortex propose accept <id>`), which is the only path that writes a directive. Defaults to pending proposals; pass status='all', 'accepted', or 'dismissed' to widen."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("status",
			mcp.Description("Which proposals to return: pending (default), accepted, dismissed, or all."),
			mcp.Enum("pending", "accepted", "dismissed", "all"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("directive proposals require SQLiteStore"), nil
		}

		opts := store.ProposalListOpts{}
		if status, err := req.RequireString("status"); err == nil && strings.TrimSpace(status) != "" {
			opts.Status = status
		}

		proposals, err := sqlStore.ListProposals(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list proposals error: %v", err)), nil
		}
		if proposals == nil {
			proposals = []*store.DirectiveProposal{}
		}
		data, _ := json.MarshalIndent(proposals, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}
