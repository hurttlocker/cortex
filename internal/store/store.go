// Package store provides the SQLite + FTS5 storage layer for Cortex.
//
// All memory data lives in a single SQLite database file, including:
// - Raw imported content with provenance
// - Extracted facts (key-value pairs, relationships, etc.)
// - FTS5 full-text search index
// - Embedding vectors for semantic search
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultDBPath is the default database location.
const DefaultDBPath = "~/.cortex/cortex.db"

// DefaultBatchSize is the default batch size for bulk operations.
const DefaultBatchSize = 500

// DefaultEmbeddingDimensions is the default embedding vector size (MiniLM).
const DefaultEmbeddingDimensions = 384

// Memory represents a single imported memory unit.
type Memory struct {
	ID            int64
	Content       string
	SourceFile    string
	SourceLine    int
	SourceSection string
	ContentHash   string
	ImportedAt    time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// Fact represents an extracted fact from a memory.
type Fact struct {
	ID             int64
	MemoryID       int64
	Subject        string
	Predicate      string
	Object         string
	FactType       string
	Confidence     float64
	DecayRate      float64
	LastReinforced time.Time
	SourceQuote    string
	CreatedAt      time.Time
}

// MemoryEvent represents an entry in the append-only event log.
type MemoryEvent struct {
	ID        int64
	EventType string
	FactID    int64
	OldValue  string
	NewValue  string
	Source    string
	CreatedAt time.Time
}

// ListOpts controls pagination and filtering for List operations.
type ListOpts struct {
	Limit    int
	Offset   int
	SortBy   string // "date", "confidence", "recalls"
	FactType string // filter by fact type
}

// SearchResult holds a search result with score and optional snippet.
type SearchResult struct {
	Memory  Memory
	Score   float64
	Snippet string
}

// StoreStats holds observability statistics about the store.
type StoreStats struct {
	MemoryCount    int64
	FactCount      int64
	EmbeddingCount int64
	EventCount     int64
	DBSizeBytes    int64
}

// StoreConfig holds configuration for NewStore.
type StoreConfig struct {
	DBPath              string
	BatchSize           int
	EmbeddingDimensions int
}

// Store defines the core storage interface.
type Store interface {
	// Memories
	AddMemory(ctx context.Context, m *Memory) (int64, error)
	GetMemory(ctx context.Context, id int64) (*Memory, error)
	ListMemories(ctx context.Context, opts ListOpts) ([]*Memory, error)
	DeleteMemory(ctx context.Context, id int64) error

	// Batch
	AddMemoryBatch(ctx context.Context, memories []*Memory) ([]int64, error)

	// Facts
	AddFact(ctx context.Context, f *Fact) (int64, error)
	GetFact(ctx context.Context, id int64) (*Fact, error)
	ListFacts(ctx context.Context, opts ListOpts) ([]*Fact, error)
	UpdateFactConfidence(ctx context.Context, id int64, confidence float64) error
	ReinforceFact(ctx context.Context, id int64) error

	// Search
	SearchFTS(ctx context.Context, query string, limit int) ([]*SearchResult, error)
	SearchEmbedding(ctx context.Context, vector []float32, limit int, minSimilarity float64) ([]*SearchResult, error)

	// Embeddings
	AddEmbedding(ctx context.Context, memoryID int64, vector []float32) error
	GetEmbedding(ctx context.Context, memoryID int64) ([]float32, error)

	// Deduplication
	FindByHash(ctx context.Context, hash string) (*Memory, error)

	// Events
	LogEvent(ctx context.Context, e *MemoryEvent) error

	// Observability
	Stats(ctx context.Context) (*StoreStats, error)
	StaleFacts(ctx context.Context, maxConfidence float64, daysSinceRecall int) ([]*Fact, error)

	// Maintenance
	Vacuum(ctx context.Context) error
	Close() error
}

// SQLiteStore implements Store using SQLite + FTS5.
type SQLiteStore struct {
	db        *sql.DB
	dbPath    string
	batchSize int
	embDims   int
}

// NewStore creates a new SQLite-backed Store.
// Pass ":memory:" for in-memory databases (testing).
func NewStore(cfg StoreConfig) (Store, error) {
	if cfg.DBPath == "" {
		cfg.DBPath = expandPath(DefaultDBPath)
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.EmbeddingDimensions <= 0 {
		cfg.EmbeddingDimensions = DefaultEmbeddingDimensions
	}

	// Create parent directory for non-memory databases
	if cfg.DBPath != ":memory:" {
		dir := filepath.Dir(cfg.DBPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("creating db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Enable WAL mode and foreign keys
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", p, err)
		}
	}

	s := &SQLiteStore{
		db:        db,
		dbPath:    cfg.DBPath,
		batchSize: cfg.BatchSize,
		embDims:   cfg.EmbeddingDimensions,
	}

	// Run migrations
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return s, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Vacuum runs VACUUM on the database. Manual only — never auto-vacuum.
func (s *SQLiteStore) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return err
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
