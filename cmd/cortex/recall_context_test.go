package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestRunRecall_PromotesStrongDurableFact(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Q prefers green for additions and blue for deletions in Cortex IDE code diffs.",
		SourceFile: "memory/2026-03-18.md",
	})
	if err != nil {
		t.Fatalf("AddMemory durable: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Q",
		Predicate:  "prefers",
		Object:     "green for additions and blue for deletions in Cortex IDE code diffs",
		FactType:   "preference",
		Confidence: 0.95,
	}); err != nil {
		t.Fatalf("AddFact durable: %v", err)
	}

	noiseID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Run these test queries and verify the code diff preference prompt.",
		SourceFile: "/tmp/cortex-capture-abc/auto-capture.md",
	})
	if err != nil {
		t.Fatalf("AddMemory noise: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   noiseID,
		Subject:    "task",
		Predicate:  "query",
		Object:     "code diff preference prompt",
		FactType:   "kv",
		Confidence: 0.30,
	}); err != nil {
		t.Fatalf("AddFact noise: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var (
		runErr error
		out    string
		resp   recallResponse
	)
	out = captureStdout(func() {
		runErr = runRecall([]string{"What UI preference do I have for code diffs in Cortex IDE?", "--mode", "keyword", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runRecall: %v\nout=%s", runErr, out)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode recall json: %v\nout=%q", err, out)
	}
	if len(resp.Items) == 0 {
		t.Fatalf("expected recall items, got %+v", resp)
	}
	if !resp.Items[0].PromptEligible {
		t.Fatalf("expected top recall item prompt_eligible=true, got %+v", resp.Items[0])
	}
	if resp.Items[0].RetrievalVisibility != retrievalVisibilityPromptSafe {
		t.Fatalf("expected top recall item prompt_safe, got %+v", resp.Items[0])
	}
	if resp.Items[0].SourceFile != "memory/2026-03-18.md" {
		t.Fatalf("expected durable source first, got %+v", resp.Items[0])
	}
}

func TestRunContext_ReportsDropReasonsWhenStrictSelectionEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()

	if _, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Journal note about possible future UI ideas for diff colors.",
		SourceFile: "memory/2026-03-19.md",
	}); err != nil {
		t.Fatalf("AddMemory journal: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var (
		runErr error
		out    string
		resp   contextResponse
	)
	out = captureStdout(func() {
		runErr = runContextCommand([]string{"future diff colors", "--mode", "keyword", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runContextCommand: %v\nout=%s", runErr, out)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode context json: %v\nout=%q", err, out)
	}
	if resp.StructuredBlock != "" {
		t.Fatalf("expected empty structured block, got %q", resp.StructuredBlock)
	}
	if len(resp.Dropped) == 0 {
		t.Fatalf("expected dropped items, got %+v", resp)
	}
	if resp.Diagnostics.DroppedByPolicy == 0 {
		t.Fatalf("expected dropped_by_policy > 0, got %+v", resp.Diagnostics)
	}
	if !containsString(resp.Dropped[0].DropReasons, "journal_only") {
		t.Fatalf("expected journal_only reason, got %+v", resp.Dropped[0])
	}
}

func TestRunContext_AllowEvidenceFallbackBuildsStructuredBlock(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Task prompt about checking diff color wording in the IDE.",
		SourceFile: "/tmp/cortex-capture-xyz/auto-capture.md",
	})
	if err != nil {
		t.Fatalf("AddMemory evidence: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "task",
		Predicate:  "mentions",
		Object:     "diff color wording in the IDE",
		FactType:   "kv",
		Confidence: 0.35,
	}); err != nil {
		t.Fatalf("AddFact evidence: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var (
		runErr error
		out    string
		resp   contextResponse
	)
	out = captureStdout(func() {
		runErr = runContextCommand([]string{"diff color wording", "--mode", "keyword", "--allow-evidence-fallback", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runContextCommand fallback: %v\nout=%s", runErr, out)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode context json: %v\nout=%q", err, out)
	}
	if resp.StructuredBlock == "" {
		t.Fatalf("expected non-empty structured block with fallback, got %+v", resp)
	}
	if !resp.Diagnostics.FallbackUsed {
		t.Fatalf("expected fallback_used=true, got %+v", resp.Diagnostics)
	}
	if len(resp.Items) == 0 || resp.Items[0].RetrievalVisibility != retrievalVisibilityEvidenceOnly {
		t.Fatalf("expected evidence_only injected item, got %+v", resp.Items)
	}
	if !strings.Contains(resp.StructuredBlock, "<cortex-memories>") {
		t.Fatalf("expected structured block wrapper, got %q", resp.StructuredBlock)
	}
}

func TestRunRecall_ExactPreferenceFactOutranksGenericRuleMemory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()

	_, err = s.AddMemory(ctx, &store.Memory{
		Content:     "When asked what UI preference I have for code diffs in Cortex IDE, answer with a specific explanation about code diffs in Cortex IDE and be specific.",
		SourceFile:  "memory/2026-03-12.md",
		MemoryClass: store.MemoryClassRule,
	})
	if err != nil {
		t.Fatalf("AddMemory generic rule: %v", err)
	}

	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Green for additions, blue for deletions in Cortex IDE code diffs.",
		SourceFile: "memory/2026-03-13.md",
	})
	if err != nil {
		t.Fatalf("AddMemory preference: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "UI",
		Predicate:  "preference",
		Object:     "green for additions, blue for deletions in Cortex IDE code diffs",
		FactType:   "preference",
		Confidence: 0.95,
	}); err != nil {
		t.Fatalf("AddFact preference: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	query := "What UI preference do I have for code diffs in Cortex IDE? Be specific."

	var (
		runErr error
		out    string
		recall recallResponse
	)
	out = captureStdout(func() {
		runErr = runRecall([]string{query, "--mode", "keyword", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runRecall: %v\nout=%s", runErr, out)
	}
	if err := json.Unmarshal([]byte(out), &recall); err != nil {
		t.Fatalf("decode recall json: %v\nout=%q", err, out)
	}
	if len(recall.Items) < 2 {
		t.Fatalf("expected at least 2 recall items, got %+v", recall)
	}
	if recall.Items[0].SourceFile != "memory/2026-03-13.md" {
		t.Fatalf("expected preference memory ranked first, got %+v", recall.Items[0])
	}
	if recall.Items[0].RankScore <= recall.Items[1].RankScore {
		t.Fatalf("expected preference rank_score to beat generic rule: first=%+v second=%+v", recall.Items[0], recall.Items[1])
	}

	var contextResp contextResponse
	out = captureStdout(func() {
		runErr = runContextCommand([]string{query, "--mode", "keyword", "--limit", "8", "--max-items", "6", "--max-tokens", "450", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runContextCommand: %v\nout=%s", runErr, out)
	}
	if err := json.Unmarshal([]byte(out), &contextResp); err != nil {
		t.Fatalf("decode context json: %v\nout=%q", err, out)
	}
	if len(contextResp.Items) == 0 {
		t.Fatalf("expected selected context items, got %+v", contextResp)
	}
	if contextResp.Items[0].SourceFile != "memory/2026-03-13.md" {
		t.Fatalf("expected preference memory selected first in context, got %+v", contextResp.Items[0])
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
