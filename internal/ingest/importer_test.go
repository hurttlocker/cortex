package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestCosineTextSimilarity(t *testing.T) {
	a := vectorizeText("deploy pipeline run tests first")
	b := vectorizeText("deploy pipeline run tests first")
	c := vectorizeText("banana mango tropical fruit")

	if sim := cosineTextSimilarity(a, b); sim < 0.99 {
		t.Fatalf("expected identical vectors to be ~1.0, got %.3f", sim)
	}
	if sim := cosineTextSimilarity(a, c); sim > 0.35 {
		t.Fatalf("expected unrelated vectors to be low similarity, got %.3f", sim)
	}
}

func TestFindNearDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Deployment checklist: run tests before merge",
		SourceFile: "auto-capture.md",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	opts := ImportOptions{
		CaptureDedupeEnabled:       true,
		CaptureSimilarityThreshold: 0.80,
		CaptureDedupeWindowSec:     600,
	}

	dup, score, _, err := findNearDuplicate(ctx, s, "Deployment checklist run tests before merge", opts)
	if err != nil {
		t.Fatalf("findNearDuplicate: %v", err)
	}
	if !dup {
		t.Fatalf("expected near duplicate to be detected (score=%.3f)", score)
	}
}

func TestProcessMemory_NearDuplicateSuppressed(t *testing.T) {
	s := newTestStore(t)
	engine := NewEngine(s)
	ctx := context.Background()

	// Existing recent capture.
	_, err := s.AddMemory(ctx, &store.Memory{
		Content:    "ok sounds good let's deploy after tests",
		SourceFile: "auto-capture.md",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	result := &ImportResult{}
	raw := RawMemory{
		Content:       "ok sounds good lets deploy after tests",
		SourceFile:    "auto-capture.md",
		SourceLine:    1,
		SourceSection: "capture",
	}
	opts := ImportOptions{
		CaptureDedupeEnabled:       true,
		CaptureSimilarityThreshold: 0.85,
		CaptureDedupeWindowSec:     int((5 * time.Minute).Seconds()),
	}

	if err := engine.processMemory(ctx, raw, opts, result); err != nil {
		t.Fatalf("processMemory: %v", err)
	}

	if result.MemoriesNearDuped != 1 {
		t.Fatalf("expected 1 near-duplicate suppression, got %d", result.MemoriesNearDuped)
	}

	memories, err := s.ListMemories(ctx, store.ListOpts{Limit: 10})
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected memory count to remain 1, got %d", len(memories))
	}
}

func TestShouldSkipLowSignalCapture(t *testing.T) {
	opts := ImportOptions{CaptureLowSignalEnabled: true, CaptureMinChars: 20}

	if !shouldSkipLowSignalCapture("### User\nHEARTBEAT_OK\n\n### Assistant\nok", opts) {
		t.Fatal("expected HEARTBEAT_OK capture to be filtered")
	}
	if !shouldSkipLowSignalCapture("### User\nFire the test\n\n### Assistant\nSounds good", opts) {
		t.Fatal("expected trivial command capture to be filtered")
	}
	if shouldSkipLowSignalCapture("### User\nQ prefers Sonnet for coding tasks\n\n### Assistant\nSaved", opts) {
		t.Fatal("did not expect meaningful preference capture to be filtered")
	}
}

func TestProcessMemory_LowSignalSuppressed(t *testing.T) {
	s := newTestStore(t)
	engine := NewEngine(s)
	ctx := context.Background()

	result := &ImportResult{}
	raw := RawMemory{
		Content:       "### User\nHEARTBEAT_OK\n\n### Assistant\nok",
		SourceFile:    "auto-capture.md",
		SourceLine:    1,
		SourceSection: "capture",
	}
	opts := ImportOptions{CaptureLowSignalEnabled: true, CaptureMinChars: 20}

	if err := engine.processMemory(ctx, raw, opts, result); err != nil {
		t.Fatalf("processMemory: %v", err)
	}
	if result.MemoriesNew != 0 {
		t.Fatalf("expected no new memory for low-signal capture, got %d", result.MemoriesNew)
	}
	if result.MemoriesUnchanged != 1 {
		t.Fatalf("expected unchanged counter to increment, got %d", result.MemoriesUnchanged)
	}
}
