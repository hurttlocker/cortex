package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name = ?`, name,
	).Scan(&count); err != nil {
		t.Fatalf("checking table %s: %v", name, err)
	}
	return count == 1
}

// TestMigrateDirectivesTable_FreshAndIdempotent proves a fresh DB gets the table
// (via the full migrate path in NewStore) and that re-running the migration is a no-op.
func TestMigrateDirectivesTable_FreshAndIdempotent(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)

	if !tableExists(t, s.db, "directives") {
		t.Fatal("expected directives table to exist after fresh migration")
	}
	if !tableExists(t, s.db, "directives_fts") {
		t.Fatal("expected directives_fts virtual table to exist after fresh migration")
	}

	// Idempotent: running the migration again must not error and must not disturb data.
	ctx := context.Background()
	id, err := s.AddDirective(ctx, &Directive{Rule: "always write tests", Scope: "global"})
	if err != nil {
		t.Fatalf("AddDirective: %v", err)
	}
	if err := s.migrateDirectivesTable(); err != nil {
		t.Fatalf("second migrateDirectivesTable: %v", err)
	}
	got, err := s.GetDirective(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("GetDirective after re-migration: got=%v err=%v", got, err)
	}
	if got.Rule != "always write tests" {
		t.Fatalf("directive survived re-migration but rule changed: %q", got.Rule)
	}
}

// TestMigrateDirectivesTable_UpgradesPreV2 simulates a DB created before v2 (no
// directives table, no directives_v1 flag) and proves the migration upgrades it
// cleanly and the table is functional afterward.
func TestMigrateDirectivesTable_UpgradesPreV2(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pre-v2.db")
	si, err := NewStore(StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s := si.(*SQLiteStore)

	// Roll the DB back to a pre-v2 state: drop the directives artifacts and the flag.
	for _, stmt := range []string{
		`DROP TRIGGER IF EXISTS directives_ai`,
		`DROP TRIGGER IF EXISTS directives_au`,
		`DROP TRIGGER IF EXISTS directives_ad`,
		`DROP TABLE IF EXISTS directives_fts`,
		`DROP TABLE IF EXISTS directives`,
		`DELETE FROM meta WHERE key = 'directives_v1'`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("simulating pre-v2 (%s): %v", stmt, err)
		}
	}
	if tableExists(t, s.db, "directives") {
		t.Fatal("directives table should be gone before upgrade")
	}

	// Re-run the migration — this is the upgrade path an existing install takes.
	if err := s.migrateDirectivesTable(); err != nil {
		t.Fatalf("upgrade migrateDirectivesTable: %v", err)
	}
	if !tableExists(t, s.db, "directives") || !tableExists(t, s.db, "directives_fts") {
		t.Fatal("expected directives + directives_fts to exist after upgrade")
	}

	// Functional after upgrade.
	ctx := context.Background()
	if _, err := s.AddDirective(ctx, &Directive{Rule: "never push to main", Scope: "global"}); err != nil {
		t.Fatalf("AddDirective after upgrade: %v", err)
	}
}

// TestDirectiveStore_RoundTrip drives the store API end-to-end: add → get → list →
// active (scope) → update → archive → delete, asserting the memory_events audit trail.
func TestDirectiveStore_RoundTrip(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	id, err := s.AddDirective(ctx, &Directive{Rule: "always run go test before commit", Author: "quise"})
	if err != nil {
		t.Fatalf("AddDirective: %v", err)
	}

	got, err := s.GetDirective(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("GetDirective: got=%v err=%v", got, err)
	}
	if got.Scope != DirectiveScopeGlobal {
		t.Fatalf("expected default scope %q, got %q", DirectiveScopeGlobal, got.Scope)
	}
	if got.Status != DirectiveStatusActive {
		t.Fatalf("expected active status, got %q", got.Status)
	}
	if !got.Pinned {
		t.Fatal("expected directive to be pinned by design")
	}
	if got.Author != "quise" {
		t.Fatalf("expected author quise, got %q", got.Author)
	}

	// A scoped directive plus a global one: ActiveDirectives(scope) returns both.
	scopedID, err := s.AddDirective(ctx, &Directive{Rule: "prefer webpack in this repo", Scope: "o8"})
	if err != nil {
		t.Fatalf("AddDirective scoped: %v", err)
	}
	active, err := s.ActiveDirectives(ctx, "o8")
	if err != nil {
		t.Fatalf("ActiveDirectives(o8): %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected global + o8 directive, got %d", len(active))
	}
	// A different scope only sees the global one.
	activeOther, err := s.ActiveDirectives(ctx, "trading")
	if err != nil {
		t.Fatalf("ActiveDirectives(trading): %v", err)
	}
	if len(activeOther) != 1 || activeOther[0].Scope != DirectiveScopeGlobal {
		t.Fatalf("expected only the global directive for a different scope, got %+v", activeOther)
	}

	// List: active-only by default (both are active), archived widens.
	list, err := s.ListDirectives(ctx, DirectiveListOpts{})
	if err != nil {
		t.Fatalf("ListDirectives: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 active directives, got %d", len(list))
	}

	// Update the scoped directive's rule.
	newRule := "prefer turbopack in this repo"
	if err := s.UpdateDirective(ctx, scopedID, DirectiveUpdate{Rule: &newRule}); err != nil {
		t.Fatalf("UpdateDirective: %v", err)
	}
	updated, _ := s.GetDirective(ctx, scopedID)
	if updated.Rule != newRule {
		t.Fatalf("expected updated rule, got %q", updated.Rule)
	}

	// Archive drops it out of active retrieval but keeps the row.
	if err := s.ArchiveDirective(ctx, scopedID); err != nil {
		t.Fatalf("ArchiveDirective: %v", err)
	}
	activeAfterArchive, _ := s.ActiveDirectives(ctx, "o8")
	if len(activeAfterArchive) != 1 {
		t.Fatalf("archived directive should drop from active, got %d", len(activeAfterArchive))
	}
	archivedList, _ := s.ListDirectives(ctx, DirectiveListOpts{Status: DirectiveStatusArchived})
	if len(archivedList) != 1 || archivedList[0].ID != scopedID {
		t.Fatalf("expected the archived directive in the archived list, got %+v", archivedList)
	}

	// Delete hard-removes the global one.
	if err := s.DeleteDirective(ctx, id); err != nil {
		t.Fatalf("DeleteDirective: %v", err)
	}
	gone, _ := s.GetDirective(ctx, id)
	if gone != nil {
		t.Fatal("expected deleted directive to be gone")
	}

	// Audit trail: add(x2) + update + archive + delete recorded under directive: sources.
	var eventCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_events WHERE source LIKE 'directive:%'`,
	).Scan(&eventCount); err != nil {
		t.Fatalf("counting directive events: %v", err)
	}
	if eventCount != 5 {
		t.Fatalf("expected 5 directive lifecycle events, got %d", eventCount)
	}
}
