package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
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

// DecayThresholds configures when decay alerts fire.
type DecayThresholds struct {
	Warning  float64 // Alert when effective confidence drops below this (default 0.5)
	Critical float64 // Alert when effective confidence drops below this (default 0.3)
}

// DefaultDecayThresholds returns sensible defaults.
func DefaultDecayThresholds() DecayThresholds {
	return DecayThresholds{
		Warning:  0.5,
		Critical: 0.3,
	}
}

// DecayAlertResult summarizes what CheckDecayAlerts found.
type DecayAlertResult struct {
	FactsScanned    int
	AlertsCreated   int
	AlertsSkipped   int // Already had unacked decay alert
	WarningCount    int
	CriticalCount   int
	SubjectGroups   map[string]int // subject → count of fading facts
}

// DecayFactDetail is the JSON detail stored with each decay alert.
type DecayFactDetail struct {
	FactID              int64   `json:"fact_id"`
	Subject             string  `json:"subject"`
	Predicate           string  `json:"predicate"`
	Object              string  `json:"object"`
	BaseConfidence      float64 `json:"base_confidence"`
	EffectiveConfidence float64 `json:"effective_confidence"`
	DecayRate           float64 `json:"decay_rate"`
	DaysSinceReinforced float64 `json:"days_since_reinforced"`
}

// CheckDecayAlerts scans all active facts, computes effective confidence via
// Ebbinghaus decay, and creates alerts for facts that have crossed the thresholds.
// It deduplicates: if a fact already has an unacknowledged decay alert, it won't create another.
func (s *SQLiteStore) CheckDecayAlerts(ctx context.Context, thresholds DecayThresholds) (*DecayAlertResult, error) {
	// Get all active facts with their decay parameters
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, memory_id, subject, predicate, object, fact_type,
		        confidence, decay_rate, last_reinforced, source_quote, created_at
		 FROM facts
		 WHERE superseded_by IS NULL
		   AND confidence > 0`)
	if err != nil {
		return nil, fmt.Errorf("querying facts for decay check: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	result := &DecayAlertResult{
		SubjectGroups: make(map[string]int),
	}

	type fadingFact struct {
		fact                Fact
		effectiveConfidence float64
		daysSinceReinforced float64
		severity            AlertSeverity
	}
	var fading []fadingFact

	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object,
			&f.FactType, &f.Confidence, &f.DecayRate, &f.LastReinforced,
			&f.SourceQuote, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning fact: %w", err)
		}
		result.FactsScanned++

		// Calculate effective confidence: confidence * exp(-decay_rate * days)
		daysSince := now.Sub(f.LastReinforced).Hours() / 24
		effective := f.Confidence * math.Exp(-f.DecayRate*daysSince)

		// Determine if this fact crosses a threshold
		var severity AlertSeverity
		if effective < thresholds.Critical {
			severity = AlertSeverityCritical
		} else if effective < thresholds.Warning {
			severity = AlertSeverityWarning
		} else {
			continue // Above all thresholds, no alert needed
		}

		fading = append(fading, fadingFact{
			fact:                f,
			effectiveConfidence: effective,
			daysSinceReinforced: daysSince,
			severity:            severity,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating facts: %w", err)
	}

	if len(fading) == 0 {
		return result, nil
	}

	// Get existing unacknowledged decay alerts to deduplicate
	existingAlerts, err := s.getUnackedDecayFactIDs(ctx)
	if err != nil {
		return nil, err
	}

	// Create alerts for facts that don't already have one
	for _, ff := range fading {
		if _, exists := existingAlerts[ff.fact.ID]; exists {
			result.AlertsSkipped++
			continue
		}

		detail := DecayFactDetail{
			FactID:              ff.fact.ID,
			Subject:             ff.fact.Subject,
			Predicate:           ff.fact.Predicate,
			Object:              ff.fact.Object,
			BaseConfidence:      ff.fact.Confidence,
			EffectiveConfidence: ff.effectiveConfidence,
			DecayRate:           ff.fact.DecayRate,
			DaysSinceReinforced: ff.daysSinceReinforced,
		}
		detailJSON, _ := json.Marshal(detail)

		msg := fmt.Sprintf("Fact #%d fading: \"%s %s %s\" — confidence %.0f%% (was %.0f%%, unreinforced %.0f days)",
			ff.fact.ID, ff.fact.Subject, ff.fact.Predicate, truncate(ff.fact.Object, 40),
			ff.effectiveConfidence*100, ff.fact.Confidence*100, ff.daysSinceReinforced)

		factID := ff.fact.ID
		alert := &Alert{
			AlertType: AlertTypeDecay,
			Severity:  ff.severity,
			FactID:    &factID,
			Message:   msg,
			Details:   string(detailJSON),
		}

		if err := s.CreateAlert(ctx, alert); err != nil {
			return nil, fmt.Errorf("creating decay alert for fact %d: %w", ff.fact.ID, err)
		}

		result.AlertsCreated++
		switch ff.severity {
		case AlertSeverityWarning:
			result.WarningCount++
		case AlertSeverityCritical:
			result.CriticalCount++
		}

		// Track subject grouping
		subject := ff.fact.Subject
		if subject == "" {
			subject = "(no subject)"
		}
		result.SubjectGroups[subject]++
	}

	return result, nil
}

// getUnackedDecayFactIDs returns a set of fact IDs that already have unacknowledged decay alerts.
func (s *SQLiteStore) getUnackedDecayFactIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fact_id FROM alerts WHERE alert_type = 'decay' AND acknowledged = 0 AND fact_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("querying existing decay alerts: %w", err)
	}
	defer rows.Close()

	existing := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning decay alert fact_id: %w", err)
		}
		existing[id] = true
	}
	return existing, rows.Err()
}

// GetDecayDigest returns a grouped summary of fading facts for batch notifications.
// Groups by subject, showing count and worst confidence per group.
func (s *SQLiteStore) GetDecayDigest(ctx context.Context, thresholds DecayThresholds) ([]DecayDigestGroup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, subject, predicate, object, confidence, decay_rate, last_reinforced
		 FROM facts
		 WHERE superseded_by IS NULL
		   AND confidence > 0`)
	if err != nil {
		return nil, fmt.Errorf("querying facts for decay digest: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	groups := make(map[string]*DecayDigestGroup)

	for rows.Next() {
		var id int64
		var subject, predicate, object string
		var confidence, decayRate float64
		var lastReinforced time.Time

		if err := rows.Scan(&id, &subject, &predicate, &object, &confidence, &decayRate, &lastReinforced); err != nil {
			return nil, fmt.Errorf("scanning fact: %w", err)
		}

		daysSince := now.Sub(lastReinforced).Hours() / 24
		effective := confidence * math.Exp(-decayRate*daysSince)

		if effective >= thresholds.Warning {
			continue // Not fading
		}

		key := strings.ToLower(subject)
		if key == "" {
			key = "(no subject)"
		}

		g, exists := groups[key]
		if !exists {
			g = &DecayDigestGroup{
				Subject:         subject,
				WorstConfidence: effective,
			}
			groups[key] = g
		}

		g.FactCount++
		if effective < g.WorstConfidence {
			g.WorstConfidence = effective
		}
		if effective < thresholds.Critical {
			g.CriticalCount++
		} else {
			g.WarningCount++
		}
		g.SampleFacts = append(g.SampleFacts, DecayDigestFact{
			FactID:    id,
			Predicate: predicate,
			Object:    truncate(object, 50),
			Effective: effective,
		})
		// Cap samples at 5 per group for readability
		if len(g.SampleFacts) > 5 {
			g.SampleFacts = g.SampleFacts[:5]
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating facts: %w", err)
	}

	// Convert map to sorted slice (worst first)
	var result []DecayDigestGroup
	for _, g := range groups {
		result = append(result, *g)
	}
	// Sort by worst confidence ascending
	sortDecayDigest(result)

	return result, nil
}

// DecayDigestGroup represents a cluster of fading facts about the same subject.
type DecayDigestGroup struct {
	Subject         string            `json:"subject"`
	FactCount       int               `json:"fact_count"`
	WarningCount    int               `json:"warning_count"`
	CriticalCount   int               `json:"critical_count"`
	WorstConfidence float64           `json:"worst_confidence"`
	SampleFacts     []DecayDigestFact `json:"sample_facts"`
}

// DecayDigestFact is a single fading fact within a digest group.
type DecayDigestFact struct {
	FactID    int64   `json:"fact_id"`
	Predicate string  `json:"predicate"`
	Object    string  `json:"object"`
	Effective float64 `json:"effective_confidence"`
}

// sortDecayDigest sorts groups by worst confidence ascending (most urgent first).
func sortDecayDigest(groups []DecayDigestGroup) {
	for i := 1; i < len(groups); i++ {
		key := groups[i]
		j := i - 1
		for j >= 0 && groups[j].WorstConfidence > key.WorstConfidence {
			groups[j+1] = groups[j]
			j--
		}
		groups[j+1] = key
	}
}

// ReinforceFromAlert reinforces a fact referenced by a decay alert and acknowledges the alert.
// This is the "yes, still true" flow from notifications.
func (s *SQLiteStore) ReinforceFromAlert(ctx context.Context, alertID int64) error {
	// Get the alert
	var factID sql.NullInt64
	var alertType string
	err := s.db.QueryRowContext(ctx,
		`SELECT fact_id, alert_type FROM alerts WHERE id = ?`, alertID,
	).Scan(&factID, &alertType)
	if err != nil {
		return fmt.Errorf("alert %d not found: %w", alertID, err)
	}

	if alertType != string(AlertTypeDecay) {
		return fmt.Errorf("alert %d is type %q, not decay", alertID, alertType)
	}

	if !factID.Valid {
		return fmt.Errorf("alert %d has no associated fact", alertID)
	}

	// Reinforce the fact
	if err := s.ReinforceFact(ctx, factID.Int64); err != nil {
		return fmt.Errorf("reinforcing fact %d: %w", factID.Int64, err)
	}

	// Acknowledge the alert
	return s.AcknowledgeAlert(ctx, alertID)
}

// nullableInt64 converts a *int64 to sql.NullInt64 for database operations.
func nullableInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}
