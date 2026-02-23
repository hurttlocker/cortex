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
	Growth                 Growth                        `json:"growth"`
	Alerts                 []string                      `json:"alerts,omitempty"`
	ConfidenceDistribution *store.ConfidenceDistribution `json:"confidence_distribution,omitempty"`
}

// Growth holds short-window growth metrics for ops guardrails.
type Growth struct {
	Memories24h int `json:"memories_24h"`
	Memories7d  int `json:"memories_7d"`
	Facts24h    int `json:"facts_24h"`
	Facts7d     int `json:"facts_7d"`
}

// GrowthReportOpts configures the growth composition report.
type GrowthReportOpts struct {
	TopSourceFiles int
}

// GrowthBucket captures one grouped contributor row.
type GrowthBucket struct {
	Key     string  `json:"key"`
	Count   int     `json:"count"`
	Percent float64 `json:"percent"`
}

// GrowthWindowReport holds growth composition for a single time window.
type GrowthWindowReport struct {
	Window            string         `json:"window"`
	MemoriesDelta     int            `json:"memories_delta"`
	FactsDelta        int            `json:"facts_delta"`
	MemoriesBySource  []GrowthBucket `json:"memories_by_source_type"`
	TopMemorySources  []GrowthBucket `json:"top_memory_sources"`
	MemoriesByPathway []GrowthBucket `json:"memories_by_capture_pathway"`
	FactsByType       []GrowthBucket `json:"facts_by_type"`
}

// GrowthReport is the high-signal composition report for ingest growth.
type GrowthReport struct {
	GeneratedAt    string               `json:"generated_at"`
	Windows        []GrowthWindowReport `json:"windows"`
	Recommendation string               `json:"recommendation"`
	Guidance       []string             `json:"guidance,omitempty"`
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
	CrossAgent   bool       `json:"cross_agent,omitempty"`
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

	// Growth metrics + guardrail alerts are only available on SQLite store.
	if sq, ok := e.store.(*store.SQLiteStore); ok {
		growth, err := e.getGrowth(ctx, sq)
		if err == nil {
			stats.Growth = growth
			stats.Alerts = buildGrowthAlerts(stats.StorageBytes, growth)
		}
	}

	return stats, nil
}

func (e *Engine) getGrowth(ctx context.Context, sq *store.SQLiteStore) (Growth, error) {
	g := Growth{}

	if err := sq.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memories
		WHERE deleted_at IS NULL
		  AND imported_at >= datetime('now', '-1 day')
	`).Scan(&g.Memories24h); err != nil {
		return g, err
	}

	if err := sq.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memories
		WHERE deleted_at IS NULL
		  AND imported_at >= datetime('now', '-7 day')
	`).Scan(&g.Memories7d); err != nil {
		return g, err
	}

	if err := sq.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM facts
		WHERE superseded_by IS NULL
		  AND created_at >= datetime('now', '-1 day')
	`).Scan(&g.Facts24h); err != nil {
		return g, err
	}

	if err := sq.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM facts
		WHERE superseded_by IS NULL
		  AND created_at >= datetime('now', '-7 day')
	`).Scan(&g.Facts7d); err != nil {
		return g, err
	}

	return g, nil
}

func buildGrowthAlerts(storageBytes int64, growth Growth) []string {
	alerts := make([]string, 0)

	const (
		warnStorageBytes = int64(1.5 * 1024 * 1024 * 1024) // 1.5 GB
		noteStorageBytes = int64(1.0 * 1024 * 1024 * 1024) // 1.0 GB
	)

	switch {
	case storageBytes >= warnStorageBytes:
		alerts = append(alerts, "db_size_high: storage is above 1.5GB; run maintenance (VACUUM/cleanup) soon")
	case storageBytes >= noteStorageBytes:
		alerts = append(alerts, "db_size_notice: storage is above 1.0GB; monitor growth weekly")
	}

	if growth.Facts24h >= 200000 {
		alerts = append(alerts, "fact_growth_spike: 24h fact growth is unusually high")
	}
	if growth.Memories24h >= 500 {
		alerts = append(alerts, "memory_growth_spike: 24h memory growth is unusually high")
	}

	return alerts
}

// GetGrowthReport returns 24h/7d growth composition with recommendation guidance.
func (e *Engine) GetGrowthReport(ctx context.Context, opts GrowthReportOpts) (*GrowthReport, error) {
	if opts.TopSourceFiles <= 0 {
		opts.TopSourceFiles = 10
	}

	sq, ok := e.store.(*store.SQLiteStore)
	if !ok {
		return nil, fmt.Errorf("growth report requires sqlite store")
	}

	windows := []struct {
		label string
		sql   string
	}{
		{label: "24h", sql: "-1 day"},
		{label: "7d", sql: "-7 day"},
	}

	report := &GrowthReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Windows:     make([]GrowthWindowReport, 0, len(windows)),
	}

	for _, w := range windows {
		memoryDelta, err := querySingleInt(ctx, sq, `
			SELECT COUNT(*)
			FROM memories
			WHERE deleted_at IS NULL
			  AND imported_at >= datetime('now', ?)
		`, w.sql)
		if err != nil {
			return nil, fmt.Errorf("query %s memory delta: %w", w.label, err)
		}

		factDelta, err := querySingleInt(ctx, sq, `
			SELECT COUNT(*)
			FROM facts
			WHERE superseded_by IS NULL
			  AND created_at >= datetime('now', ?)
		`, w.sql)
		if err != nil {
			return nil, fmt.Errorf("query %s fact delta: %w", w.label, err)
		}

		bySourceType, err := queryBuckets(ctx, sq, `
			SELECT
				CASE
					WHEN json_valid(COALESCE(metadata, '')) AND COALESCE(json_extract(metadata, '$.source_type'), '') != '' THEN LOWER(json_extract(metadata, '$.source_type'))
					WHEN LOWER(COALESCE(source_file, '')) LIKE '%.md' OR LOWER(COALESCE(source_file, '')) LIKE '%.markdown' THEN 'markdown'
					WHEN LOWER(COALESCE(source_file, '')) LIKE '%.json' OR LOWER(COALESCE(source_file, '')) LIKE '%.jsonl' THEN 'json'
					WHEN LOWER(COALESCE(source_file, '')) LIKE '%.yaml' OR LOWER(COALESCE(source_file, '')) LIKE '%.yml' THEN 'yaml'
					WHEN LOWER(COALESCE(source_file, '')) LIKE '%.txt' OR LOWER(COALESCE(source_file, '')) LIKE '%.text' THEN 'text'
					WHEN COALESCE(source_file, '') = '' THEN 'unknown'
					ELSE 'other'
				END AS key,
				COUNT(*) AS count
			FROM memories
			WHERE deleted_at IS NULL
			  AND imported_at >= datetime('now', ?)
			GROUP BY key
			ORDER BY count DESC, key ASC
		`, memoryDelta, w.sql)
		if err != nil {
			return nil, fmt.Errorf("query %s source type composition: %w", w.label, err)
		}

		topSources, err := queryBuckets(ctx, sq, `
			SELECT
				CASE
					WHEN COALESCE(source_file, '') = '' THEN '(unknown)'
					ELSE source_file
				END AS key,
				COUNT(*) AS count
			FROM memories
			WHERE deleted_at IS NULL
			  AND imported_at >= datetime('now', ?)
			GROUP BY key
			ORDER BY count DESC, key ASC
			LIMIT ?
		`, memoryDelta, w.sql, opts.TopSourceFiles)
		if err != nil {
			return nil, fmt.Errorf("query %s top source files: %w", w.label, err)
		}

		byPathway, err := queryBuckets(ctx, sq, `
			SELECT
				CASE
					WHEN LOWER(COALESCE(source_file, '')) LIKE '%/cortex-capture-%' THEN 'plugin_capture'
					ELSE 'manual_import'
				END AS key,
				COUNT(*) AS count
			FROM memories
			WHERE deleted_at IS NULL
			  AND imported_at >= datetime('now', ?)
			GROUP BY key
			ORDER BY count DESC, key ASC
		`, memoryDelta, w.sql)
		if err != nil {
			return nil, fmt.Errorf("query %s capture pathway composition: %w", w.label, err)
		}

		factsByType, err := queryBuckets(ctx, sq, `
			SELECT
				COALESCE(NULLIF(LOWER(fact_type), ''), 'unknown') AS key,
				COUNT(*) AS count
			FROM facts
			WHERE superseded_by IS NULL
			  AND created_at >= datetime('now', ?)
			GROUP BY key
			ORDER BY count DESC, key ASC
		`, factDelta, w.sql)
		if err != nil {
			return nil, fmt.Errorf("query %s fact type composition: %w", w.label, err)
		}

		report.Windows = append(report.Windows, GrowthWindowReport{
			Window:            w.label,
			MemoriesDelta:     memoryDelta,
			FactsDelta:        factDelta,
			MemoriesBySource:  bySourceType,
			TopMemorySources:  topSources,
			MemoriesByPathway: byPathway,
			FactsByType:       factsByType,
		})
	}

	primary24h := report.Windows[0]
	primary7d := report.Windows[1]
	report.Recommendation, report.Guidance = buildGrowthRecommendation(primary24h, primary7d)

	return report, nil
}

func querySingleInt(ctx context.Context, sq *store.SQLiteStore, sql string, args ...any) (int, error) {
	var out int
	if err := sq.QueryRowContext(ctx, sql, args...).Scan(&out); err != nil {
		return 0, err
	}
	return out, nil
}

func queryBuckets(ctx context.Context, sq *store.SQLiteStore, sql string, total int, args ...any) ([]GrowthBucket, error) {
	rows, err := sq.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GrowthBucket, 0)
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		pct := 0.0
		if total > 0 {
			pct = (float64(count) / float64(total)) * 100
		}
		out = append(out, GrowthBucket{Key: key, Count: count, Percent: pct})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildGrowthRecommendation(w24h GrowthWindowReport, w7d GrowthWindowReport) (string, []string) {
	if w24h.MemoriesDelta == 0 && w24h.FactsDelta == 0 {
		return "no-op", []string{"No ingest growth detected in the last 24h. No maintenance action needed."}
	}

	expectedMemPerDay := float64(w7d.MemoriesDelta) / 7.0
	if expectedMemPerDay < 1 {
		expectedMemPerDay = 1
	}
	expectedFactsPerDay := float64(w7d.FactsDelta) / 7.0
	if expectedFactsPerDay < 1 {
		expectedFactsPerDay = 1
	}

	recommendedMemorySpike := int(math.Ceil(expectedMemPerDay * 3))
	if recommendedMemorySpike < 500 {
		recommendedMemorySpike = 500
	}
	recommendedFactSpike := int(math.Ceil(expectedFactsPerDay * 3))
	if recommendedFactSpike < 200000 {
		recommendedFactSpike = 200000
	}

	guidance := []string{
		fmt.Sprintf("Suggested growth alert thresholds: memories_24h >= %d, facts_24h >= %d (based on 7d baseline x3).", recommendedMemorySpike, recommendedFactSpike),
	}

	recommendation := "no-op"
	if w24h.MemoriesDelta >= 500 || w24h.FactsDelta >= 200000 {
		recommendation = "maintenance-pass"
		guidance = append(guidance,
			"Growth spike exceeds current guardrails; run report-first maintenance (backup, cleanup/optimize), then compare before/after stats.",
		)
	}

	return recommendation, guidance
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
			CrossAgent:   sc.CrossAgent,
		}
	}

	return conflicts, nil
}
