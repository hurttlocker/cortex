package search

import (
	"math"
	"sort"
	"strings"
)

const defaultRRFK = 60

// RRFConfig holds parameters for Reciprocal Rank Fusion.
type RRFConfig struct {
	K              int
	BM25Weight     float64
	SemanticWeight float64
	EntityWeight   float64
}

// DefaultRRFConfig returns the default RRF configuration.
func DefaultRRFConfig() RRFConfig {
	return RRFConfig{
		K:              defaultRRFK,
		BM25Weight:     1.0,
		SemanticWeight: 1.0,
		EntityWeight:   1.0,
	}
}

// FuseRRF merges BM25 and semantic ranked result lists using Reciprocal Rank Fusion.
func FuseRRF(bm25Results, semanticResults []Result, cfg RRFConfig) []Result {
	return fuseRRFChannelsWithOptions([]rrfChannel{
		{name: "bm25", results: bm25Results, weight: cfg.BM25Weight},
		{name: "semantic", results: semanticResults, weight: cfg.SemanticWeight},
	}, 0, false, cfg)
}

func fuseRRFWithOptions(bm25Results, semanticResults []Result, limit int, explain bool, cfg RRFConfig) []Result {
	return fuseRRFChannelsWithOptions([]rrfChannel{
		{name: "bm25", results: bm25Results, weight: cfg.BM25Weight},
		{name: "semantic", results: semanticResults, weight: cfg.SemanticWeight},
	}, limit, explain, cfg)
}

type rrfChannel struct {
	name    string
	results []Result
	weight  float64
}

func fuseRRFChannelsWithOptions(channels []rrfChannel, limit int, explain bool, cfg RRFConfig) []Result {
	cfg = normalizeRRFConfig(cfg)

	type fusedEntry struct {
		result Result
		ranks  map[string]int
	}

	fusedMap := make(map[int64]*fusedEntry)
	penaltyRanks := make(map[string]int, len(channels))

	for _, channel := range channels {
		penaltyRanks[channel.name] = len(channel.results) + 1
	}

	for _, channel := range channels {
		for i, r := range channel.results {
			entry, exists := fusedMap[r.MemoryID]
			if !exists {
				entry = &fusedEntry{
					result: r,
					ranks:  make(map[string]int, len(channels)),
				}
				for _, other := range channels {
					entry.ranks[other.name] = penaltyRanks[other.name]
				}
				fusedMap[r.MemoryID] = entry
			}
			entry.ranks[channel.name] = i + 1
			if len(strings.TrimSpace(r.Content)) > len(strings.TrimSpace(entry.result.Content)) {
				entry.result.Content = r.Content
			}
			if entry.result.Snippet == "" {
				entry.result.Snippet = r.Snippet
			}
			entry.result.FactIDs = appendUniqueInt64(entry.result.FactIDs, r.FactIDs...)
		}
	}

	merged := make([]Result, 0, len(fusedMap))
	for _, entry := range fusedMap {
		rrfScore := 0.0
		bm25Reciprocal := 0.0
		semanticReciprocal := 0.0
		bm25Contribution := 0.0
		semanticContribution := 0.0
		entityContribution := 0.0
		for _, channel := range channels {
			reciprocal := 1.0 / float64(cfg.K+entry.ranks[channel.name])
			contribution := channel.weight * reciprocal
			rrfScore += contribution
			switch channel.name {
			case "bm25":
				bm25Reciprocal = reciprocal
				bm25Contribution = contribution
			case "semantic":
				semanticReciprocal = reciprocal
				semanticContribution = contribution
			case "entity":
				entityContribution = contribution
			}
		}
		prior, priorReason := hybridMetadataPrior(entry.result)
		finalScore := rrfScore * prior

		entry.result.Score = finalScore
		entry.result.MatchType = "rrf"

		if explain {
			ensureExplain(&entry.result)
			entry.result.Explain.RankComponents.BaseScore = finalScore
			entry.result.Explain.RankComponents.PreConfidenceScore = finalScore
			entry.result.Explain.RankComponents.FinalScore = finalScore
			entry.result.Explain.RankComponents.ClassBoostMultiplier = 1.0
			entry.result.Explain.RankComponents.ConfidenceWeight = ConfidenceWeight
			if bm25Reciprocal > 0 {
				entry.result.Explain.RankComponents.HybridBM25Normalized = floatPtr(bm25Reciprocal)
				entry.result.Explain.RankComponents.HybridBM25Contribution = floatPtr(bm25Contribution)
			}
			if semanticReciprocal > 0 {
				entry.result.Explain.RankComponents.HybridSemanticNormalized = floatPtr(semanticReciprocal)
				entry.result.Explain.RankComponents.HybridSemanticContribution = floatPtr(semanticContribution)
			}
			if entityContribution > 0 {
				entry.result.Explain.RankComponents.EntityChannelScore = floatPtr(entityContribution)
			}
			if priorReason != "" {
				if entry.result.Explain.Why == "" {
					entry.result.Explain.Why = priorReason
				} else {
					entry.result.Explain.Why += "; " + priorReason
				}
			}
		}

		merged = append(merged, entry.result)
	}

	sort.Slice(merged, func(i, j int) bool {
		delta := merged[i].Score - merged[j].Score
		if math.Abs(delta) <= 1e-12 {
			return merged[i].MemoryID < merged[j].MemoryID
		}
		return delta > 0
	})

	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}

	return merged
}

func normalizeRRFConfig(cfg RRFConfig) RRFConfig {
	if cfg.K <= 0 {
		cfg.K = defaultRRFK
	}
	if cfg.BM25Weight == 0 {
		cfg.BM25Weight = 1.0
	}
	if cfg.SemanticWeight == 0 {
		cfg.SemanticWeight = 1.0
	}
	if cfg.EntityWeight == 0 {
		cfg.EntityWeight = 1.0
	}
	return cfg
}
