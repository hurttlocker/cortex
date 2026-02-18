package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
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

	query := `SELECT f.id, f.memory_id, f.subject, f.predicate, f.object, f.fact_type, 
			         f.confidence, f.decay_rate, f.last_reinforced, f.source_quote, f.created_at
		      FROM facts f`
	args := []interface{}{}

	// Build WHERE clause
	var where []string
	if opts.FactType != "" {
		where = append(where, "f.fact_type = ?")
		args = append(args, opts.FactType)
	}
	if opts.SourceFile != "" {
		query += " JOIN memories m ON f.memory_id = m.id"
		where = append(where, "m.source_file = ?")
		args = append(args, opts.SourceFile)
	}

	if len(where) > 0 {
		query += " WHERE " + fmt.Sprintf("%s", where[0])
		for _, clause := range where[1:] {
			query += " AND " + clause
		}
	}

	orderBy := "f.created_at DESC"
	if opts.SortBy == "confidence" {
		orderBy = "f.confidence DESC"
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

// GetFactsByMemoryIDs retrieves all facts linked to the given memory IDs.
func (s *SQLiteStore) GetFactsByMemoryIDs(ctx context.Context, memoryIDs []int64) ([]*Fact, error) {
	if len(memoryIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(memoryIDs))
	args := make([]interface{}, len(memoryIDs))
	for i, id := range memoryIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT id, memory_id, subject, predicate, object, fact_type, confidence, decay_rate, last_reinforced, source_quote, created_at
		 FROM facts WHERE memory_id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("getting facts by memory IDs: %w", err)
	}
	defer rows.Close()

	var facts []*Fact
	for rows.Next() {
		f := &Fact{}
		if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object,
			&f.FactType, &f.Confidence, &f.DecayRate, &f.LastReinforced,
			&f.SourceQuote, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning fact: %w", err)
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// ReinforceFactsByMemoryIDs updates last_reinforced for all facts linked to the given memory IDs.
// Returns the number of facts reinforced.
func (s *SQLiteStore) ReinforceFactsByMemoryIDs(ctx context.Context, memoryIDs []int64) (int, error) {
	if len(memoryIDs) == 0 {
		return 0, nil
	}

	now := time.Now().UTC()

	// Build placeholders for IN clause
	placeholders := make([]string, len(memoryIDs))
	args := make([]interface{}, 0, len(memoryIDs)+1)
	args = append(args, now)
	for i, id := range memoryIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(
		"UPDATE facts SET last_reinforced = ? WHERE memory_id IN (%s)",
		strings.Join(placeholders, ","),
	)

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("reinforcing facts by memory IDs: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected: %w", err)
	}
	return int(rows), nil
}

// GetConfidenceDistribution returns the distribution of effective confidence across all facts.
// It calculates effective confidence using Ebbinghaus decay: confidence * exp(-decay_rate * days).
func (s *SQLiteStore) GetConfidenceDistribution(ctx context.Context) (*ConfidenceDistribution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT confidence, decay_rate, last_reinforced FROM facts`)
	if err != nil {
		return nil, fmt.Errorf("querying facts for confidence distribution: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	dist := &ConfidenceDistribution{}

	for rows.Next() {
		var confidence, decayRate float64
		var lastReinforced time.Time
		if err := rows.Scan(&confidence, &decayRate, &lastReinforced); err != nil {
			return nil, fmt.Errorf("scanning fact: %w", err)
		}

		daysSince := now.Sub(lastReinforced).Hours() / 24
		effective := confidence * math.Exp(-decayRate*daysSince)

		dist.Total++
		switch {
		case effective >= 0.7:
			dist.High++
		case effective >= 0.3:
			dist.Medium++
		default:
			dist.Low++
		}
	}

	return dist, rows.Err()
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
