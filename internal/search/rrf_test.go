package search

import (
	"context"
	"math"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestRRFBasicFusion(t *testing.T) {
	bm25 := []Result{
		{MemoryID: 1, Score: 0.9, Content: "bm25-1"},
		{MemoryID: 2, Score: 0.8, Content: "bm25-2"},
		{MemoryID: 3, Score: 0.7, Content: "bm25-3"},
	}
	semantic := []Result{
		{MemoryID: 2, Score: 0.95, Content: "semantic-2"},
		{MemoryID: 1, Score: 0.92, Content: "semantic-1"},
		{MemoryID: 4, Score: 0.90, Content: "semantic-4"},
	}

	got := FuseRRF(bm25, semantic, DefaultRRFConfig())
	if len(got) != 4 {
		t.Fatalf("expected 4 fused results, got %d", len(got))
	}

	wantOrder := []int64{1, 2, 3, 4}
	for i, wantID := range wantOrder {
		if got[i].MemoryID != wantID {
			t.Fatalf("rank %d: got memory_id=%d want=%d", i+1, got[i].MemoryID, wantID)
		}
		if got[i].MatchType != "rrf" {
			t.Fatalf("expected match_type rrf, got %q", got[i].MatchType)
		}
	}
}

func TestRRFDisjointLists(t *testing.T) {
	bm25 := []Result{
		{MemoryID: 10, Score: 1.0},
		{MemoryID: 11, Score: 0.9},
	}
	semantic := []Result{
		{MemoryID: 20, Score: 0.98},
	}

	got := FuseRRF(bm25, semantic, DefaultRRFConfig())
	if len(got) != 3 {
		t.Fatalf("expected 3 fused results, got %d", len(got))
	}

	scores := map[int64]float64{}
	for _, r := range got {
		scores[r.MemoryID] = r.Score
	}
	if scores[10] <= scores[11] {
		t.Fatalf("expected higher BM25 rank to score higher for disjoint list: %.8f <= %.8f", scores[10], scores[11])
	}
	if scores[20] <= scores[11] {
		t.Fatalf("expected semantic-only top rank to outrank lower BM25 rank: %.8f <= %.8f", scores[20], scores[11])
	}
}

func TestRRFSingleList(t *testing.T) {
	bm25 := []Result{
		{MemoryID: 1, Score: 1.0},
		{MemoryID: 2, Score: 0.8},
	}

	got := FuseRRF(bm25, nil, DefaultRRFConfig())
	if len(got) != 2 {
		t.Fatalf("expected 2 fused results, got %d", len(got))
	}
	if got[0].MemoryID != 1 || got[1].MemoryID != 2 {
		t.Fatalf("unexpected ranking for single-list fusion: %+v", []int64{got[0].MemoryID, got[1].MemoryID})
	}
	for _, r := range got {
		if r.Score <= 0 {
			t.Fatalf("expected positive RRF score for single-list result, got %.8f", r.Score)
		}
	}
}

func TestRRFKParameter(t *testing.T) {
	const total = 100

	bm25 := make([]Result, 0, total)
	semantic := make([]Result, 0, total)

	bm25Filler := int64(1000)
	semanticFiller := int64(2000)
	for rank := 1; rank <= total; rank++ {
		switch rank {
		case 1:
			bm25 = append(bm25, Result{MemoryID: 1, Score: float64(total - rank)})
		case 10:
			bm25 = append(bm25, Result{MemoryID: 2, Score: float64(total - rank)})
		default:
			bm25 = append(bm25, Result{MemoryID: bm25Filler, Score: float64(total - rank)})
			bm25Filler++
		}
	}

	for rank := 1; rank <= total; rank++ {
		switch rank {
		case 10:
			semantic = append(semantic, Result{MemoryID: 2, Score: float64(total - rank)})
		case 100:
			semantic = append(semantic, Result{MemoryID: 1, Score: float64(total - rank)})
		default:
			semantic = append(semantic, Result{MemoryID: semanticFiller, Score: float64(total - rank)})
			semanticFiller++
		}
	}

	lowK := FuseRRF(bm25, semantic, RRFConfig{K: 1, BM25Weight: 1, SemanticWeight: 1})
	highK := FuseRRF(bm25, semantic, RRFConfig{K: 60, BM25Weight: 1, SemanticWeight: 1})

	lowKPos := positionsByID(lowK)
	highKPos := positionsByID(highK)

	if lowKPos[1] >= lowKPos[2] {
		t.Fatalf("expected memory 1 to outrank memory 2 at low K, got pos1=%d pos2=%d", lowKPos[1], lowKPos[2])
	}
	if highKPos[2] >= highKPos[1] {
		t.Fatalf("expected memory 2 to outrank memory 1 at high K, got pos1=%d pos2=%d", highKPos[1], highKPos[2])
	}
}

func TestRRFConfidenceDecay(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id1, err := s.AddMemory(ctx, &store.Memory{Content: "high confidence memory", SourceFile: "a.md"})
	if err != nil {
		t.Fatalf("add memory 1: %v", err)
	}
	id2, err := s.AddMemory(ctx, &store.Memory{Content: "low confidence memory", SourceFile: "b.md"})
	if err != nil {
		t.Fatalf("add memory 2: %v", err)
	}

	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   id1,
		Subject:    "svc",
		Predicate:  "status",
		Object:     "stable",
		FactType:   "identity",
		Confidence: 1.0,
		DecayRate:  0.001,
	}); err != nil {
		t.Fatalf("add fact 1: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   id2,
		Subject:    "svc",
		Predicate:  "status",
		Object:     "degraded",
		FactType:   "temporal",
		Confidence: 0.2,
		DecayRate:  0.1,
	}); err != nil {
		t.Fatalf("add fact 2: %v", err)
	}

	fused := FuseRRF(
		[]Result{{MemoryID: id1, Score: 1.0}, {MemoryID: id2, Score: 0.9}},
		[]Result{{MemoryID: id1, Score: 1.0}, {MemoryID: id2, Score: 0.9}},
		DefaultRRFConfig(),
	)

	before := scoresByID(fused)
	engine := NewEngine(s)
	after, _ := engine.applyConfidenceDecay(ctx, fused, false, false)
	afterScores := scoresByID(after)

	if afterScores[id2] >= before[id2] {
		t.Fatalf("expected low-confidence memory score to decrease after confidence decay, before=%.8f after=%.8f", before[id2], afterScores[id2])
	}
	if afterScores[id1] <= afterScores[id2] {
		t.Fatalf("expected high-confidence memory to outrank low-confidence memory after decay, high=%.8f low=%.8f", afterScores[id1], afterScores[id2])
	}
}

func TestRRFDeterministic(t *testing.T) {
	bm25 := []Result{
		{MemoryID: 1, Score: 0.9},
		{MemoryID: 2, Score: 0.8},
		{MemoryID: 3, Score: 0.7},
	}
	semantic := []Result{
		{MemoryID: 3, Score: 0.99},
		{MemoryID: 1, Score: 0.95},
		{MemoryID: 4, Score: 0.92},
	}

	first := FuseRRF(bm25, semantic, DefaultRRFConfig())
	for i := 0; i < 10; i++ {
		next := FuseRRF(bm25, semantic, DefaultRRFConfig())
		if len(first) != len(next) {
			t.Fatalf("iteration %d: length mismatch %d != %d", i, len(first), len(next))
		}
		for j := range first {
			if first[j].MemoryID != next[j].MemoryID {
				t.Fatalf("iteration %d rank %d: memory_id mismatch %d != %d", i, j+1, first[j].MemoryID, next[j].MemoryID)
			}
			if math.Abs(first[j].Score-next[j].Score) > 1e-12 {
				t.Fatalf("iteration %d rank %d: score mismatch %.12f != %.12f", i, j+1, first[j].Score, next[j].Score)
			}
		}
	}
}

func TestSearchModeRRF_Integration(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)

	ctx := context.Background()
	embedder := newMockEmbedder()

	if err := s.AddEmbedding(ctx, 3, []float32{0.8, 0.2, 0.1}); err != nil {
		t.Fatalf("add embedding 3: %v", err)
	}
	if err := s.AddEmbedding(ctx, 10, []float32{0.7, 0.2, 0.2}); err != nil {
		t.Fatalf("add embedding 10: %v", err)
	}
	embedder.embeddings["Go programming"] = []float32{0.75, 0.25, 0.15}

	engine := NewEngineWithEmbedder(s, embedder)

	hybrid, err := engine.Search(ctx, "Go programming", Options{Mode: ModeHybrid, Limit: 10})
	if err != nil {
		t.Fatalf("hybrid search failed: %v", err)
	}
	if len(hybrid) == 0 {
		t.Fatal("expected hybrid results")
	}

	rrf, err := engine.Search(ctx, "Go programming", Options{Mode: ModeRRF, Limit: 10})
	if err != nil {
		t.Fatalf("rrf search failed: %v", err)
	}
	if len(rrf) == 0 {
		t.Fatal("expected rrf results")
	}

	for _, r := range rrf {
		if r.MatchType != "rrf" {
			t.Fatalf("expected rrf match_type, got %q", r.MatchType)
		}
		if r.Score <= 0 || r.Score > 1 {
			t.Fatalf("expected RRF score in (0,1], got %.8f", r.Score)
		}
	}
}

func TestSearchModeRRF_FallbackWithoutEmbedder(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)

	engine := NewEngine(s)
	results, err := engine.Search(context.Background(), "Go", Options{Mode: ModeRRF, Limit: 10})
	if err != nil {
		t.Fatalf("rrf search without embedder should fall back to BM25 results: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one fallback result in rrf mode")
	}
	// When embedder is nil, RRF gracefully degrades to BM25 keyword search
	for _, r := range results {
		if r.MatchType != "bm25" {
			t.Fatalf("expected match_type bm25 in nil-embedder fallback, got %q", r.MatchType)
		}
	}
}

func positionsByID(results []Result) map[int64]int {
	positions := make(map[int64]int, len(results))
	for i, r := range results {
		positions[r.MemoryID] = i
	}
	return positions
}

func scoresByID(results []Result) map[int64]float64 {
	m := make(map[int64]float64, len(results))
	for _, r := range results {
		m[r.MemoryID] = r.Score
	}
	return m
}
