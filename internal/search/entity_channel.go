package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

var entityQueryStopwords = map[string]struct{}{
	"what": {}, "when": {}, "where": {}, "who": {}, "which": {}, "how": {},
	"does": {}, "did": {}, "do": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "to": {}, "of": {},
	"for": {}, "about": {}, "in": {}, "on": {}, "at": {}, "with": {}, "from": {},
	"have": {}, "has": {}, "had": {}, "tell": {}, "me": {}, "common": {},
}

const (
	entityGraphMaxHops          = 2
	entityGraphDecay            = 0.7
	entityGraphActivationFloor  = 0.1
	entityGraphSemanticLimit    = 5
	entityGraphSemanticMinScore = 0.70
	entityGraphTemporalWindow   = 7 * 24 * time.Hour
)

type semanticNeighbor struct {
	fact  *store.Fact
	score float64
}

func (e *Engine) searchEntityProfiles(ctx context.Context, query string, opts Options) ([]Result, error) {
	entities, err := e.resolveQueryEntities(ctx, query)
	if err != nil || len(entities) == 0 {
		return nil, err
	}

	entityIDs := make([]int64, 0, len(entities))
	for _, entity := range entities {
		entityIDs = append(entityIDs, entity.ID)
	}

	factLimit := opts.Limit * 6
	if factLimit < 12 {
		factLimit = 12
	}
	facts, err := e.store.GetFactsByEntityIDs(ctx, entityIDs, opts.IncludeSuperseded, factLimit)
	if err != nil || len(facts) == 0 {
		return nil, err
	}

	memoryIDs := make([]int64, 0, len(facts))
	seenMemory := make(map[int64]struct{}, len(facts))
	for _, fact := range facts {
		if fact.MemoryID <= 0 {
			continue
		}
		if _, ok := seenMemory[fact.MemoryID]; ok {
			continue
		}
		seenMemory[fact.MemoryID] = struct{}{}
		memoryIDs = append(memoryIDs, fact.MemoryID)
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
		memory := memoryByID[fact.MemoryID]
		if memory == nil {
			continue
		}
		score := 1.05 + factTextScore(queryLower, queryTokens, fact) + 0.15*clamp01(fact.Confidence)
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
				Snippet:       strings.TrimSpace(strings.Join([]string{fact.Subject, fact.Predicate, fact.Object}, " ")),
				MatchType:     "profile",
			}
			grouped[fact.MemoryID] = entry
		}
		if score > entry.Score {
			entry.Score = score
			entry.Snippet = strings.TrimSpace(strings.Join([]string{fact.Subject, fact.Predicate, fact.Object}, " "))
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

func (e *Engine) resolveQueryEntities(ctx context.Context, query string) ([]*store.Entity, error) {
	candidates := extractQueryEntityCandidates(query)
	if len(candidates) == 0 {
		return nil, nil
	}

	seen := make(map[int64]struct{}, len(candidates))
	entities := make([]*store.Entity, 0, len(candidates))
	for _, candidate := range candidates {
		entity, err := e.store.GetEntityByName(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if entity == nil {
			continue
		}
		if _, ok := seen[entity.ID]; ok {
			continue
		}
		seen[entity.ID] = struct{}{}
		entities = append(entities, entity)
	}
	return entities, nil
}

func extractQueryEntityCandidates(query string) []string {
	tokens := strings.FieldsFunc(strings.TrimSpace(query), func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= 'A' && r <= 'Z':
			return false
		case r >= '0' && r <= '9':
			return false
		case r == '\'' || r == '-' || r == '_':
			return false
		default:
			return true
		}
	})
	if len(tokens) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	candidates := make([]string, 0, len(tokens))
	maxNGram := 4
	if len(tokens) < maxNGram {
		maxNGram = len(tokens)
	}
	for size := maxNGram; size >= 1; size-- {
		for start := 0; start+size <= len(tokens); start++ {
			segment := tokens[start : start+size]
			if allEntityStopwords(segment) {
				continue
			}
			candidate := strings.TrimSpace(strings.Join(segment, " "))
			if len(candidate) < 2 {
				continue
			}
			key := strings.ToLower(candidate)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func allEntityStopwords(tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	for _, token := range tokens {
		if _, ok := entityQueryStopwords[strings.ToLower(strings.TrimSpace(token))]; !ok {
			return false
		}
	}
	return true
}

func mergeInjectedResults(base []Result, injected []Result) []Result {
	if len(injected) == 0 {
		return base
	}

	merged := make(map[int64]Result, len(base)+len(injected))
	for _, result := range base {
		merged[result.MemoryID] = result
	}
	for _, result := range injected {
		current, ok := merged[result.MemoryID]
		if !ok {
			merged[result.MemoryID] = result
			continue
		}
		current.Score += result.Score
		current.FactIDs = appendUniqueInt64(current.FactIDs, result.FactIDs...)
		if strings.TrimSpace(current.Snippet) == "" {
			current.Snippet = result.Snippet
		}
		merged[result.MemoryID] = current
	}

	results := make([]Result, 0, len(merged))
	for _, result := range merged {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].MemoryID < results[j].MemoryID
		}
		return results[i].Score > results[j].Score
	})
	return results
}

func appendUniqueInt64(values []int64, additions ...int64) []int64 {
	seen := make(map[int64]struct{}, len(values)+len(additions))
	out := make([]int64, 0, len(values)+len(additions))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range additions {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (e *Engine) searchEntityChannel(ctx context.Context, query string, opts Options) ([]Result, error) {
	entities, err := e.resolveQueryEntities(ctx, query)
	if err != nil || len(entities) == 0 {
		return nil, err
	}

	entityIDs := make([]int64, 0, len(entities))
	for _, entity := range entities {
		entityIDs = append(entityIDs, entity.ID)
	}

	factLimit := opts.Limit * 12
	if factLimit < 40 {
		factLimit = 40
	}
	seedFacts, err := e.store.GetFactsByEntityIDs(ctx, entityIDs, opts.IncludeSuperseded, factLimit)
	if err != nil || len(seedFacts) == 0 {
		return nil, err
	}

	activation := make(map[int64]float64, len(seedFacts))
	factCache := make(map[int64]*store.Fact, len(seedFacts))
	entityFactCache := make(map[int64][]*store.Fact, len(entityIDs))
	memoryCache := make(map[int64]*store.Memory)
	semanticCache := make(map[int64][]semanticNeighbor)
	frontier := make([]int64, 0, len(seedFacts))

	for _, fact := range seedFacts {
		factCache[fact.ID] = fact
		entityFactCache[fact.EntityID] = append(entityFactCache[fact.EntityID], fact)
		if _, ok := activation[fact.ID]; !ok {
			activation[fact.ID] = 1.0
			frontier = append(frontier, fact.ID)
		}
	}

	for hop := 1; hop <= entityGraphMaxHops; hop++ {
		nextFrontier := make([]int64, 0, len(frontier))
		for _, factID := range frontier {
			currentFact := factCache[factID]
			if currentFact == nil {
				continue
			}
			currentActivation := activation[factID]
			if currentActivation < entityGraphActivationFloor {
				continue
			}

			for _, neighbor := range e.entityNeighbors(ctx, currentFact, opts, entityFactCache, factCache) {
				propagated := currentActivation * entityGraphDecay
				if propagated <= activation[neighbor.ID] || propagated < entityGraphActivationFloor {
					continue
				}
				activation[neighbor.ID] = propagated
				nextFrontier = append(nextFrontier, neighbor.ID)
			}

			for _, candidate := range e.temporalNeighbors(ctx, currentFact, opts, entityFactCache, factCache, memoryCache) {
				propagated := currentActivation * entityGraphDecay * candidate.score
				if propagated <= activation[candidate.fact.ID] || propagated < entityGraphActivationFloor {
					continue
				}
				activation[candidate.fact.ID] = propagated
				nextFrontier = append(nextFrontier, candidate.fact.ID)
			}

			for _, candidate := range e.semanticNeighbors(ctx, currentFact, opts, factCache, memoryCache, semanticCache) {
				propagated := currentActivation * entityGraphDecay * candidate.score
				if propagated <= activation[candidate.fact.ID] || propagated < entityGraphActivationFloor {
					continue
				}
				activation[candidate.fact.ID] = propagated
				nextFrontier = append(nextFrontier, candidate.fact.ID)
			}
		}
		if len(nextFrontier) == 0 {
			break
		}
		frontier = dedupeInt64(nextFrontier)
	}

	return e.resultsFromFactActivation(ctx, activation, factCache, memoryCache)
}

func (e *Engine) entityNeighbors(ctx context.Context, fact *store.Fact, opts Options, entityFactCache map[int64][]*store.Fact, factCache map[int64]*store.Fact) []*store.Fact {
	if fact == nil || fact.EntityID <= 0 {
		return nil
	}
	facts := entityFactCache[fact.EntityID]
	if len(facts) == 0 {
		loaded, err := e.store.GetFactsByEntityIDs(ctx, []int64{fact.EntityID}, opts.IncludeSuperseded, 100)
		if err != nil {
			return nil
		}
		entityFactCache[fact.EntityID] = loaded
		facts = loaded
		for _, loadedFact := range loaded {
			factCache[loadedFact.ID] = loadedFact
		}
	}
	neighbors := make([]*store.Fact, 0, len(facts))
	for _, candidate := range facts {
		if candidate.ID == fact.ID {
			continue
		}
		neighbors = append(neighbors, candidate)
	}
	return neighbors
}

func (e *Engine) temporalNeighbors(ctx context.Context, fact *store.Fact, opts Options, entityFactCache map[int64][]*store.Fact, factCache map[int64]*store.Fact, memoryCache map[int64]*store.Memory) []semanticNeighbor {
	if fact == nil || fact.EntityID <= 0 {
		return nil
	}
	anchor, ok := e.factAnchorTime(ctx, fact, memoryCache)
	if !ok {
		return nil
	}
	neighbors := e.entityNeighbors(ctx, fact, opts, entityFactCache, factCache)
	out := make([]semanticNeighbor, 0, len(neighbors))
	for _, neighbor := range neighbors {
		neighborAnchor, ok := e.factAnchorTime(ctx, neighbor, memoryCache)
		if !ok {
			continue
		}
		delta := anchor.Sub(neighborAnchor)
		if delta < 0 {
			delta = -delta
		}
		if delta > entityGraphTemporalWindow {
			continue
		}
		score := 1.0 - 0.4*(float64(delta)/float64(entityGraphTemporalWindow))
		if score < 0.6 {
			score = 0.6
		}
		out = append(out, semanticNeighbor{fact: neighbor, score: score})
	}
	return out
}

func (e *Engine) semanticNeighbors(ctx context.Context, fact *store.Fact, opts Options, factCache map[int64]*store.Fact, memoryCache map[int64]*store.Memory, semanticCache map[int64][]semanticNeighbor) []semanticNeighbor {
	if fact == nil || fact.MemoryID <= 0 {
		return nil
	}
	if cached, ok := semanticCache[fact.MemoryID]; ok {
		return cached
	}

	vector, err := e.store.GetEmbedding(ctx, fact.MemoryID)
	if err != nil || len(vector) == 0 {
		semanticCache[fact.MemoryID] = nil
		return nil
	}

	var memoryResults []*store.SearchResult
	if strings.TrimSpace(opts.Project) != "" {
		memoryResults, err = e.store.SearchEmbeddingWithProject(ctx, vector, entityGraphSemanticLimit+1, entityGraphSemanticMinScore, opts.Project)
	} else {
		memoryResults, err = e.store.SearchEmbedding(ctx, vector, entityGraphSemanticLimit+1, entityGraphSemanticMinScore)
	}
	if err != nil || len(memoryResults) == 0 {
		semanticCache[fact.MemoryID] = nil
		return nil
	}

	memoryIDs := make([]int64, 0, len(memoryResults))
	memoryScore := make(map[int64]float64, len(memoryResults))
	for _, result := range memoryResults {
		if result == nil || result.Memory.ID <= 0 || result.Memory.ID == fact.MemoryID {
			continue
		}
		memoryIDs = append(memoryIDs, result.Memory.ID)
		memoryScore[result.Memory.ID] = result.Score
	}
	if len(memoryIDs) == 0 {
		semanticCache[fact.MemoryID] = nil
		return nil
	}

	facts, err := e.store.GetFactsByMemoryIDs(ctx, memoryIDs)
	if err != nil || len(facts) == 0 {
		semanticCache[fact.MemoryID] = nil
		return nil
	}

	neighbors := make([]semanticNeighbor, 0, len(facts))
	for _, candidate := range facts {
		if candidate.ID == fact.ID {
			continue
		}
		score := memoryScore[candidate.MemoryID]
		if score < entityGraphSemanticMinScore {
			continue
		}
		factCache[candidate.ID] = candidate
		neighbors = append(neighbors, semanticNeighbor{fact: candidate, score: score})
	}
	semanticCache[fact.MemoryID] = neighbors
	return neighbors
}

func (e *Engine) resultsFromFactActivation(ctx context.Context, activation map[int64]float64, factCache map[int64]*store.Fact, memoryCache map[int64]*store.Memory) ([]Result, error) {
	if len(activation) == 0 {
		return nil, nil
	}
	resultsByMemory := make(map[int64]*Result)
	for factID, score := range activation {
		if score < entityGraphActivationFloor {
			continue
		}
		fact := factCache[factID]
		if fact == nil || fact.MemoryID <= 0 {
			continue
		}
		memory, err := e.getMemoryCached(ctx, fact.MemoryID, memoryCache)
		if err != nil || memory == nil {
			continue
		}
		entry, ok := resultsByMemory[memory.ID]
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
				MatchType:     "entity_graph",
				Snippet:       strings.TrimSpace(strings.Join([]string{fact.Subject, fact.Predicate, fact.Object}, " ")),
			}
			resultsByMemory[memory.ID] = entry
		}
		if score > entry.Score {
			entry.Score = score
			entry.Snippet = strings.TrimSpace(strings.Join([]string{fact.Subject, fact.Predicate, fact.Object}, " "))
		}
		entry.FactIDs = appendUniqueInt64(entry.FactIDs, factID)
	}

	results := make([]Result, 0, len(resultsByMemory))
	for _, result := range resultsByMemory {
		results = append(results, *result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].MemoryID < results[j].MemoryID
		}
		return results[i].Score > results[j].Score
	})
	return results, nil
}

func (e *Engine) discoverBridgeResults(ctx context.Context, query string, fused []Result, opts Options) ([]Result, error) {
	entities, err := e.resolveQueryEntities(ctx, query)
	if err != nil || len(entities) < 2 || len(fused) == 0 {
		return nil, err
	}

	entityIDs := make([]int64, 0, len(entities))
	for _, entity := range entities {
		entityIDs = append(entityIDs, entity.ID)
	}
	facts, err := e.store.GetFactsByEntityIDs(ctx, entityIDs, opts.IncludeSuperseded, 200)
	if err != nil || len(facts) == 0 {
		return nil, err
	}

	type bridgeCandidate struct {
		fact  *store.Fact
		score float64
	}

	byEntity := make(map[int64][]*store.Fact, len(entityIDs))
	for _, fact := range facts {
		byEntity[fact.EntityID] = append(byEntity[fact.EntityID], fact)
	}

	candidates := make(map[int64]bridgeCandidate)
	for i := 0; i < len(entityIDs); i++ {
		for j := i + 1; j < len(entityIDs); j++ {
			left := byEntity[entityIDs[i]]
			right := byEntity[entityIDs[j]]
			for _, lf := range left {
				for _, rf := range right {
					score := bridgeScore(lf, rf, entities[i], entities[j])
					if score <= 0 {
						continue
					}
					if score > candidates[lf.ID].score {
						candidates[lf.ID] = bridgeCandidate{fact: lf, score: score}
					}
					if score > candidates[rf.ID].score {
						candidates[rf.ID] = bridgeCandidate{fact: rf, score: score}
					}
				}
			}
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	maxScore := fused[0].Score
	activation := make(map[int64]float64, len(candidates))
	factCache := make(map[int64]*store.Fact, len(candidates))
	memoryCache := make(map[int64]*store.Memory)
	for factID, candidate := range candidates {
		activation[factID] = maxScore * 0.9 * candidate.score
		factCache[factID] = candidate.fact
	}
	return e.resultsFromFactActivation(ctx, activation, factCache, memoryCache)
}

func bridgeScore(left *store.Fact, right *store.Fact, leftEntity *store.Entity, rightEntity *store.Entity) float64 {
	if left == nil || right == nil {
		return 0
	}
	leftObject := strings.ToLower(strings.TrimSpace(left.Object))
	rightObject := strings.ToLower(strings.TrimSpace(right.Object))
	leftPredicate := strings.ToLower(strings.TrimSpace(left.Predicate))
	rightPredicate := strings.ToLower(strings.TrimSpace(right.Predicate))

	switch {
	case left.MemoryID == right.MemoryID:
		return 0.95
	case leftObject != "" && leftObject == rightObject:
		return 0.88
	case leftPredicate != "" && leftPredicate == rightPredicate:
		return 0.72
	case leftEntity != nil && strings.EqualFold(left.Object, leftEntity.CanonicalName):
		return 0.85
	case rightEntity != nil && strings.EqualFold(right.Object, rightEntity.CanonicalName):
		return 0.85
	default:
		return 0
	}
}

func (e *Engine) factAnchorTime(ctx context.Context, fact *store.Fact, memoryCache map[int64]*store.Memory) (time.Time, bool) {
	if fact == nil {
		return time.Time{}, false
	}
	if fact.TemporalNorm != nil {
		if ts, ok := parseEntityFactTime(fact.TemporalNorm.Value); ok {
			return ts, true
		}
		if ts, ok := parseEntityFactTime(fact.TemporalNorm.Start); ok {
			return ts, true
		}
	}
	memory, err := e.getMemoryCached(ctx, fact.MemoryID, memoryCache)
	if err != nil || memory == nil || memory.Metadata == nil {
		return time.Time{}, false
	}
	return parseEntityFactTime(memory.Metadata.TimestampStart)
}

func (e *Engine) getMemoryCached(ctx context.Context, memoryID int64, memoryCache map[int64]*store.Memory) (*store.Memory, error) {
	if memoryID <= 0 {
		return nil, nil
	}
	if memory, ok := memoryCache[memoryID]; ok {
		return memory, nil
	}
	memory, err := e.store.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, fmt.Errorf("loading memory %d for entity graph: %w", memoryID, err)
	}
	memoryCache[memoryID] = memory
	return memory, nil
}

func parseEntityFactTime(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func dedupeInt64(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
