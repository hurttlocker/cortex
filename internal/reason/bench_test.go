package reason

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDefaultBenchPresets_AllFive(t *testing.T) {
	want := []string{"daily-digest", "fact-audit", "conflict-check", "weekly-dive", "agent-review"}
	if len(DefaultBenchPresets) != len(want) {
		t.Fatalf("expected %d presets, got %d", len(want), len(DefaultBenchPresets))
	}
	for i, name := range want {
		if DefaultBenchPresets[i].Name != name {
			t.Fatalf("preset[%d]=%q, want %q", i, DefaultBenchPresets[i].Name, name)
		}
		if strings.TrimSpace(DefaultBenchPresets[i].Query) == "" {
			t.Fatalf("preset %q has empty default query", name)
		}
	}
}

func TestExtractQualitySignals(t *testing.T) {
	content := `## Weekly Findings

1. **Action Items**
- Implement retry logic in importer
- Review conflict queue
- [ ] Schedule follow-up test run

Referenced memory #42 and memory 105 for root cause.`

	s := extractQualitySignals(content)
	if !s.HasHeaders {
		t.Fatal("expected headers=true")
	}
	if !s.HasActionableItems {
		t.Fatal("expected actionable_items=true")
	}
	if s.WordCount < 15 {
		t.Fatalf("expected word_count >= 15, got %d", s.WordCount)
	}
	if s.UniqueMemoryRefs != 2 {
		t.Fatalf("expected 2 memory refs, got %d", s.UniqueMemoryRefs)
	}
}

func TestEstimateCost_UnknownForPreviewAndUnknownModels(t *testing.T) {
	cost, known := estimateCost("openai/gpt-oss-120b", 1000, 1000)
	if known {
		t.Fatal("expected preview model cost to be unknown")
	}
	if cost != 0 {
		t.Fatalf("expected zero cost when unknown, got %f", cost)
	}

	cost, known = estimateCost("unknown/model", 1000, 1000)
	if known {
		t.Fatal("expected unknown model cost to be unknown")
	}
	if cost != 0 {
		t.Fatalf("expected zero cost when unknown, got %f", cost)
	}
}

func TestRunBenchmark_RecursiveMode(t *testing.T) {
	engine := &Engine{}
	opts := BenchOptions{
		Models:        []BenchModel{{Label: "test-model", Provider: "openrouter", Model: "deepseek/deepseek-v3.2"}},
		Presets:       []BenchPreset{{Name: "weekly-dive", Query: "weekly synthesis"}},
		Recursive:     true,
		MaxIterations: 6,
		MaxDepth:      2,
		llmFactory: func(model BenchModel) (*LLM, error) {
			return &LLM{provider: model.Provider, model: model.Model}, nil
		},
		recursiveFn: func(_ context.Context, ropts RecursiveOptions) (*RecursiveResult, error) {
			if ropts.MaxIterations != 6 {
				t.Fatalf("expected max iterations 6, got %d", ropts.MaxIterations)
			}
			if ropts.MaxDepth != 2 {
				t.Fatalf("expected max depth 2, got %d", ropts.MaxDepth)
			}
			return &RecursiveResult{
				ReasonResult: ReasonResult{
					Content:      "## Findings\n- Implement guardrails\n- Review Memory #42 and memory 77",
					SearchTime:   25 * time.Millisecond,
					LLMTime:      40 * time.Millisecond,
					TokensIn:     120,
					TokensOut:    240,
					MemoriesUsed: 5,
					FactsUsed:    3,
				},
				Iterations: 3,
				SubQueries: []SubQueryResult{{Depth: 1}, {Depth: 2}},
			}, nil
		},
	}

	report, err := engine.RunBenchmark(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunBenchmark failed: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(report.Results))
	}

	r := report.Results[0]
	if !r.Recursive {
		t.Fatal("expected recursive=true")
	}
	if r.Iterations != 3 {
		t.Fatalf("expected 3 iterations, got %d", r.Iterations)
	}
	if r.FactsUsed != 3 {
		t.Fatalf("expected facts_used=3, got %d", r.FactsUsed)
	}
	if r.RecursiveDepth != 2 {
		t.Fatalf("expected recursive_depth=2, got %d", r.RecursiveDepth)
	}
	if !r.CostKnown {
		t.Fatal("expected cost known for deepseek-v3.2")
	}
	if r.QualitySignals.UniqueMemoryRefs != 2 {
		t.Fatalf("expected 2 unique memory refs, got %d", r.QualitySignals.UniqueMemoryRefs)
	}
}

func TestBenchReport_CompareSection(t *testing.T) {
	report := &BenchReport{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Models:         2,
		Presets:        1,
		CompareMode:    true,
		ComparedModels: []string{"model-a", "model-b"},
		Results: []BenchResult{
			{Label: "model-a", Model: "model-a", Preset: "daily-digest", WallTime: 2 * time.Second, TokensOut: 100, QualitySignals: BenchQualitySignals{WordCount: 120, HasActionableItems: true}},
			{Label: "model-b", Model: "model-b", Preset: "daily-digest", WallTime: 3 * time.Second, TokensOut: 80, QualitySignals: BenchQualitySignals{WordCount: 90, HasActionableItems: false}},
		},
		Summary: []BenchSummary{
			{Label: "model-a", AvgTime: 2.0, Errors: 0},
			{Label: "model-b", AvgTime: 3.0, Errors: 0},
		},
	}

	md := report.FormatMarkdown()
	if !strings.Contains(md, "## A/B Comparison") {
		t.Fatalf("expected compare section in markdown, got:\n%s", md)
	}
}
