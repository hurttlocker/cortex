package ask

import (
	"context"
	"errors"
	"testing"

	"github.com/hurttlocker/cortex/internal/llm"
	"github.com/hurttlocker/cortex/internal/search"
)

type mockProvider struct {
	resp string
	err  error
}

func (m mockProvider) Complete(ctx context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.resp, nil
}

func (m mockProvider) Name() string { return "mock/test" }

func TestAsk_NoResultsReturnsNotEnoughEvidence(t *testing.T) {
	e := NewEngine(nil, "")
	res, err := e.Ask(context.Background(), Options{Question: "what?", Results: nil})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if res.Answer != "not enough evidence" {
		t.Fatalf("answer = %q, want not enough evidence", res.Answer)
	}
}

func TestAsk_DegradesWithoutLLM(t *testing.T) {
	e := NewEngine(nil, "")
	res, err := e.Ask(context.Background(), Options{
		Question: "what?",
		Results: []search.Result{{
			MemoryID:      1,
			SourceFile:    "doc.md",
			SourceSection: "prefs",
			Score:         0.9,
			Content:       "Q prefers green",
			FactIDs:       []int64{11, 12},
		}},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if !res.Degraded || res.Reason != "no_llm_configured" {
		t.Fatalf("expected no_llm_configured degrade, got degraded=%v reason=%q", res.Degraded, res.Reason)
	}
	if res.Error != "" {
		t.Fatalf("expected empty error for no_llm_configured, got %q", res.Error)
	}
	if len(res.Citations) != 1 || len(res.Citations[0].Facts) != 2 {
		t.Fatalf("expected structured fallback citations, got %+v", res.Citations)
	}
}

func TestAsk_SuccessWithValidCitations(t *testing.T) {
	e := NewEngine(mockProvider{
		resp: "Q prefers green for additions [1]. Q prefers blue for deletions [2].",
	}, "google/gemini-2.5-flash-lite")
	res, err := e.Ask(context.Background(), Options{
		Question: "What are Q's code diff preferences?",
		Results: []search.Result{
			{MemoryID: 1, SourceFile: "q.md", SourceSection: "prefs", Score: 0.93, Content: "Q prefers green for additions", FactIDs: []int64{1}},
			{MemoryID: 2, SourceFile: "q.md", SourceSection: "prefs", Score: 0.91, Content: "Q prefers blue for deletions", FactIDs: []int64{2}},
		},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if res.Degraded {
		t.Fatalf("expected non-degraded result, got reason=%q", res.Reason)
	}
	if len(res.Citations) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(res.Citations))
	}
	if res.Citations[0].SourceSection != "prefs" {
		t.Fatalf("expected source section in citation, got %+v", res.Citations[0])
	}
}

func TestAsk_CitationIntegrityFailure(t *testing.T) {
	e := NewEngine(mockProvider{
		resp: "This cites an unknown source [9].",
	}, "google/gemini-2.5-flash-lite")
	res, err := e.Ask(context.Background(), Options{
		Question: "what?",
		Results:  []search.Result{{MemoryID: 1, SourceFile: "doc.md", Score: 0.8, Content: "alpha"}},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if !res.Degraded || res.Reason != "citation_integrity_failed" {
		t.Fatalf("expected citation_integrity_failed degrade, got degraded=%v reason=%q", res.Degraded, res.Reason)
	}
	if res.RawAnswer == "" {
		t.Fatal("expected raw answer to be preserved on citation failure")
	}
}

func TestAsk_HandlesProviderError(t *testing.T) {
	e := NewEngine(mockProvider{err: errors.New("boom")}, "google/gemini-2.5-flash-lite")
	res, err := e.Ask(context.Background(), Options{
		Question: "what?",
		Results:  []search.Result{{MemoryID: 1, SourceFile: "doc.md", Score: 0.8, Content: "alpha"}},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if !res.Degraded || res.Reason != "llm_error" {
		t.Fatalf("expected llm_error degrade, got degraded=%v reason=%q", res.Degraded, res.Reason)
	}
	if res.Error == "" {
		t.Fatal("expected underlying error detail to be populated")
	}
}

func TestIsNotEnoughEvidence(t *testing.T) {
	cases := []string{
		"not enough evidence",
		"not enough evidence.",
		" Not enough evidence! ",
	}
	for _, c := range cases {
		if !isNotEnoughEvidence(c) {
			t.Fatalf("expected %q to normalize as not enough evidence", c)
		}
	}
}
