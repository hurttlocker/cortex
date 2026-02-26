package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func newTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}
	return s.(*store.SQLiteStore)
}

func TestVisualizerHTML(t *testing.T) {
	data, err := visualizerFS.ReadFile("visualizer.html")
	if err != nil {
		t.Fatalf("visualizer.html not embedded: %v", err)
	}
	if len(data) < 1000 {
		t.Fatalf("visualizer.html too small: %d bytes", len(data))
	}
	if string(data[:15]) != "<!DOCTYPE html>" {
		t.Fatal("visualizer.html doesn't start with DOCTYPE")
	}
}

func TestVisualizerImpactModeWiring(t *testing.T) {
	data, err := visualizerFS.ReadFile("visualizer.html")
	if err != nil {
		t.Fatalf("visualizer.html not embedded: %v", err)
	}
	html := string(data)
	if !strings.Contains(html, "modeImpactBtn") {
		t.Fatal("expected impact mode button in visualizer")
	}
	if !strings.Contains(html, "/api/impact") {
		t.Fatal("expected visualizer to call /api/impact")
	}
	if !strings.Contains(html, "impactConcentric") {
		t.Fatal("expected impact concentric layout toggle in visualizer")
	}
}

func TestGraphAPIEndpoint(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		handleGraphAPI(w, r, st)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Missing fact_id
	resp, err := http.Get(ts.URL + "/api/graph")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing fact_id, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Invalid fact_id
	resp, err = http.Get(ts.URL + "/api/graph?fact_id=abc")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid fact_id, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid request (no data — empty graph)
	resp, err = http.Get(ts.URL + "/api/graph?fact_id=1&depth=2")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	var result ExportResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if result.Meta["root_fact_id"] != float64(1) {
		t.Fatalf("expected root_fact_id 1, got %v", result.Meta["root_fact_id"])
	}
	if result.Meta["depth"] != float64(2) {
		t.Fatalf("expected depth 2, got %v", result.Meta["depth"])
	}
}

func TestGraphAPIWithData(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()

	// Add a memory first (facts need a parent)
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "test content about cortex",
		SourceFile:    "test.md",
		SourceLine:    1,
		SourceSection: "test",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	// Add some facts
	fact1 := &store.Fact{MemoryID: memID, Subject: "cortex", Predicate: "language", Object: "Go", Confidence: 0.9, FactType: "kv"}
	fact2 := &store.Fact{MemoryID: memID, Subject: "cortex", Predicate: "database", Object: "SQLite", Confidence: 0.85, FactType: "kv"}

	id1, err := st.AddFact(ctx, fact1)
	if err != nil {
		t.Fatalf("add fact1: %v", err)
	}
	fact1.ID = id1

	id2, err := st.AddFact(ctx, fact2)
	if err != nil {
		t.Fatalf("add fact2: %v", err)
	}
	fact2.ID = id2

	// Add an edge
	err = st.AddEdge(ctx, &store.FactEdge{
		SourceFactID: fact1.ID,
		TargetFactID: fact2.ID,
		EdgeType:     store.EdgeTypeRelatesTo,
		Confidence:   0.8,
		Source:       store.EdgeSourceExplicit,
	})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		handleGraphAPI(w, r, st)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + fmt.Sprintf("/api/graph?fact_id=%d&depth=2", fact1.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result ExportResult
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Nodes) < 1 {
		t.Fatal("expected at least 1 node")
	}
	if result.Meta["total_nodes"].(float64) < 1 {
		t.Fatal("expected total_nodes >= 1")
	}
}

func TestGraphAPIDepthCap(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		handleGraphAPI(w, r, st)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/graph?fact_id=1&depth=99")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result ExportResult
	json.NewDecoder(resp.Body).Decode(&result)

	// Depth should be capped at 5
	if result.Meta["depth"] != float64(5) {
		t.Fatalf("expected depth capped at 5, got %v", result.Meta["depth"])
	}
}

func TestSearchAPIEndpoint(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()

	// Add a memory and facts
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "cortex uses Go and SQLite",
		SourceFile:    "test.md",
		SourceLine:    1,
		SourceSection: "test",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	_, err = st.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "cortex", Predicate: "language", Object: "Go", Confidence: 0.9, FactType: "kv"})
	if err != nil {
		t.Fatalf("add fact: %v", err)
	}

	_, err = st.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "cortex", Predicate: "database", Object: "SQLite", Confidence: 0.85, FactType: "kv"})
	if err != nil {
		t.Fatalf("add fact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		handleSearchAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Missing query
	resp, err := http.Get(ts.URL + "/api/search")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing q, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Search for "cortex"
	resp, err = http.Get(ts.URL + "/api/search?q=cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(result.Facts) != 2 {
		t.Fatalf("expected 2 facts matching 'cortex', got %d", len(result.Facts))
	}

	// Search for "SQLite" — should only match 1
	resp2, err := http.Get(ts.URL + "/api/search?q=SQLite")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	var result2 SearchResult
	json.NewDecoder(resp2.Body).Decode(&result2)

	if len(result2.Facts) != 1 {
		t.Fatalf("expected 1 fact matching 'SQLite', got %d", len(result2.Facts))
	}
	if result2.Facts[0].Object != "SQLite" {
		t.Fatalf("expected Object 'SQLite', got '%s'", result2.Facts[0].Object)
	}

	// Search with limit
	resp3, err := http.Get(ts.URL + "/api/search?q=cortex&limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()

	var result3 SearchResult
	json.NewDecoder(resp3.Body).Decode(&result3)

	if len(result3.Facts) != 1 {
		t.Fatalf("expected 1 fact with limit=1, got %d", len(result3.Facts))
	}
}

func TestSearchAPINoResults(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		handleSearchAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/search?q=nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result SearchResult
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Total != 0 {
		t.Fatalf("expected 0 results, got %d", result.Total)
	}
}

func TestFactsAPIBySubject(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "Cortex graph API facts endpoint test",
		SourceFile:    "graph.md",
		SourceLine:    1,
		SourceSection: "graph",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	if _, err := st.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Cortex",
		Predicate:  "language",
		Object:     "Go",
		Confidence: 0.9,
		FactType:   "identity",
	}); err != nil {
		t.Fatalf("add fact1: %v", err)
	}
	if _, err := st.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Cortex",
		Predicate:  "database",
		Object:     "SQLite",
		Confidence: 0.8,
		FactType:   "kv",
	}); err != nil {
		t.Fatalf("add fact2: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/facts", func(w http.ResponseWriter, r *http.Request) {
		handleFactsAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/facts?subject=cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out FactsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Total != 2 {
		t.Fatalf("expected 2 facts, got %d", out.Total)
	}
	for _, f := range out.Facts {
		if f.MemoryID != memID {
			t.Fatalf("expected memory_id %d, got %d", memID, f.MemoryID)
		}
		if !strings.Contains(f.Source, "graph.md") {
			t.Fatalf("expected source to include graph.md, got %q", f.Source)
		}
	}
}

func TestFactsAPIByMemoryID(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "Fact by memory id endpoint test",
		SourceFile:    "memory-id.md",
		SourceLine:    1,
		SourceSection: "memory",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	if _, err := st.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Memory",
		Predicate:  "kind",
		Object:     "test",
		Confidence: 0.9,
		FactType:   "kv",
	}); err != nil {
		t.Fatalf("add fact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/facts", func(w http.ResponseWriter, r *http.Request) {
		handleFactsAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + fmt.Sprintf("/api/facts?memory_id=%d", memID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out FactsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Total != 1 {
		t.Fatalf("expected 1 fact, got %d", out.Total)
	}
	if out.Facts[0].MemoryID != memID {
		t.Fatalf("expected memory_id %d, got %d", memID, out.Facts[0].MemoryID)
	}
}

func TestSearchAPIIncludesMatchedNodeIDs(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "Alice is the CEO of Acme Corp",
		SourceFile:    "acme.md",
		SourceLine:    1,
		SourceSection: "acme",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	if _, err := st.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Alice",
		Predicate:  "role",
		Object:     "CEO",
		Confidence: 0.95,
		FactType:   "identity",
	}); err != nil {
		t.Fatalf("add fact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		handleSearchAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/search?q=CEO")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Facts) == 0 {
		t.Fatal("expected at least one fact result")
	}
	if len(out.MatchedNodeIDs) == 0 {
		t.Fatal("expected matched_node_ids in search response")
	}
}

func TestGraphAPIBySubject(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "subject graph seed",
		SourceFile:    "subject.md",
		SourceLine:    1,
		SourceSection: "subject",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	id1, err := st.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Cortex",
		Predicate:  "language",
		Object:     "Go",
		Confidence: 0.95,
		FactType:   "kv",
	})
	if err != nil {
		t.Fatalf("add fact1: %v", err)
	}
	id2, err := st.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Cortex",
		Predicate:  "database",
		Object:     "SQLite",
		Confidence: 0.9,
		FactType:   "kv",
	})
	if err != nil {
		t.Fatalf("add fact2: %v", err)
	}

	if err := st.AddEdge(ctx, &store.FactEdge{
		SourceFactID: id1,
		TargetFactID: id2,
		EdgeType:     store.EdgeTypeRelatesTo,
		Confidence:   0.8,
		Source:       store.EdgeSourceInferred,
	}); err != nil {
		t.Fatalf("add edge: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		handleGraphAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/graph?subject=cortex")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result ExportResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Meta["mode"] != "subject" {
		t.Fatalf("expected mode=subject, got %v", result.Meta["mode"])
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 subject facts, got %d", len(result.Nodes))
	}
	if len(result.Edges) == 0 {
		t.Fatal("expected subject graph to include at least one edge")
	}
}

func TestGraphAPIBySubjectPaginationMetadata(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "subject graph pagination seed",
		SourceFile:    "subject-pagination.md",
		SourceLine:    1,
		SourceSection: "subject",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	for i := 0; i < 4; i++ {
		_, err := st.AddFact(ctx, &store.Fact{
			MemoryID:   memID,
			Subject:    "Cortex",
			Predicate:  fmt.Sprintf("p%d", i),
			Object:     fmt.Sprintf("o%d", i),
			Confidence: 0.9 - float64(i)*0.05,
			FactType:   "kv",
		})
		if err != nil {
			t.Fatalf("add fact %d: %v", i, err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		handleGraphAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/graph?subject=cortex&limit=2&offset=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out ExportResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(out.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(out.Nodes))
	}
	if out.Nodes[0].Rank != 2 {
		t.Fatalf("expected first node rank=2, got %d", out.Nodes[0].Rank)
	}
	if out.Nodes[0].Relevance != out.Nodes[0].Confidence {
		t.Fatalf("expected relevance to follow confidence, got relevance=%.3f confidence=%.3f", out.Nodes[0].Relevance, out.Nodes[0].Confidence)
	}
	if requireMetaInt(t, out.Meta, "limit") != 2 {
		t.Fatalf("expected meta limit=2, got %v", out.Meta["limit"])
	}
	if requireMetaInt(t, out.Meta, "offset") != 1 {
		t.Fatalf("expected meta offset=1, got %v", out.Meta["offset"])
	}
	if requireMetaInt(t, out.Meta, "total") != 4 {
		t.Fatalf("expected meta total=4, got %v", out.Meta["total"])
	}
	if requireMetaInt(t, out.Meta, "returned") != 2 {
		t.Fatalf("expected meta returned=2, got %v", out.Meta["returned"])
	}
	if !requireMetaBool(t, out.Meta, "has_more") {
		t.Fatal("expected has_more=true")
	}
}

func TestClusterAPILimitRespected(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "cluster seed",
		SourceFile:    "cluster.md",
		SourceLine:    1,
		SourceSection: "cluster",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	// Seed 3 subjects with 4 facts each so they satisfy cluster bounds.
	subjects := []string{"alpha", "beta", "gamma"}
	for _, subject := range subjects {
		for i := 0; i < 4; i++ {
			_, err := st.AddFact(ctx, &store.Fact{
				MemoryID:   memID,
				Subject:    subject,
				Predicate:  "p",
				Object:     fmt.Sprintf("o%d", i),
				Confidence: 0.7 + float64(i)*0.05,
				FactType:   "kv",
			})
			if err != nil {
				t.Fatalf("add fact %s/%d: %v", subject, i, err)
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster", func(w http.ResponseWriter, r *http.Request) {
		handleClusterAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cluster?limit=5")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result ExportResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode cluster result: %v", err)
	}
	if len(result.Nodes) > 5 {
		t.Fatalf("expected <= 5 nodes due limit, got %d", len(result.Nodes))
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected non-empty cluster data")
	}
	if result.Meta["mode"] != "cluster" {
		t.Fatalf("expected mode=cluster, got %v", result.Meta["mode"])
	}
}

func TestClusterAPIPaginationMetadata(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "cluster pagination seed",
		SourceFile:    "cluster-page.md",
		SourceLine:    1,
		SourceSection: "cluster",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	subjects := []string{"alpha", "beta", "gamma"}
	for _, subject := range subjects {
		for i := 0; i < 4; i++ {
			_, err := st.AddFact(ctx, &store.Fact{
				MemoryID:   memID,
				Subject:    subject,
				Predicate:  "mode",
				Object:     fmt.Sprintf("%s-%d", subject, i),
				Confidence: 0.95 - float64(i)*0.08,
				FactType:   "kv",
			})
			if err != nil {
				t.Fatalf("add fact %s/%d: %v", subject, i, err)
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster", func(w http.ResponseWriter, r *http.Request) {
		handleClusterAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cluster?limit=2&offset=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out ExportResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(out.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(out.Nodes))
	}
	if out.Nodes[0].Rank != 2 {
		t.Fatalf("expected first node rank=2, got %d", out.Nodes[0].Rank)
	}
	if requireMetaInt(t, out.Meta, "limit") != 2 || requireMetaInt(t, out.Meta, "offset") != 1 {
		t.Fatalf("unexpected pagination meta: %#v", out.Meta)
	}
	if requireMetaInt(t, out.Meta, "total") < 3 {
		t.Fatalf("expected total >= 3, got %v", out.Meta["total"])
	}
}

func TestClustersListAPI(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	seedClustersForGraphAPI(t, ctx, st)
	if _, err := st.RebuildClusters(ctx); err != nil {
		t.Fatalf("rebuild clusters: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/clusters", func(w http.ResponseWriter, r *http.Request) {
		handleClustersListAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/clusters?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out ClustersResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode clusters response: %v", err)
	}
	if len(out.Clusters) == 0 {
		t.Fatal("expected non-empty clusters list")
	}
	if out.TotalFacts <= 0 {
		t.Fatalf("expected total facts > 0, got %d", out.TotalFacts)
	}
	if out.Clusters[0].Name == "" {
		t.Fatal("expected cluster name")
	}
	if out.Clusters[0].Color == "" {
		t.Fatal("expected cluster color")
	}
}

func TestClustersListPaginationMetadata(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	seedClustersForGraphAPI(t, ctx, st)
	if _, err := st.RebuildClusters(ctx); err != nil {
		t.Fatalf("rebuild clusters: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/clusters", func(w http.ResponseWriter, r *http.Request) {
		handleClustersListAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/clusters?limit=1&offset=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out ClustersResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode clusters response: %v", err)
	}
	if len(out.Clusters) != 1 {
		t.Fatalf("expected 1 cluster due limit, got %d", len(out.Clusters))
	}
	if out.Clusters[0].Rank != 2 {
		t.Fatalf("expected cluster rank=2, got %d", out.Clusters[0].Rank)
	}
	if out.Clusters[0].Relevance <= 0 {
		t.Fatalf("expected positive cluster relevance, got %.3f", out.Clusters[0].Relevance)
	}
	if requireMetaInt(t, out.Meta, "limit") != 1 || requireMetaInt(t, out.Meta, "offset") != 1 {
		t.Fatalf("unexpected pagination meta: %#v", out.Meta)
	}
}

func TestClusterDetailAPI(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	seedClustersForGraphAPI(t, ctx, st)
	if _, err := st.RebuildClusters(ctx); err != nil {
		t.Fatalf("rebuild clusters: %v", err)
	}

	clusters, err := st.ListClusters(ctx)
	if err != nil {
		t.Fatalf("list clusters: %v", err)
	}
	if len(clusters) == 0 {
		t.Fatal("expected clusters")
	}
	clusterID := clusters[0].ID

	mux := http.NewServeMux()
	mux.HandleFunc("/api/clusters/", func(w http.ResponseWriter, r *http.Request) {
		handleClusterDetailAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + fmt.Sprintf("/api/clusters/%d", clusterID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out ClusterDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode cluster detail: %v", err)
	}
	if out.Cluster.ID != clusterID {
		t.Fatalf("expected cluster id %d, got %d", clusterID, out.Cluster.ID)
	}
	if len(out.Facts) == 0 {
		t.Fatal("expected detail facts")
	}
	if len(out.Nodes) == 0 {
		t.Fatal("expected detail nodes")
	}
	if out.Meta["mode"] != "cluster_detail" {
		t.Fatalf("expected mode=cluster_detail, got %v", out.Meta["mode"])
	}

	missing, err := http.Get(ts.URL + "/api/clusters/999999")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != 404 {
		t.Fatalf("expected 404 for missing cluster, got %d", missing.StatusCode)
	}
}

func TestClusterDetailPaginationMetadata(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	seedClustersForGraphAPI(t, ctx, st)
	if _, err := st.RebuildClusters(ctx); err != nil {
		t.Fatalf("rebuild clusters: %v", err)
	}

	clusters, err := st.ListClusters(ctx)
	if err != nil {
		t.Fatalf("list clusters: %v", err)
	}
	if len(clusters) == 0 {
		t.Fatal("expected clusters")
	}
	clusterID := clusters[0].ID

	mux := http.NewServeMux()
	mux.HandleFunc("/api/clusters/", func(w http.ResponseWriter, r *http.Request) {
		handleClusterDetailAPI(w, r, st)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + fmt.Sprintf("/api/clusters/%d?limit=2&offset=1", clusterID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out ClusterDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Nodes) == 0 {
		t.Fatal("expected paged cluster nodes")
	}
	if out.Nodes[0].Rank != 2 {
		t.Fatalf("expected first node rank=2, got %d", out.Nodes[0].Rank)
	}
	if requireMetaInt(t, out.Meta, "limit") != 2 || requireMetaInt(t, out.Meta, "offset") != 1 {
		t.Fatalf("unexpected pagination meta: %#v", out.Meta)
	}
	if requireMetaInt(t, out.Meta, "total") <= len(out.Nodes) {
		t.Fatalf("expected total to exceed returned nodes, meta=%#v", out.Meta)
	}
}

func seedClustersForGraphAPI(t *testing.T, ctx context.Context, st *store.SQLiteStore) {
	t.Helper()

	memTradingA, err := st.AddMemory(ctx, &store.Memory{
		Content:       "cluster api seed trading A",
		SourceFile:    "cluster-api.md",
		SourceLine:    1,
		SourceSection: "cluster",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	memTradingB, err := st.AddMemory(ctx, &store.Memory{
		Content:       "cluster api seed trading B",
		SourceFile:    "cluster-api.md",
		SourceLine:    2,
		SourceSection: "cluster",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	memSpear, err := st.AddMemory(ctx, &store.Memory{
		Content:       "cluster api seed spear",
		SourceFile:    "cluster-api.md",
		SourceLine:    3,
		SourceSection: "cluster",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	facts := []struct {
		memID      int64
		subject    string
		predicate  string
		object     string
		confidence float64
	}{
		{memTradingA, "trading", "strategy", "orb", 0.92},
		{memTradingA, "orb strategy", "runs_on", "alpaca", 0.86},
		{memTradingA, "alpaca", "mode", "paper", 0.8},
		{memTradingB, "trading", "uses", "options", 0.91},
		{memTradingB, "orb strategy", "entry", "breakout", 0.84},
		{memTradingB, "options", "symbol", "qqq", 0.77},
		{memSpear, "spear", "uses", "hha", 0.82},
		{memSpear, "paypal", "linked_to", "spear", 0.74},
		{memSpear, "rustdesk", "fleet", "managed", 0.76},
	}
	for i, f := range facts {
		if _, err := st.AddFact(ctx, &store.Fact{
			MemoryID:   f.memID,
			Subject:    f.subject,
			Predicate:  f.predicate,
			Object:     f.object,
			Confidence: f.confidence,
			FactType:   "kv",
		}); err != nil {
			t.Fatalf("add fact #%d: %v", i+1, err)
		}
	}
}
