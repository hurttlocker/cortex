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

// registerDirectiveAddTool exposes cortex_directive_add — create an explicit,
// human-authored governance rule. Input schema is flat (no oneOf/anyOf/allOf) so
// OpenAI strict-mode clients accept it.
func registerDirectiveAddTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_directive_add",
		mcp.WithDescription("Add an explicit governance directive — a human-authored rule ('always X', 'never Y') that is pinned above search results and never decays. Use for durable operating rules, not for one-off facts (use cortex_import for those)."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("rule",
			mcp.Required(),
			mcp.Description("The directive text, e.g. 'always run tests before committing'."),
		),
		mcp.WithString("scope",
			mcp.Description("Scope this directive applies to (e.g. a project name). Defaults to 'global' (applies everywhere)."),
		),
		mcp.WithString("author",
			mcp.Description("Who authored the directive (optional)."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		rule, err := req.RequireString("rule")
		if err != nil || strings.TrimSpace(rule) == "" {
			return mcp.NewToolResultError("rule is required"), nil
		}

		d := &store.Directive{Rule: rule}
		if scope, err := req.RequireString("scope"); err == nil && strings.TrimSpace(scope) != "" {
			d.Scope = scope
		}
		if author, err := req.RequireString("author"); err == nil && strings.TrimSpace(author) != "" {
			d.Author = author
		}

		id, err := st.AddDirective(ctx, d)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("add directive error: %v", err)), nil
		}

		created, err := st.GetDirective(ctx, id)
		if err != nil || created == nil {
			return mcp.NewToolResultError(fmt.Sprintf("directive %d created but could not be read back", id)), nil
		}
		data, _ := json.MarshalIndent(created, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

// registerDirectiveListTool exposes cortex_directive_list — list governance directives.
func registerDirectiveListTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_directive_list",
		mcp.WithDescription("List governance directives (human-authored rules). Defaults to active directives; pass status='all' or 'archived' to widen. Optionally filter by scope."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("status",
			mcp.Description("Which directives to return: active (default), archived, or all."),
			mcp.Enum("active", "archived", "all"),
		),
		mcp.WithString("scope",
			mcp.Description("Filter to directives with this exact scope (e.g. a project name). Empty = all scopes."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		opts := store.DirectiveListOpts{}
		if status, err := req.RequireString("status"); err == nil && strings.TrimSpace(status) != "" {
			opts.Status = status
		}
		if scope, err := req.RequireString("scope"); err == nil && strings.TrimSpace(scope) != "" {
			opts.Scope = scope
		}

		directives, err := st.ListDirectives(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list directives error: %v", err)), nil
		}
		if directives == nil {
			directives = []*store.Directive{}
		}
		data, _ := json.MarshalIndent(directives, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}
