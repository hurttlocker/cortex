package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
)

// BenchmarkSearchFTS measures FTS search latency at various scales.
func BenchmarkSearchFTS(b *testing.B) {
	ctx := context.Background()
	s := setupBenchStore(b, 1000)
	defer s.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.SearchFTS(ctx, "test query benchmark", 10)
	}
}

// BenchmarkStaleFacts measures stale fact query latency.
func BenchmarkStaleFacts(b *testing.B) {
	ctx := context.Background()
	s := setupBenchStore(b, 1000)
	defer s.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.StaleFacts(ctx, 0.5, 30)
	}
}

// BenchmarkConflicts measures conflict detection latency.
func BenchmarkConflicts(b *testing.B) {
	ctx := context.Background()
	s := setupBenchStore(b, 1000)
	defer s.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.GetAttributeConflictsLimit(ctx, 20)
	}
}

// BenchmarkSearchFTS_10K measures FTS at 10K memories.
func BenchmarkSearchFTS_10K(b *testing.B) {
	ctx := context.Background()
	s := setupBenchStore(b, 10000)
	defer s.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.SearchFTS(ctx, "important decision about trading strategy", 10)
	}
}

// BenchmarkAddMemoryWithFacts measures insert throughput.
func BenchmarkAddMemoryWithFacts(b *testing.B) {
	ctx := context.Background()
	s := setupBenchStore(b, 0)
	defer s.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("bench-insert-%d", i))))
		m := &Memory{
			Content:     fmt.Sprintf("Benchmark memory %d with some realistic content about project decisions and configurations", i),
			ContentHash: hash,
		}
		id, err := s.AddMemory(ctx, m)
		if err != nil {
			b.Fatalf("AddMemory: %v", err)
		}

		// Add 10 facts per memory (realistic post-governor ratio)
		for j := 0; j < 10; j++ {
			f := &Fact{
				MemoryID:   id,
				Subject:    fmt.Sprintf("subject-%d", i),
				Predicate:  fmt.Sprintf("predicate-%d", j),
				Object:     fmt.Sprintf("value for memory %d fact %d with enough content", i, j),
				FactType:   "kv",
				Confidence: 0.9,
			}
			if _, err := s.AddFact(ctx, f); err != nil {
				b.Fatalf("AddFact: %v", err)
			}
		}
	}
}

func setupBenchStore(b *testing.B, memoryCount int) *SQLiteStore {
	b.Helper()
	tmpDir := b.TempDir()
	dbPath := tmpDir + "/bench.db"

	cfg := StoreConfig{DBPath: dbPath}
	s, err := NewStore(cfg)
	if err != nil {
		b.Fatalf("NewStore: %v", err)
	}

	ss, ok := s.(*SQLiteStore)
	if !ok {
		b.Fatal("expected SQLiteStore")
	}

	ctx := context.Background()

	// Seed data
	for i := 0; i < memoryCount; i++ {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("memory-%d", i))))
		m := &Memory{
			Content:     fmt.Sprintf("Memory %d: This is a realistic test memory about project %d with decisions about architecture and trading strategy configuration number %d", i, i%50, i),
			ContentHash: hash,
			SourceFile:  fmt.Sprintf("memory/2026-02-%02d.md", (i%28)+1),
		}
		id, err := ss.AddMemory(ctx, m)
		if err != nil {
			b.Fatalf("seed AddMemory %d: %v", i, err)
		}

		// 10 facts per memory (realistic post-governor)
		factTypes := []string{"kv", "identity", "decision", "preference", "temporal", "state", "location", "relationship"}
		for j := 0; j < 10; j++ {
			ft := factTypes[j%len(factTypes)]
			f := &Fact{
				MemoryID:   id,
				Subject:    fmt.Sprintf("entity-%d", i%200),
				Predicate:  fmt.Sprintf("attr-%d", j),
				Object:     fmt.Sprintf("value for entity-%d attr-%d with some detail", i%200, j),
				FactType:   ft,
				Confidence: 0.85 + float64(j)*0.01,
			}
			if _, err := ss.AddFact(ctx, f); err != nil {
				b.Fatalf("seed AddFact %d/%d: %v", i, j, err)
			}
		}
	}

	return ss
}
