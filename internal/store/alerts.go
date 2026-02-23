package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// AlertType represents the kind of alert.
type AlertType string

const (
	AlertTypeConflict AlertType = "conflict"
	AlertTypeDecay    AlertType = "decay"
	AlertTypeMatch    AlertType = "match" // For future watch queries (#164)
)

// AlertSeverity represents the urgency of an alert.
type AlertSeverity string

const (
	AlertSeverityInfo     AlertSeverity = "info"
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityCritical AlertSeverity = "critical"
)

// Alert represents a proactive notification from Cortex.
type Alert struct {
	ID             int64
	AlertType      AlertType
	Severity       AlertSeverity
	FactID         *int64 // Primary fact involved
	RelatedFactID  *int64 // Secondary fact (e.g., the conflicting one)
	AgentID        string // Which agent this alert is for (empty = all)
	Message        string
	Details        string // JSON details (fact content, scores, etc.)
	Acknowledged   bool
	AcknowledgedAt *time.Time
	CreatedAt      time.Time
}

// AlertFilter controls which alerts to retrieve.
type AlertFilter struct {
	Type           AlertType     // Filter by type (empty = all)
	Severity       AlertSeverity // Filter by severity (empty = all)
	AgentID        string        // Filter by agent (empty = all)
	Acknowledged   *bool         // nil = all, true = acked only, false = unacked only
	Limit          int           // Max results (0 = default 50)
	SinceCreatedAt *time.Time    // Only alerts after this time
}

// CreateAlert inserts a new alert into the database.
func (s *SQLiteStore) CreateAlert(ctx context.Context, alert *Alert) error {
	now := time.Now().UTC()

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO alerts (alert_type, severity, fact_id, related_fact_id, agent_id,
		                     message, details, acknowledged, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		alert.AlertType, alert.Severity, nullableInt64(alert.FactID),
		nullableInt64(alert.RelatedFactID), alert.AgentID,
		alert.Message, alert.Details, now,
	)
	if err != nil {
		return fmt.Errorf("creating alert: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting alert id: %w", err)
	}

	alert.ID = id
	alert.CreatedAt = now
	return nil
}

// ListAlerts retrieves alerts matching the given filter.
func (s *SQLiteStore) ListAlerts(ctx context.Context, filter AlertFilter) ([]Alert, error) {
	var conditions []string
	var args []interface{}

	if filter.Type != "" {
		conditions = append(conditions, "alert_type = ?")
		args = append(args, filter.Type)
	}
	if filter.Severity != "" {
		conditions = append(conditions, "severity = ?")
		args = append(args, filter.Severity)
	}
	if filter.AgentID != "" {
		conditions = append(conditions, "(agent_id = ? OR agent_id = '')")
		args = append(args, filter.AgentID)
	}
	if filter.Acknowledged != nil {
		if *filter.Acknowledged {
			conditions = append(conditions, "acknowledged = 1")
		} else {
			conditions = append(conditions, "acknowledged = 0")
		}
	}
	if filter.SinceCreatedAt != nil {
		conditions = append(conditions, "created_at > ?")
		args = append(args, filter.SinceCreatedAt.UTC().Format(time.RFC3339))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)

	query := fmt.Sprintf(
		`SELECT id, alert_type, severity, fact_id, related_fact_id, agent_id,
		        message, details, acknowledged, acknowledged_at, created_at
		 FROM alerts %s ORDER BY created_at DESC LIMIT ?`, where)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing alerts: %w", err)
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var factID, relatedFactID sql.NullInt64
		var agentID sql.NullString
		var details sql.NullString
		var ackedAt sql.NullTime

		if err := rows.Scan(&a.ID, &a.AlertType, &a.Severity,
			&factID, &relatedFactID, &agentID,
			&a.Message, &details, &a.Acknowledged, &ackedAt, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning alert: %w", err)
		}

		if factID.Valid {
			v := factID.Int64
			a.FactID = &v
		}
		if relatedFactID.Valid {
			v := relatedFactID.Int64
			a.RelatedFactID = &v
		}
		a.AgentID = agentID.String
		a.Details = details.String
		if ackedAt.Valid {
			a.AcknowledgedAt = &ackedAt.Time
		}

		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// AcknowledgeAlert marks an alert as acknowledged.
func (s *SQLiteStore) AcknowledgeAlert(ctx context.Context, alertID int64) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE alerts SET acknowledged = 1, acknowledged_at = ? WHERE id = ?`,
		now, alertID,
	)
	if err != nil {
		return fmt.Errorf("acknowledging alert: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("alert %d not found", alertID)
	}
	return nil
}

// AcknowledgeAllAlerts marks all unacknowledged alerts matching the filter as acknowledged.
func (s *SQLiteStore) AcknowledgeAllAlerts(ctx context.Context, alertType AlertType) (int64, error) {
	now := time.Now().UTC()
	var result sql.Result
	var err error

	if alertType != "" {
		result, err = s.db.ExecContext(ctx,
			`UPDATE alerts SET acknowledged = 1, acknowledged_at = ? WHERE acknowledged = 0 AND alert_type = ?`,
			now, alertType,
		)
	} else {
		result, err = s.db.ExecContext(ctx,
			`UPDATE alerts SET acknowledged = 1, acknowledged_at = ? WHERE acknowledged = 0`,
			now,
		)
	}
	if err != nil {
		return 0, fmt.Errorf("acknowledging all alerts: %w", err)
	}
	return result.RowsAffected()
}

// CountUnacknowledgedAlerts returns the number of unacknowledged alerts.
func (s *SQLiteStore) CountUnacknowledgedAlerts(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts WHERE acknowledged = 0`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting unacked alerts: %w", err)
	}
	return count, nil
}

// CheckConflictsForFact checks if a newly created fact conflicts with existing facts.
// Returns any conflicts found (caller decides whether to create alerts).
func (s *SQLiteStore) CheckConflictsForFact(ctx context.Context, fact *Fact) ([]Conflict, error) {
	if fact.Subject == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, memory_id, subject, predicate, object, fact_type,
		        confidence, decay_rate, last_reinforced, source_quote, created_at, superseded_by
		 FROM facts
		 WHERE LOWER(subject) = LOWER(?)
		   AND LOWER(predicate) = LOWER(?)
		   AND id != ?
		   AND superseded_by IS NULL
		   AND confidence > 0
		 LIMIT 20`,
		fact.Subject, fact.Predicate, fact.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("checking conflicts for fact: %w", err)
	}
	defer rows.Close()

	var conflicts []Conflict
	for rows.Next() {
		var existing Fact
		var supersededBy sql.NullInt64
		if err := rows.Scan(
			&existing.ID, &existing.MemoryID, &existing.Subject, &existing.Predicate,
			&existing.Object, &existing.FactType, &existing.Confidence, &existing.DecayRate,
			&existing.LastReinforced, &existing.SourceQuote, &existing.CreatedAt, &supersededBy,
		); err != nil {
			return nil, fmt.Errorf("scanning existing fact: %w", err)
		}
		if supersededBy.Valid {
			v := supersededBy.Int64
			existing.SupersededBy = &v
		}

		// Only conflict if objects differ
		if strings.EqualFold(existing.Object, fact.Object) {
			continue
		}

		conflicts = append(conflicts, Conflict{
			Fact1:        *fact,
			Fact2:        existing,
			ConflictType: "attribute",
			Similarity:   1.0, // Exact subject+predicate match
		})
	}

	return conflicts, rows.Err()
}

// nullableInt64 converts a *int64 to sql.NullInt64 for database operations.
func nullableInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}
