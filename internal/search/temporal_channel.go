package search

import (
	"context"
	"sort"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/hurttlocker/cortex/internal/temporal"
)

const (
	temporalChannelMinFactLimit = 60
	temporalChannelMaxFactLimit = 400
)

func (e *Engine) searchTemporalChannel(ctx context.Context, query string, opts Options, strategy queryStrategyDecision) ([]Result, error) {
	tq := strategy.TemporalQuery
	if tq == nil {
		tq = opts.TemporalQuery
	}
	if tq == nil || !tq.TemporalIntent {
		return nil, nil
	}

	factLimit := opts.Limit * 20
	if factLimit < temporalChannelMinFactLimit {
		factLimit = temporalChannelMinFactLimit
	}
	if factLimit > temporalChannelMaxFactLimit {
		factLimit = temporalChannelMaxFactLimit
	}

	facts, err := e.store.ListFacts(ctx, store.ListOpts{
		Limit:             factLimit,
		FactType:          "temporal",
		IncludeSuperseded: opts.IncludeSuperseded,
		Agent:             opts.Agent,
	})
	if err != nil || len(facts) == 0 {
		return nil, err
	}

	memoryIDs := make([]int64, 0, len(facts))
	seen := make(map[int64]struct{}, len(facts))
	for _, fact := range facts {
		if fact == nil || fact.MemoryID <= 0 {
			continue
		}
		if _, ok := seen[fact.MemoryID]; ok {
			continue
		}
		seen[fact.MemoryID] = struct{}{}
		memoryIDs = append(memoryIDs, fact.MemoryID)
	}
	if len(memoryIDs) == 0 {
		return nil, nil
	}

	memories, err := e.store.GetMemoriesByIDs(ctx, memoryIDs)
	if err != nil {
		return nil, err
	}
	memoryByID := make(map[int64]*store.Memory, len(memories))
	for _, memory := range memories {
		memoryByID[memory.ID] = memory
	}

	queryLower := strings.ToLower(strings.TrimSpace(query))
	queryTokens := queryTokenSet(query)
	grouped := make(map[int64]*Result, len(memoryIDs))

	for _, fact := range facts {
		if fact == nil {
			continue
		}
		memory := memoryByID[fact.MemoryID]
		if memory == nil {
			continue
		}

		score := temporalFactScore(queryLower, queryTokens, tq, memory, fact)
		if score <= 0 {
			continue
		}

		entry, ok := grouped[fact.MemoryID]
		if !ok {
			entry = &Result{
				Content:       memory.Content,
				SourceFile:    memory.SourceFile,
				SourceTier:    SourceTierForFile(memory.SourceFile),
				SourceLine:    memory.SourceLine,
				SourceSection: memory.SourceSection,
				Project:       memory.Project,
				MemoryClass:   memory.MemoryClass,
				Metadata:      memory.Metadata,
				ImportedAt:    memory.ImportedAt,
				MemoryID:      memory.ID,
				MatchType:     "temporal",
			}
			if memory.Metadata != nil && len(memory.Metadata.TimestampStart) >= 10 {
				entry.TemporalAnchor = memory.Metadata.TimestampStart[:10]
			}
			grouped[fact.MemoryID] = entry
		}
		if score > entry.Score {
			entry.Score = score
			entry.Snippet = strings.TrimSpace(strings.Join([]string{fact.Subject, fact.Predicate, fact.Object}, " "))
		}
		if fact.TemporalNorm != nil {
			entry.TemporalNorms = appendTemporalNorm(entry.TemporalNorms, *fact.TemporalNorm)
		}
		entry.FactIDs = appendUniqueInt64(entry.FactIDs, fact.ID)
	}

	results := make([]Result, 0, len(grouped))
	for _, result := range grouped {
		results = append(results, *result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].MemoryID < results[j].MemoryID
		}
		return results[i].Score > results[j].Score
	})
	if opts.Limit > 0 && len(results) > opts.Limit*2 {
		results = results[:opts.Limit*2]
	}
	return results, nil
}

func temporalFactScore(queryLower string, queryTokens map[string]struct{}, tq *temporal.Query, memory *store.Memory, fact *store.Fact) float64 {
	score := 0.0
	textScore := factTextScore(queryLower, queryTokens, fact)
	score += 0.35 * textScore

	if fact.TemporalNorm != nil {
		score += 0.35
		if tq != nil && tq.Resolved && temporal.MatchesQuery(tq, fact.TemporalNorm) {
			score += 1.10
		}
	}

	if memory != nil && memory.Metadata != nil && len(memory.Metadata.TimestampStart) >= 10 {
		score += 0.10
		if tq != nil && tq.Resolved {
			if anchorNorm := temporal.NormalizeLiteral(memory.Metadata.TimestampStart[:10], ""); anchorNorm != nil && temporal.MatchesQuery(tq, anchorNorm) {
				score += 0.75
			}
		}
	}

	score += 0.15 * clamp01(fact.Confidence)
	if tq != nil && tq.Resolved && score < 0.55 {
		return 0
	}
	return score
}

func appendTemporalNorm(existing []temporal.Norm, norm temporal.Norm) []temporal.Norm {
	for _, current := range existing {
		if current.Kind == norm.Kind &&
			current.Value == norm.Value &&
			current.Start == norm.Start &&
			current.End == norm.End &&
			current.Anchor == norm.Anchor {
			return existing
		}
	}
	return append(existing, norm)
}
