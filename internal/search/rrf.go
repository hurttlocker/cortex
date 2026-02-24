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
}

// DefaultRRFConfig returns the default RRF configuration.
func DefaultRRFConfig() RRFConfig {
	return RRFConfig{
		K:              defaultRRFK,
		BM25Weight:     1.0,
		SemanticWeight: 1.0,
	}
}

// FuseRRF merges BM25 and semantic ranked result lists using Reciprocal Rank Fusion.
func FuseRRF(bm25Results, semanticResults []Result, cfg RRFConfig) []Result {
	return fuseRRFWithOptions(bm25Results, semanticResults, 0, false, cfg)
}

func fuseRRFWithOptions(bm25Results, semanticResults []Result, limit int, explain bool, cfg RRFConfig) []Result {
	cfg = normalizeRRFConfig(cfg)

	bm25PenaltyRank := len(bm25Results) + 1
	semanticPenaltyRank := len(semanticResults) + 1

	type fusedEntry struct {
		result       Result
		bm25Rank     int
		semanticRank int
	}

	fusedMap := make(map[int64]*fusedEntry)

	for i, r := range bm25Results {
		fusedMap[r.MemoryID] = &fusedEntry{
			result:       r,
			bm25Rank:     i + 1,
			semanticRank: semanticPenaltyRank,
		}
	}

	for i, r := range semanticResults {
		if entry, exists := fusedMap[r.MemoryID]; exists {
			entry.semanticRank = i + 1
			if len(strings.TrimSpace(r.Content)) > len(strings.TrimSpace(entry.result.Content)) {
				entry.result.Content = r.Content
			}
			if entry.result.Snippet == "" {
				entry.result.Snippet = r.Snippet
			}
		} else {
			fusedMap[r.MemoryID] = &fusedEntry{
				result:       r,
				bm25Rank:     bm25PenaltyRank,
				semanticRank: i + 1,
			}
		}
	}

	merged := make([]Result, 0, len(fusedMap))
	for _, entry := range fusedMap {
		bm25Reciprocal := 1.0 / float64(cfg.K+entry.bm25Rank)
		semanticReciprocal := 1.0 / float64(cfg.K+entry.semanticRank)

		bm25Contribution := cfg.BM25Weight * bm25Reciprocal
		semanticContribution := cfg.SemanticWeight * semanticReciprocal

		rrfScore := bm25Contribution + semanticContribution
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
			entry.result.Explain.RankComponents.HybridBM25Normalized = floatPtr(bm25Reciprocal)
			entry.result.Explain.RankComponents.HybridSemanticNormalized = floatPtr(semanticReciprocal)
			entry.result.Explain.RankComponents.HybridBM25Contribution = floatPtr(bm25Contribution)
			entry.result.Explain.RankComponents.HybridSemanticContribution = floatPtr(semanticContribution)
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
	return cfg
}
