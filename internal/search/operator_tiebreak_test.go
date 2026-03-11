package search

import (
	"strings"
	"testing"
)

func TestApplyOperatorTop1TieBreak_ReordersNearTop(t *testing.T) {
	shape := queryShapeDecision{Applied: true}
	query := "operator workflow runbook retrieval prompt ranking tie break"
	results := []Result{
		{MemoryID: 1, Score: 1.00, SourceFile: "notes/general.md", SourceSection: "misc notes", Content: "general memo"},
		{MemoryID: 2, Score: 0.99, SourceFile: "docs/operator-workflow.md", SourceSection: "operator workflow runbook", Content: "retrieval runbook"},
		{MemoryID: 3, Score: 0.70, SourceFile: "notes/other.md", SourceSection: "other", Content: "unrelated"},
	}

	got := applyOperatorTop1TieBreak(shape, query, results, false)
	if got[0].MemoryID != 2 {
		t.Fatalf("expected memory_id=2 to win top-1 tie-break, got %d", got[0].MemoryID)
	}
}

func TestApplyOperatorTop1TieBreak_NoopWhenNotShaped(t *testing.T) {
	shape := queryShapeDecision{Applied: false}
	query := "operator workflow runbook retrieval prompt ranking tie break"
	results := []Result{
		{MemoryID: 1, Score: 1.00, SourceFile: "notes/general.md", SourceSection: "misc notes", Content: "general memo"},
		{MemoryID: 2, Score: 0.99, SourceFile: "docs/operator-workflow.md", SourceSection: "operator workflow runbook", Content: "retrieval runbook"},
	}

	got := applyOperatorTop1TieBreak(shape, query, results, false)
	if got[0].MemoryID != 1 {
		t.Fatalf("expected no-op when query shaping not applied; got top memory_id=%d", got[0].MemoryID)
	}
}

func TestApplyOperatorTop1TieBreak_NoopWhenScoresNotNearTop(t *testing.T) {
	shape := queryShapeDecision{Applied: true}
	query := "operator workflow runbook retrieval prompt ranking tie break"
	results := []Result{
		{MemoryID: 1, Score: 1.00, SourceFile: "notes/general.md", SourceSection: "misc notes", Content: "general memo"},
		{MemoryID: 2, Score: 0.90, SourceFile: "docs/operator-workflow.md", SourceSection: "operator workflow runbook", Content: "retrieval runbook"},
	}

	got := applyOperatorTop1TieBreak(shape, query, results, false)
	if got[0].MemoryID != 1 {
		t.Fatalf("expected no-op when candidate is outside tie window; got top memory_id=%d", got[0].MemoryID)
	}
}

func TestApplyOperatorTop1TieBreak_ExplainAnnotation(t *testing.T) {
	shape := queryShapeDecision{Applied: true}
	query := "operator workflow runbook retrieval prompt ranking tie break"
	results := []Result{
		{MemoryID: 1, Score: 1.00, SourceFile: "notes/general.md", SourceSection: "misc notes", Content: "general memo", Explain: &ExplainDetails{Why: "base", RankComponents: RankComponents{FinalScore: 1.00}}},
		{MemoryID: 2, Score: 0.99, SourceFile: "docs/operator-workflow.md", SourceSection: "operator workflow runbook", Content: "retrieval runbook", Explain: &ExplainDetails{Why: "base", RankComponents: RankComponents{FinalScore: 0.99}}},
	}

	got := applyOperatorTop1TieBreak(shape, query, results, true)
	if got[0].Explain == nil {
		t.Fatalf("expected explain payload on top result")
	}
	if !strings.Contains(got[0].Explain.Why, "operator top-1 tie-break") {
		t.Fatalf("expected explain why to mention tie-break, got %q", got[0].Explain.Why)
	}
}
