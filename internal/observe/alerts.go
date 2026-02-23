package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
)

// ConflictAlertDetails holds structured details for a conflict alert.
type ConflictAlertDetails struct {
	Fact1ID     int64   `json:"fact1_id"`
	Fact1Value  string  `json:"fact1_value"`
	Fact1Agent  string  `json:"fact1_agent,omitempty"`
	Fact2ID     int64   `json:"fact2_id"`
	Fact2Value  string  `json:"fact2_value"`
	Fact2Agent  string  `json:"fact2_agent,omitempty"`
	Subject     string  `json:"subject"`
	Predicate   string  `json:"predicate"`
	Confidence1 float64 `json:"confidence1"`
	Confidence2 float64 `json:"confidence2"`
	CrossAgent  bool    `json:"cross_agent"`
}

// CheckAndAlertConflicts checks a newly created fact for conflicts with existing facts
// and creates alerts for any found. Returns the number of alerts created.
func CheckAndAlertConflicts(ctx context.Context, s *store.SQLiteStore, fact *store.Fact) (int, error) {
	if fact == nil || fact.Subject == "" {
		return 0, nil
	}

	conflicts, err := s.CheckConflictsForFact(ctx, fact)
	if err != nil {
		return 0, fmt.Errorf("checking conflicts: %w", err)
	}

	if len(conflicts) == 0 {
		return 0, nil
	}

	created := 0
	for _, c := range conflicts {
		// Determine severity based on confidence of both facts
		severity := store.AlertSeverityInfo
		if c.Fact1.Confidence > 0.7 && c.Fact2.Confidence > 0.7 {
			severity = store.AlertSeverityCritical
		} else if c.Fact1.Confidence > 0.5 || c.Fact2.Confidence > 0.5 {
			severity = store.AlertSeverityWarning
		}

		// Cross-agent conflicts get elevated severity
		if c.CrossAgent && severity == store.AlertSeverityInfo {
			severity = store.AlertSeverityWarning
		} else if c.CrossAgent && severity == store.AlertSeverityWarning {
			severity = store.AlertSeverityCritical
		}

		// Build human-readable message
		msg := fmt.Sprintf("Conflicting facts for %q %s: %q vs %q",
			c.Fact1.Subject, c.Fact1.Predicate,
			truncateStr(c.Fact1.Object, 80), truncateStr(c.Fact2.Object, 80))
		if c.CrossAgent {
			agent1 := c.Fact1.AgentID
			if agent1 == "" {
				agent1 = "global"
			}
			agent2 := c.Fact2.AgentID
			if agent2 == "" {
				agent2 = "global"
			}
			msg = fmt.Sprintf("⚠️ CROSS-AGENT: %s vs %s — %s",
				agent1, agent2, msg)
		}

		// Build structured details
		details := ConflictAlertDetails{
			Fact1ID:     c.Fact1.ID,
			Fact1Value:  c.Fact1.Object,
			Fact1Agent:  c.Fact1.AgentID,
			Fact2ID:     c.Fact2.ID,
			Fact2Value:  c.Fact2.Object,
			Fact2Agent:  c.Fact2.AgentID,
			Subject:     c.Fact1.Subject,
			Predicate:   c.Fact1.Predicate,
			Confidence1: c.Fact1.Confidence,
			Confidence2: c.Fact2.Confidence,
			CrossAgent:  c.CrossAgent,
		}
		detailsJSON, _ := json.Marshal(details)

		fact1ID := c.Fact1.ID
		fact2ID := c.Fact2.ID

		alert := &store.Alert{
			AlertType:     store.AlertTypeConflict,
			Severity:      severity,
			FactID:        &fact1ID,
			RelatedFactID: &fact2ID,
			Message:       msg,
			Details:       string(detailsJSON),
		}

		if err := s.CreateAlert(ctx, alert); err != nil {
			return created, fmt.Errorf("creating conflict alert: %w", err)
		}

		// Auto-create 'contradicts' edge in knowledge graph
		_ = s.AddEdge(ctx, &store.FactEdge{
			SourceFactID: c.Fact1.ID,
			TargetFactID: c.Fact2.ID,
			EdgeType:     store.EdgeTypeContradicts,
			Confidence:   1.0,
			Source:       store.EdgeSourceDetected,
		})

		created++
	}

	return created, nil
}

// AlertSummary returns a human-readable summary of unacknowledged alerts.
func AlertSummary(ctx context.Context, s *store.SQLiteStore) (string, error) {
	count, err := s.CountUnacknowledgedAlerts(ctx)
	if err != nil {
		return "", err
	}

	if count == 0 {
		return "No unacknowledged alerts.", nil
	}

	// Get breakdown by type
	unacked := false
	alerts, err := s.ListAlerts(ctx, store.AlertFilter{
		Acknowledged: &unacked,
		Limit:        100,
	})
	if err != nil {
		return "", err
	}

	typeCounts := make(map[store.AlertType]int)
	severityCounts := make(map[store.AlertSeverity]int)
	for _, a := range alerts {
		typeCounts[a.AlertType]++
		severityCounts[a.Severity]++
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("%d unacknowledged alert(s)", count))

	if c := severityCounts[store.AlertSeverityCritical]; c > 0 {
		parts = append(parts, fmt.Sprintf("  🔴 %d critical", c))
	}
	if c := severityCounts[store.AlertSeverityWarning]; c > 0 {
		parts = append(parts, fmt.Sprintf("  🟡 %d warning", c))
	}
	if c := severityCounts[store.AlertSeverityInfo]; c > 0 {
		parts = append(parts, fmt.Sprintf("  ℹ️  %d info", c))
	}

	for t, c := range typeCounts {
		parts = append(parts, fmt.Sprintf("  Type %s: %d", t, c))
	}

	return strings.Join(parts, "\n"), nil
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
