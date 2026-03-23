package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type mockScorer struct {
	scores []float64
	err    error
}

func (m mockScorer) Name() string    { return "mock" }
func (m mockScorer) Available() bool { return true }
func (m mockScorer) Close() error    { return nil }
func (m mockScorer) Score(ctx context.Context, query string, docs []string) ([]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	return append([]float64(nil), m.scores[:len(docs)]...), nil
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
	}{
		{"", ModeAuto},
		{"auto", ModeAuto},
		{"on", ModeOn},
		{"true", ModeOn},
		{"off", ModeOff},
		{"false", ModeOff},
	}
	for _, tc := range tests {
		got, err := ParseMode(tc.input)
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("ParseMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestServiceRerank_ResortsPrefixAndPreservesTail(t *testing.T) {
	service := NewService(mockScorer{scores: []float64{0.2, 0.9, 0.4}}, 3)
	candidates := []Candidate{
		{Index: 0, BaseScore: 0.91, Text: "alpha"},
		{Index: 1, BaseScore: 0.85, Text: "beta"},
		{Index: 2, BaseScore: 0.80, Text: "gamma"},
		{Index: 3, BaseScore: 0.50, Text: "delta"},
	}

	got, err := service.Rerank(context.Background(), "query", candidates, 0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != len(candidates) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(candidates))
	}

	wantOrder := []int{1, 2, 0, 3}
	for i, want := range wantOrder {
		if got[i].Index != want {
			t.Fatalf("rank %d = %d, want %d", i, got[i].Index, want)
		}
	}
}

func TestServiceRerank_SkipsWhenUnavailable(t *testing.T) {
	service := NewService(nil, 30)
	candidates := []Candidate{
		{Index: 0, BaseScore: 0.9, Text: "alpha"},
		{Index: 1, BaseScore: 0.8, Text: "beta"},
	}
	got, err := service.Rerank(context.Background(), "query", candidates, 1)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Index != 0 {
		t.Fatalf("got first index %d, want 0", got[0].Index)
	}
}

func TestServiceRerank_PropagatesScorerError(t *testing.T) {
	service := NewService(mockScorer{err: errors.New("boom")}, 30)
	_, err := service.Rerank(context.Background(), "query", []Candidate{
		{Index: 0, BaseScore: 0.9, Text: "alpha"},
		{Index: 1, BaseScore: 0.8, Text: "beta"},
	}, 0)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected scorer error, got %v", err)
	}
}

func TestONNXScorer_RealModelIfAvailable(t *testing.T) {
	spec := DefaultModelSpec()
	files, err := ResolveModelFiles(spec)
	if err != nil {
		t.Fatalf("ResolveModelFiles: %v", err)
	}
	if !ModelReady(files) {
		t.Skip("reranker model not downloaded")
	}
	if DetectORTLibraryPath() == "" {
		t.Skip("onnxruntime shared library not found")
	}

	scorer, err := NewONNXScorer(Config{
		Spec:              spec,
		Files:             files,
		LibraryPath:       DetectORTLibraryPath(),
		BatchSize:         2,
		MaxSequenceLength: spec.MaxSequenceLen,
	})
	if err != nil {
		t.Fatalf("NewONNXScorer: %v", err)
	}
	defer scorer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	scores, err := scorer.Score(ctx, "When is the product launch date?", []string{
		"The product launch date is April 21, 2026 according to the release calendar.",
		"This note explains how to cook pasta with garlic and olive oil.",
	})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores[0] <= scores[1] {
		t.Fatalf("expected relevant document to outrank irrelevant document, got %v", scores)
	}
}

func TestDaemonHandler_ReranksCandidatesByID(t *testing.T) {
	handler := NewDaemonHandler(mockScorer{scores: []float64{0.4, 1.2}})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := NewDaemonScorer(DaemonClientConfig{
		BaseURL: server.URL,
		Timeout: 2 * time.Second,
	})
	pinger, ok := client.(interface{ Ping(context.Context) error })
	if !ok {
		t.Fatal("expected daemon scorer to implement Ping")
	}
	if err := pinger.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	scores, err := client.Score(context.Background(), "query", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("len(scores) = %d, want 2", len(scores))
	}
	if scores[0] != 0.4 || scores[1] != 1.2 {
		t.Fatalf("unexpected daemon scores: %v", scores)
	}
}

func TestDaemonHandler_HealthIncludesModel(t *testing.T) {
	server := httptest.NewServer(NewDaemonHandler(mockScorer{scores: []float64{0.1}}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var payload DaemonHealth
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !payload.Available {
		t.Fatal("expected health endpoint to report available")
	}
	if payload.Model != "mock" {
		t.Fatalf("model = %q, want mock", payload.Model)
	}
}
