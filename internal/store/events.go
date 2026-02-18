package store

import (
	"context"
	"database/sql"
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
	return s.SearchFTSWithProject(ctx, query, limit, "")
}

// SearchFTSWithProject performs full-text search, optionally scoped to a project.
// If project is empty, searches all memories (backward-compatible).
func (s *SQLiteStore) SearchFTSWithProject(ctx context.Context, query string, limit int, project string) ([]*SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	var rows *sql.Rows
	var err error

	if project != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT m.id, m.content, m.source_file, m.source_line, m.source_section,
			        m.content_hash, m.project, m.metadata, m.imported_at, m.updated_at,
			        rank,
			        snippet(memories_fts, 0, '<b>', '</b>', '...', 32)
			 FROM memories_fts
			 JOIN memories m ON memories_fts.rowid = m.id
			 WHERE memories_fts MATCH ?
			   AND m.deleted_at IS NULL
			   AND m.project = ?
			 ORDER BY rank
			 LIMIT ?`,
			query, project, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT m.id, m.content, m.source_file, m.source_line, m.source_section,
			        m.content_hash, m.project, m.metadata, m.imported_at, m.updated_at,
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
	}
	if err != nil {
		return nil, fmt.Errorf("FTS search: %w", err)
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		r := &SearchResult{}
		var metadataStr sql.NullString
		if err := rows.Scan(&r.Memory.ID, &r.Memory.Content, &r.Memory.SourceFile,
			&r.Memory.SourceLine, &r.Memory.SourceSection, &r.Memory.ContentHash,
			&r.Memory.Project, &metadataStr, &r.Memory.ImportedAt, &r.Memory.UpdatedAt,
			&r.Score, &r.Snippet); err != nil {
			return nil, fmt.Errorf("scanning FTS result: %w", err)
		}
		r.Memory.Metadata = unmarshalMetadata(metadataStr)
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
// Uses a two-phase approach to avoid O(N²) self-join timeout on large fact tables:
// Phase 1: Find subject+predicate pairs with multiple distinct objects (fast GROUP BY)
// Phase 2: Fetch the actual conflicting facts for those pairs
func (s *SQLiteStore) GetAttributeConflicts(ctx context.Context) ([]Conflict, error) {
	return s.GetAttributeConflictsLimit(ctx, 100)
}

// GetAttributeConflictsLimit is like GetAttributeConflicts but with a configurable limit.
func (s *SQLiteStore) GetAttributeConflictsLimit(ctx context.Context, limit int) ([]Conflict, error) {
	if limit <= 0 {
		limit = 100
	}

	// Phase 1: Find conflicting (subject, predicate) pairs efficiently.
	// Only considers facts with confidence > 0 (skip already-resolved/suppressed facts).
	pairRows, err := s.db.QueryContext(ctx,
		`SELECT LOWER(f.subject), LOWER(f.predicate), COUNT(DISTINCT f.object) as obj_count
		 FROM facts f
		 JOIN memories m ON f.memory_id = m.id AND m.deleted_at IS NULL
		 WHERE f.subject != '' AND f.subject IS NOT NULL
		   AND f.confidence > 0
		 GROUP BY LOWER(f.subject), LOWER(f.predicate)
		 HAVING COUNT(DISTINCT f.object) > 1
		 ORDER BY obj_count DESC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("finding conflicting pairs: %w", err)
	}

	type pair struct {
		subject   string
		predicate string
	}
	var pairs []pair
	for pairRows.Next() {
		var p pair
		var cnt int
		if err := pairRows.Scan(&p.subject, &p.predicate, &cnt); err != nil {
			pairRows.Close()
			return nil, fmt.Errorf("scanning pair: %w", err)
		}
		pairs = append(pairs, p)
	}
	pairRows.Close()
	if err := pairRows.Err(); err != nil {
		return nil, err
	}

	if len(pairs) == 0 {
		return nil, nil
	}

	// Phase 2: For each conflicting pair, get the actual facts.
	var conflicts []Conflict
	for _, p := range pairs {
		factRows, err := s.db.QueryContext(ctx,
			`SELECT f.id, f.memory_id, f.subject, f.predicate, f.object, f.fact_type,
			        f.confidence, f.decay_rate, f.last_reinforced, f.source_quote, f.created_at
			 FROM facts f
			 JOIN memories m ON f.memory_id = m.id AND m.deleted_at IS NULL
			 WHERE LOWER(f.subject) = ? AND LOWER(f.predicate) = ?
			   AND f.confidence > 0
			 ORDER BY f.created_at DESC
			 LIMIT 10`,
			p.subject, p.predicate,
		)
		if err != nil {
			return nil, fmt.Errorf("fetching facts for pair: %w", err)
		}

		var facts []Fact
		for factRows.Next() {
			var f Fact
			if err := factRows.Scan(
				&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object, &f.FactType,
				&f.Confidence, &f.DecayRate, &f.LastReinforced, &f.SourceQuote, &f.CreatedAt,
			); err != nil {
				factRows.Close()
				return nil, fmt.Errorf("scanning fact: %w", err)
			}
			facts = append(facts, f)
		}
		factRows.Close()

		// Generate pairwise conflicts (first vs each other)
		for i := 0; i < len(facts); i++ {
			for j := i + 1; j < len(facts); j++ {
				if facts[i].Object != facts[j].Object {
					conflicts = append(conflicts, Conflict{
						Fact1:        facts[i],
						Fact2:        facts[j],
						ConflictType: "attribute",
						Similarity:   1.0,
					})
				}
			}
		}
	}

	return conflicts, nil
}
