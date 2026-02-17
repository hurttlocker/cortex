package store

import (
	"context"
	"fmt"
	"time"
)

// LogEvent appends a memory event to the event log.
func (s *SQLiteStore) LogEvent(ctx context.Context, e *MemoryEvent) error {
	now := time.Now().UTC()

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_events (event_type, fact_id, old_value, new_value, source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.EventType, e.FactID, e.OldValue, e.NewValue, e.Source, now,
	)
	if err != nil {
		return fmt.Errorf("logging event: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting event id: %w", err)
	}

	e.ID = id
	e.CreatedAt = now
	return nil
}

// Stats returns current database statistics.
func (s *SQLiteStore) Stats(ctx context.Context) (*StoreStats, error) {
	stats := &StoreStats{}

	queries := []struct {
		query string
		dest  *int64
	}{
		{"SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL", &stats.MemoryCount},
		{"SELECT COUNT(*) FROM facts", &stats.FactCount},
		{"SELECT COUNT(*) FROM embeddings", &stats.EmbeddingCount},
		{"SELECT COUNT(*) FROM memory_events", &stats.EventCount},
	}

	for _, q := range queries {
		if err := s.db.QueryRowContext(ctx, q.query).Scan(q.dest); err != nil {
			return nil, fmt.Errorf("querying stats (%s): %w", q.query, err)
		}
	}

	// Get DB size (only works for file-based DBs)
	if s.dbPath != ":memory:" {
		var pageCount, pageSize int64
		s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount)
		s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize)
		stats.DBSizeBytes = pageCount * pageSize
	}

	return stats, nil
}

// ExtendedStats returns source file count and date range using efficient SQL.
func (s *SQLiteStore) ExtendedStats(ctx context.Context) (int, string, string, error) {
	var sourceFiles int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT source_file) FROM memories WHERE deleted_at IS NULL AND source_file != ''`,
	).Scan(&sourceFiles)
	if err != nil {
		return 0, "", "", fmt.Errorf("counting source files: %w", err)
	}

	if sourceFiles == 0 {
		return 0, "", "", nil
	}

	var earliest, latest string
	err = s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(MIN(SUBSTR(imported_at, 1, 10)), ''),
			COALESCE(MAX(SUBSTR(imported_at, 1, 10)), '')
		 FROM memories WHERE deleted_at IS NULL`,
	).Scan(&earliest, &latest)
	if err != nil {
		return sourceFiles, "", "", fmt.Errorf("getting date range: %w", err)
	}

	return sourceFiles, earliest, latest, nil
}

// SearchFTS performs full-text search using FTS5 with BM25 ranking.
func (s *SQLiteStore) SearchFTS(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.content, m.source_file, m.source_line, m.source_section,
		        m.content_hash, m.imported_at, m.updated_at,
		        rank,
		        snippet(memories_fts, 0, '<b>', '</b>', '...', 32)
		 FROM memories_fts
		 JOIN memories m ON memories_fts.rowid = m.id
		 WHERE memories_fts MATCH ?
		   AND m.deleted_at IS NULL
		 ORDER BY rank
		 LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("FTS search: %w", err)
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		r := &SearchResult{}
		if err := rows.Scan(&r.Memory.ID, &r.Memory.Content, &r.Memory.SourceFile,
			&r.Memory.SourceLine, &r.Memory.SourceSection, &r.Memory.ContentHash,
			&r.Memory.ImportedAt, &r.Memory.UpdatedAt,
			&r.Score, &r.Snippet); err != nil {
			return nil, fmt.Errorf("scanning FTS result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetSourceCount returns the number of distinct source files in memories.
func (s *SQLiteStore) GetSourceCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT source_file) FROM memories WHERE deleted_at IS NULL AND source_file != ''`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("getting source count: %w", err)
	}
	return count, nil
}

// GetAverageConfidence returns the average confidence across all facts.
func (s *SQLiteStore) GetAverageConfidence(ctx context.Context) (float64, error) {
	var avg float64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(AVG(confidence), 0.0) FROM facts`,
	).Scan(&avg)
	if err != nil {
		return 0, fmt.Errorf("getting average confidence: %w", err)
	}
	return avg, nil
}

// GetFactsByType returns a distribution of facts grouped by type.
func (s *SQLiteStore) GetFactsByType(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fact_type, COUNT(*) FROM facts GROUP BY fact_type ORDER BY COUNT(*) DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("getting facts by type: %w", err)
	}
	defer rows.Close()

	factsByType := make(map[string]int)
	for rows.Next() {
		var factType string
		var count int
		if err := rows.Scan(&factType, &count); err != nil {
			return nil, fmt.Errorf("scanning facts by type: %w", err)
		}
		factsByType[factType] = count
	}
	return factsByType, rows.Err()
}

// GetFreshnessDistribution returns memory counts bucketed by import date.
func (s *SQLiteStore) GetFreshnessDistribution(ctx context.Context) (*Freshness, error) {
	freshness := &Freshness{}

	// SQLite DATE() cannot parse Go's time format. Use SUBSTR(col, 1, 10) for date comparisons.
	// Use SUBSTR to extract date portion since timestamps include timezone
	queries := []struct {
		query string
		dest  *int
	}{
		{
			`SELECT COUNT(*) FROM memories 
			 WHERE deleted_at IS NULL 
			   AND SUBSTR(imported_at, 1, 10) = date('now')`,
			&freshness.Today,
		},
		{
			`SELECT COUNT(*) FROM memories 
			 WHERE deleted_at IS NULL 
			   AND SUBSTR(imported_at, 1, 10) >= date('now', '-7 days')
			   AND SUBSTR(imported_at, 1, 10) < date('now')`,
			&freshness.ThisWeek,
		},
		{
			`SELECT COUNT(*) FROM memories 
			 WHERE deleted_at IS NULL 
			   AND SUBSTR(imported_at, 1, 10) >= date('now', '-1 month')
			   AND SUBSTR(imported_at, 1, 10) < date('now', '-7 days')`,
			&freshness.ThisMonth,
		},
		{
			`SELECT COUNT(*) FROM memories 
			 WHERE deleted_at IS NULL 
			   AND SUBSTR(imported_at, 1, 10) < date('now', '-1 month')`,
			&freshness.Older,
		},
	}

	for _, q := range queries {
		if err := s.db.QueryRowContext(ctx, q.query).Scan(q.dest); err != nil {
			return nil, fmt.Errorf("querying freshness distribution (%s): %w", q.query[:50], err)
		}
	}

	return freshness, nil
}

// GetAttributeConflicts detects facts with same subject+predicate but different objects.
func (s *SQLiteStore) GetAttributeConflicts(ctx context.Context) ([]Conflict, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f1.id, f1.memory_id, f1.subject, f1.predicate, f1.object, f1.fact_type,
		        f1.confidence, f1.decay_rate, f1.last_reinforced, f1.source_quote, f1.created_at,
		        f2.id, f2.memory_id, f2.subject, f2.predicate, f2.object, f2.fact_type,
		        f2.confidence, f2.decay_rate, f2.last_reinforced, f2.source_quote, f2.created_at
		 FROM facts f1
		 JOIN facts f2
		   ON LOWER(f1.subject) = LOWER(f2.subject)
		  AND LOWER(f1.predicate) = LOWER(f2.predicate)
		  AND f1.object != f2.object
		  AND f1.id < f2.id
		 JOIN memories m1 ON f1.memory_id = m1.id AND m1.deleted_at IS NULL
		 JOIN memories m2 ON f2.memory_id = m2.id AND m2.deleted_at IS NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying attribute conflicts: %w", err)
	}
	defer rows.Close()

	var conflicts []Conflict
	for rows.Next() {
		var f1, f2 Fact
		if err := rows.Scan(
			&f1.ID, &f1.MemoryID, &f1.Subject, &f1.Predicate, &f1.Object, &f1.FactType,
			&f1.Confidence, &f1.DecayRate, &f1.LastReinforced, &f1.SourceQuote, &f1.CreatedAt,
			&f2.ID, &f2.MemoryID, &f2.Subject, &f2.Predicate, &f2.Object, &f2.FactType,
			&f2.Confidence, &f2.DecayRate, &f2.LastReinforced, &f2.SourceQuote, &f2.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning conflict row: %w", err)
		}

		conflicts = append(conflicts, Conflict{
			Fact1:        f1,
			Fact2:        f2,
			ConflictType: "attribute",
			Similarity:   1.0, // Exact subject+predicate match
		})
	}
	return conflicts, rows.Err()
}
