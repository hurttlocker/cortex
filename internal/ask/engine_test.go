package ask

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/llm"
	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

type mockProvider struct {
	resp       string
	err        error
	seenPrompt *string
}

func (m mockProvider) Complete(ctx context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	if m.seenPrompt != nil {
		*m.seenPrompt = prompt
	}
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

// TestAsk_CitationTitle_RealPath (M4) drives a real store + real search
// engine — mirroring how the CLI wires cortex ask — instead of constructing
// search.Result values by hand, proving Citation.Title reaches the ask
// engine's real fallback path from an actually-imported memory.
func TestAsk_CitationTitle_RealPath(t *testing.T) {
	s, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if _, err := s.AddMemory(ctx, &store.Memory{
		Content:       "Q prefers green over blue for the accent color.",
		SourceFile:    "prefs.md",
		SourceSection: "Color Preferences",
	}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	searchEngine := search.NewEngine(s)
	results, err := searchEngine.Search(ctx, "green blue accent color", search.Options{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}

	e := NewEngine(nil, "")
	res, err := e.Ask(ctx, Options{Question: "what accent color does Q prefer?", Results: results})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if len(res.Citations) == 0 {
		t.Fatal("expected at least one citation")
	}
	if got := res.Citations[0].Title; got != "Color Preferences" {
		t.Fatalf("expected Title == section header %q, got %q", "Color Preferences", got)
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

func TestAsk_AcceptsDanglingTrailingCitation(t *testing.T) {
	e := NewEngine(mockProvider{
		resp: "Jon's ideal studio should have Marley flooring [1",
	}, "google/gemini-2.5-flash-lite")
	res, err := e.Ask(context.Background(), Options{
		Question: "what?",
		Results:  []search.Result{{MemoryID: 1, SourceFile: "doc.md", Score: 0.8, Content: "Marley flooring is ideal"}},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if res.Degraded {
		t.Fatalf("expected non-degraded result, got reason=%q raw=%q", res.Reason, res.RawAnswer)
	}
	if len(res.Citations) != 1 || res.Citations[0].Index != 1 {
		t.Fatalf("expected citation [1], got %+v", res.Citations)
	}
}

func TestAsk_AcceptsCommaSeparatedCitationGroup(t *testing.T) {
	e := NewEngine(mockProvider{
		resp: "Jon and Gina both started businesses they care about [1, 2].",
	}, "google/gemini-2.5-flash-lite")
	res, err := e.Ask(context.Background(), Options{
		Question: "what?",
		Results: []search.Result{
			{MemoryID: 1, SourceFile: "doc.md", Score: 0.8, Content: "Jon started a dance studio"},
			{MemoryID: 2, SourceFile: "doc.md", Score: 0.7, Content: "Gina started a clothing store"},
		},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if res.Degraded {
		t.Fatalf("expected non-degraded result, got reason=%q raw=%q", res.Reason, res.RawAnswer)
	}
	if len(res.Citations) != 2 {
		t.Fatalf("expected 2 citations, got %+v", res.Citations)
	}
}

func TestAsk_RepairsMissingCitationsFromOverlap(t *testing.T) {
	e := NewEngine(mockProvider{
		resp: "Jon thinks the ideal dance studio should have Marley flooring.",
	}, "google/gemini-2.5-flash-lite")
	res, err := e.Ask(context.Background(), Options{
		Question: "what?",
		Results: []search.Result{
			{MemoryID: 1, SourceFile: "doc.md", Score: 0.95, Content: "Jon says the ideal dance studio should have Marley flooring because it is grippy and easy to clean."},
			{MemoryID: 2, SourceFile: "doc.md", Score: 0.70, Content: "Gina likes supportive studios."},
		},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if res.Degraded {
		t.Fatalf("expected repaired non-degraded result, got reason=%q raw=%q", res.Reason, res.RawAnswer)
	}
	if len(res.Citations) == 0 || res.Citations[0].Index != 1 {
		t.Fatalf("expected repaired citation [1], got %+v", res.Citations)
	}
	if res.Answer == "" || res.Answer == "not enough evidence" {
		t.Fatalf("unexpected repaired answer: %q", res.Answer)
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

func TestAsk_GroupsEvidenceBlocksByScene(t *testing.T) {
	var prompt string
	e := NewEngine(mockProvider{
		resp:       "June 20, 2023 [1].",
		seenPrompt: &prompt,
	}, "google/gemini-2.5-flash-lite")
	_, err := e.Ask(context.Background(), Options{
		Question: "When is Jon's opening night?",
		Results: []search.Result{
			{
				MemoryID:      1,
				SourceFile:    "conv-30.md",
				SourceSection: "Session 9",
				Score:         0.95,
				Content:       "Jon started a dance studio after leaving banking.",
				Metadata:      &store.Metadata{SessionKey: "conv-30:session-9"},
			},
			{
				MemoryID:      2,
				SourceFile:    "conv-30.md",
				SourceSection: "Session 9",
				Score:         0.91,
				Content:       "The official opening night is June 20, 2023.",
				Metadata:      &store.Metadata{SessionKey: "conv-30:session-9"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ask err: %v", err)
	}
	if !strings.Contains(prompt, "Evidence group 1") {
		t.Fatalf("expected grouped evidence header, got %q", prompt)
	}
	if !strings.Contains(prompt, "session:conv-30:session-9") {
		t.Fatalf("expected session label in grouped prompt, got %q", prompt)
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
