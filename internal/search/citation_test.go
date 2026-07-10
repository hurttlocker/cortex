package search

import "testing"

func TestCitationTitleForResult_PrefersSourceSection(t *testing.T) {
	r := Result{SourceFile: "/a/b/conv-30.md", SourceSection: "Session 9"}
	if got := CitationTitleForResult(r); got != "Session 9" {
		t.Fatalf("expected %q, got %q", "Session 9", got)
	}
}

func TestCitationTitleForResult_FallsBackToBaseFilename(t *testing.T) {
	r := Result{SourceFile: "/a/b/notes.md"}
	if got := CitationTitleForResult(r); got != "notes.md" {
		t.Fatalf("expected base filename %q, got %q", "notes.md", got)
	}
}

func TestCitationTitleForResult_DirectiveUsesRuleTextEvenWithScopeSection(t *testing.T) {
	// A directive's SourceSection carries its governance scope (e.g. "global"),
	// not a section header — the rule text must win regardless.
	r := Result{Kind: "directive", Content: "never commit secrets to the repository", SourceFile: "directive", SourceSection: "global"}
	if got := CitationTitleForResult(r); got != "never commit secrets to the repository" {
		t.Fatalf("expected directive rule text, got %q", got)
	}
}

func TestCitationTitleForResult_DirectiveTruncatesLongRule(t *testing.T) {
	longRule := "this is a very long governance rule that goes on and on well past the eighty character budget we allow"
	r := Result{Kind: "directive", Content: longRule, SourceSection: "global"}
	got := CitationTitleForResult(r)
	if len(got) > 80 {
		t.Fatalf("expected title truncated to <=80 chars, got %d chars: %q", len(got), got)
	}
	if got[len(got)-3:] != "..." {
		t.Fatalf("expected truncated title to end with ellipsis, got %q", got)
	}
}

func TestCitationTitleForResult_EmptyWhenNothingAvailable(t *testing.T) {
	r := Result{MemoryID: 42}
	if got := CitationTitleForResult(r); got != "" {
		t.Fatalf("expected empty title so caller falls back to locator, got %q", got)
	}
}
