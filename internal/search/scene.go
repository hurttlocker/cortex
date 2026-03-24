package search

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/hurttlocker/cortex/internal/temporal"
)

const (
	sceneSeedLimit              = 4
	sceneSourceFetchLimit       = 200
	sceneLineWindow             = 120
	sceneMinAffinity            = 0.45
	sceneMaxNeighborsPerSeed    = 2
	sceneTemporalSignalBoost    = 0.10
	sceneSessionAffinityBoost   = 0.15
	sceneSectionAffinityBoost   = 0.10
	sceneMinimumNeighborOverlap = 0.01
)

// SceneLabelForResult returns a stable grouping label for prompt rendering and
// scene-aware retrieval expansion.
func SceneLabelForResult(r Result) string {
	if r.Metadata != nil && strings.TrimSpace(r.Metadata.SessionKey) != "" {
		return "session:" + strings.TrimSpace(r.Metadata.SessionKey)
	}
	if strings.TrimSpace(r.SourceSection) != "" {
		return "source:" + strings.TrimSpace(r.SourceFile) + "#" + strings.TrimSpace(r.SourceSection)
	}
	if strings.TrimSpace(r.SourceFile) != "" {
		return "source:" + strings.TrimSpace(r.SourceFile)
	}
	return "memory"
}

func (e *Engine) searchSceneExpansion(ctx context.Context, query string, fused []Result, opts Options, strategy queryStrategyDecision) ([]Result, error) {
	if len(fused) == 0 || !strategy.EnableSceneExpand {
		return nil, nil
	}

	queryTokens := queryTokenSet(query)
	entitySet := make(map[string]struct{}, len(strategy.Entities))
	for _, entity := range strategy.Entities {
		entitySet[strings.ToLower(strings.TrimSpace(entity))] = struct{}{}
	}

	limit := sceneSeedLimit
	if len(fused) < limit {
		limit = len(fused)
	}

	injected := make(map[int64]Result)
	for _, seed := range fused[:limit] {
		if strings.TrimSpace(seed.SourceFile) == "" {
			continue
		}
		memories, err := e.store.ListMemories(ctx, store.ListOpts{
			Limit:      sceneSourceFetchLimit,
			SourceFile: seed.SourceFile,
			Agent:      opts.Agent,
		})
		if err != nil || len(memories) == 0 {
			continue
		}

		candidates := make([]Result, 0, len(memories))
		for _, memory := range memories {
			if memory == nil || memory.ID == seed.MemoryID {
				continue
			}
			candidate := Result{
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
				MatchType:     "scene",
			}
			affinity, ok := sceneAffinity(seed, candidate, queryTokens, entitySet, strategy.TemporalQuery)
			if !ok {
				continue
			}
			candidate.Score = seed.Score * affinity
			candidate.Snippet = truncateSceneSnippet(candidate.Content)
			candidates = append(candidates, candidate)
		}

		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Score == candidates[j].Score {
				return candidates[i].MemoryID < candidates[j].MemoryID
			}
			return candidates[i].Score > candidates[j].Score
		})
		if len(candidates) > sceneMaxNeighborsPerSeed {
			candidates = candidates[:sceneMaxNeighborsPerSeed]
		}
		for _, candidate := range candidates {
			current, ok := injected[candidate.MemoryID]
			if !ok || candidate.Score > current.Score {
				injected[candidate.MemoryID] = candidate
			}
		}
	}

	if len(injected) == 0 {
		return nil, nil
	}
	results := make([]Result, 0, len(injected))
	for _, result := range injected {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].MemoryID < results[j].MemoryID
		}
		return results[i].Score > results[j].Score
	})
	return results, nil
}

func sceneAffinity(seed Result, candidate Result, queryTokens map[string]struct{}, entitySet map[string]struct{}, tq *temporal.Query) (float64, bool) {
	if strings.TrimSpace(seed.SourceFile) == "" || !strings.EqualFold(strings.TrimSpace(seed.SourceFile), strings.TrimSpace(candidate.SourceFile)) {
		return 0, false
	}

	proximity := lineProximity(seed.SourceLine, candidate.SourceLine)
	sameSession := sameSession(seed, candidate)
	sameSection := sameSection(seed, candidate)
	lexical := overlapCoverage(queryTokens, queryTokenSet(candidate.Content+" "+candidate.SourceSection))
	entityCoverage := sceneEntityCoverage(candidate, entitySet)
	temporalSupport := sceneTemporalSupport(candidate, tq)

	if lexical < sceneMinimumNeighborOverlap && entityCoverage == 0 && temporalSupport == 0 && !sameSession && !sameSection {
		return 0, false
	}

	affinity := 0.20 + 0.25*proximity + 0.25*lexical + 0.20*entityCoverage
	if temporalSupport > 0 {
		affinity += sceneTemporalSignalBoost * temporalSupport
	}
	if sameSession {
		affinity += sceneSessionAffinityBoost
	}
	if sameSection {
		affinity += sceneSectionAffinityBoost
	}
	if affinity < sceneMinAffinity {
		return 0, false
	}
	if affinity > 0.95 {
		affinity = 0.95
	}
	return affinity, true
}

func lineProximity(seedLine, candidateLine int) float64 {
	if seedLine <= 0 || candidateLine <= 0 {
		return 0
	}
	delta := math.Abs(float64(seedLine - candidateLine))
	if delta > sceneLineWindow {
		return 0
	}
	return 1.0 - (delta / sceneLineWindow)
}

func sameSession(left, right Result) bool {
	if left.Metadata == nil || right.Metadata == nil {
		return false
	}
	return strings.TrimSpace(left.Metadata.SessionKey) != "" &&
		strings.EqualFold(strings.TrimSpace(left.Metadata.SessionKey), strings.TrimSpace(right.Metadata.SessionKey))
}

func sameSection(left, right Result) bool {
	return strings.TrimSpace(left.SourceSection) != "" &&
		strings.EqualFold(strings.TrimSpace(left.SourceSection), strings.TrimSpace(right.SourceSection))
}

func sceneEntityCoverage(candidate Result, entitySet map[string]struct{}) float64 {
	if len(entitySet) == 0 {
		return 0
	}
	text := strings.ToLower(strings.TrimSpace(candidate.Content + " " + candidate.SourceSection))
	if text == "" {
		return 0
	}
	hits := 0
	for entity := range entitySet {
		if entity != "" && strings.Contains(text, entity) {
			hits++
		}
	}
	if hits == 0 {
		return 0
	}
	return float64(hits) / float64(len(entitySet))
}

func sceneTemporalSupport(candidate Result, tq *temporal.Query) float64 {
	if tq == nil || !tq.TemporalIntent {
		return 0
	}
	if candidate.Metadata != nil && len(candidate.Metadata.TimestampStart) >= 10 {
		if norm := temporal.NormalizeLiteral(candidate.Metadata.TimestampStart[:10], ""); norm != nil && temporal.MatchesQuery(tq, norm) {
			return 1.0
		}
	}
	if rerankTemporalCueRE.MatchString(candidate.Content + " " + candidate.SourceSection) {
		return 0.5
	}
	return 0
}

func truncateSceneSnippet(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if len(content) <= 160 {
		return content
	}
	return content[:157] + "..."
}
