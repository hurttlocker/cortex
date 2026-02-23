package store

import (
	"context"
	"fmt"
	"time"
)

// CooccurrencePair represents two facts that appear together.
type CooccurrencePair struct {
	FactIDA  int64     `json:"fact_id_a"`
	FactIDB  int64     `json:"fact_id_b"`
	Count    int       `json:"count"`
	LastSeen time.Time `json:"last_seen"`
}

// RecordCooccurrence records that two facts appeared together (e.g., in same search results).
// Always stores with fact_id_a < fact_id_b to avoid duplicate pairs.
func (s *SQLiteStore) RecordCooccurrence(ctx context.Context, factIDA, factIDB int64) error {
	if factIDA == factIDB {
		return nil // No self-co-occurrence
	}

	// Canonical ordering
	a, b := factIDA, factIDB
	if a > b {
		a, b = b, a
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO fact_cooccurrence_v1 (fact_id_a, fact_id_b, count, last_seen)
		 VALUES (?, ?, 1, CURRENT_TIMESTAMP)
		 ON CONFLICT(fact_id_a, fact_id_b) DO UPDATE SET
		   count = count + 1,
		   last_seen = CURRENT_TIMESTAMP`,
		a, b,
	)
	if err != nil {
		return fmt.Errorf("recording co-occurrence: %w", err)
	}
	return nil
}

// RecordCooccurrenceBatch records co-occurrences for a set of fact IDs
// (e.g., all facts returned in a single search). Generates all pairs.
func (s *SQLiteStore) RecordCooccurrenceBatch(ctx context.Context, factIDs []int64) error {
	if len(factIDs) < 2 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin co-occurrence batch: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO fact_cooccurrence_v1 (fact_id_a, fact_id_b, count, last_seen)
		 VALUES (?, ?, 1, CURRENT_TIMESTAMP)
		 ON CONFLICT(fact_id_a, fact_id_b) DO UPDATE SET
		   count = count + 1,
		   last_seen = CURRENT_TIMESTAMP`,
	)
	if err != nil {
		return fmt.Errorf("prepare co-occurrence: %w", err)
	}
	defer stmt.Close()

	// Generate all pairs (n*(n-1)/2)
	for i := 0; i < len(factIDs); i++ {
		for j := i + 1; j < len(factIDs); j++ {
			a, b := factIDs[i], factIDs[j]
			if a > b {
				a, b = b, a
			}
			if _, err := stmt.ExecContext(ctx, a, b); err != nil {
				continue // Skip failures
			}
		}
	}

	return tx.Commit()
}

// GetTopCooccurrences returns the most frequent co-occurrence pairs.
func (s *SQLiteStore) GetTopCooccurrences(ctx context.Context, limit int) ([]CooccurrencePair, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT fact_id_a, fact_id_b, count, last_seen
		 FROM fact_cooccurrence_v1
		 ORDER BY count DESC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting top co-occurrences: %w", err)
	}
	defer rows.Close()

	return scanCooccurrences(rows)
}

// GetCooccurrencesForFact returns co-occurrence pairs involving the given fact.
func (s *SQLiteStore) GetCooccurrencesForFact(ctx context.Context, factID int64, limit int) ([]CooccurrencePair, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT fact_id_a, fact_id_b, count, last_seen
		 FROM fact_cooccurrence_v1
		 WHERE fact_id_a = ? OR fact_id_b = ?
		 ORDER BY count DESC
		 LIMIT ?`, factID, factID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting co-occurrences for fact: %w", err)
	}
	defer rows.Close()

	return scanCooccurrences(rows)
}

// SuggestEdgesFromCooccurrence returns fact pairs that co-occur above the threshold
// but don't yet have a 'relates_to' edge. These are edge suggestions.
func (s *SQLiteStore) SuggestEdgesFromCooccurrence(ctx context.Context, minCount int) ([]CooccurrencePair, error) {
	if minCount <= 0 {
		minCount = 5
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT c.fact_id_a, c.fact_id_b, c.count, c.last_seen
		 FROM fact_cooccurrence_v1 c
		 WHERE c.count >= ?
		   AND NOT EXISTS (
		     SELECT 1 FROM fact_edges_v1 e
		     WHERE e.edge_type = 'relates_to'
		       AND ((e.source_fact_id = c.fact_id_a AND e.target_fact_id = c.fact_id_b)
		         OR (e.source_fact_id = c.fact_id_b AND e.target_fact_id = c.fact_id_a))
		   )
		 ORDER BY c.count DESC
		 LIMIT 50`, minCount,
	)
	if err != nil {
		return nil, fmt.Errorf("suggesting edges: %w", err)
	}
	defer rows.Close()

	return scanCooccurrences(rows)
}

// CountCooccurrences returns total co-occurrence pairs tracked.
func (s *SQLiteStore) CountCooccurrences(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fact_cooccurrence_v1`).Scan(&count)
	return count, err
}

func scanCooccurrences(rows interface{ Next() bool; Scan(...interface{}) error; Err() error }) ([]CooccurrencePair, error) {
	var pairs []CooccurrencePair
	for rows.Next() {
		var p CooccurrencePair
		var lastSeenStr string
		if err := rows.Scan(&p.FactIDA, &p.FactIDB, &p.Count, &lastSeenStr); err != nil {
			return nil, fmt.Errorf("scanning co-occurrence: %w", err)
		}
		if t, err := time.Parse("2006-01-02 15:04:05", lastSeenStr); err == nil {
			p.LastSeen = t
		} else if t, err := time.Parse(time.RFC3339, lastSeenStr); err == nil {
			p.LastSeen = t
		}
		pairs = append(pairs, p)
	}
	return pairs, rows.Err()
}
