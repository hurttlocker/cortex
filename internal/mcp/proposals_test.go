package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/mark3labs/mcp-go/server"
)

func setupProposalToolServer(t *testing.T) (*store.SQLiteStore, *server.MCPServer) {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}
	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})
	return s.(*store.SQLiteStore), srv
}

// listToolNames drives the real tools/list JSON-RPC path and returns the set of
// registered tool names.
func listToolNames(t *testing.T, srv *server.MCPServer) map[string]bool {
	t.Helper()
	raw := srv.HandleMessage(context.Background(), mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	}))
	respBytes, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal tools/list: %v\nraw: %s", err, string(respBytes))
	}
	names := make(map[string]bool)
	for _, tool := range resp.Result.Tools {
		names[tool.Name] = true
	}
	return names
}

// TestMCPProposeList_ReadOnlyBoundary proves the MCP surface exposes ONLY the
// read-only cortex_propose_list — accept/dismiss/scan are never reachable over
// MCP, keeping the sole directive-writing path (accept) human-gated at the CLI.
func TestMCPProposeList_ReadOnlyBoundary(t *testing.T) {
	_, srv := setupProposalToolServer(t)

	names := listToolNames(t, srv)
	if !names["cortex_propose_list"] {
		t.Fatal("expected cortex_propose_list to be registered")
	}
	for _, forbidden := range []string{"cortex_propose_accept", "cortex_propose_dismiss", "cortex_propose_scan", "cortex_propose_create"} {
		if names[forbidden] {
			t.Fatalf("GOVERNANCE VIOLATION: %s must NOT be exposed over MCP", forbidden)
		}
	}
}

// TestMCPProposeList_ReturnsProposalsThroughRealHandler seeds a proposal in the
// store and reads it back through the real cortex_propose_list handler.
func TestMCPProposeList_ReturnsProposalsThroughRealHandler(t *testing.T) {
	s, srv := setupProposalToolServer(t)
	ctx := context.Background()

	if _, err := s.CreateProposal(ctx, &store.DirectiveProposal{
		CandidateRule: "Recurring fix pattern: add nil check",
		PatternKey:    "add nil check",
		Occurrences:   3,
		WindowStart:   time.Now().UTC().Add(-time.Hour),
		WindowEnd:     time.Now().UTC(),
		Evidence:      []int64{1, 2, 3},
	}); err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}

	result := callTool(t, srv, "cortex_propose_list", map[string]interface{}{})
	text := getTextContent(t, result)

	var proposals []store.DirectiveProposal
	if err := json.Unmarshal([]byte(text), &proposals); err != nil {
		t.Fatalf("parse cortex_propose_list result: %v\nraw: %s", err, text)
	}
	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal through the real MCP handler, got %d", len(proposals))
	}
	if proposals[0].PatternKey != "add nil check" || proposals[0].Status != store.ProposalStatusPending {
		t.Fatalf("unexpected proposal returned: %+v", proposals[0])
	}
	if proposals[0].Occurrences != 3 {
		t.Fatalf("expected occurrences=3, got %d", proposals[0].Occurrences)
	}
}
