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
