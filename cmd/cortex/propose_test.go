package main

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

// withTempDB points the CLI commands at a fresh file-backed DB and restores the
// global afterward. Commands open/close their own store on this path, exactly as
// they do in production.
func withTempDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cortex-propose-cli.db")
	old := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = old })
	t.Setenv("HOME", t.TempDir())
	return dbPath
}

func countDirectives(t *testing.T, dbPath string) int {
	t.Helper()
	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	list, err := s.ListDirectives(context.Background(), store.DirectiveListOpts{Status: "all"})
	if err != nil {
		t.Fatalf("ListDirectives: %v", err)
	}
	return len(list)
}

// TestPropose_GovernanceGuarantee is THE governance test. It proves the whole
// positioning line of v2 — "the memory primitive that proposes, never writes" —
// end to end through the REAL command paths:
//
//   - seed 3 ledger rows sharing a fix pattern via the real `ledger record` path
//   - run the real `propose scan` path → a pending proposal exists AND the
//     directives count is UNCHANGED (scan has no path to a directive write)
//   - run the real `propose accept` path → exactly one directive now exists, the
//     proposal is accepted with created_directive_id set, and the new directive
//     surfaces PINNED at the top of a real search (M1's pinning path).
func TestPropose_GovernanceGuarantee(t *testing.T) {
	dbPath := withTempDB(t)
	ctx := context.Background()

	const pattern = "add nil check before dereference"

	// Seed >= 3 ledger rows sharing a fix pattern through the REAL ledger path.
	for i := 0; i < 3; i++ {
		if err := runLedgerRecord([]string{"--summary", "fixed a crash", "--outcome", "success", "--pattern", pattern}); err != nil {
			t.Fatalf("runLedgerRecord[%d]: %v", i, err)
		}
	}

	// Baseline: no directives yet.
	if n := countDirectives(t, dbPath); n != 0 {
		t.Fatalf("expected 0 directives before scan, got %d", n)
	}

	// REAL scan path.
	if err := runProposeScan(nil); err != nil {
		t.Fatalf("runProposeScan: %v", err)
	}

	// A pending proposal exists AND directives are UNCHANGED — the crux of the
	// governance guarantee: scanning proposes, it never writes.
	var proposalID int64
	func() {
		s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer s.Close()
		ss := s.(*store.SQLiteStore)
		pending, err := ss.ListProposals(ctx, store.ProposalListOpts{})
		if err != nil {
			t.Fatalf("ListProposals: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("expected 1 pending proposal after scan, got %d", len(pending))
		}
		proposalID = pending[0].ID
		if pending[0].Occurrences != 3 {
			t.Fatalf("expected occurrences=3, got %d", pending[0].Occurrences)
		}
		if pending[0].PatternKey != pattern {
			t.Fatalf("expected pattern_key %q, got %q", pattern, pending[0].PatternKey)
		}
	}()
	if n := countDirectives(t, dbPath); n != 0 {
		t.Fatalf("GOVERNANCE VIOLATION: scan created a directive (count=%d, want 0)", n)
	}

	// REAL accept path — the ONLY path from a proposal to a directive.
	if err := runProposeAccept([]string{strconv.FormatInt(proposalID, 10)}); err != nil {
		t.Fatalf("runProposeAccept: %v", err)
	}

	// Exactly one directive now exists.
	if n := countDirectives(t, dbPath); n != 1 {
		t.Fatalf("expected exactly 1 directive after accept, got %d", n)
	}

	// Proposal is accepted with the created directive id set; the directive
	// surfaces PINNED at the top of a real search.
	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	ss := s.(*store.SQLiteStore)

	accepted, err := ss.GetProposal(ctx, proposalID)
	if err != nil || accepted == nil {
		t.Fatalf("GetProposal: got=%v err=%v", accepted, err)
	}
	if accepted.Status != store.ProposalStatusAccepted {
		t.Fatalf("expected accepted status, got %q", accepted.Status)
	}
	if accepted.CreatedDirectiveID == nil {
		t.Fatal("expected created_directive_id set after accept")
	}
	if accepted.ResolvedAt == nil {
		t.Fatal("expected resolved_at set after accept")
	}

	// The accepted directive carries the mechanically-derived rule...
	dir, err := ss.GetDirective(ctx, *accepted.CreatedDirectiveID)
	if err != nil || dir == nil {
		t.Fatalf("GetDirective: got=%v err=%v", dir, err)
	}
	wantRule := "Recurring fix pattern: " + pattern
	if dir.Rule != wantRule {
		t.Fatalf("directive rule = %q, want %q", dir.Rule, wantRule)
	}
	if !dir.Pinned {
		t.Fatal("expected the created directive to be pinned")
	}

	// ...and it is pinned at the top of a real search (M1's shared pinning path),
	// even for a query that shares no tokens with the rule.
	if _, err := s.AddMemory(ctx, &store.Memory{
		Content:    "The kangaroo mascot appears on the login screen.",
		SourceFile: "notes.md",
		SourceLine: 1,
	}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	engine := search.NewEngine(s)
	results, err := engine.Search(ctx, "kangaroo", search.Options{Mode: search.ModeKeyword, Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least the pinned directive in search results")
	}
	if results[0].Kind != "directive" {
		t.Fatalf("expected the accepted directive pinned at results[0], got Kind=%q content=%q", results[0].Kind, results[0].Content)
	}
	if results[0].Content != wantRule {
		t.Fatalf("expected pinned directive rule %q, got %q", wantRule, results[0].Content)
	}
}

// TestPropose_DismissDropsAndBlocksReproposal drives the real dismiss path: a
// dismissed proposal writes no directive, and re-scanning over the same evidence
// window does NOT re-propose the same pattern while that dismissed proposal
// exists for it.
func TestPropose_DismissDropsAndBlocksReproposal(t *testing.T) {
	dbPath := withTempDB(t)
	ctx := context.Background()

	const pattern = "wrap returned error with %w"
	for i := 0; i < 3; i++ {
		if err := runLedgerRecord([]string{"--summary", "improved errors", "--outcome", "success", "--pattern", pattern}); err != nil {
			t.Fatalf("runLedgerRecord[%d]: %v", i, err)
		}
	}

	if err := runProposeScan(nil); err != nil {
		t.Fatalf("runProposeScan: %v", err)
	}

	// Grab the pending proposal id.
	var proposalID int64
	func() {
		s, _ := store.NewStore(store.StoreConfig{DBPath: dbPath})
		defer s.Close()
		ss := s.(*store.SQLiteStore)
		pending, _ := ss.ListProposals(ctx, store.ProposalListOpts{})
		if len(pending) != 1 {
			t.Fatalf("expected 1 pending proposal, got %d", len(pending))
		}
		proposalID = pending[0].ID
	}()

	// REAL dismiss path.
	if err := runProposeDismiss([]string{strconv.FormatInt(proposalID, 10)}); err != nil {
		t.Fatalf("runProposeDismiss: %v", err)
	}

	// No directive written by dismiss.
	if n := countDirectives(t, dbPath); n != 0 {
		t.Fatalf("dismiss must write no directive, got %d", n)
	}

	// Re-scan over the same evidence window must not re-propose.
	if err := runProposeScan(nil); err != nil {
		t.Fatalf("second runProposeScan: %v", err)
	}
	func() {
		s, _ := store.NewStore(store.StoreConfig{DBPath: dbPath})
		defer s.Close()
		ss := s.(*store.SQLiteStore)
		all, _ := ss.ListProposals(ctx, store.ProposalListOpts{Status: "all"})
		if len(all) != 1 {
			t.Fatalf("expected exactly 1 proposal (dismissed, not re-proposed), got %d", len(all))
		}
		if all[0].Status != store.ProposalStatusDismissed {
			t.Fatalf("expected the proposal to stay dismissed, got %q", all[0].Status)
		}
		pending, _ := ss.ListProposals(ctx, store.ProposalListOpts{})
		if len(pending) != 0 {
			t.Fatalf("expected 0 pending proposals after dismiss+rescan, got %d", len(pending))
		}
	}()
}
