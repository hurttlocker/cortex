package search

import (
	"context"
	"strings"

	"github.com/hurttlocker/cortex/internal/temporal"
)

// QueryStrategy classifies the primary retrieval shape for a query.
type QueryStrategy string

const (
	StrategyDefault    QueryStrategy = "default"
	StrategyTemporal   QueryStrategy = "temporal"
	StrategyEntity     QueryStrategy = "entity"
	StrategyComparison QueryStrategy = "comparison"
	StrategyBridge     QueryStrategy = "bridge"
)

type queryStrategyDecision struct {
	Raw                  string
	Primary              QueryStrategy
	Entities             []string
	TemporalQuery        *temporal.Query
	EnableSceneExpand    bool
	EnableBridgeDiscover bool
	Reason               string
}

var comparisonCueTokens = map[string]struct{}{
	"both": {}, "common": {}, "same": {}, "different": {}, "compare": {}, "shared": {},
}

var entityCueTokens = map[string]struct{}{
	"who": {}, "whose": {}, "where": {}, "about": {},
}

var bridgeCueTokens = map[string]struct{}{
	"why": {}, "how": {}, "after": {}, "before": {}, "because": {}, "happen": {}, "happened": {},
}

func (e *Engine) classifyQueryStrategy(ctx context.Context, query string) queryStrategyDecision {
	query = strings.TrimSpace(query)
	decision := queryStrategyDecision{
		Raw:     query,
		Primary: StrategyDefault,
	}
	if query == "" {
		return decision
	}

	decision.TemporalQuery = temporal.ParseQuery(query)

	resolvedEntities, err := e.resolveQueryEntities(ctx, query)
	if err == nil && len(resolvedEntities) > 0 {
		decision.Entities = make([]string, 0, len(resolvedEntities))
		for _, entity := range resolvedEntities {
			if entity == nil {
				continue
			}
			decision.Entities = append(decision.Entities, entity.CanonicalName)
		}
	}

	if len(decision.Entities) == 0 {
		decision.Entities = likelyQueryEntities(query)
	}

	decision = classifyQueryStrategyHeuristic(query, decision.Entities, decision.TemporalQuery)
	return decision
}

func classifyQueryStrategyHeuristic(query string, entities []string, tq *temporal.Query) queryStrategyDecision {
	decision := queryStrategyDecision{
		Raw:           strings.TrimSpace(query),
		Primary:       StrategyDefault,
		Entities:      dedupeStrings(entities),
		TemporalQuery: tq,
	}
	tokenSet := queryTokenSet(query)
	entityCount := len(decision.Entities)
	hasTemporal := tq != nil && tq.TemporalIntent
	hasComparison := hasAnyToken(tokenSet, comparisonCueTokens)
	hasBridge := hasAnyToken(tokenSet, bridgeCueTokens)
	hasEntityCue := hasAnyToken(tokenSet, entityCueTokens) || strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "what does ")

	switch {
	case hasComparison || (entityCount >= 2 && strings.Contains(strings.ToLower(query), " in common")):
		decision.Primary = StrategyComparison
		decision.EnableSceneExpand = true
		decision.EnableBridgeDiscover = true
		decision.Reason = "comparison/commonality cues"
	case hasBridge || (entityCount >= 2 && !hasTemporal):
		decision.Primary = StrategyBridge
		decision.EnableSceneExpand = true
		decision.EnableBridgeDiscover = true
		decision.Reason = "bridge/composition cues"
	case hasTemporal:
		decision.Primary = StrategyTemporal
		decision.EnableSceneExpand = true
		decision.Reason = "temporal cues"
	case entityCount > 0 && hasEntityCue:
		decision.Primary = StrategyEntity
		decision.Reason = "entity-directed question"
	default:
		decision.Primary = StrategyDefault
		decision.Reason = "default lexical/semantic retrieval"
	}

	if hasTemporal && decision.Primary != StrategyTemporal {
		decision.EnableSceneExpand = true
		if decision.Reason == "" {
			decision.Reason = "temporal cues"
		} else {
			decision.Reason += " + temporal cues"
		}
	}

	return decision
}

func likelyQueryEntities(query string) []string {
	raw := extractQueryEntityCandidates(query)
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, candidate := range raw {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, " ") {
			parts := strings.Fields(candidate)
			if len(parts) > 2 {
				continue
			}
		}
		if !looksLikeEntity(candidate) {
			continue
		}
		out = append(out, candidate)
	}
	return dedupeStrings(out)
}

func looksLikeEntity(candidate string) bool {
	fields := strings.Fields(strings.TrimSpace(candidate))
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if field == "" {
			return false
		}
		r := []rune(field)[0]
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func rrfConfigForStrategy(decision queryStrategyDecision) RRFConfig {
	cfg := DefaultRRFConfig()
	cfg.EntityWeight = 0.6
	cfg.TemporalWeight = 0.4

	switch decision.Primary {
	case StrategyTemporal:
		cfg.BM25Weight = 0.7
		cfg.SemanticWeight = 0.8
		cfg.EntityWeight = 0.3
		cfg.TemporalWeight = 1.4
	case StrategyEntity:
		cfg.BM25Weight = 0.7
		cfg.SemanticWeight = 0.8
		cfg.EntityWeight = 1.4
		cfg.TemporalWeight = 0.3
	case StrategyComparison:
		cfg.BM25Weight = 0.8
		cfg.SemanticWeight = 1.0
		cfg.EntityWeight = 1.2
		cfg.TemporalWeight = 0.4
	case StrategyBridge:
		cfg.BM25Weight = 0.9
		cfg.SemanticWeight = 1.1
		cfg.EntityWeight = 1.0
		cfg.TemporalWeight = 0.7
	}

	if decision.TemporalQuery != nil && decision.TemporalQuery.TemporalIntent && cfg.TemporalWeight < 1.0 {
		cfg.TemporalWeight = 1.0
	}
	return normalizeRRFConfig(cfg)
}
