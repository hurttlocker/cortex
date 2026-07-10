package main

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

// TestRunDirective_AddListArchiveRoundTrip drives the real CLI command entry points
// (runDirectiveAdd → runDirectiveList → runDirectiveArchive) against a temp DB and
// verifies the observable effect via the store the commands write to.
func TestRunDirective_AddListArchiveRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex-directive-cli.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })
	t.Setenv("HOME", t.TempDir())

	// add
	if err := runDirectiveAdd([]string{"always run go test before commit", "--scope", "o8", "--author", "quise"}); err != nil {
		t.Fatalf("runDirectiveAdd: %v", err)
	}

	// Verify the add landed as an active, pinned directive with the right fields.
	var directiveID int64
	func() {
		s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
		if err != nil {
			t.Fatalf("open store for verify: %v", err)
		}
		defer s.Close()
		list, err := s.ListDirectives(context.Background(), store.DirectiveListOpts{})
		if err != nil {
			t.Fatalf("ListDirectives: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("expected 1 active directive after add, got %d", len(list))
		}
		d := list[0]
		if d.Scope != "o8" || d.Author != "quise" || !d.Pinned {
			t.Fatalf("directive fields wrong after add: %+v", d)
		}
		directiveID = d.ID
	}()

	// list (command-level smoke — must not error in JSON or table path)
	if err := runDirectiveList([]string{"--json"}); err != nil {
		t.Fatalf("runDirectiveList --json: %v", err)
	}
	if err := runDirectiveList([]string{"--scope", "o8"}); err != nil {
		t.Fatalf("runDirectiveList --scope: %v", err)
	}

	// archive
	if err := runDirectiveArchive([]string{strconv.FormatInt(directiveID, 10)}); err != nil {
		t.Fatalf("runDirectiveArchive: %v", err)
	}

	// Verify it dropped out of active and shows under archived.
	func() {
		s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
		if err != nil {
			t.Fatalf("open store for verify: %v", err)
		}
		defer s.Close()
		active, _ := s.ListDirectives(context.Background(), store.DirectiveListOpts{})
		if len(active) != 0 {
			t.Fatalf("expected 0 active directives after archive, got %d", len(active))
		}
		archived, _ := s.ListDirectives(context.Background(), store.DirectiveListOpts{Status: store.DirectiveStatusArchived})
		if len(archived) != 1 || archived[0].ID != directiveID {
			t.Fatalf("expected the archived directive, got %+v", archived)
		}
	}()
}
