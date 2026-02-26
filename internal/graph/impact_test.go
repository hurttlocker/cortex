package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func seedImpactTestData(t *testing.T, st *store.SQLiteStore) {
	t.Helper()
	ctx := context.Background()

	memID, err := st.AddMemory(ctx, &store.Memory{
		Content:       "impact test seed",
		SourceFile:    "impact.md",
		SourceLine:    1,
		SourceSection: "impact",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	addFact := func(f *store.Fact) int64 {
		id, err := st.AddFact(ctx, f)
		if err != nil {
			t.Fatalf("add fact %+v: %v", f, err)
		}
		return id
	}

	f1 := addFact(&store.Fact{MemoryID: memID, Subject: "trading", Predicate: "uses", Object: "alpaca", Confidence: 0.92, FactType: "kv"})
	f2 := addFact(&store.Fact{MemoryID: memID, Subject: "trading", Predicate: "runs on", Object: "public.com", Confidence: 0.82, FactType: "kv"})
	_ = f2
	_ = addFact(&store.Fact{MemoryID: memID, Subject: "trading", Predicate: "configured with", Object: "max-risk=2%", Confidence: 0.42, FactType: "kv"}) // low confidence
	f4 := addFact(&store.Fact{MemoryID: memID, Subject: "trading", Predicate: "works with", Object: "options", Confidence: 0.78, FactType: "kv"})
	f5 := addFact(&store.Fact{MemoryID: memID, Subject: "alpaca", Predicate: "provides", Object: "broker-api", Confidence: 0.88, FactType: "kv"})
	f6 := addFact(&store.Fact{MemoryID: memID, Subject: "broker-api", Predicate: "located at", Object: "us-east", Confidence: 0.73, FactType: "kv"})
	f7 := addFact(&store.Fact{MemoryID: memID, Subject: "infra", Predicate: "depends on", Object: "postgres", Confidence: 0.66, FactType: "kv"})
	f8 := addFact(&store.Fact{MemoryID: memID, Subject: "options", Predicate: "strategy", Object: "ORB", Confidence: 0.81, FactType: "kv"})

	addEdge := func(source, target int64, conf float64) {
		if err := st.AddEdge(ctx, &store.FactEdge{
			SourceFactID: source,
			TargetFactID: target,
			EdgeType:     store.EdgeTypeRelatesTo,
			Confidence:   conf,
			Source:       store.EdgeSourceInferred,
		}); err != nil {
			t.Fatalf("add edge %d->%d: %v", source, target, err)
		}
	}

	addEdge(f1, f5, 0.90)
	addEdge(f5, f6, 0.87)
	addEdge(f6, f7, 0.80)
	addEdge(f4, f8, 0.85)
}

func newImpactTestServer(st *store.SQLiteStore) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/impact", func(w http.ResponseWriter, r *http.Request) {
		handleImpactAPI(w, r, st)
	})
	return httptest.NewServer(mux)
}

func decodeImpactResponse(t *testing.T, resp *http.Response) ImpactResult {
	t.Helper()
	var out ImpactResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode impact response: %v", err)
	}
	return out
}

func TestImpactBasicSubject(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	ts := newImpactTestServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/impact?subject=trading")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	result := decodeImpactResponse(t, resp)
	if result.Subject != "trading" {
		t.Fatalf("expected subject trading, got %q", result.Subject)
	}
	if result.TotalFacts == 0 || len(result.Groups) == 0 {
		t.Fatalf("expected grouped impact facts, got total=%d groups=%d", result.TotalFacts, len(result.Groups))
	}
}

func TestImpactPaginationAndRankingMetadata(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	ts := newImpactTestServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/impact?subject=trading&limit=2&offset=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeImpactResponse(t, resp)
	if out.TotalFacts < 3 {
		t.Fatalf("expected unpaginated total facts >= 3, got %d", out.TotalFacts)
	}
	facts := make([]ImpactFact, 0)
	for _, g := range out.Groups {
		facts = append(facts, g.Facts...)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 paged facts, got %d", len(facts))
	}
	if facts[0].Rank != 2 {
		t.Fatalf("expected first paged fact rank=2, got %d", facts[0].Rank)
	}
	if facts[0].Relevance <= 0 {
		t.Fatalf("expected relevance score > 0, got %.3f", facts[0].Relevance)
	}

	if requireMetaInt(t, out.Meta, "limit") != 2 || requireMetaInt(t, out.Meta, "offset") != 1 {
		t.Fatalf("unexpected impact pagination meta: %#v", out.Meta)
	}
	if requireMetaInt(t, out.Meta, "returned") != 2 {
		t.Fatalf("expected returned=2, got %#v", out.Meta["returned"])
	}
}

func TestImpactDepthLimiting(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	ts := newImpactTestServer(st)
	defer ts.Close()

	resp1, err := http.Get(ts.URL + "/api/impact?subject=trading&depth=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != 200 {
		t.Fatalf("expected 200 for depth=1, got %d", resp1.StatusCode)
	}
	r1 := decodeImpactResponse(t, resp1)

	resp3, err := http.Get(ts.URL + "/api/impact?subject=trading&depth=3")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("expected 200 for depth=3, got %d", resp3.StatusCode)
	}
	r3 := decodeImpactResponse(t, resp3)

	if r3.TotalFacts <= r1.TotalFacts {
		t.Fatalf("expected depth=3 to return more facts than depth=1, got depth1=%d depth3=%d", r1.TotalFacts, r3.TotalFacts)
	}
}

func TestImpactConfidenceFilter(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	ts := newImpactTestServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/impact?subject=trading&min_confidence=0.5")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	result := decodeImpactResponse(t, resp)
	for _, g := range result.Groups {
		for _, f := range g.Facts {
			if f.Confidence < 0.5 {
				t.Fatalf("expected min_confidence filter to remove low confidence facts; got %.2f in group %q", f.Confidence, g.Relationship)
			}
		}
	}
}

func TestImpactPredicateGrouping(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	result, err := buildImpactResult(context.Background(), st, "trading", 3, 0.0)
	if err != nil {
		t.Fatalf("buildImpactResult: %v", err)
	}

	var hasToolCount int
	for _, g := range result.Groups {
		if g.Relationship == "has_tool" {
			hasToolCount = g.FactCount
		}
	}
	if hasToolCount < 2 {
		t.Fatalf("expected has_tool group to include similar predicates (uses, runs on), got count=%d", hasToolCount)
	}
}

func TestImpactEmptySubject(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	ts := newImpactTestServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/impact?subject=unknown-subject")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 for unknown subject, got %d", resp.StatusCode)
	}
}

func TestImpactConnectedSubjects(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	result, err := buildImpactResult(context.Background(), st, "trading", 3, 0.0)
	if err != nil {
		t.Fatalf("buildImpactResult: %v", err)
	}

	if len(result.ConnectedSubjects) == 0 {
		t.Fatal("expected connected subjects to be populated")
	}
	seen := map[string]bool{}
	for _, subj := range result.ConnectedSubjects {
		if strings.EqualFold(subj, "trading") {
			t.Fatalf("root subject should not appear in connected_subjects: %+v", result.ConnectedSubjects)
		}
		key := strings.ToLower(subj)
		if seen[key] {
			t.Fatalf("connected subjects should be deduplicated, got duplicate %q", subj)
		}
		seen[key] = true
	}
	if !seen["alpaca"] || !seen["options"] {
		t.Fatalf("expected key connected subjects to be present, got %+v", result.ConnectedSubjects)
	}
}

func TestImpactConcentric(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	seedImpactTestData(t, st)

	result, err := buildImpactResult(context.Background(), st, "trading", 3, 0.0)
	if err != nil {
		t.Fatalf("buildImpactResult: %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected nodes in impact result")
	}

	hasDepth0 := false
	hasDepth2Plus := false
	for _, n := range result.Nodes {
		if n.Depth == 0 {
			hasDepth0 = true
		}
		if n.Depth >= 2 {
			hasDepth2Plus = true
		}
	}
	if !hasDepth0 || !hasDepth2Plus {
		t.Fatalf("expected impact nodes to carry traversal depth metadata, depth0=%v depth2plus=%v", hasDepth0, hasDepth2Plus)
	}
}
