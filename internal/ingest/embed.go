package ingest

import (
	"context"
	"fmt"

	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/store"
)

// EmbedOptions configures an embedding operation.
type EmbedOptions struct {
	BatchSize  int                             // Number of texts to embed per API call (default: 50)
	ProgressFn func(current, total int)        // Progress callback
	FilterFn   func(memory *store.Memory) bool // Optional filter for which memories to embed
}

// DefaultEmbedOptions returns sensible defaults for embedding.
func DefaultEmbedOptions() EmbedOptions {
	return EmbedOptions{
		BatchSize: 50,
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
// This is designed to be called after import to add embeddings to newly imported memories.
func (e *EmbedEngine) EmbedMemories(ctx context.Context, opts EmbedOptions) (*EmbedResult, error) {
	result := &EmbedResult{}

	// Get all memories that don't have embeddings yet
	memories, err := e.getMemoriesWithoutEmbeddings(ctx, opts.FilterFn)
	if err != nil {
		return nil, fmt.Errorf("getting memories without embeddings: %w", err)
	}

	if len(memories) == 0 {
		return result, nil
	}

	result.MemoriesProcessed = len(memories)

	// Process in batches
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	for i := 0; i < len(memories); i += batchSize {
		end := i + batchSize
		if end > len(memories) {
			end = len(memories)
		}

		batch := memories[i:end]
		batchResult, err := e.processBatch(ctx, batch)
		if err != nil {
			// Log error but continue with next batch
			for _, memory := range batch {
				result.Errors = append(result.Errors, EmbedError{
					MemoryID: memory.ID,
					Message:  err.Error(),
				})
			}
			continue
		}

		result.EmbeddingsAdded += batchResult.Added
		result.EmbeddingsSkipped += batchResult.Skipped
		result.Errors = append(result.Errors, batchResult.Errors...)

		// Progress callback
		if opts.ProgressFn != nil {
			opts.ProgressFn(end, len(memories))
		}

		// Check for cancellation
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
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

	// Extract texts and memory IDs for batch embedding
	texts := make([]string, len(memories))
	for i, memory := range memories {
		texts[i] = memory.Content
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

// getMemoriesWithoutEmbeddings retrieves all memories that don't have embeddings yet.
func (e *EmbedEngine) getMemoriesWithoutEmbeddings(ctx context.Context, filterFn func(*store.Memory) bool) ([]*store.Memory, error) {
	// Get IDs of memories without embeddings efficiently (no N+1 queries)
	ids, err := e.store.ListMemoryIDsWithoutEmbeddings(ctx, 10000) // Reasonable batch limit
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
