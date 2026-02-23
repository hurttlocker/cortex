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
	"strings"
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
	Project       string    // Project/thread tag for scoped search (e.g., "trading", "eyes-web")
	MemoryClass   string    // Optional class label (rule, decision, preference, identity, status, scratch)
	Metadata      *Metadata // Structured metadata (session, channel, agent, model, etc.)
	ImportedAt    time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// Metadata holds structured context about how a memory was created.
// Stored as JSON in the metadata column. All fields are optional.
type Metadata struct {
	SessionKey     string `json:"session_key,omitempty"`  // e.g., "agent:main:main"
	Channel        string `json:"channel,omitempty"`      // e.g., "discord", "telegram"
	ChannelID      string `json:"channel_id,omitempty"`   // e.g., "1473406695219658964"
	ChannelName    string `json:"channel_name,omitempty"` // e.g., "#x"
	AgentID        string `json:"agent_id,omitempty"`     // e.g., "main", "sage", "hawk"
	AgentName      string `json:"agent_name,omitempty"`   // e.g., "mister", "sage"
	Model          string `json:"model,omitempty"`        // e.g., "anthropic/claude-opus-4-6"
	InputTokens    int    `json:"input_tokens,omitempty"` // Token usage
	OutputTokens   int    `json:"output_tokens,omitempty"`
	MessageCount   int    `json:"message_count,omitempty"`   // Messages in the conversation
	Surface        string `json:"surface,omitempty"`         // e.g., "discord", "telegram", "webchat"
	ChatType       string `json:"chat_type,omitempty"`       // e.g., "channel", "group", "dm"
	TimestampStart string `json:"timestamp_start,omitempty"` // ISO 8601
	TimestampEnd   string `json:"timestamp_end,omitempty"`
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
	SupersededBy   *int64 // Fact ID that superseded this fact (nil = active)
	AgentID        string // Which agent created this fact (empty = global, visible to all)
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
	Limit             int
	Offset            int
	SortBy            string   // "date", "confidence", "recalls"
	FactType          string   // filter by fact type
	SourceFile        string   // filter by source file
	Project           string   // filter by project tag
	MemoryClasses     []string // filter by memory class
	Agent             string   // filter by metadata agent_id
	Channel           string   // filter by metadata channel
	After             string   // filter memories imported after this date (YYYY-MM-DD)
	Before            string   // filter memories imported before this date (YYYY-MM-DD)
	IncludeSuperseded bool     // include superseded facts where relevant
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

// ConfidenceDistribution holds the distribution of effective confidence across facts.
type ConfidenceDistribution struct {
	High   int `json:"high"`   // >= 0.7
	Medium int `json:"medium"` // 0.3 - 0.7
	Low    int `json:"low"`    // < 0.3
	Total  int `json:"total"`
}

// Freshness holds distribution of memories by import date buckets.
type Freshness struct {
	Today     int `json:"today"`
	ThisWeek  int `json:"this_week"`
	ThisMonth int `json:"this_month"`
	Older     int `json:"older"`
}

// Conflict represents two facts that may contradict each other.
type Conflict struct {
	Fact1        Fact    `json:"fact1"`
	Fact2        Fact    `json:"fact2"`
	ConflictType string  `json:"conflict_type"` // "attribute"
	Similarity   float64 `json:"similarity"`
	CrossAgent   bool    `json:"cross_agent,omitempty"` // true if facts from different agents
}

// ProjectInfo holds metadata about a project tag.
type ProjectInfo struct {
	Name        string `json:"name"`
	MemoryCount int    `json:"memory_count"`
	FactCount   int    `json:"fact_count"`
}

// StoreConfig holds configuration for NewStore.
type StoreConfig struct {
	DBPath              string
	BatchSize           int
	EmbeddingDimensions int
	ReadOnly            bool // skip migrations, open for read-only access
}

// Store defines the core storage interface.
type Store interface {
	// Memories
	AddMemory(ctx context.Context, m *Memory) (int64, error)
	GetMemory(ctx context.Context, id int64) (*Memory, error)
	ListMemories(ctx context.Context, opts ListOpts) ([]*Memory, error)
	DeleteMemory(ctx context.Context, id int64) error
	UpdateMemory(ctx context.Context, id int64, content string) error
	UpdateMemoryMetadata(ctx context.Context, id int64, meta *Metadata) error

	// Batch
	AddMemoryBatch(ctx context.Context, memories []*Memory) ([]int64, error)

	// Facts
	AddFact(ctx context.Context, f *Fact) (int64, error)
	GetFact(ctx context.Context, id int64) (*Fact, error)
	ListFacts(ctx context.Context, opts ListOpts) ([]*Fact, error)
	UpdateFactConfidence(ctx context.Context, id int64, confidence float64) error
	ReinforceFact(ctx context.Context, id int64) error
	SupersedeFact(ctx context.Context, oldFactID, newFactID int64, reason string) error
	ReinforceFactsByMemoryIDs(ctx context.Context, memoryIDs []int64) (int, error)
	GetFactsByMemoryIDs(ctx context.Context, memoryIDs []int64) ([]*Fact, error)
	GetFactsByMemoryIDsIncludingSuperseded(ctx context.Context, memoryIDs []int64) ([]*Fact, error)
	DeleteFactsByMemoryID(ctx context.Context, memoryID int64) (int64, error)
	GetConfidenceDistribution(ctx context.Context) (*ConfidenceDistribution, error)

	// Search
	SearchFTS(ctx context.Context, query string, limit int) ([]*SearchResult, error)
	SearchFTSWithProject(ctx context.Context, query string, limit int, project string) ([]*SearchResult, error)
	SearchEmbedding(ctx context.Context, vector []float32, limit int, minSimilarity float64) ([]*SearchResult, error)
	SearchEmbeddingWithProject(ctx context.Context, vector []float32, limit int, minSimilarity float64, project string) ([]*SearchResult, error)

	// Embeddings
	AddEmbedding(ctx context.Context, memoryID int64, vector []float32) error
	GetEmbedding(ctx context.Context, memoryID int64) ([]float32, error)
	DeleteAllEmbeddings(ctx context.Context) (int64, error)
	ListMemoryIDsWithoutEmbeddings(ctx context.Context, limit int) ([]int64, error)
	ListMemoryIDsWithEmbeddings(ctx context.Context, limit int) ([]int64, error)
	GetMemoriesByIDs(ctx context.Context, ids []int64) ([]*Memory, error)
	GetEmbeddingDimensions(ctx context.Context) (int, error)

	// Deduplication
	FindByHash(ctx context.Context, hash string) (*Memory, error)

	// Projects
	ListProjects(ctx context.Context) ([]ProjectInfo, error)
	TagMemories(ctx context.Context, project string, memoryIDs []int64) (int64, error)
	TagMemoriesBySource(ctx context.Context, project string, sourcePattern string) (int64, error)

	// Events
	LogEvent(ctx context.Context, e *MemoryEvent) error

	// Observability
	Stats(ctx context.Context) (*StoreStats, error)
	StaleFacts(ctx context.Context, maxConfidence float64, daysSinceRecall int) ([]*Fact, error)

	// Enhanced observability methods
	GetSourceCount(ctx context.Context) (int, error)
	GetAverageConfidence(ctx context.Context) (float64, error)
	GetFactsByType(ctx context.Context) (map[string]int, error)
	GetFreshnessDistribution(ctx context.Context) (*Freshness, error)
	GetAttributeConflicts(ctx context.Context) ([]Conflict, error)
	GetAttributeConflictsLimit(ctx context.Context, limit int) ([]Conflict, error)
	GetAttributeConflictsLimitWithSuperseded(ctx context.Context, limit int, includeSuperseded bool) ([]Conflict, error)

	// Co-occurrence tracking
	RecordCooccurrenceBatch(ctx context.Context, factIDs []int64) error

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

	// Webhook is an optional alert delivery channel. If non-nil and enabled,
	// alerts are POSTed to the configured URL after creation.
	Webhook *WebhookNotifier
}

// ExecContext executes a SQL statement. This is exposed for testing purposes.
func (s *SQLiteStore) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}

// QueryRowContext executes a query expected to return at most one row.
func (s *SQLiteStore) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

// QueryContext executes a query that returns multiple rows.
func (s *SQLiteStore) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
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

	// Create parent directory for non-memory, non-read-only databases
	if cfg.DBPath != ":memory:" && !cfg.ReadOnly {
		dir := filepath.Dir(cfg.DBPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("creating db directory: %w", err)
		}
	}

	dsn := cfg.DBPath
	if cfg.ReadOnly && cfg.DBPath != ":memory:" {
		dsn = cfg.DBPath + "?mode=ro"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Verify connection
	if err := pingWithRetry(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Connection pool settings for concurrent access
	db.SetMaxOpenConns(1) // SQLite handles one writer at a time
	db.SetMaxIdleConns(1)

	// Enable pragmas — read-only mode skips WAL and synchronous (they require write access)
	// busy_timeout=30000 (30s) handles concurrent multi-process access (#50)
	var pragmas []string
	if cfg.ReadOnly {
		pragmas = []string{
			"PRAGMA foreign_keys=ON",
			"PRAGMA busy_timeout=30000",
		}
	} else {
		pragmas = []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA foreign_keys=ON",
			"PRAGMA busy_timeout=30000",
			"PRAGMA synchronous=NORMAL",
		}
	}
	for _, p := range pragmas {
		if err := execWithRetry(db, p); err != nil {
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

	// Run migrations (skip for read-only access)
	if !cfg.ReadOnly {
		if err := s.migrateWithRetry(); err != nil {
			db.Close()
			return nil, fmt.Errorf("running migrations: %w", err)
		}
	}

	return s, nil
}

func execWithRetry(db *sql.DB, stmt string, args ...interface{}) error {
	const maxAttempts = 8
	const baseDelay = 100 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		_, err := db.Exec(stmt, args...)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) || attempt == maxAttempts {
			break
		}

		delay := baseDelay * time.Duration(1<<(attempt-1))
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		time.Sleep(delay)
	}
	return lastErr
}

func pingWithRetry(db *sql.DB) error {
	const maxAttempts = 8
	const baseDelay = 100 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := db.Ping()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) || attempt == maxAttempts {
			break
		}

		delay := baseDelay * time.Duration(1<<(attempt-1))
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		time.Sleep(delay)
	}
	return lastErr
}

func (s *SQLiteStore) migrateWithRetry() error {
	const maxAttempts = 8
	const baseDelay = 100 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := s.migrate()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) || attempt == maxAttempts {
			break
		}

		delay := baseDelay * time.Duration(1<<(attempt-1))
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		time.Sleep(delay)
	}
	return lastErr
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
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
