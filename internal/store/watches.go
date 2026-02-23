package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// WatchQuery represents a persistent search that triggers alerts on new matches.
type WatchQuery struct {
	ID              int64
	Query           string
	Threshold       float64 // Minimum match score (0-1), default 0.7
	DeliveryChannel string  // "alert", "webhook", "mcp"
	WebhookURL      string  // URL for webhook delivery
	AgentID         string  // Which agent owns this watch (empty = all)
	Active          bool
	CreatedAt       time.Time
	LastMatchedAt   *time.Time
	MatchCount      int64 // Total times this watch has matched
}

// CreateWatch registers a new watch query.
func (s *SQLiteStore) CreateWatch(ctx context.Context, w *WatchQuery) error {
	if w.Query == "" {
		return fmt.Errorf("watch query cannot be empty")
	}
	if w.Threshold <= 0 {
		w.Threshold = 0.7
	}
	if w.DeliveryChannel == "" {
		w.DeliveryChannel = "alert"
	}

	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO watches_v1 (query, threshold, delivery_channel, webhook_url, agent_id, active, created_at, match_count)
		 VALUES (?, ?, ?, ?, ?, 1, ?, 0)`,
		w.Query, w.Threshold, w.DeliveryChannel, w.WebhookURL, w.AgentID, now,
	)
	if err != nil {
		return fmt.Errorf("creating watch: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting watch id: %w", err)
	}

	w.ID = id
	w.Active = true
	w.CreatedAt = now
	return nil
}

// ListWatches returns all watches, optionally filtered.
func (s *SQLiteStore) ListWatches(ctx context.Context, activeOnly bool) ([]WatchQuery, error) {
	query := `SELECT id, query, threshold, delivery_channel, webhook_url, agent_id,
	                 active, created_at, last_matched_at, match_count
	          FROM watches_v1`
	if activeOnly {
		query += " WHERE active = 1"
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing watches: %w", err)
	}
	defer rows.Close()

	var watches []WatchQuery
	for rows.Next() {
		var w WatchQuery
		var webhookURL, agentID sql.NullString
		var lastMatched sql.NullTime

		if err := rows.Scan(&w.ID, &w.Query, &w.Threshold, &w.DeliveryChannel,
			&webhookURL, &agentID, &w.Active, &w.CreatedAt, &lastMatched, &w.MatchCount); err != nil {
			return nil, fmt.Errorf("scanning watch: %w", err)
		}

		w.WebhookURL = webhookURL.String
		w.AgentID = agentID.String
		if lastMatched.Valid {
			w.LastMatchedAt = &lastMatched.Time
		}

		watches = append(watches, w)
	}
	return watches, rows.Err()
}

// GetWatch returns a single watch by ID.
func (s *SQLiteStore) GetWatch(ctx context.Context, id int64) (*WatchQuery, error) {
	var w WatchQuery
	var webhookURL, agentID sql.NullString
	var lastMatched sql.NullTime

	err := s.db.QueryRowContext(ctx,
		`SELECT id, query, threshold, delivery_channel, webhook_url, agent_id,
		        active, created_at, last_matched_at, match_count
		 FROM watches_v1 WHERE id = ?`, id,
	).Scan(&w.ID, &w.Query, &w.Threshold, &w.DeliveryChannel,
		&webhookURL, &agentID, &w.Active, &w.CreatedAt, &lastMatched, &w.MatchCount)

	if err != nil {
		return nil, fmt.Errorf("watch %d not found: %w", id, err)
	}

	w.WebhookURL = webhookURL.String
	w.AgentID = agentID.String
	if lastMatched.Valid {
		w.LastMatchedAt = &lastMatched.Time
	}

	return &w, nil
}

// RemoveWatch deletes a watch query.
func (s *SQLiteStore) RemoveWatch(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM watches_v1 WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("removing watch: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("watch %d not found", id)
	}
	return nil
}

// SetWatchActive enables or disables a watch.
func (s *SQLiteStore) SetWatchActive(ctx context.Context, id int64, active bool) error {
	activeInt := 0
	if active {
		activeInt = 1
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE watches_v1 SET active = ? WHERE id = ?", activeInt, id)
	if err != nil {
		return fmt.Errorf("updating watch active state: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("watch %d not found", id)
	}
	return nil
}

// RecordWatchMatch updates the last_matched_at and match_count for a watch.
func (s *SQLiteStore) RecordWatchMatch(ctx context.Context, watchID int64) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE watches_v1 SET last_matched_at = ?, match_count = match_count + 1 WHERE id = ?",
		now, watchID)
	if err != nil {
		return fmt.Errorf("recording watch match: %w", err)
	}
	return nil
}

// GetActiveWatchQueries returns just the query strings and IDs of active watches.
// Used by the import pipeline to check new content against watches.
func (s *SQLiteStore) GetActiveWatchQueries(ctx context.Context) ([]WatchQuery, error) {
	return s.ListWatches(ctx, true)
}
