package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// helper: create a test store with some data
func setupTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}

	ctx := context.Background()

	// Add some test memories
	memories := []*store.Memory{
		{Content: "The wedding venue is Villa Rosa in Positano, Italy", SourceFile: "notes.md", ImportedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{Content: "Cortex is an open-source Go memory layer for AI agents", SourceFile: "readme.md", ImportedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{Content: "The Spear product handles HHA Exchange workflow augmentation", SourceFile: "business.md", ImportedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
	}

	for _, m := range memories {
		if _, err := s.AddMemory(ctx, m); err != nil {
			t.Fatalf("adding test memory: %v", err)
		}
	}

	// Add some test facts
	facts := []*store.Fact{
		{MemoryID: 1, Subject: "wedding", Predicate: "venue", Object: "Villa Rosa", FactType: "kv", Confidence: 0.9, DecayRate: 0.01, LastReinforced: time.Now().UTC()},
		{MemoryID: 2, Subject: "cortex", Predicate: "language", Object: "Go", FactType: "kv", Confidence: 0.95, DecayRate: 0.01, LastReinforced: time.Now().UTC()},
		{MemoryID: 3, Subject: "spear", Predicate: "domain", Object: "HHA Exchange", FactType: "kv", Confidence: 0.85, DecayRate: 0.01, LastReinforced: time.Now().UTC()},
	}

	for _, f := range facts {
		if _, err := s.AddFact(ctx, f); err != nil {
			t.Fatalf("adding test fact: %v", err)
		}
	}

	return s
}

func TestNewServer(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

// callTool is a helper that invokes an MCP tool by building a CallToolRequest.
func callTool(t *testing.T, srv *server.MCPServer, name string, args map[string]interface{}) *mcplib.CallToolResult {
	t.Helper()

	req := mcplib.CallToolRequest{}
	req.Method = "tools/call"
	req.Params.Name = name
	req.Params.Arguments = args

	result := srv.HandleMessage(context.Background(), mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      name,
			"arguments": args,
		},
	}))

	// Parse the JSON-RPC response
	respBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, string(respBytes))
	}

	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %d %s", resp.Error.Code, resp.Error.Message)
	}

	// Build a CallToolResult from the parsed response
	callResult := &mcplib.CallToolResult{
		IsError: resp.Result.IsError,
	}
	for _, c := range resp.Result.Content {
		if c.Type == "text" {
			callResult.Content = append(callResult.Content, mcplib.NewTextContent(c.Text))
		}
	}

	return callResult
}

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func getTextContent(t *testing.T, result *mcplib.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("no content in result")
	}
	// Get the text from the first TextContent
	for _, c := range result.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content found")
	return ""
}

func TestSearchTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})

	result := callTool(t, srv, "cortex_search", map[string]interface{}{
		"query": "wedding venue",
	})

	text := getTextContent(t, result)
	if text == "" {
		t.Fatal("empty search result")
	}

	var results []search.Result
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("parsing search results: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}

	// First result should mention Villa Rosa
	found := false
	for _, r := range results {
		if containsInsensitive(r.Content, "villa rosa") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected results to contain 'Villa Rosa', got: %v", results)
	}
}

func TestSearchToolWithMode(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})

	result := callTool(t, srv, "cortex_search", map[string]interface{}{
		"query": "open-source Go memory",
		"mode":  "bm25",
		"limit": float64(5),
	})

	text := getTextContent(t, result)
	var results []search.Result
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("parsing search results: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one BM25 result for 'open-source Go memory'")
	}
}

func TestImportTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})

	result := callTool(t, srv, "cortex_import", map[string]interface{}{
		"content": "This is a test memory imported via MCP",
		"source":  "test-mcp",
	})

	text := getTextContent(t, result)
	var importResult map[string]interface{}
	if err := json.Unmarshal([]byte(text), &importResult); err != nil {
		t.Fatalf("parsing import result: %v", err)
	}

	if importResult["ids"] == nil {
		t.Fatal("expected import result to have ids")
	}
	ids := importResult["ids"].([]interface{})
	if len(ids) == 0 {
		t.Fatal("expected at least one imported memory ID")
	}
	if importResult["message"] == nil || importResult["message"] == "" {
		t.Error("expected non-empty message")
	}

	// Verify we can search for it
	searchResult := callTool(t, srv, "cortex_search", map[string]interface{}{
		"query": "test memory imported via MCP",
	})

	searchText := getTextContent(t, searchResult)
	var searchResults []search.Result
	if err := json.Unmarshal([]byte(searchText), &searchResults); err != nil {
		t.Fatalf("parsing search results: %v", err)
	}

	if len(searchResults) == 0 {
		t.Fatal("imported memory should be searchable")
	}
}

func TestStatsTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})

	result := callTool(t, srv, "cortex_stats", map[string]interface{}{})

	text := getTextContent(t, result)
	var stats map[string]interface{}
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("parsing stats: %v", err)
	}

	memories := stats["memories"].(float64)
	if memories != 3 {
		t.Errorf("expected 3 memories, got %v", memories)
	}

	facts := stats["facts"].(float64)
	if facts != 3 {
		t.Errorf("expected 3 facts, got %v", facts)
	}
}

func TestFactsTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})

	result := callTool(t, srv, "cortex_facts", map[string]interface{}{
		"limit": float64(10),
	})

	text := getTextContent(t, result)
	var facts []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &facts); err != nil {
		t.Fatalf("parsing facts: %v", err)
	}

	if len(facts) != 3 {
		t.Errorf("expected 3 facts, got %d", len(facts))
	}
}

func TestFactsToolWithSubjectFilter(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})

	result := callTool(t, srv, "cortex_facts", map[string]interface{}{
		"subject": "wedding",
	})

	text := getTextContent(t, result)
	var facts []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &facts); err != nil {
		t.Fatalf("parsing facts: %v", err)
	}

	if len(facts) != 1 {
		t.Errorf("expected 1 wedding fact, got %d", len(facts))
	}
}

func TestStaleTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:"})

	// All facts were just reinforced, so none should be stale with defaults
	result := callTool(t, srv, "cortex_stale", map[string]interface{}{})

	text := getTextContent(t, result)
	var stale []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &stale); err != nil {
		t.Fatalf("parsing stale facts: %v", err)
	}

	// Newly created facts should not be stale
	if len(stale) != 0 {
		t.Errorf("expected 0 stale facts for fresh data, got %d", len(stale))
	}
}

func TestContainsInsensitive(t *testing.T) {
	tests := []struct {
		s, substr string
		want      bool
	}{
		{"Hello World", "hello", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"", "a", false},
	}

	for _, tt := range tests {
		got := containsInsensitive(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("containsInsensitive(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestReinforceTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	ctx := context.Background()

	mcpServer := NewServer(ServerConfig{
		Store:   s,
		DBPath:  ":memory:",
		Version: "test",
	})

	// Get a fact's last_reinforced before
	factBefore, _ := s.GetFact(ctx, 1)
	originalTime := factBefore.LastReinforced

	time.Sleep(10 * time.Millisecond)

	// Call reinforce tool
	result := callTool(t, mcpServer, "cortex_reinforce", map[string]interface{}{
		"fact_ids": "1,2",
	})

	text := getTextContent(t, result)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}

	reinforced := int(resp["reinforced"].(float64))
	if reinforced != 2 {
		t.Errorf("expected 2 reinforced, got %d", reinforced)
	}

	// Verify fact was actually reinforced
	factAfter, _ := s.GetFact(ctx, 1)
	if !factAfter.LastReinforced.After(originalTime) {
		t.Error("expected last_reinforced to be updated")
	}
}

func TestReinforceToolInvalidIDs(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	mcpServer := NewServer(ServerConfig{
		Store:   s,
		DBPath:  ":memory:",
		Version: "test",
	})

	// Invalid ID
	result := callTool(t, mcpServer, "cortex_reinforce", map[string]interface{}{
		"fact_ids": "abc,999999",
	})

	text := getTextContent(t, result)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}

	// Should have errors for both (abc is invalid format, 999999 not found)
	if resp["errors"] == nil {
		t.Error("expected errors for invalid/missing IDs")
	}
}

func TestWatchAddTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	mcpServer := NewServer(ServerConfig{
		Store:   s,
		DBPath:  ":memory:",
		Version: "test",
	})

	result := callTool(t, mcpServer, "cortex_watch_add", map[string]interface{}{
		"query":     "deployment failures",
		"threshold": 0.6,
	})

	text := getTextContent(t, result)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("parsing watch add response: %v", err)
	}

	if resp["id"] == nil || resp["id"].(float64) == 0 {
		t.Error("expected non-zero watch ID")
	}
	if resp["query"] != "deployment failures" {
		t.Errorf("expected query 'deployment failures', got %v", resp["query"])
	}
	if resp["active"] != true {
		t.Error("expected watch to be active")
	}
}

func TestWatchAddToolMissingQuery(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	mcpServer := NewServer(ServerConfig{
		Store:   s,
		DBPath:  ":memory:",
		Version: "test",
	})

	result := callTool(t, mcpServer, "cortex_watch_add", map[string]interface{}{})

	// Should return error
	if !result.IsError {
		t.Error("expected error for missing query")
	}
}

func TestWatchListTool(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	mcpServer := NewServer(ServerConfig{
		Store:   s,
		DBPath:  ":memory:",
		Version: "test",
	})

	// Empty list
	result := callTool(t, mcpServer, "cortex_watch_list", map[string]interface{}{})
	text := getTextContent(t, result)
	if text == "" {
		t.Error("expected non-empty response")
	}

	// Add a watch then list
	callTool(t, mcpServer, "cortex_watch_add", map[string]interface{}{
		"query": "test watch",
	})

	result = callTool(t, mcpServer, "cortex_watch_list", map[string]interface{}{})
	text = getTextContent(t, result)
	var watches []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &watches); err != nil {
		t.Fatalf("parsing watch list: %v", err)
	}
	if len(watches) != 1 {
		t.Errorf("expected 1 watch, got %d", len(watches))
	}
}

func TestImportTriggersWatchMatch(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	mcpServer := NewServer(ServerConfig{
		Store:   s,
		DBPath:  ":memory:",
		Version: "test",
	})

	// Create a watch for "cortex memory"
	callTool(t, mcpServer, "cortex_watch_add", map[string]interface{}{
		"query":     "cortex memory layer",
		"threshold": 0.5,
	})

	// Import content that matches
	result := callTool(t, mcpServer, "cortex_import", map[string]interface{}{
		"content": "Cortex is the best memory layer for AI agents. It handles memory storage and retrieval.",
		"source":  "test-match",
	})

	text := getTextContent(t, result)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("parsing import response: %v", err)
	}

	if resp["watches_triggered"] == nil {
		t.Error("expected watches_triggered in import response")
	} else if resp["watches_triggered"].(float64) < 1 {
		t.Error("expected at least 1 watch triggered")
	}
}

func TestReinforceToolEmptyIDs(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	mcpServer := NewServer(ServerConfig{
		Store:   s,
		DBPath:  ":memory:",
		Version: "test",
	})

	result := callTool(t, mcpServer, "cortex_reinforce", map[string]interface{}{
		"fact_ids": "",
	})

	text := getTextContent(t, result)
	// Empty fact_ids should return error
	if text == "" {
		t.Error("expected non-empty response for empty fact_ids")
	}
}
