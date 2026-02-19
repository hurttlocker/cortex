package reason

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BenchModel defines a model to benchmark.
type BenchModel struct {
	Label    string // Display name
	Provider string // "ollama" or "openrouter"
	Model    string // Model ID
}

// DefaultBenchModels is the standard model lineup for benchmarks.
// Covers the cost/speed/quality spectrum. Updated as new models drop.
var DefaultBenchModels = []BenchModel{
	{Label: "gpt-5.1-codex-mini", Provider: "openrouter", Model: "openai/gpt-5.1-codex-mini"},
	{Label: "gpt-5.1-codex", Provider: "openrouter", Model: "openai/gpt-5.1-codex"},
	{Label: "gemini-3-flash", Provider: "openrouter", Model: "google/gemini-3-flash-preview"},
	{Label: "deepseek-v3.2", Provider: "openrouter", Model: "deepseek/deepseek-v3.2"},
	{Label: "claude-sonnet-4.5", Provider: "openrouter", Model: "anthropic/claude-sonnet-4.5"},
}

// LocalBenchModels are the local ollama models to test.
var LocalBenchModels = []BenchModel{
	{Label: "phi4-mini", Provider: "ollama", Model: "phi4-mini"},
	{Label: "qwen3:4b", Provider: "ollama", Model: "qwen3:4b"},
	{Label: "gemma2:9b", Provider: "ollama", Model: "gemma2:9b"},
}

// BenchPreset defines a test query for benchmarking.
type BenchPreset struct {
	Name  string
	Query string
}

// DefaultBenchPresets are the standard test queries.
var DefaultBenchPresets = []BenchPreset{
	{Name: "daily-digest", Query: "recent development activity and decisions"},
	{Name: "fact-audit", Query: "data quality issues in extracted facts"},
	{Name: "conflict-check", Query: "contradictory or inconsistent information"},
}

// Pricing per million tokens (input, output) — updated Feb 2026.
// Models with {0, 0} are free tier / preview / pricing TBD.
var ModelPricing = map[string][2]float64{
	"google/gemini-2.5-flash":       {0.15, 0.60},
	"google/gemini-3-flash-preview": {0.15, 0.60}, // Preview pricing, may change
	"deepseek/deepseek-chat":        {0.14, 0.28},
	"deepseek/deepseek-v3.2":        {0.14, 0.28}, // Same tier as v3
	"meta-llama/llama-4-maverick":   {0.20, 0.60},
	"x-ai/grok-4.1-fast":            {0.20, 0.50},
	"minimax/minimax-m2.5":          {0.10, 1.10},
	"deepseek/deepseek-r1":          {0.55, 2.19},
	"google/gemini-2.5-pro":         {1.25, 10.00},
	"anthropic/claude-sonnet-4":     {3.00, 15.00},
	"qwen/qwen-3-235b":              {0.20, 1.20},
	"openai/gpt-oss-120b":           {0, 0}, // Preview/free
	"openai/gpt-oss-safeguard-20b":  {0, 0}, // Preview/free
}

// BenchResult holds one model × preset test result.
type BenchResult struct {
	Model       string        `json:"model"`
	Label       string        `json:"label"`
	Provider    string        `json:"provider"`
	Preset      string        `json:"preset"`
	Query       string        `json:"query"`
	WallTime    time.Duration `json:"wall_time"`
	SearchTime  time.Duration `json:"search_time"`
	LLMTime     time.Duration `json:"llm_time"`
	TokensIn    int           `json:"tokens_in"`
	TokensOut   int           `json:"tokens_out"`
	CostUSD     float64       `json:"cost_usd"`
	Content     string        `json:"content"`
	ContentLen  int           `json:"content_len"`
	Error       string        `json:"error,omitempty"`
	MemoryCount int           `json:"memories_used"`
}

// BenchReport is the full benchmark output.
type BenchReport struct {
	Timestamp string         `json:"timestamp"`
	Models    int            `json:"models_tested"`
	Presets   int            `json:"presets_tested"`
	Results   []BenchResult  `json:"results"`
	Summary   []BenchSummary `json:"summary"`
}

// BenchSummary aggregates a model's performance across all presets.
type BenchSummary struct {
	Label     string  `json:"label"`
	Model     string  `json:"model"`
	Provider  string  `json:"provider"`
	AvgTime   float64 `json:"avg_time_sec"`
	AvgTokens int     `json:"avg_tokens_out"`
	TotalCost float64 `json:"total_cost_usd"`
	AvgCost   float64 `json:"avg_cost_usd"`
	Errors    int     `json:"errors"`
	Verdict   string  `json:"verdict"`
}

// BenchOptions configures a benchmark run.
type BenchOptions struct {
	Models       []BenchModel                             // Models to test (nil = DefaultBenchModels)
	Presets      []BenchPreset                            // Presets to test (nil = DefaultBenchPresets)
	IncludeLocal bool                                     // Include local ollama models
	MaxContext   int                                      // Max context chars (default: 8000)
	Verbose      bool                                     // Print progress
	ProgressFn   func(model, preset string, i, total int) // Progress callback
}

// RunBenchmark executes the full benchmark suite.
func (e *Engine) RunBenchmark(ctx context.Context, opts BenchOptions) (*BenchReport, error) {
	models := opts.Models
	if models == nil {
		models = DefaultBenchModels
		if opts.IncludeLocal {
			models = append(models, LocalBenchModels...)
		}
	}

	presets := opts.Presets
	if presets == nil {
		presets = DefaultBenchPresets
	}

	total := len(models) * len(presets)
	var results []BenchResult
	i := 0

	for _, model := range models {
		// Create LLM client for this model
		llm, err := NewLLM(LLMConfig{
			Provider: model.Provider,
			Model:    model.Model,
		})

		for _, bp := range presets {
			i++
			if opts.ProgressFn != nil {
				opts.ProgressFn(model.Label, bp.Name, i, total)
			}

			if err != nil {
				results = append(results, BenchResult{
					Model:    model.Model,
					Label:    model.Label,
					Provider: model.Provider,
					Preset:   bp.Name,
					Query:    bp.Query,
					Error:    fmt.Sprintf("LLM init: %v", err),
				})
				continue
			}

			// Swap the LLM for this model
			origLLM := e.llm
			e.llm = llm

			maxCtx := opts.MaxContext
			if maxCtx <= 0 {
				maxCtx = 8000
			}

			start := time.Now()
			result, reasonErr := e.Reason(ctx, ReasonOptions{
				Query:      bp.Query,
				Preset:     bp.Name,
				MaxContext: maxCtx,
			})
			wallTime := time.Since(start)

			e.llm = origLLM

			br := BenchResult{
				Model:    model.Model,
				Label:    model.Label,
				Provider: model.Provider,
				Preset:   bp.Name,
				Query:    bp.Query,
				WallTime: wallTime,
			}

			if reasonErr != nil {
				br.Error = reasonErr.Error()
			} else {
				br.SearchTime = result.SearchTime
				br.LLMTime = result.LLMTime
				br.TokensIn = result.TokensIn
				br.TokensOut = result.TokensOut
				br.Content = result.Content
				br.ContentLen = len(result.Content)
				br.MemoryCount = result.MemoriesUsed
				br.CostUSD = estimateCost(model.Model, result.TokensIn, result.TokensOut)
			}

			results = append(results, br)
		}
	}

	// Build summary
	summary := buildSummary(results, presets)

	return &BenchReport{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Models:    len(models),
		Presets:   len(presets),
		Results:   results,
		Summary:   summary,
	}, nil
}

// estimateCost calculates the cost of a run based on token counts.
func estimateCost(model string, tokensIn, tokensOut int) float64 {
	pricing, ok := ModelPricing[model]
	if !ok {
		return 0
	}
	return (float64(tokensIn) * pricing[0] / 1_000_000) + (float64(tokensOut) * pricing[1] / 1_000_000)
}

// buildSummary aggregates results by model.
func buildSummary(results []BenchResult, presets []BenchPreset) []BenchSummary {
	byModel := make(map[string]*BenchSummary)
	for _, r := range results {
		s, ok := byModel[r.Label]
		if !ok {
			s = &BenchSummary{
				Label:    r.Label,
				Model:    r.Model,
				Provider: r.Provider,
			}
			byModel[r.Label] = s
		}
		if r.Error != "" {
			s.Errors++
			continue
		}
		s.AvgTime += r.WallTime.Seconds()
		s.AvgTokens += r.TokensOut
		s.TotalCost += r.CostUSD
	}

	var summaries []BenchSummary
	for _, s := range byModel {
		runs := len(presets) - s.Errors
		if runs > 0 {
			s.AvgTime /= float64(runs)
			s.AvgTokens /= runs
			s.AvgCost = s.TotalCost / float64(runs)
		}
		s.Verdict = categorize(s)
		summaries = append(summaries, *s)
	}

	// Sort by avg time
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].AvgTime < summaries[j].AvgTime
	})

	return summaries
}

func categorize(s *BenchSummary) string {
	if s.Errors > 0 {
		return "⚠️ errors"
	}
	switch {
	case s.AvgTime < 2.0 && s.AvgCost < 0.002:
		return "🏆 best overall"
	case s.AvgTime < 3.0:
		return "⚡ fast"
	case s.AvgTime < 10.0:
		return "✅ solid"
	case s.AvgCost == 0:
		return "🔒 private (local)"
	default:
		return "🐌 slow"
	}
}

// FormatMarkdown renders the benchmark report as a markdown table.
func (r *BenchReport) FormatMarkdown() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Cortex Reason Benchmark\n"))
	sb.WriteString(fmt.Sprintf("*%s — %d models × %d presets*\n\n", r.Timestamp[:10], r.Models, r.Presets))

	// Summary table
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Model | Provider | Avg Time | Avg Tokens | Avg Cost | Verdict |\n")
	sb.WriteString("|---|---|---|---|---|---|\n")
	for _, s := range r.Summary {
		sb.WriteString(fmt.Sprintf("| %s | %s | %.1fs | %d | $%.4f | %s |\n",
			s.Label, s.Provider, s.AvgTime, s.AvgTokens, s.AvgCost, s.Verdict))
	}

	// Detailed results
	sb.WriteString("\n## Detailed Results\n\n")
	for _, res := range r.Results {
		if res.Error != "" {
			sb.WriteString(fmt.Sprintf("### %s × %s — ❌ %s\n\n", res.Label, res.Preset, res.Error))
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s × %s — %.1fs, $%.4f\n", res.Label, res.Preset, res.WallTime.Seconds(), res.CostUSD))
		sb.WriteString(fmt.Sprintf("*%d→%d tokens, search %dms, llm %dms, %d memories*\n\n",
			res.TokensIn, res.TokensOut,
			res.SearchTime.Milliseconds(), res.LLMTime.Milliseconds(),
			res.MemoryCount))

		// Truncate content for report
		content := res.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString("```\n" + content + "\n```\n\n")
	}

	return sb.String()
}
