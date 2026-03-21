package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/store"
)

// EmbedOptions configures an embedding operation.
type EmbedOptions struct {
	BatchSize         int                                             // Number of texts to embed per API call (default: 50)
	Workers           int                                             // Concurrent embedding workers (default: 2)
	AdaptiveBatching  bool                                            // Halve batch size on failure (default: true)
	HealthCheckEvery  int                                             // Run health check every N batches (default: 5, 0 = disabled)
	ProgressFn        func(current, total int)                        // Progress callback
	VerboseProgressFn func(current, total, batchSize int, msg string) // Detailed progress
	SourceFile        string                                          // Optional source_file scope for SQL-side filtering
	FilterFn          func(memory *store.Memory) bool                 // Optional filter for which memories to embed
}

// DefaultEmbedOptions returns sensible defaults for embedding.
func DefaultEmbedOptions() EmbedOptions {
	return EmbedOptions{
		BatchSize:        50,
		Workers:          2,
		AdaptiveBatching: true,
		HealthCheckEvery: 5,
	}
}

// EmbedResult summarizes an embedding operation.
type EmbedResult struct {
	MemoriesProcessed int
	EmbeddingsAdded   int
	EmbeddingsSkipped int // Already had embeddings
	Errors            []EmbedError
}

// EmbedError records a non-fatal error during embedding.
type EmbedError struct {
	MemoryID int64
	Message  string
}

// EmbedEngine handles batch embedding of memories.
type EmbedEngine struct {
	store    store.Store
	embedder embed.Embedder
}

// NewEmbedEngine creates a new embedding engine.
func NewEmbedEngine(s store.Store, e embed.Embedder) *EmbedEngine {
	return &EmbedEngine{
		store:    s,
		embedder: e,
	}
}

// EmbedMemories generates and stores embeddings for memories that don't have them yet.
// Features adaptive batch sizing (halves on failure), health checks, and resilient retry.
func (e *EmbedEngine) EmbedMemories(ctx context.Context, opts EmbedOptions) (*EmbedResult, error) {
	result := &EmbedResult{}

	// Get all memories that don't have embeddings yet
	memories, err := e.getMemoriesWithoutEmbeddings(ctx, opts.SourceFile, opts.FilterFn)
	if err != nil {
		return nil, fmt.Errorf("getting memories without embeddings: %w", err)
	}

	if len(memories) == 0 {
		return result, nil
	}

	result.MemoriesProcessed = len(memories)

	workers := opts.Workers
	if workers <= 0 {
		workers = 2
	}
	if workers == 1 {
		return e.embedMemoriesSequential(ctx, memories, opts, result)
	}
	return e.embedMemoriesConcurrent(ctx, memories, opts, result)
}

func (e *EmbedEngine) embedMemoriesSequential(ctx context.Context, memories []*store.Memory, opts EmbedOptions, result *EmbedResult) (*EmbedResult, error) {

	// Process in batches with adaptive sizing
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	originalBatchSize := batchSize
	consecutiveFailures := 0
	batchCount := 0

	// Log resume context
	if opts.VerboseProgressFn != nil {
		opts.VerboseProgressFn(0, len(memories), batchSize, fmt.Sprintf("Starting: %d memories to embed", len(memories)))
	}

	i := 0
	for i < len(memories) {
		// Health check every N batches (if embedder supports it)
		if opts.HealthCheckEvery > 0 && batchCount > 0 && batchCount%opts.HealthCheckEvery == 0 {
			if checker, ok := e.embedder.(interface{ HealthCheck(context.Context) error }); ok {
				if hcErr := checker.HealthCheck(ctx); hcErr != nil {
					if opts.VerboseProgressFn != nil {
						opts.VerboseProgressFn(i, len(memories), batchSize, fmt.Sprintf("Health check failed: %v — waiting 10s", hcErr))
					}
					// Wait and retry health check up to 3 times
					healthy := false
					for attempt := 0; attempt < 3; attempt++ {
						select {
						case <-ctx.Done():
							return result, ctx.Err()
						case <-time.After(10 * time.Second):
						}
						if checker.HealthCheck(ctx) == nil {
							healthy = true
							break
						}
					}
					if !healthy {
						return result, fmt.Errorf("embedding provider unhealthy after 3 health check retries at %d/%d memories", i, len(memories))
					}
				}
			}
		}

		end := i + batchSize
		if end > len(memories) {
			end = len(memories)
		}

		batch := memories[i:end]
		batchResult, err := e.processBatch(ctx, batch)
		if err != nil {
			consecutiveFailures++

			// Adaptive batch sizing: halve on failure
			if opts.AdaptiveBatching && batchSize > 1 {
				newSize := batchSize / 2
				if newSize < 1 {
					newSize = 1
				}
				if opts.VerboseProgressFn != nil {
					opts.VerboseProgressFn(i, len(memories), newSize,
						fmt.Sprintf("Batch failed (%v) — reducing batch size %d→%d", err, batchSize, newSize))
				}
				batchSize = newSize

				// Wait before retry with smaller batch (exponential: 2s, 4s, 8s, max 30s)
				backoff := time.Duration(1<<consecutiveFailures) * time.Second
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
				select {
				case <-ctx.Done():
					return result, ctx.Err()
				case <-time.After(backoff):
				}

				// Don't advance i — retry the same position with smaller batch
				continue
			}

			// At batch size 1 or adaptive disabled: fall back to individual embedding
			for _, memory := range batch {
				singleResult, singleErr := e.processBatch(ctx, []*store.Memory{memory})
				if singleErr != nil {
					// Check if retryable
					if embed.IsRetryableError(singleErr) && consecutiveFailures < 10 {
						// Wait and retry this single memory
						select {
						case <-ctx.Done():
							return result, ctx.Err()
						case <-time.After(5 * time.Second):
						}
						retryResult, retryErr := e.processBatch(ctx, []*store.Memory{memory})
						if retryErr != nil {
							result.Errors = append(result.Errors, EmbedError{
								MemoryID: memory.ID,
								Message:  retryErr.Error(),
							})
						} else {
							result.EmbeddingsAdded += retryResult.Added
							result.EmbeddingsSkipped += retryResult.Skipped
						}
					} else {
						result.Errors = append(result.Errors, EmbedError{
							MemoryID: memory.ID,
							Message:  singleErr.Error(),
						})
					}
				} else {
					result.EmbeddingsAdded += singleResult.Added
					result.EmbeddingsSkipped += singleResult.Skipped
					result.Errors = append(result.Errors, singleResult.Errors...)
				}
			}

			if opts.ProgressFn != nil {
				opts.ProgressFn(end, len(memories))
			}
			i = end
			batchCount++
			continue
		}

		// Success — reset failure tracking
		consecutiveFailures = 0

		// Gradually restore batch size after success streak
		if opts.AdaptiveBatching && batchSize < originalBatchSize && batchCount%3 == 0 {
			newSize := batchSize * 2
			if newSize > originalBatchSize {
				newSize = originalBatchSize
			}
			if newSize != batchSize {
				if opts.VerboseProgressFn != nil {
					opts.VerboseProgressFn(end, len(memories), newSize,
						fmt.Sprintf("Success streak — restoring batch size %d→%d", batchSize, newSize))
				}
				batchSize = newSize
			}
		}

		result.EmbeddingsAdded += batchResult.Added
		result.EmbeddingsSkipped += batchResult.Skipped
		result.Errors = append(result.Errors, batchResult.Errors...)

		// Progress callbacks
		if opts.VerboseProgressFn != nil {
			opts.VerboseProgressFn(end, len(memories), batchSize, "")
		}
		if opts.ProgressFn != nil {
			opts.ProgressFn(end, len(memories))
		}

		i = end
		batchCount++

		// Check for cancellation
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
	}

	return result, nil
}

func (e *EmbedEngine) embedMemoriesConcurrent(ctx context.Context, memories []*store.Memory, opts EmbedOptions, result *EmbedResult) (*EmbedResult, error) {
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 2
	}

	if opts.VerboseProgressFn != nil {
		opts.VerboseProgressFn(0, len(memories), batchSize, fmt.Sprintf("Starting: %d memories to embed (%d workers)", len(memories), workers))
	}

	type workResult struct {
		processed int
		result    *batchResult
		err       error
	}

	jobCh := make(chan []*store.Memory)
	resultCh := make(chan workResult, workers)

	for i := 0; i < workers; i++ {
		go func() {
			for batch := range jobCh {
				br, err := e.processBatchAdaptive(ctx, batch, opts)
				resultCh <- workResult{processed: len(batch), result: br, err: err}
			}
		}()
	}

	totalBatches := 0
	for i := 0; i < len(memories); i += batchSize {
		end := i + batchSize
		if end > len(memories) {
			end = len(memories)
		}
		totalBatches++
	}
	go func() {
		defer close(jobCh)
		for i := 0; i < len(memories); i += batchSize {
			end := i + batchSize
			if end > len(memories) {
				end = len(memories)
			}
			jobCh <- memories[i:end]
		}
	}()

	processed := 0
	for i := 0; i < totalBatches; i++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case wr := <-resultCh:
			if wr.err != nil {
				return result, wr.err
			}
			if wr.result != nil {
				result.EmbeddingsAdded += wr.result.Added
				result.EmbeddingsSkipped += wr.result.Skipped
				result.Errors = append(result.Errors, wr.result.Errors...)
			}
			processed += wr.processed
			if opts.ProgressFn != nil {
				opts.ProgressFn(processed, len(memories))
			}
			if opts.VerboseProgressFn != nil {
				opts.VerboseProgressFn(processed, len(memories), batchSize, "")
			}
		}
	}

	return result, nil
}

// batchResult holds results for a single batch.
type batchResult struct {
	Added   int
	Skipped int
	Errors  []EmbedError
}

// processBatch processes a single batch of memories.
func (e *EmbedEngine) processBatch(ctx context.Context, memories []*store.Memory) (*batchResult, error) {
	result := &batchResult{}

	// Extract texts with context enrichment (Issue #26).
	// Prepend source file stem + section header to give the embedding model
	// topic/source signal that raw chunk text may lack.
	// Example: "[2026-02-18 > Cortex Audit] Conflicts query hanging..."
	texts := make([]string, len(memories))
	for i, memory := range memories {
		texts[i] = store.EnrichedContent(memory.Content, memory.SourceFile, memory.SourceSection)
	}

	// Generate embeddings for batch
	embeddings, err := e.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("generating embeddings: %w", err)
	}

	if len(embeddings) != len(memories) {
		return nil, fmt.Errorf("embedding count mismatch: got %d, expected %d", len(embeddings), len(memories))
	}

	// Store each embedding
	for i, memory := range memories {
		embedding := embeddings[i]
		if len(embedding) == 0 {
			result.Errors = append(result.Errors, EmbedError{
				MemoryID: memory.ID,
				Message:  "empty embedding returned",
			})
			continue
		}

		err := e.store.AddEmbedding(ctx, memory.ID, embedding)
		if err != nil {
			result.Errors = append(result.Errors, EmbedError{
				MemoryID: memory.ID,
				Message:  fmt.Sprintf("storing embedding: %v", err),
			})
			continue
		}

		result.Added++
	}

	return result, nil
}

func (e *EmbedEngine) processBatchAdaptive(ctx context.Context, memories []*store.Memory, opts EmbedOptions) (*batchResult, error) {
	result, err := e.processBatch(ctx, memories)
	if err == nil {
		return result, nil
	}

	if opts.AdaptiveBatching && len(memories) > 1 {
		mid := len(memories) / 2
		left, err := e.processBatchAdaptive(ctx, memories[:mid], opts)
		if err != nil {
			return nil, err
		}
		right, err := e.processBatchAdaptive(ctx, memories[mid:], opts)
		if err != nil {
			return nil, err
		}
		return mergeBatchResults(left, right), nil
	}

	if len(memories) == 1 {
		single := &batchResult{}
		finalErr := err
		if embed.IsRetryableError(err) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
			}
			retryResult, retryErr := e.processBatch(ctx, memories)
			if retryErr == nil {
				return retryResult, nil
			}
			finalErr = retryErr
		}
		single.Errors = append(single.Errors, EmbedError{
			MemoryID: memories[0].ID,
			Message:  finalErr.Error(),
		})
		return single, nil
	}

	return nil, err
}

func mergeBatchResults(left, right *batchResult) *batchResult {
	merged := &batchResult{}
	if left != nil {
		merged.Added += left.Added
		merged.Skipped += left.Skipped
		merged.Errors = append(merged.Errors, left.Errors...)
	}
	if right != nil {
		merged.Added += right.Added
		merged.Skipped += right.Skipped
		merged.Errors = append(merged.Errors, right.Errors...)
	}
	return merged
}

// getMemoriesWithoutEmbeddings retrieves all memories that don't have embeddings yet.
func (e *EmbedEngine) getMemoriesWithoutEmbeddings(ctx context.Context, sourceFile string, filterFn func(*store.Memory) bool) ([]*store.Memory, error) {
	// Get IDs of memories without embeddings efficiently (no N+1 queries)
	var (
		ids []int64
		err error
	)
	if sourceFile != "" {
		ids, err = e.store.ListMemoryIDsWithoutEmbeddingsBySourceFile(ctx, sourceFile, 10000)
	} else {
		ids, err = e.store.ListMemoryIDsWithoutEmbeddings(ctx, 10000) // Reasonable batch limit
	}
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return nil, nil
	}

	// Fetch all those memories in a single query
	memories, err := e.store.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Apply filter if provided
	if filterFn == nil {
		return memories, nil
	}

	var result []*store.Memory
	for _, memory := range memories {
		if filterFn(memory) {
			result = append(result, memory)
		}
	}

	return result, nil
}
