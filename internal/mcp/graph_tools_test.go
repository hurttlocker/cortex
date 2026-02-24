package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestMCPGraphExploreBySubject(t *testing.T) {
	s, srv, _ := setupGraphToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "graph_explore", map[string]interface{}{
		"subject": "cortex",
		"depth":   float64(2),
	})

	text := getTextContent(t, result)
	var payload graphExploreResult
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse graph_explore result: %v", err)
	}
	if payload.TotalFacts == 0 {
		t.Fatal("expected graph_explore to return facts")
	}
	if payload.Root != "cortex" {
		t.Fatalf("expected root cortex, got %q", payload.Root)
	}
}

func TestMCPGraphExploreByFactID(t *testing.T) {
	s, srv, _ := setupGraphToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "graph_explore", map[string]interface{}{
		"fact_id": float64(2),
		"depth":   float64(2),
	})

	text := getTextContent(t, result)
	var payload graphExploreResult
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse graph_explore result: %v", err)
	}
	if payload.TotalFacts == 0 {
		t.Fatal("expected graph_explore to return facts")
	}
	if payload.Root != "#2" {
		t.Fatalf("expected root #2, got %q", payload.Root)
	}
}

func TestMCPGraphExploreDepthLimit(t *testing.T) {
	s, srv, _ := setupGraphToolServer(t)
	defer s.Close()

	resultDepth1 := callTool(t, srv, "graph_explore", map[string]interface{}{
		"fact_id": float64(2),
		"depth":   float64(1),
	})
	resultDepth3 := callTool(t, srv, "graph_explore", map[string]interface{}{
		"fact_id": float64(2),
		"depth":   float64(3),
	})

	var d1 graphExploreResult
	var d3 graphExploreResult
	if err := json.Unmarshal([]byte(getTextContent(t, resultDepth1)), &d1); err != nil {
		t.Fatalf("parse depth1: %v", err)
	}
	if err := json.Unmarshal([]byte(getTextContent(t, resultDepth3)), &d3); err != nil {
		t.Fatalf("parse depth3: %v", err)
	}

	if d3.TotalFacts < d1.TotalFacts {
		t.Fatalf("expected depth3 facts >= depth1 facts, got %d < %d", d3.TotalFacts, d1.TotalFacts)
	}
}

func TestMCPGraphExploreSourceFilter(t *testing.T) {
	s, srv, _ := setupGraphToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "graph_explore", map[string]interface{}{
		"subject": "cortex",
		"depth":   float64(3),
		"source":  "readme",
	})

	var payload graphExploreResult
	if err := json.Unmarshal([]byte(getTextContent(t, result)), &payload); err != nil {
		t.Fatalf("parse graph_explore result: %v", err)
	}
	if payload.TotalFacts == 0 {
		t.Fatal("expected source-filtered results")
	}
	for _, fact := range payload.Facts {
		if !strings.HasPrefix(strings.ToLower(fact.Source), "readme") {
			t.Fatalf("expected source prefix readme, got %q", fact.Source)
		}
	}
}

func TestMCPGraphImpactBasic(t *testing.T) {
	s, srv, _ := setupGraphToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "graph_impact", map[string]interface{}{
		"subject": "cortex",
		"depth":   float64(3),
	})

	var payload graphImpactResult
	if err := json.Unmarshal([]byte(getTextContent(t, result)), &payload); err != nil {
		t.Fatalf("parse graph_impact result: %v", err)
	}
	if payload.TotalFacts == 0 {
		t.Fatal("expected impact analysis to return facts")
	}
	if len(payload.Groups) == 0 {
		t.Fatal("expected grouped impact facts")
	}
	if payload.ConfidenceDistribution["total"] != payload.TotalFacts {
		t.Fatalf("confidence total mismatch: %d != %d", payload.ConfidenceDistribution["total"], payload.TotalFacts)
	}
}

func TestMCPGraphImpactUnknownSubject(t *testing.T) {
	s, srv, _ := setupGraphToolServer(t)
	defer s.Close()

	result := callTool(t, srv, "graph_impact", map[string]interface{}{
		"subject": "subject-that-does-not-exist",
	})

	var payload graphImpactResult
	if err := json.Unmarshal([]byte(getTextContent(t, result)), &payload); err != nil {
		t.Fatalf("parse graph_impact result: %v", err)
	}
	if payload.TotalFacts != 0 {
		t.Fatalf("expected empty impact result for unknown subject, got %d", payload.TotalFacts)
	}
}

func TestMCPListClusters(t *testing.T) {
	s, srv, sqlStore := setupGraphToolServer(t)
	defer s.Close()

	seedClusterTables(t, sqlStore)

	result := callTool(t, srv, "list_clusters", map[string]interface{}{})
	text := getTextContent(t, result)

	var payload struct {
		Available bool                `json:"available"`
		Clusters  []mcpClusterSummary `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse list_clusters result: %v", err)
	}
	if !payload.Available {
		t.Fatal("expected clusters to be available")
	}
	if len(payload.Clusters) == 0 {
		t.Fatal("expected non-empty cluster list")
	}
}

func TestMCPListClustersNoTable(t *testing.T) {
	s, srv, sqlStore := setupGraphToolServer(t)
	defer s.Close()

	// Drop cluster tables to simulate pre-migration DB
	db := sqlStore.GetDB()
	db.ExecContext(context.Background(), "DROP TABLE IF EXISTS fact_clusters")
	db.ExecContext(context.Background(), "DROP TABLE IF EXISTS clusters")

	result := callTool(t, srv, "list_clusters", map[string]interface{}{})
	text := getTextContent(t, result)

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse list_clusters result: %v", err)
	}
	if available, ok := payload["available"].(bool); !ok || available {
		t.Fatalf("expected available=false when tables missing, got %v", payload["available"])
	}
}

func TestMCPResourceSubjects(t *testing.T) {
	s, srv, _ := setupGraphToolServer(t)
	defer s.Close()

	text := callResource(t, srv, "cortex://graph/subjects")
	var payload struct {
		Subjects []struct {
			Subject string `json:"subject"`
		} `json:"subjects"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse subjects resource: %v", err)
	}
	if payload.Count == 0 || len(payload.Subjects) == 0 {
		t.Fatal("expected non-empty subjects resource")
	}
	foundCortex := false
	for _, item := range payload.Subjects {
		if strings.EqualFold(item.Subject, "cortex") {
			foundCortex = true
			break
		}
	}
	if !foundCortex {
		t.Fatal("expected cortex subject in resource")
	}
}

func setupGraphToolServer(t *testing.T) (store.Store, *server.MCPServer, *store.SQLiteStore) {
	t.Helper()

	s := setupTestStore(t)
	sqlStore := s.(*store.SQLiteStore)
	ctx := context.Background()

	// Add a second cortex fact for subject-based seed coverage.
	f4, err := s.AddFact(ctx, &store.Fact{
		MemoryID:       2,
		Subject:        "cortex",
		Predicate:      "uses tool",
		Object:         "hnsw",
		FactType:       "relationship",
		Confidence:     0.9,
		DecayRate:      0.01,
		LastReinforced: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("add extra cortex fact: %v", err)
	}

	// Build a simple chain 2 -> 3 -> 1 and 4 -> 2 for depth tests.
	_ = sqlStore.AddEdge(ctx, &store.FactEdge{SourceFactID: 2, TargetFactID: 3, EdgeType: store.EdgeTypeRelatesTo, Confidence: 0.85, Source: store.EdgeSourceExplicit})
	_ = sqlStore.AddEdge(ctx, &store.FactEdge{SourceFactID: 3, TargetFactID: 1, EdgeType: store.EdgeTypeRelatesTo, Confidence: 0.8, Source: store.EdgeSourceExplicit})
	_ = sqlStore.AddEdge(ctx, &store.FactEdge{SourceFactID: f4, TargetFactID: 2, EdgeType: store.EdgeTypeSupports, Confidence: 0.9, Source: store.EdgeSourceExplicit})

	srv := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})
	return s, srv, sqlStore
}

func seedClusterTables(t *testing.T, sqlStore *store.SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	db := sqlStore.GetDB()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS clusters (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			aliases TEXT DEFAULT '[]',
			cohesion REAL DEFAULT 0.0,
			fact_count INTEGER DEFAULT 0,
			avg_confidence REAL DEFAULT 0.0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS fact_clusters (
			fact_id INTEGER NOT NULL,
			cluster_id INTEGER NOT NULL,
			relevance REAL DEFAULT 1.0,
			PRIMARY KEY (fact_id, cluster_id),
			FOREIGN KEY (fact_id) REFERENCES facts(id) ON DELETE CASCADE,
			FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed cluster schema: %v", err)
		}
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO clusters (id, name, aliases, cohesion, fact_count, avg_confidence)
		 VALUES (1, 'cortex', '["memory","graph"]', 0.81, 2, 0.92)`,
	); err != nil {
		t.Fatalf("seed cluster row: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO fact_clusters (fact_id, cluster_id, relevance)
		 VALUES (2, 1, 1.0), (4, 1, 1.0)`,
	); err != nil {
		t.Fatalf("seed fact_clusters: %v", err)
	}
}

func callResource(t *testing.T, srv *server.MCPServer, uri string) string {
	t.Helper()

	result := srv.HandleMessage(context.Background(), mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params": map[string]interface{}{
			"uri": uri,
		},
	}))

	respBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var resp struct {
		Result struct {
			Contents []struct {
				Text string `json:"text"`
			} `json:"contents"`
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
	if len(resp.Result.Contents) == 0 {
		t.Fatalf("no resource contents for %s", uri)
	}
	return resp.Result.Contents[0].Text
}

func TestCallResourceHelperCompiles(t *testing.T) {
	// Sanity test to ensure helper types stay linked with mcp-go response shapes.
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})
	text := callResource(t, mcpServer, "cortex://stats")
	if text == "" {
		t.Fatal("expected non-empty resource text")
	}
}

func TestGraphExploreToolSchemaRegistered(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})

	// This is intentionally shallow: ensures the tool name is callable without transport wiring.
	result := callTool(t, mcpServer, "graph_explore", map[string]interface{}{"fact_id": float64(1)})
	if result.IsError {
		t.Fatal("expected graph_explore tool to be registered")
	}
}

func TestGraphImpactToolSchemaRegistered(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})

	result := callTool(t, mcpServer, "graph_impact", map[string]interface{}{"subject": "cortex"})
	if result.IsError {
		t.Fatal("expected graph_impact tool to be registered")
	}
}

func TestClusterResourceNoTable(t *testing.T) {
	s, srv, sqlStore := setupGraphToolServer(t)
	defer s.Close()

	// Drop cluster tables to simulate pre-migration DB
	db := sqlStore.GetDB()
	db.ExecContext(context.Background(), "DROP TABLE IF EXISTS fact_clusters")
	db.ExecContext(context.Background(), "DROP TABLE IF EXISTS clusters")

	text := callResource(t, srv, "cortex://graph/clusters")
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse cluster resource payload: %v", err)
	}
	if available, ok := payload["available"].(bool); !ok || available {
		t.Fatalf("expected available=false when cluster tables missing, got %v", payload["available"])
	}
}

func TestClusterResourceWithTable(t *testing.T) {
	s, srv, sqlStore := setupGraphToolServer(t)
	defer s.Close()

	seedClusterTables(t, sqlStore)
	text := callResource(t, srv, "cortex://graph/clusters")
	var payload struct {
		Available bool                `json:"available"`
		Clusters  []mcpClusterSummary `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parse cluster resource payload: %v", err)
	}
	if !payload.Available {
		t.Fatal("expected available=true")
	}
	if len(payload.Clusters) == 0 {
		t.Fatal("expected resource clusters")
	}
}

func TestGraphExploreValidation(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})

	result := callTool(t, mcpServer, "graph_explore", map[string]interface{}{})
	if !result.IsError {
		t.Fatal("expected validation error when no subject/fact_id")
	}
	text := getTextContent(t, result)
	if !strings.Contains(strings.ToLower(text), "subject") && !strings.Contains(strings.ToLower(text), "fact_id") {
		t.Fatalf("expected validation error text, got: %s", text)
	}
}

func TestGraphImpactValidation(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})

	result := callTool(t, mcpServer, "graph_impact", map[string]interface{}{})
	if !result.IsError {
		t.Fatal("expected validation error when subject missing")
	}
}

func TestListClustersValidation(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})

	result := callTool(t, mcpServer, "list_clusters", map[string]interface{}{"limit": float64(9999)})
	if result.IsError {
		t.Fatal("expected list_clusters to clamp oversized limit without error")
	}
}

func TestCallResourceEnvelopeShape(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})

	result := mcpServer.HandleMessage(context.Background(), mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params": map[string]interface{}{
			"uri": "cortex://stats",
		},
	}))
	if result == nil {
		t.Fatal("expected non-nil resources/read response")
	}
}

func TestCallToolEnvelopeShape(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()
	mcpServer := NewServer(ServerConfig{Store: s, DBPath: ":memory:", Version: "test"})

	result := mcpServer.HandleMessage(context.Background(), mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "cortex_search",
			"arguments": map[string]interface{}{"query": "cortex"},
		},
	}))
	if result == nil {
		t.Fatal("expected non-nil tools/call response")
	}
}

func TestGraphExploreMCPTypes(t *testing.T) {
	// Compile-time guard against accidental local type drift.
	var _ = mcplib.CallToolResult{}
}
