package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

// AddEmbedding stores an embedding vector for a memory.
// Replaces any existing embedding for the same memory_id.
func (s *SQLiteStore) AddEmbedding(ctx context.Context, memoryID int64, vector []float32) error {
	blob := float32ToBytes(vector)
	dims := len(vector)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO embeddings (memory_id, vector, dimensions) VALUES (?, ?, ?)
		 ON CONFLICT(memory_id) DO UPDATE SET vector = excluded.vector, dimensions = excluded.dimensions`,
		memoryID, blob, dims,
	)
	if err != nil {
		return fmt.Errorf("storing embedding for memory %d: %w", memoryID, err)
	}
	return nil
}

// GetEmbedding retrieves the embedding vector for a memory.
func (s *SQLiteStore) GetEmbedding(ctx context.Context, memoryID int64) ([]float32, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		"SELECT vector FROM embeddings WHERE memory_id = ?", memoryID,
	).Scan(&blob)
	if err != nil {
		return nil, fmt.Errorf("getting embedding for memory %d: %w", memoryID, err)
	}
	return bytesToFloat32(blob), nil
}

// SearchEmbedding performs brute-force cosine similarity search across all embeddings.
// Returns top-K results above minSimilarity threshold.
func (s *SQLiteStore) SearchEmbedding(ctx context.Context, query []float32, limit int, minSimilarity float64) ([]*SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT e.memory_id, e.vector, m.id, m.content, m.source_file, m.source_line,
		        m.source_section, m.content_hash, m.imported_at, m.updated_at
		 FROM embeddings e
		 JOIN memories m ON e.memory_id = m.id
		 WHERE m.deleted_at IS NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying embeddings: %w", err)
	}
	defer rows.Close()

	type scored struct {
		result *SearchResult
		score  float64
	}

	var candidates []scored

	for rows.Next() {
		var blob []byte
		var memID int64
		m := &Memory{}

		if err := rows.Scan(&memID, &blob, &m.ID, &m.Content, &m.SourceFile,
			&m.SourceLine, &m.SourceSection, &m.ContentHash,
			&m.ImportedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning embedding row: %w", err)
		}

		vec := bytesToFloat32(blob)
		sim := cosineSimilarity(query, vec)

		if sim >= minSimilarity {
			candidates = append(candidates, scored{
				result: &SearchResult{Memory: *m, Score: sim},
				score:  sim,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by score descending (simple insertion sort for small N)
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].score > candidates[j-1].score; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}

	// Take top-K
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	results := make([]*SearchResult, len(candidates))
	for i, c := range candidates {
		results[i] = c.result
	}
	return results, nil
}

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// float32ToBytes converts a float32 slice to a byte slice (little-endian).
func float32ToBytes(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// bytesToFloat32 converts a byte slice back to float32 slice (little-endian).
func bytesToFloat32(buf []byte) []float32 {
	vec := make([]float32, len(buf)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return vec
}
