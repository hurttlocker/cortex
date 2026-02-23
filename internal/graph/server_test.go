package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	resp, _ := http.Get(ts.URL + "/api/graph?fact_id=1&depth=99")
	defer resp.Body.Close()

	var result ExportResult
	json.NewDecoder(resp.Body).Decode(&result)

	// Depth should be capped at 5
	if result.Meta["depth"] != float64(5) {
		t.Fatalf("expected depth capped at 5, got %v", result.Meta["depth"])
	}
}

// removed itoa — using fmt.Sprintf instead
