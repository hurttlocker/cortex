package search

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

// TestSearch_PinsActiveDirectivesAboveResults proves the retrieval pinning guarantee
// through the REAL store + search path: a directive whose rule shares no tokens with
// the query is still returned at the very top, flagged Kind:"directive", while the
// BM25 memory hit follows. This is the shared assembly path that the CLI search/recall
// surface, the answer engine, and their MCP equivalents all funnel through.
func TestSearch_PinsActiveDirectivesAboveResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// A memory that the query will match on BM25.
	if _, err := s.AddMemory(ctx, &store.Memory{
		Content:    "The kangaroo mascot appears on the login screen of the app.",
		SourceFile: "notes.md",
		SourceLine: 1,
	}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// A governance directive that shares NO tokens with the query "kangaroo".
	if _, err := s.AddDirective(ctx, &store.Directive{
		Rule:  "never commit secrets to the repository",
		Scope: store.DirectiveScopeGlobal,
	}); err != nil {
		t.Fatalf("AddDirective: %v", err)
	}

	engine := NewEngine(s)
	results, err := engine.Search(ctx, "kangaroo", Options{Mode: ModeKeyword, Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least the pinned directive + 1 BM25 hit, got %d", len(results))
	}

	// Directive is pinned at the very top, flagged and carrying the rule text.
	top := results[0]
	if top.Kind != "directive" {
		t.Fatalf("expected results[0].Kind == \"directive\", got %q (content=%q)", top.Kind, top.Content)
	}
	if top.Content != "never commit secrets to the repository" {
		t.Fatalf("expected directive rule at top, got %q", top.Content)
	}

	// The BM25 memory hit follows the directive and is NOT flagged as a directive.
	foundMemory := false
	for _, r := range results[1:] {
		if r.Kind == "directive" {
			t.Fatalf("did not expect a second directive in results: %q", r.Content)
		}
		if r.MemoryID != 0 {
			foundMemory = true
		}
	}
	if !foundMemory {
		t.Fatal("expected the BM25 memory result to follow the pinned directive")
	}

	// Sanity: an archived directive must NOT be pinned.
	list, _ := s.ListDirectives(ctx, store.DirectiveListOpts{})
	if len(list) == 1 {
		if err := s.ArchiveDirective(ctx, list[0].ID); err != nil {
			t.Fatalf("ArchiveDirective: %v", err)
		}
	}
	afterArchive, err := engine.Search(ctx, "kangaroo", Options{Mode: ModeKeyword, Limit: 5})
	if err != nil {
		t.Fatalf("Search after archive: %v", err)
	}
	for _, r := range afterArchive {
		if r.Kind == "directive" {
			t.Fatalf("archived directive should not be pinned, but found %q", r.Content)
		}
	}
}
