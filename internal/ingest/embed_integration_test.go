package ingest

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/store"
)

// mockEmbedder is a deterministic embedder that returns fixed vectors for testing.
type mockEmbedder struct {
	dims    int
	calls   int
	batches [][]string // records batches received
}

func newMockEmbedder(dims int) *mockEmbedder {
	return &mockEmbedder{dims: dims}
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := m.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	m.calls++
	m.batches = append(m.batches, texts)
	result := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, m.dims)
		// Use text length as a simple signal so vectors differ per input.
		vec[0] = float32(len(texts[i]))
		result[i] = vec
	}
	return result, nil
}

func (m *mockEmbedder) Dimensions() int { return m.dims }

// compile-time check that mockEmbedder satisfies the embed.Embedder interface.
var _ embed.Embedder = (*mockEmbedder)(nil)

// ==================== EmbedEngine integration tests ====================

func TestEmbedMemories_ProcessesAndStoresEmbeddings(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Add memories to embed.
	memories := []*store.Memory{
		{Content: "The capital of France is Paris.", SourceFile: "geo.md"},
		{Content: "The Eiffel Tower is located in Paris.", SourceFile: "geo.md"},
		{Content: "Mount Everest is the tallest mountain on Earth.", SourceFile: "geo.md"},
	}
	ids, err := s.AddMemoryBatch(ctx, memories)
	if err != nil {
		t.Fatalf("AddMemoryBatch: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 inserted IDs, got %d", len(ids))
	}

	embedder := newMockEmbedder(384)
	engine := NewEmbedEngine(s, embedder)

	result, err := engine.EmbedMemories(ctx, DefaultEmbedOptions())
	if err != nil {
		t.Fatalf("EmbedMemories: %v", err)
	}

	if result.MemoriesProcessed != 3 {
		t.Errorf("MemoriesProcessed = %d, want 3", result.MemoriesProcessed)
	}
	if result.EmbeddingsAdded != 3 {
		t.Errorf("EmbeddingsAdded = %d, want 3", result.EmbeddingsAdded)
	}
	if len(result.Errors) != 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}

	// Verify embeddings are actually stored.
	for _, id := range ids {
		vec, err := s.GetEmbedding(ctx, id)
		if err != nil {
			t.Errorf("GetEmbedding(%d): %v", id, err)
		}
		if len(vec) != 384 {
			t.Errorf("embedding dims = %d, want 384", len(vec))
		}
	}
}

func TestEmbedMemories_SkipsAlreadyEmbedded(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	m := &store.Memory{Content: "Pre-embedded content here.", SourceFile: "test.md"}
	id, err := s.AddMemory(ctx, m)
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Manually store an embedding so this memory is already covered.
	preVec := make([]float32, 384)
	preVec[0] = 99.0
	if err := s.AddEmbedding(ctx, id, preVec); err != nil {
		t.Fatalf("AddEmbedding: %v", err)
	}

	// Add a second memory without an embedding.
	m2 := &store.Memory{Content: "Second memory that needs embedding.", SourceFile: "test.md"}
	if _, err := s.AddMemory(ctx, m2); err != nil {
		t.Fatalf("AddMemory m2: %v", err)
	}

	embedder := newMockEmbedder(384)
	engine := NewEmbedEngine(s, embedder)

	result, err := engine.EmbedMemories(ctx, DefaultEmbedOptions())
	if err != nil {
		t.Fatalf("EmbedMemories: %v", err)
	}

	// Only 1 memory should be processed (the one without an embedding).
	if result.MemoriesProcessed != 1 {
		t.Errorf("MemoriesProcessed = %d, want 1", result.MemoriesProcessed)
	}
	if result.EmbeddingsAdded != 1 {
		t.Errorf("EmbeddingsAdded = %d, want 1", result.EmbeddingsAdded)
	}

	// The pre-existing embedding must be unchanged.
	vec, err := s.GetEmbedding(ctx, id)
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	if vec[0] != 99.0 {
		t.Errorf("pre-existing embedding mutated: vec[0] = %f, want 99.0", vec[0])
	}
}

func TestEmbedMemories_NoMemories(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	embedder := newMockEmbedder(384)
	engine := NewEmbedEngine(s, embedder)

	result, err := engine.EmbedMemories(ctx, DefaultEmbedOptions())
	if err != nil {
		t.Fatalf("EmbedMemories on empty store: %v", err)
	}
	if result.MemoriesProcessed != 0 {
		t.Errorf("MemoriesProcessed = %d, want 0", result.MemoriesProcessed)
	}
	if embedder.calls != 0 {
		t.Errorf("embedder called %d times, want 0", embedder.calls)
	}
}

func TestEmbedMemories_BatchSizeRespected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Insert 7 memories to test batching with batch size 3.
	var memories []*store.Memory
	for i := 0; i < 7; i++ {
		memories = append(memories, &store.Memory{
			Content:    "Memory content number one two three four five six seven",
			SourceFile: "batch.md",
		})
		// Make each content unique to avoid dedup hash collision.
		memories[i].Content += " " + string(rune('A'+i))
	}
	if _, err := s.AddMemoryBatch(ctx, memories); err != nil {
		t.Fatalf("AddMemoryBatch: %v", err)
	}

	embedder := newMockEmbedder(384)
	engine := NewEmbedEngine(s, embedder)

	opts := EmbedOptions{BatchSize: 3}
	result, err := engine.EmbedMemories(ctx, opts)
	if err != nil {
		t.Fatalf("EmbedMemories: %v", err)
	}

	if result.EmbeddingsAdded != 7 {
		t.Errorf("EmbeddingsAdded = %d, want 7", result.EmbeddingsAdded)
	}

	// With 7 memories and batch size 3 → 3 batches: [3, 3, 1]
	if embedder.calls != 3 {
		t.Errorf("embedder.calls = %d, want 3", embedder.calls)
	}
}
