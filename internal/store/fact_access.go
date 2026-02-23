package store

import (
	"context"
	"fmt"
	"math"
	"time"
)

// AccessType defines the type of fact access.
type AccessType string

const (
	AccessTypeSearch    AccessType = "search"
	AccessTypeReinforce AccessType = "reinforce"
	AccessTypeImport    AccessType = "import"
	AccessTypeReference AccessType = "reference"
)

// ReinforcementWeights defines how much each access type resets the decay timer.
var ReinforcementWeights = map[AccessType]float64{
	AccessTypeReinforce: 1.0, // Full reset
	AccessTypeImport:    0.8, // Strong — new data confirms
	AccessTypeReference: 0.5, // Cross-agent access
	AccessTypeSearch:    0.3, // Light — appeared in results
}

// FactAccess represents a recorded access to a fact.
type FactAccess struct {
	ID         int64
	FactID     int64
	AgentID    string
	AccessType AccessType
	CreatedAt  time.Time
}

// FactAccessSummary provides an aggregate view of a fact's access patterns.
type FactAccessSummary struct {
	FactID       int64    `json:"fact_id"`
	TotalAccess  int      `json:"total_accesses"`
	UniqueAgents int      `json:"unique_agents"`
	AgentIDs     []string `json:"agent_ids"`
	LastAccess   time.Time `json:"last_access"`
	SearchCount  int      `json:"search_count"`
	CrossAgent   bool     `json:"cross_agent"` // 2+ distinct agents
}

// RecordFactAccess logs a fact access and applies implicit reinforcement.
func (s *SQLiteStore) RecordFactAccess(ctx context.Context, factID int64, agentID string, accessType AccessType) error {
	// Record the access
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO fact_accesses_v1 (fact_id, agent_id, access_type) VALUES (?, ?, ?)`,
		factID, agentID, string(accessType),
	)
	if err != nil {
		return fmt.Errorf("recording fact access: %w", err)
	}

	// Apply weighted reinforcement
	weight, ok := ReinforcementWeights[accessType]
	if !ok {
		weight = 0.3 // Default to search weight
	}

	return s.applyWeightedReinforcement(ctx, factID, weight)
}

// RecordFactAccessBatch records accesses for multiple facts (e.g., search results).
func (s *SQLiteStore) RecordFactAccessBatch(ctx context.Context, factIDs []int64, agentID string, accessType AccessType) error {
	if len(factIDs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin batch access: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO fact_accesses_v1 (fact_id, agent_id, access_type) VALUES (?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare batch access: %w", err)
	}
	defer stmt.Close()

	weight, ok := ReinforcementWeights[accessType]
	if !ok {
		weight = 0.3
	}

	for _, factID := range factIDs {
		if _, err := stmt.ExecContext(ctx, factID, agentID, string(accessType)); err != nil {
			continue // Skip individual failures
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch access: %w", err)
	}

	// Apply weighted reinforcement to all facts
	for _, factID := range factIDs {
		_ = s.applyWeightedReinforcement(ctx, factID, weight)
	}

	return nil
}

// GetFactAccessSummary returns access patterns for a fact.
func (s *SQLiteStore) GetFactAccessSummary(ctx context.Context, factID int64) (*FactAccessSummary, error) {
	summary := &FactAccessSummary{FactID: factID}

	// Total + search counts
	var lastAccessStr *string
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), 
		        SUM(CASE WHEN access_type = 'search' THEN 1 ELSE 0 END),
		        MAX(created_at)
		 FROM fact_accesses_v1 WHERE fact_id = ?`, factID,
	).Scan(&summary.TotalAccess, &summary.SearchCount, &lastAccessStr)
	if err != nil {
		return nil, fmt.Errorf("getting access summary: %w", err)
	}
	if lastAccessStr != nil {
		if t, err := time.Parse("2006-01-02 15:04:05", *lastAccessStr); err == nil {
			summary.LastAccess = t
		} else if t, err := time.Parse(time.RFC3339, *lastAccessStr); err == nil {
			summary.LastAccess = t
		}
	}

	// Unique agents
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT agent_id FROM fact_accesses_v1 WHERE fact_id = ? AND agent_id != ''`,
		factID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting unique agents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var agent string
		if err := rows.Scan(&agent); err != nil {
			continue
		}
		summary.AgentIDs = append(summary.AgentIDs, agent)
	}
	summary.UniqueAgents = len(summary.AgentIDs)
	summary.CrossAgent = summary.UniqueAgents >= 2

	return summary, nil
}

// CheckCrossAgentReinforcement checks if 2+ agents accessed a fact recently
// and applies the cross-agent amplification bonus.
func (s *SQLiteStore) CheckCrossAgentReinforcement(ctx context.Context, factID int64, windowDays int) (bool, error) {
	if windowDays <= 0 {
		windowDays = 30
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -windowDays)
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT agent_id) FROM fact_accesses_v1 
		 WHERE fact_id = ? AND agent_id != '' AND created_at > ?`,
		factID, cutoff,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking cross-agent access: %w", err)
	}

	if count >= 2 {
		// Apply cross-agent amplification
		_ = s.applyWeightedReinforcement(ctx, factID, ReinforcementWeights[AccessTypeReference])
		return true, nil
	}

	return false, nil
}

// applyWeightedReinforcement partially resets the decay timer based on weight.
// weight=1.0 is a full reset to now, weight=0.3 moves last_reinforced 30% toward now.
func (s *SQLiteStore) applyWeightedReinforcement(ctx context.Context, factID int64, weight float64) error {
	if weight <= 0 {
		return nil
	}

	// Get current last_reinforced
	var lastReinforced time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT last_reinforced FROM facts WHERE id = ? AND superseded_by IS NULL`,
		factID,
	).Scan(&lastReinforced)
	if err != nil {
		return nil // Fact not found or superseded — no-op
	}

	now := time.Now().UTC()

	if weight >= 1.0 {
		// Full reset
		_, err = s.db.ExecContext(ctx,
			`UPDATE facts SET last_reinforced = ? WHERE id = ?`, now, factID)
		return err
	}

	// Partial reset: move last_reinforced toward now by weight fraction
	elapsed := now.Sub(lastReinforced)
	adjustment := time.Duration(float64(elapsed) * weight)
	newTime := lastReinforced.Add(adjustment)

	// Don't go past now
	if newTime.After(now) {
		newTime = now
	}

	_, err = s.db.ExecContext(ctx,
		`UPDATE facts SET last_reinforced = ? WHERE id = ?`, newTime, factID)
	return err
}

// EffectiveConfidence calculates the current confidence with Ebbinghaus decay.
func EffectiveConfidence(confidence, decayRate float64, lastReinforced time.Time) float64 {
	daysSince := time.Since(lastReinforced).Hours() / 24
	return confidence * math.Exp(-decayRate*daysSince)
}
