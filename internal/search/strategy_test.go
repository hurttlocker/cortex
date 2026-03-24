package search

import (
	"context"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/hurttlocker/cortex/internal/temporal"
)

func TestClassifyQueryStrategyHeuristic(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		entities []string
		want     QueryStrategy
	}{
		{
			name:  "temporal",
			query: "When did Jon go to the fair?",
			want:  StrategyTemporal,
		},
		{
			name:     "entity",
			query:    "What does Alice do?",
			entities: []string{"Alice"},
			want:     StrategyEntity,
		},
		{
			name:     "comparison",
			query:    "What do Jon and Gina both have in common?",
			entities: []string{"Jon", "Gina"},
			want:     StrategyComparison,
		},
		{
			name:     "bridge",
			query:    "How did Jon and Gina meet after dance class?",
			entities: []string{"Jon", "Gina"},
			want:     StrategyBridge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyQueryStrategyHeuristic(tt.query, tt.entities, nil)
			if tt.want == StrategyTemporal {
				got = classifyQueryStrategyHeuristic(tt.query, tt.entities, temporalQuery(tt.query))
			}
			if got.Primary != tt.want {
				t.Fatalf("primary = %s, want %s", got.Primary, tt.want)
			}
		})
	}
}

func TestSearchExplain_AttachesQueryStrategy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:       "General meeting notes without the entity name in the body.",
		SourceFile:    "entity-profile.md",
		SourceSection: "notes",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:    memID,
		Subject:     "Alice",
		Predicate:   "role",
		Object:      "project manager",
		FactType:    "relationship",
		Confidence:  0.95,
		SourceQuote: "Alice is the project manager.",
	}); err != nil {
		t.Fatalf("add fact: %v", err)
	}

	engine := NewEngine(s)
	results, err := engine.Search(ctx, "What does Alice do?", Options{
		Mode:    ModeRRF,
		Limit:   5,
		Explain: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].Explain == nil || results[0].Explain.QueryStrategy == nil {
		t.Fatal("expected query strategy explain payload")
	}
	if results[0].Explain.QueryStrategy.Primary != StrategyEntity {
		t.Fatalf("strategy = %s, want %s", results[0].Explain.QueryStrategy.Primary, StrategyEntity)
	}
}

func TestSearchRRFTemporalChannelWithoutEmbedder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Jon went to the fair last week.",
		SourceFile: "jon.md",
		Metadata: &store.Metadata{
			TimestampStart: "2023-03-09T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:    memID,
		Subject:     "Jon",
		Predicate:   "went_to",
		Object:      "the fair last week",
		FactType:    "temporal",
		Confidence:  0.95,
		SourceQuote: "Jon went to the fair last week.",
		TemporalNorm: &temporal.Norm{
			Kind:       "date_range",
			Literal:    "last week",
			Start:      "2023-03-02",
			End:        "2023-03-08",
			Anchor:     "2023-03-09",
			Precision:  "day",
			Resolution: "resolved_from_anchor",
		},
	}); err != nil {
		t.Fatalf("add fact: %v", err)
	}

	engine := NewEngine(s)
	results, err := engine.Search(ctx, "What happened the week before March 9, 2023?", Options{
		Mode:  ModeRRF,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected temporal channel results")
	}
	if results[0].MemoryID != memID {
		t.Fatalf("top result memory_id = %d, want %d", results[0].MemoryID, memID)
	}
	if results[0].MatchType != "rrf" {
		t.Fatalf("match_type = %q, want rrf", results[0].MatchType)
	}
}

func TestRRFConfigForStrategyComparisonBoostsEntity(t *testing.T) {
	cfg := rrfConfigForStrategy(queryStrategyDecision{Primary: StrategyComparison})
	if cfg.EntityWeight <= cfg.BM25Weight {
		t.Fatalf("entity weight %.2f should exceed bm25 %.2f for comparison", cfg.EntityWeight, cfg.BM25Weight)
	}
}

func temporalQuery(query string) *temporal.Query {
	return temporal.ParseQuery(query)
}
