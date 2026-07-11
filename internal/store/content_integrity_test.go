package store

import (
	"context"
	"testing"
	"time"
)

// TestMemoryContentRoundTripExact guards byte-exact content preservation
// through AddMemory/GetMemory — em-dashes, quotes, and non-ASCII must come
// back untouched. Memory that silently normalizes punctuation is memory
// that rewrites the record.
func TestMemoryContentRoundTripExact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	fixtures := []string{
		"Rainwater keeps the receipts — eighty-eight keys, one conductor, and nothing writes itself.",
		"The ledger is append-only: no update path, no delete path, \"no exceptions\".",
		"Phạm's rule survives translation: đề xuất, đừng ghi đè — propose, never overwrite.",
	}

	for _, content := range fixtures {
		id, err := s.AddMemory(ctx, &Memory{
			Content:    content,
			SourceFile: "/vault/rainwater/conductor-notes.md",
			ImportedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("AddMemory(%q): %v", content, err)
		}
		got, err := s.GetMemory(ctx, id)
		if err != nil {
			t.Fatalf("GetMemory(%d): %v", id, err)
		}
		if got.Content != content {
			t.Errorf("content round-trip mutated:\n want %q\n got  %q", content, got.Content)
		}
	}
}
