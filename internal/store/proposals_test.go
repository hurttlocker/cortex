package store

import (
	"context"
	"testing"
	"time"
)

// TestMigrateDirectiveProposalsTable_FreshAndIdempotent proves a fresh DB gets
// the table (via NewStore's migrate path) and that re-running the migration is a
// no-op that preserves data.
func TestMigrateDirectiveProposalsTable_FreshAndIdempotent(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	if !tableExists(t, s.db, "directive_proposals") {
		t.Fatal("expected directive_proposals table after fresh migration")
	}
	flag, err := s.isMetaFlagEnabled("directive_proposals_v1")
	if err != nil {
		t.Fatalf("checking directive_proposals_v1 flag: %v", err)
	}
	if !flag {
		t.Fatal("expected directive_proposals_v1 meta flag set after migration")
	}

	// Insert a proposal, re-run migration, confirm it survived untouched.
	id, err := s.CreateProposal(ctx, &DirectiveProposal{
		CandidateRule: "Recurring fix pattern: add nil check",
		PatternKey:    "add nil check",
		Occurrences:   3,
		WindowStart:   time.Now().UTC().Add(-time.Hour),
		WindowEnd:     time.Now().UTC(),
		Evidence:      []int64{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if err := s.migrateDirectiveProposalsTable(); err != nil {
		t.Fatalf("second migrateDirectiveProposalsTable: %v", err)
	}
	got, err := s.GetProposal(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("GetProposal after re-migration: got=%v err=%v", got, err)
	}
	if got.PatternKey != "add nil check" || got.Occurrences != 3 || len(got.Evidence) != 3 {
		t.Fatalf("proposal changed across re-migration: %+v", got)
	}
	if got.Status != ProposalStatusPending {
		t.Fatalf("expected pending status, got %q", got.Status)
	}
}

// seedPattern records n ledger rows through the real ledger write path, all
// sharing the same fix pattern, and returns the created ids.
func seedPattern(t *testing.T, s *SQLiteStore, pattern string, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	for i := 0; i < n; i++ {
		id, err := s.RecordLedgerEntry(ctx, &LedgerEntry{
			TaskSummary: "did a thing",
			Outcome:     LedgerOutcomeSuccess,
			FixPattern:  pattern,
		})
		if err != nil {
			t.Fatalf("RecordLedgerEntry: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestScanForProposals_Threshold proves a pattern below the min-occurrences
// threshold produces no proposal.
func TestScanForProposals_Threshold(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	seedPattern(t, s, "add nil check before deref", 2) // only 2, threshold is 3

	res, err := s.ScanForProposals(ctx, ScanOptions{Window: 14 * 24 * time.Hour, MinOccurrences: 3})
	if err != nil {
		t.Fatalf("ScanForProposals: %v", err)
	}
	if len(res.Candidates) != 0 || len(res.Created) != 0 {
		t.Fatalf("expected no candidates/created below threshold, got candidates=%d created=%d", len(res.Candidates), len(res.Created))
	}
	pending, _ := s.ListProposals(ctx, ProposalListOpts{})
	if len(pending) != 0 {
		t.Fatalf("expected 0 persisted proposals, got %d", len(pending))
	}
}

// TestScanForProposals_DryRunPersistsNothing proves --dry-run surfaces candidates
// but writes nothing.
func TestScanForProposals_DryRunPersistsNothing(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	seedPattern(t, s, "wrap error with %w", 3)

	res, err := s.ScanForProposals(ctx, ScanOptions{Window: 14 * 24 * time.Hour, MinOccurrences: 3, DryRun: true})
	if err != nil {
		t.Fatalf("ScanForProposals dry-run: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 dry-run candidate, got %d", len(res.Candidates))
	}
	if len(res.Created) != 0 {
		t.Fatalf("dry-run must create nothing, got %d created", len(res.Created))
	}
	if got := res.Candidates[0].CandidateRule; got != "Recurring fix pattern: wrap error with %w" {
		t.Fatalf("unexpected candidate rule derivation: %q", got)
	}
	persisted, _ := s.ListProposals(ctx, ProposalListOpts{Status: "all"})
	if len(persisted) != 0 {
		t.Fatalf("dry-run persisted %d proposals, expected 0", len(persisted))
	}
}

// TestScanForProposals_DedupeNoDuplicate proves a second scan over the same
// evidence window does not create a duplicate proposal.
func TestScanForProposals_DedupeNoDuplicate(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	seedPattern(t, s, "add nil check before deref", 3)

	first, err := s.ScanForProposals(ctx, ScanOptions{Window: 14 * 24 * time.Hour, MinOccurrences: 3})
	if err != nil {
		t.Fatalf("first ScanForProposals: %v", err)
	}
	if len(first.Created) != 1 {
		t.Fatalf("expected 1 proposal on first scan, got %d", len(first.Created))
	}

	second, err := s.ScanForProposals(ctx, ScanOptions{Window: 14 * 24 * time.Hour, MinOccurrences: 3})
	if err != nil {
		t.Fatalf("second ScanForProposals: %v", err)
	}
	if len(second.Created) != 0 {
		t.Fatalf("expected 0 new proposals on second scan, got %d", len(second.Created))
	}
	if len(second.SkippedExisting) != 1 {
		t.Fatalf("expected 1 skipped-existing pattern on second scan, got %d", len(second.SkippedExisting))
	}

	all, _ := s.ListProposals(ctx, ProposalListOpts{Status: "all"})
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 proposal after two scans, got %d", len(all))
	}
}

// TestProposalStore_GuardsAndRoundTrip covers create/get/list plus the
// accept-only-pending and dismiss-only-pending guards at the store layer.
func TestProposalStore_GuardsAndRoundTrip(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	id, err := s.CreateProposal(ctx, &DirectiveProposal{
		CandidateRule: "Recurring fix pattern: guard the map write",
		PatternKey:    "guard the map write",
		Occurrences:   4,
		WindowStart:   time.Now().UTC().Add(-2 * time.Hour),
		WindowEnd:     time.Now().UTC(),
		Evidence:      []int64{10, 11, 12, 13},
	})
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}

	// Default list = pending only.
	pending, _ := s.ListProposals(ctx, ProposalListOpts{})
	if len(pending) != 1 || pending[0].ID != id {
		t.Fatalf("expected the pending proposal, got %+v", pending)
	}

	// Accept → creates a directive, flips status, records the directive id.
	before, _ := s.ListDirectives(ctx, DirectiveListOpts{Status: "all"})
	dirID, err := s.AcceptProposal(ctx, id)
	if err != nil {
		t.Fatalf("AcceptProposal: %v", err)
	}
	after, _ := s.ListDirectives(ctx, DirectiveListOpts{Status: "all"})
	if len(after) != len(before)+1 {
		t.Fatalf("expected exactly one new directive, before=%d after=%d", len(before), len(after))
	}
	got, _ := s.GetProposal(ctx, id)
	if got.Status != ProposalStatusAccepted {
		t.Fatalf("expected accepted status, got %q", got.Status)
	}
	if got.CreatedDirectiveID == nil || *got.CreatedDirectiveID != dirID {
		t.Fatalf("expected created_directive_id=%d, got %v", dirID, got.CreatedDirectiveID)
	}
	if got.ResolvedAt == nil {
		t.Fatal("expected resolved_at set after accept")
	}

	// Re-accepting a resolved proposal must fail (and write no second directive).
	if _, err := s.AcceptProposal(ctx, id); err == nil {
		t.Fatal("expected error re-accepting an already-accepted proposal")
	}
	afterReaccept, _ := s.ListDirectives(ctx, DirectiveListOpts{Status: "all"})
	if len(afterReaccept) != len(after) {
		t.Fatalf("re-accept must not create another directive: %d → %d", len(after), len(afterReaccept))
	}

	// Dismiss guard: a resolved proposal cannot be dismissed.
	if err := s.DismissProposal(ctx, id); err == nil {
		t.Fatal("expected error dismissing an already-accepted proposal")
	}
}
