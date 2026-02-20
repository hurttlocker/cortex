package ingest

import (
	"context"
	"sync"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestProcessMemory_ConcurrentIdenticalImports_NoUniqueErrors(t *testing.T) {
	s := newTestStore(t)
	engine := NewEngine(s)
	ctx := context.Background()

	raw := RawMemory{
		Content:       "concurrent identical import content",
		SourceFile:    "capture.md",
		SourceLine:    1,
		SourceSection: "capture",
	}
	opts := ImportOptions{}

	const workers = 100
	start := make(chan struct{})
	errCh := make(chan error, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			if err := engine.processMemory(ctx, raw, opts, &ImportResult{}); err != nil {
				errCh <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("expected no errors from concurrent identical imports, got: %v", err)
		}
	}

	memories, err := s.ListMemories(ctx, store.ListOpts{Limit: 10})
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected exactly 1 stored memory, got %d", len(memories))
	}
}
