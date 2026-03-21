package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/ingest"
	"github.com/hurttlocker/cortex/internal/observe"
	"github.com/hurttlocker/cortex/internal/store"
)

func importAndExtractFacts(t *testing.T, ctx context.Context, engine *ingest.Engine, s store.Store, filePath string) (*ingest.ImportResult, *ExtractionStats) {
	t.Helper()

	importResult, err := engine.ImportFile(ctx, filePath, ingest.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportFile(%s): %v", filePath, err)
	}

	extractionStats, err := runExtractionOnImportedMemories(ctx, s, "", importResult.NewMemoryIDs, nil)
	if err != nil {
		t.Fatalf("runExtractionOnImportedMemories(%s): %v", filePath, err)
	}

	return importResult, extractionStats
}

func TestRunExtractionOnImportedMemories_LeavesChangedObjectAsConflict(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cortex.db")

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	engine := ingest.NewEngine(s)

	filePath := filepath.Join(t.TempDir(), "service-status.md")
	if err := os.WriteFile(filePath, []byte("status: running\nnotes: this service keeps telemetry pipeline healthy during daily sync runs.\n"), 0o644); err != nil {
		t.Fatalf("write v1 file: %v", err)
	}

	firstImport, firstExtraction := importAndExtractFacts(t, ctx, engine, s, filePath)
	if firstImport.MemoriesNew != 1 {
		t.Fatalf("expected first import to create 1 memory, got %d", firstImport.MemoriesNew)
	}
	if firstExtraction.FactsExtracted == 0 {
		t.Fatalf("expected first extraction to produce at least one fact")
	}

	activeBefore, err := s.ListFacts(ctx, store.ListOpts{Limit: 50})
	if err != nil {
		t.Fatalf("ListFacts active before: %v", err)
	}
	if len(activeBefore) == 0 {
		t.Fatalf("expected at least one active fact after first import")
	}

	var seed *store.Fact
	for _, f := range activeBefore {
		if strings.EqualFold(strings.TrimSpace(f.Object), "running") {
			seed = f
			break
		}
	}
	if seed == nil {
		t.Fatalf("expected to find extracted running fact; active facts=%d", len(activeBefore))
	}

	if err := os.WriteFile(filePath, []byte("status: stopped\nnotes: this service keeps telemetry pipeline healthy during daily sync runs.\n"), 0o644); err != nil {
		t.Fatalf("write v2 file: %v", err)
	}

	secondImport, secondExtraction := importAndExtractFacts(t, ctx, engine, s, filePath)
	if secondImport.MemoriesNew != 1 {
		t.Fatalf("expected second import to create 1 new memory for changed content, got %d", secondImport.MemoriesNew)
	}
	if secondExtraction.FactsExtracted == 0 {
		t.Fatalf("expected second extraction to produce at least one fact")
	}

	allFacts, err := s.ListFacts(ctx, store.ListOpts{Limit: 200, IncludeSuperseded: true})
	if err != nil {
		t.Fatalf("ListFacts include superseded: %v", err)
	}

	matching := make([]*store.Fact, 0)
	for _, f := range allFacts {
		if strings.EqualFold(strings.TrimSpace(f.Subject), strings.TrimSpace(seed.Subject)) &&
			strings.EqualFold(strings.TrimSpace(f.Predicate), strings.TrimSpace(seed.Predicate)) {
			matching = append(matching, f)
		}
	}
	if len(matching) != 2 {
		t.Fatalf("expected 2 facts for same subject+predicate after value change, got %d", len(matching))
	}

	activeFacts := 0
	for _, f := range matching {
		if f.SupersededBy == nil {
			activeFacts++
		}
	}
	if activeFacts != 2 {
		t.Fatalf("expected both facts to remain active for lifecycle resolution, got %d active", activeFacts)
	}

	engineObs := observe.NewEngine(s, dbPath)
	conflicts, err := engineObs.GetConflicts(ctx)
	if err != nil {
		t.Fatalf("GetConflicts: %v", err)
	}
	if len(conflicts) == 0 {
		t.Fatalf("expected active conflict after changed object import")
	}
}

func TestRunExtractionOnImportedMemories_SkipsDuplicateObjectForSameSubjectPredicate(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cortex.db")

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	engine := ingest.NewEngine(s)

	filePath := filepath.Join(t.TempDir(), "deploy-status.md")
	if err := os.WriteFile(filePath, []byte("status: running\nnotes: first capture includes context so markdown chunk length stays above threshold.\n"), 0o644); err != nil {
		t.Fatalf("write v1 file: %v", err)
	}

	_, firstExtraction := importAndExtractFacts(t, ctx, engine, s, filePath)
	if firstExtraction.FactsExtracted == 0 {
		t.Fatalf("expected first extraction to produce facts")
	}

	allBefore, err := s.ListFacts(ctx, store.ListOpts{Limit: 200, IncludeSuperseded: true})
	if err != nil {
		t.Fatalf("ListFacts before second import: %v", err)
	}
	countRunningBefore := 0
	for _, f := range allBefore {
		if strings.EqualFold(strings.TrimSpace(f.Predicate), "status") && strings.EqualFold(strings.TrimSpace(f.Object), "running") {
			countRunningBefore++
		}
	}

	if err := os.WriteFile(filePath, []byte("status: running\nnotes: changed line now references an updated paragraph but same status value.\n"), 0o644); err != nil {
		t.Fatalf("write v2 file: %v", err)
	}

	_, secondExtraction := importAndExtractFacts(t, ctx, engine, s, filePath)
	if secondExtraction.FactsExtracted == 0 {
		t.Fatalf("expected second extraction to still produce facts for changed content")
	}

	allAfter, err := s.ListFacts(ctx, store.ListOpts{Limit: 200, IncludeSuperseded: true})
	if err != nil {
		t.Fatalf("ListFacts after second import: %v", err)
	}
	countRunningAfter := 0
	for _, f := range allAfter {
		if strings.EqualFold(strings.TrimSpace(f.Predicate), "status") && strings.EqualFold(strings.TrimSpace(f.Object), "running") {
			countRunningAfter++
		}
	}

	if countRunningAfter != countRunningBefore {
		t.Fatalf("expected duplicate same-object status fact to be skipped (before=%d after=%d)", countRunningBefore, countRunningAfter)
	}
}
