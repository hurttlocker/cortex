// Package observe provides memory observability for Cortex.
//
// Three core capabilities:
// - Stats: total entries, sources, freshness distribution, storage size
// - Stale detection: entries not referenced or updated within a threshold
// - Conflict detection: pairs of facts that may contradict each other
//
// This package answers the question: "What does my agent actually know?"
package observe

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

// Stats holds aggregate memory statistics for observability.
type Stats struct {
	TotalMemories          int                           `json:"memories"`
	TotalFacts             int                           `json:"facts"`
	TotalSources           int                           `json:"sources"`
	StorageBytes           int64                         `json:"storage_bytes"`
	AvgConfidence          float64                       `json:"avg_confidence"`
	FactsByType            map[string]int                `json:"facts_by_type"`
	Freshness              Freshness                     `json:"freshness"`
	ConfidenceDistribution *store.ConfidenceDistribution `json:"confidence_distribution,omitempty"`
}

// Freshness holds distribution of memories by import date buckets.
type Freshness struct {
	Today     int `json:"today"`
	ThisWeek  int `json:"this_week"`
	ThisMonth int `json:"this_month"`
	Older     int `json:"older"`
}

// StaleFact represents a fact that has decayed below threshold.
type StaleFact struct {
	Fact                store.Fact `json:"fact"`
	EffectiveConfidence float64    `json:"effective_confidence"`
	DaysSinceReinforced int        `json:"days_since_reinforced"`
}

// StaleOpts configures stale fact detection parameters.
type StaleOpts struct {
	MaxConfidence     float64 // effective confidence threshold (default: 0.5)
	MaxDays           int     // days without reinforcement (default: 30)
	Limit             int     // max results (default: 50)
	IncludeSuperseded bool    // include superseded facts in stale scan
}

// Conflict represents two facts that may contradict each other.
type Conflict struct {
	Fact1        store.Fact `json:"fact1"`
	Fact2        store.Fact `json:"fact2"`
	ConflictType string     `json:"conflict_type"` // "attribute"
	Similarity   float64    `json:"similarity"`
}

// Engine provides memory observability capabilities.
type Engine struct {
	store  store.Store
	dbPath string
}

// NewEngine creates a new observability engine.
func NewEngine(s store.Store, dbPath string) *Engine {
	return &Engine{
		store:  s,
		dbPath: dbPath,
	}
}

// GetStats returns comprehensive memory statistics.
func (e *Engine) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{
		FactsByType: make(map[string]int),
	}

	// Get basic stats from store
	storeStats, err := e.store.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting store stats: %w", err)
	}

	stats.TotalMemories = int(storeStats.MemoryCount)
	stats.TotalFacts = int(storeStats.FactCount)
	stats.StorageBytes = storeStats.DBSizeBytes

	// Get storage size from file if store doesn't provide it
	if stats.StorageBytes == 0 && e.dbPath != ":memory:" {
		if info, err := os.Stat(e.dbPath); err == nil {
			stats.StorageBytes = info.Size()
		}
	}

	// Get extended stats that require custom queries
	sourceCount, err := e.store.GetSourceCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting source count: %w", err)
	}
	stats.TotalSources = sourceCount

	avgConfidence, err := e.store.GetAverageConfidence(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting average confidence: %w", err)
	}
	stats.AvgConfidence = avgConfidence

	factsByType, err := e.store.GetFactsByType(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting facts by type: %w", err)
	}
	stats.FactsByType = factsByType

	freshness, err := e.store.GetFreshnessDistribution(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting freshness distribution: %w", err)
	}
	stats.Freshness = Freshness{
		Today:     freshness.Today,
		ThisWeek:  freshness.ThisWeek,
		ThisMonth: freshness.ThisMonth,
		Older:     freshness.Older,
	}

	// Get confidence distribution (decay-aware)
	confDist, err := e.store.GetConfidenceDistribution(ctx)
	if err == nil {
		stats.ConfidenceDistribution = confDist
	}

	return stats, nil
}

// GetStaleFacts returns facts that have decayed below the confidence threshold.
func (e *Engine) GetStaleFacts(ctx context.Context, opts StaleOpts) ([]StaleFact, error) {
	// Set defaults
	if opts.MaxConfidence == 0 {
		opts.MaxConfidence = 0.5
	}
	if opts.MaxDays == 0 {
		opts.MaxDays = 30
	}
	if opts.Limit == 0 {
		opts.Limit = 50
	}

	// Get all facts to calculate effective confidence
	facts, err := e.store.ListFacts(ctx, store.ListOpts{Limit: 10000, IncludeSuperseded: opts.IncludeSuperseded}) // Large limit to get all facts
	if err != nil {
		return nil, fmt.Errorf("listing facts: %w", err)
	}

	now := time.Now().UTC()
	staleFacts := make([]StaleFact, 0)

	for _, fact := range facts {
		// Calculate days since reinforced
		daysSinceReinforced := int(now.Sub(fact.LastReinforced).Hours() / 24)

		// Skip if within the day threshold
		if daysSinceReinforced < opts.MaxDays {
			continue
		}

		// Calculate effective confidence using exponential decay
		// effective_confidence = confidence * exp(-decay_rate * days_since_reinforced)
		effectiveConfidence := fact.Confidence * math.Exp(-fact.DecayRate*float64(daysSinceReinforced))

		// Skip if above confidence threshold
		if effectiveConfidence >= opts.MaxConfidence {
			continue
		}

		staleFacts = append(staleFacts, StaleFact{
			Fact:                *fact,
			EffectiveConfidence: effectiveConfidence,
			DaysSinceReinforced: daysSinceReinforced,
		})

		// Apply limit
		if len(staleFacts) >= opts.Limit {
			break
		}
	}

	// Sort by effective confidence (ascending - stalest first)
	for i := 0; i < len(staleFacts); i++ {
		for j := i + 1; j < len(staleFacts); j++ {
			if staleFacts[i].EffectiveConfidence > staleFacts[j].EffectiveConfidence {
				staleFacts[i], staleFacts[j] = staleFacts[j], staleFacts[i]
			}
		}
	}

	return staleFacts, nil
}

// GetConflicts detects attribute conflicts between active (non-superseded) facts.
func (e *Engine) GetConflicts(ctx context.Context) ([]Conflict, error) {
	return e.GetConflictsLimitWithSuperseded(ctx, 100, false)
}

// GetConflictsLimit detects attribute conflicts with a configurable limit.
func (e *Engine) GetConflictsLimit(ctx context.Context, limit int) ([]Conflict, error) {
	return e.GetConflictsLimitWithSuperseded(ctx, limit, false)
}

// GetConflictsLimitWithSuperseded allows historical conflicts to be included when requested.
func (e *Engine) GetConflictsLimitWithSuperseded(ctx context.Context, limit int, includeSuperseded bool) ([]Conflict, error) {
	storeConflicts, err := e.store.GetAttributeConflictsLimitWithSuperseded(ctx, limit, includeSuperseded)
	if err != nil {
		return nil, fmt.Errorf("getting attribute conflicts: %w", err)
	}

	// Convert store.Conflict to observe.Conflict
	conflicts := make([]Conflict, len(storeConflicts))
	for i, sc := range storeConflicts {
		conflicts[i] = Conflict{
			Fact1:        sc.Fact1,
			Fact2:        sc.Fact2,
			ConflictType: sc.ConflictType,
			Similarity:   sc.Similarity,
		}
	}

	return conflicts, nil
}
