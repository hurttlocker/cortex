package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AddFact inserts a new fact linked to a memory.
func (s *SQLiteStore) AddFact(ctx context.Context, f *Fact) (int64, error) {
	now := time.Now().UTC()
	if f.Confidence == 0 {
		f.Confidence = 1.0
	}
	if f.DecayRate == 0 {
		f.DecayRate = 0.01
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO facts (memory_id, subject, predicate, object, fact_type, confidence, decay_rate, last_reinforced, source_quote, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.MemoryID, f.Subject, f.Predicate, f.Object, f.FactType,
		f.Confidence, f.DecayRate, now, f.SourceQuote, now,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting fact: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}

	f.ID = id
	f.CreatedAt = now
	f.LastReinforced = now
	return id, nil
}

// GetFact retrieves a fact by ID.
func (s *SQLiteStore) GetFact(ctx context.Context, id int64) (*Fact, error) {
	f := &Fact{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, memory_id, subject, predicate, object, fact_type, confidence, decay_rate, last_reinforced, source_quote, created_at
		 FROM facts WHERE id = ?`, id,
	).Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object,
		&f.FactType, &f.Confidence, &f.DecayRate, &f.LastReinforced,
		&f.SourceQuote, &f.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting fact %d: %w", id, err)
	}
	return f, nil
}

// ListFacts returns facts with pagination and optional type filtering.
func (s *SQLiteStore) ListFacts(ctx context.Context, opts ListOpts) ([]*Fact, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	query := `SELECT id, memory_id, subject, predicate, object, fact_type, confidence, decay_rate, last_reinforced, source_quote, created_at
		FROM facts`
	args := []interface{}{}

	if opts.FactType != "" {
		query += " WHERE fact_type = ?"
		args = append(args, opts.FactType)
	}

	orderBy := "created_at DESC"
	if opts.SortBy == "confidence" {
		orderBy = "confidence DESC"
	}
	query += fmt.Sprintf(" ORDER BY %s LIMIT ? OFFSET ?", orderBy)
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing facts: %w", err)
	}
	defer rows.Close()

	var facts []*Fact
	for rows.Next() {
		f := &Fact{}
		if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object,
			&f.FactType, &f.Confidence, &f.DecayRate, &f.LastReinforced,
			&f.SourceQuote, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning fact row: %w", err)
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// UpdateFactConfidence updates the confidence value for a fact.
func (s *SQLiteStore) UpdateFactConfidence(ctx context.Context, id int64, confidence float64) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE facts SET confidence = ? WHERE id = ?", confidence, id,
	)
	if err != nil {
		return fmt.Errorf("updating fact confidence: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("fact %d not found", id)
	}
	return nil
}

// ReinforceFact updates the last_reinforced timestamp to now.
func (s *SQLiteStore) ReinforceFact(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		"UPDATE facts SET last_reinforced = ? WHERE id = ?", now, id,
	)
	if err != nil {
		return fmt.Errorf("reinforcing fact: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("fact %d not found", id)
	}
	return nil
}

// StaleFacts returns facts with low confidence that haven't been recalled recently.
func (s *SQLiteStore) StaleFacts(ctx context.Context, maxConfidence float64, daysSinceRecall int) ([]*Fact, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -daysSinceRecall)

	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.memory_id, f.subject, f.predicate, f.object, f.fact_type,
		        f.confidence, f.decay_rate, f.last_reinforced, f.source_quote, f.created_at
		 FROM facts f
		 WHERE f.confidence <= ?
		   AND f.last_reinforced < ?
		 ORDER BY f.confidence ASC`,
		maxConfidence, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("querying stale facts: %w", err)
	}
	defer rows.Close()

	var facts []*Fact
	for rows.Next() {
		f := &Fact{}
		if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object,
			&f.FactType, &f.Confidence, &f.DecayRate, &f.LastReinforced,
			&f.SourceQuote, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning stale fact: %w", err)
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}
