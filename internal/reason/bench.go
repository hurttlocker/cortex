package reason

import (
	"context"
	"fmt"
	"regexp"
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
	{Name: "fact-audit", Query: "fact extraction quality type classification missing subjects duplicates"},
	{Name: "conflict-check", Query: "conflicting agent model configuration contradictory settings"},
	{Name: "weekly-dive", Query: "architecture decisions tradeoffs implementation patterns"},
	{Name: "agent-review", Query: "agent performance coordination delegation workflow patterns"},
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

// BenchQualitySignals are lightweight quality markers for output comparison.
type BenchQualitySignals struct {
	HasHeaders         bool `json:"has_headers"`
	HasActionableItems bool `json:"has_actionable_items"`
	WordCount          int  `json:"word_count"`
	UniqueMemoryRefs   int  `json:"unique_memory_refs"`
}

// BenchResult holds one model × preset test result.
type BenchResult struct {
	Model           string              `json:"model"`
	Label           string              `json:"label"`
	Provider        string              `json:"provider"`
	Preset          string              `json:"preset"`
	Query           string              `json:"query"`
	WallTime        time.Duration       `json:"wall_time"`
	SearchTime      time.Duration       `json:"search_time"`
	LLMTime         time.Duration       `json:"llm_time"`
	TokensIn        int                 `json:"tokens_in"`
	TokensOut       int                 `json:"tokens_out"`
	CostUSD         float64             `json:"cost_usd"`
	CostKnown       bool                `json:"cost_known"`
	CostPer1KTokens float64             `json:"cost_per_1k_tokens"`
	Content         string              `json:"content"`
	ContentLen      int                 `json:"content_len"`
	Error           string              `json:"error,omitempty"`
	MemoryCount     int                 `json:"memories_used"`
	FactsUsed       int                 `json:"facts_used"`
	Iterations      int                 `json:"iterations"`
	Recursive       bool                `json:"recursive"`
	RecursiveDepth  int                 `json:"recursive_depth"`
	QualitySignals  BenchQualitySignals `json:"quality_signals"`
}

// BenchReport is the full benchmark output.
type BenchReport struct {
	Timestamp      string         `json:"timestamp"`
	Models         int            `json:"models_tested"`
	Presets        int            `json:"presets_tested"`
	Recursive      bool           `json:"recursive"`
	CompareMode    bool           `json:"compare_mode"`
	ComparedModels []string       `json:"compared_models,omitempty"`
	Results        []BenchResult  `json:"results"`
	Summary        []BenchSummary `json:"summary"`
}

// BenchSummary aggregates a model's performance across all presets.
type BenchSummary struct {
	Label              string  `json:"label"`
	Model              string  `json:"model"`
	Provider           string  `json:"provider"`
	AvgTime            float64 `json:"avg_time_sec"`
	AvgTokens          int     `json:"avg_tokens_out"`
	AvgIterations      float64 `json:"avg_iterations"`
	AvgFactsUsed       float64 `json:"avg_facts_used"`
	TotalCost          float64 `json:"total_cost_usd"`
	AvgCost            float64 `json:"avg_cost_usd"`
	AvgCostPer1KTokens float64 `json:"avg_cost_per_1k_tokens"`
	KnownCostRuns      int     `json:"known_cost_runs"`
	CostUnknownRuns    int     `json:"cost_unknown_runs"`
	Errors             int     `json:"errors"`
	Verdict            string  `json:"verdict"`
}

// BenchOptions configures a benchmark run.
type BenchOptions struct {
	Models         []BenchModel                             // Models to test (nil = DefaultBenchModels)
	Presets        []BenchPreset                            // Presets to test (nil = DefaultBenchPresets)
	IncludeLocal   bool                                     // Include local ollama models
	MaxContext     int                                      // Max context chars (default: 8000)
	Recursive      bool                                     // Use recursive reasoning mode
	MaxIterations  int                                      // Recursive mode only (default: 8)
	MaxDepth       int                                      // Recursive mode only (default: 1)
	CompareMode    bool                                     // Output compare-oriented report sections
	ComparedModels []string                                 // Models from --compare (for report metadata)
	Verbose        bool                                     // Print progress
	ProgressFn     func(model, preset string, i, total int) // Progress callback

	// Test hooks
	llmFactory  func(model BenchModel) (*LLM, error)
	reasonFn    func(ctx context.Context, opts ReasonOptions) (*ReasonResult, error)
	recursiveFn func(ctx context.Context, opts RecursiveOptions) (*RecursiveResult, error)
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

	maxCtx := opts.MaxContext
	if maxCtx <= 0 {
		maxCtx = 8000
	}
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = 8
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 1
	}

	llmFactory := opts.llmFactory
	if llmFactory == nil {
		llmFactory = func(model BenchModel) (*LLM, error) {
			return NewLLM(LLMConfig{Provider: model.Provider, Model: model.Model})
		}
	}

	reasonRunner := opts.reasonFn
	if reasonRunner == nil {
		reasonRunner = func(ctx context.Context, ropts ReasonOptions) (*ReasonResult, error) {
			return e.Reason(ctx, ropts)
		}
	}

	recursiveRunner := opts.recursiveFn
	if recursiveRunner == nil {
		recursiveRunner = func(ctx context.Context, ropts RecursiveOptions) (*RecursiveResult, error) {
			return e.ReasonRecursive(ctx, ropts)
		}
	}

	total := len(models) * len(presets)
	results := make([]BenchResult, 0, total)
	i := 0

	for _, model := range models {
		llm, llmErr := llmFactory(model)

		for _, bp := range presets {
			i++
			if opts.ProgressFn != nil {
				opts.ProgressFn(model.Label, bp.Name, i, total)
			}

			br := BenchResult{
				Model:     model.Model,
				Label:     model.Label,
				Provider:  model.Provider,
				Preset:    bp.Name,
				Query:     bp.Query,
				Recursive: opts.Recursive,
			}

			if llmErr != nil {
				br.Error = fmt.Sprintf("LLM init: %v", llmErr)
				results = append(results, br)
				continue
			}

			origLLM := e.llm
			e.llm = llm

			start := time.Now()
			if opts.Recursive {
				rResult, err := recursiveRunner(ctx, RecursiveOptions{
					Query:         bp.Query,
					Preset:        bp.Name,
					MaxContext:    maxCtx,
					MaxIterations: maxIter,
					MaxDepth:      maxDepth,
				})
				br.WallTime = time.Since(start)

				if err != nil {
					br.Error = err.Error()
				} else {
					br.SearchTime = rResult.SearchTime
					br.LLMTime = rResult.LLMTime
					br.TokensIn = rResult.TokensIn
					br.TokensOut = rResult.TokensOut
					br.Content = rResult.Content
					br.ContentLen = len(rResult.Content)
					br.MemoryCount = rResult.MemoriesUsed
					br.FactsUsed = rResult.FactsUsed
					br.Iterations = maxInt(1, rResult.Iterations)
					br.RecursiveDepth = maxSubQueryDepth(rResult)
					br.QualitySignals = extractQualitySignals(rResult.Content)
					br.CostUSD, br.CostKnown = estimateCost(model.Model, rResult.TokensIn, rResult.TokensOut)
					br.CostPer1KTokens = estimateCostPer1K(br.CostUSD, rResult.TokensIn, rResult.TokensOut, br.CostKnown)
				}
			} else {
				rResult, err := reasonRunner(ctx, ReasonOptions{
					Query:      bp.Query,
					Preset:     bp.Name,
					MaxContext: maxCtx,
				})
				br.WallTime = time.Since(start)

				if err != nil {
					br.Error = err.Error()
				} else {
					br.SearchTime = rResult.SearchTime
					br.LLMTime = rResult.LLMTime
					br.TokensIn = rResult.TokensIn
					br.TokensOut = rResult.TokensOut
					br.Content = rResult.Content
					br.ContentLen = len(rResult.Content)
					br.MemoryCount = rResult.MemoriesUsed
					br.FactsUsed = rResult.FactsUsed
					br.Iterations = 1
					br.RecursiveDepth = 0
					br.QualitySignals = extractQualitySignals(rResult.Content)
					br.CostUSD, br.CostKnown = estimateCost(model.Model, rResult.TokensIn, rResult.TokensOut)
					br.CostPer1KTokens = estimateCostPer1K(br.CostUSD, rResult.TokensIn, rResult.TokensOut, br.CostKnown)
				}
			}

			e.llm = origLLM
			results = append(results, br)
		}
	}

	summary := buildSummary(results, presets)

	return &BenchReport{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Models:         len(models),
		Presets:        len(presets),
		Recursive:      opts.Recursive,
		CompareMode:    opts.CompareMode,
		ComparedModels: opts.ComparedModels,
		Results:        results,
		Summary:        summary,
	}, nil
}

func maxSubQueryDepth(r *RecursiveResult) int {
	if r == nil {
		return 0
	}
	maxDepth := 0
	for _, sq := range r.SubQueries {
		if sq.Depth > maxDepth {
			maxDepth = sq.Depth
		}
	}
	if r.Depth > maxDepth {
		maxDepth = r.Depth
	}
	return maxDepth
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// estimateCost calculates the cost of a run based on token counts.
// Returns cost and whether cost is known.
func estimateCost(model string, tokensIn, tokensOut int) (float64, bool) {
	pricing, ok := ModelPricing[model]
	if !ok {
		return 0, false
	}
	if pricing[0] == 0 && pricing[1] == 0 {
		// Free tier / preview / unknown pricing.
		return 0, false
	}
	cost := (float64(tokensIn) * pricing[0] / 1_000_000) + (float64(tokensOut) * pricing[1] / 1_000_000)
	return cost, true
}

func estimateCostPer1K(cost float64, tokensIn, tokensOut int, costKnown bool) float64 {
	if !costKnown {
		return 0
	}
	totalTokens := tokensIn + tokensOut
	if totalTokens <= 0 {
		return 0
	}
	return cost * 1000 / float64(totalTokens)
}

var (
	headersRe        = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}\s+\S+|\d+\.\s+\*\*[^*]+\*\*)`)
	checklistRe      = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]`)
	bulletRe         = regexp.MustCompile(`(?m)^\s*(?:[-*]|\d+\.)\s+`)
	actionVerbRe     = regexp.MustCompile(`(?i)\b(should|must|recommend(?:ed|ation)?|next\s+step|action\s+item|implement|fix|update|review|investigate|monitor|run|create|prioritize|schedule)\b`)
	wordRe           = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9'/-]*`)
	memoryRefRegexes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bmemory\s+#?(\d+)\b`),
		regexp.MustCompile(`(?i)\bmem(?:ory)?[_ ]id\s*[:=# ]\s*(\d+)\b`),
	}
)

func extractQualitySignals(content string) BenchQualitySignals {
	signals := BenchQualitySignals{}
	if strings.TrimSpace(content) == "" {
		return signals
	}

	signals.HasHeaders = headersRe.MatchString(content)
	hasBullets := bulletRe.MatchString(content)
	hasActionVerbs := actionVerbRe.MatchString(content)
	signals.HasActionableItems = checklistRe.MatchString(content) || (hasBullets && hasActionVerbs)
	signals.WordCount = len(wordRe.FindAllString(content, -1))
	signals.UniqueMemoryRefs = countUniqueMemoryRefs(content)

	return signals
}

func countUniqueMemoryRefs(content string) int {
	if content == "" {
		return 0
	}

	seen := make(map[string]struct{})
	for _, re := range memoryRefRegexes {
		matches := re.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 1 && m[1] != "" {
				seen[m[1]] = struct{}{}
			}
		}
	}
	return len(seen)
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
		s.AvgIterations += float64(maxInt(1, r.Iterations))
		s.AvgFactsUsed += float64(r.FactsUsed)

		if r.CostKnown {
			s.TotalCost += r.CostUSD
			s.KnownCostRuns++
			s.AvgCostPer1KTokens += r.CostPer1KTokens
		} else {
			s.CostUnknownRuns++
		}
	}

	var summaries []BenchSummary
	for _, s := range byModel {
		runs := len(presets) - s.Errors
		if runs > 0 {
			s.AvgTime /= float64(runs)
			s.AvgTokens /= runs
			s.AvgIterations /= float64(runs)
			s.AvgFactsUsed /= float64(runs)
		}
		if s.KnownCostRuns > 0 {
			s.AvgCost = s.TotalCost / float64(s.KnownCostRuns)
			s.AvgCostPer1KTokens /= float64(s.KnownCostRuns)
		}
		s.Verdict = categorize(s)
		summaries = append(summaries, *s)
	}

	// Sort by avg time (errors last)
	sort.Slice(summaries, func(i, j int) bool {
		a, b := summaries[i], summaries[j]
		if a.Errors != b.Errors {
			return a.Errors < b.Errors
		}
		return a.AvgTime < b.AvgTime
	})

	return summaries
}

func categorize(s *BenchSummary) string {
	if s.Errors > 0 {
		return "⚠️ errors"
	}
	if s.Provider == "ollama" {
		return "🔒 private/local"
	}
	if s.KnownCostRuns == 0 {
		return "🕵️ cost unknown"
	}
	switch {
	case s.AvgTime < 2.0 && s.AvgCost < 0.002:
		return "🏆 best overall"
	case s.AvgTime < 3.0:
		return "⚡ fast"
	case s.AvgTime < 10.0:
		return "✅ solid"
	default:
		return "🐌 slow"
	}
}

// FormatMarkdown renders the benchmark report as a publication-ready markdown report.
func (r *BenchReport) FormatMarkdown() string {
	var sb strings.Builder

	date := r.Timestamp
	if len(date) >= 10 {
		date = date[:10]
	}
	mode := "single-pass"
	if r.Recursive {
		mode = "recursive"
	}

	sb.WriteString("# Cortex Reason Benchmark Report\n")
	sb.WriteString(fmt.Sprintf("*%s — %d models × %d presets · mode: %s*\n\n", date, r.Models, r.Presets, mode))
	if r.CompareMode && len(r.ComparedModels) > 0 {
		sb.WriteString(fmt.Sprintf("Compared models: `%s`\n\n", strings.Join(r.ComparedModels, "`, `")))
	}

	r.writeSummaryTable(&sb)
	r.writeWinnerSection(&sb)
	r.writePresetBreakdown(&sb)
	r.writeCostAnalysis(&sb)

	if r.CompareMode {
		r.writeCompareSection(&sb)
	}

	r.writeDetailedResults(&sb)

	return sb.String()
}

func (r *BenchReport) writeSummaryTable(sb *strings.Builder) {
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Model | Provider | Avg Time | Avg Iter | Avg Facts | Avg Tokens | Avg Cost | Cost / 1K tokens | Verdict |\n")
	sb.WriteString("|---|---|---:|---:|---:|---:|---:|---:|---|\n")
	for _, s := range r.Summary {
		sb.WriteString(fmt.Sprintf(
			"| %s | %s | %.2fs | %.1f | %.1f | %d | %s | %s | %s |\n",
			s.Label,
			s.Provider,
			s.AvgTime,
			s.AvgIterations,
			s.AvgFactsUsed,
			s.AvgTokens,
			formatSummaryCost(s.AvgCost, s.KnownCostRuns, s.CostUnknownRuns),
			formatSummaryCostPer1K(s.AvgCostPer1KTokens, s.KnownCostRuns),
			s.Verdict,
		))
	}
	sb.WriteString("\n")
}

func (r *BenchReport) writeWinnerSection(sb *strings.Builder) {
	sb.WriteString("## Winners by Category\n\n")

	valid := make([]BenchSummary, 0, len(r.Summary))
	for _, s := range r.Summary {
		if s.Errors == 0 {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		sb.WriteString("No successful runs to score.\n\n")
		return
	}

	fastest := valid[0]
	richest := valid[0]
	for _, s := range valid[1:] {
		if s.AvgTime < fastest.AvgTime {
			fastest = s
		}
		if s.AvgTokens > richest.AvgTokens {
			richest = s
		}
	}

	sb.WriteString(fmt.Sprintf("- ⚡ **Fastest**: **%s** (%.2fs avg)\n", fastest.Label, fastest.AvgTime))
	sb.WriteString(fmt.Sprintf("- 🧠 **Most verbose output**: **%s** (%d avg output tokens)\n", richest.Label, richest.AvgTokens))

	cheapestFound := false
	cheapest := BenchSummary{}
	for _, s := range valid {
		if s.KnownCostRuns == 0 {
			continue
		}
		if !cheapestFound || s.AvgCostPer1KTokens < cheapest.AvgCostPer1KTokens {
			cheapest = s
			cheapestFound = true
		}
	}
	if cheapestFound {
		sb.WriteString(fmt.Sprintf("- 💸 **Cheapest (known pricing)**: **%s** ($%.4f / 1K tokens)\n", cheapest.Label, cheapest.AvgCostPer1KTokens))
	} else {
		sb.WriteString("- 💸 **Cheapest**: unavailable (all models have unknown pricing)\n")
	}

	sb.WriteString("\n")
}

func (r *BenchReport) writePresetBreakdown(sb *strings.Builder) {
	sb.WriteString("## Per-Preset Breakdown\n\n")
	byPreset := make(map[string][]BenchResult)
	presetOrder := make([]string, 0)
	seen := make(map[string]bool)
	for _, res := range r.Results {
		if !seen[res.Preset] {
			seen[res.Preset] = true
			presetOrder = append(presetOrder, res.Preset)
		}
		byPreset[res.Preset] = append(byPreset[res.Preset], res)
	}

	for _, preset := range presetOrder {
		group := byPreset[preset]
		sb.WriteString(fmt.Sprintf("### %s\n\n", preset))
		sb.WriteString("| Model | Time | Iter | Memories | Facts | Tokens (in→out) | Cost | Quality Signals |\n")
		sb.WriteString("|---|---:|---:|---:|---:|---|---:|---|\n")

		for _, res := range group {
			if res.Error != "" {
				sb.WriteString(fmt.Sprintf("| %s | ❌ | - | - | - | - | - | %s |\n", res.Label, escapeTable(res.Error)))
				continue
			}
			quality := fmt.Sprintf("hdr:%s act:%s words:%d refs:%d",
				boolMark(res.QualitySignals.HasHeaders),
				boolMark(res.QualitySignals.HasActionableItems),
				res.QualitySignals.WordCount,
				res.QualitySignals.UniqueMemoryRefs,
			)
			sb.WriteString(fmt.Sprintf("| %s | %.2fs | %d | %d | %d | %d→%d | %s | %s |\n",
				res.Label,
				res.WallTime.Seconds(),
				maxInt(1, res.Iterations),
				res.MemoryCount,
				res.FactsUsed,
				res.TokensIn,
				res.TokensOut,
				formatResultCost(res),
				quality,
			))
		}
		sb.WriteString("\n")
	}
}

func (r *BenchReport) writeCostAnalysis(sb *strings.Builder) {
	sb.WriteString("## Cost Analysis\n\n")
	sb.WriteString("| Model | Known Cost Runs | Cost Unknown Runs | Total Cost (known) | Avg $ / 1K tokens |\n")
	sb.WriteString("|---|---:|---:|---:|---:|\n")
	for _, s := range r.Summary {
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %s | %s |\n",
			s.Label,
			s.KnownCostRuns,
			s.CostUnknownRuns,
			formatSummaryCost(s.TotalCost, s.KnownCostRuns, s.CostUnknownRuns),
			formatSummaryCostPer1K(s.AvgCostPer1KTokens, s.KnownCostRuns),
		))
	}
	sb.WriteString("\n")
	sb.WriteString("_Note: $0 models are treated as **cost unknown** for free/preview tiers unless pricing is explicitly known._\n\n")
}

func (r *BenchReport) writeCompareSection(sb *strings.Builder) {
	if len(r.Summary) != 2 {
		return
	}

	sb.WriteString("## A/B Comparison\n\n")

	left := r.Summary[0].Label
	right := r.Summary[1].Label
	if len(r.ComparedModels) == 2 {
		left = r.ComparedModels[0]
		right = r.ComparedModels[1]
	}
	sb.WriteString(fmt.Sprintf("Comparing **%s** vs **%s**\n\n", left, right))

	byPreset := make(map[string]map[string]BenchResult)
	presetOrder := make([]string, 0)
	seen := make(map[string]bool)
	for _, res := range r.Results {
		if !seen[res.Preset] {
			seen[res.Preset] = true
			presetOrder = append(presetOrder, res.Preset)
		}
		if byPreset[res.Preset] == nil {
			byPreset[res.Preset] = make(map[string]BenchResult)
		}
		byPreset[res.Preset][res.Label] = res
		byPreset[res.Preset][res.Model] = res
	}

	for _, preset := range presetOrder {
		group := byPreset[preset]
		a, okA := group[left]
		b, okB := group[right]
		if !okA || !okB {
			continue
		}

		sb.WriteString(fmt.Sprintf("### %s\n", preset))
		if a.Error != "" || b.Error != "" {
			sb.WriteString(fmt.Sprintf("- ⚠️ Errors: `%s` vs `%s`\n\n", a.Error, b.Error))
			continue
		}

		speedWinner := left
		speedDelta := b.WallTime.Seconds() - a.WallTime.Seconds()
		if b.WallTime < a.WallTime {
			speedWinner = right
			speedDelta = -speedDelta
		}

		wordsWinner := left
		if b.QualitySignals.WordCount > a.QualitySignals.WordCount {
			wordsWinner = right
		}

		sb.WriteString(fmt.Sprintf("- ⏱️ **Speed winner:** %s (Δ %.2fs)\n", speedWinner, speedDelta))
		sb.WriteString(fmt.Sprintf("- 🧠 **Word count:** %s=%d vs %s=%d (winner: %s)\n",
			left, a.QualitySignals.WordCount,
			right, b.QualitySignals.WordCount,
			wordsWinner,
		))
		sb.WriteString(fmt.Sprintf("- ✅ **Actionable items:** %s=%s, %s=%s\n",
			left, boolMark(a.QualitySignals.HasActionableItems),
			right, boolMark(b.QualitySignals.HasActionableItems),
		))
		sb.WriteString(fmt.Sprintf("- 🧾 **Memory refs:** %s=%d, %s=%d\n\n",
			left, a.QualitySignals.UniqueMemoryRefs,
			right, b.QualitySignals.UniqueMemoryRefs,
		))
	}
}

func (r *BenchReport) writeDetailedResults(sb *strings.Builder) {
	sb.WriteString("## Detailed Results\n\n")
	for _, res := range r.Results {
		if res.Error != "" {
			sb.WriteString(fmt.Sprintf("### %s × %s — ❌ %s\n\n", res.Label, res.Preset, res.Error))
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s × %s — %.2fs, %s\n",
			res.Label,
			res.Preset,
			res.WallTime.Seconds(),
			formatResultCost(res),
		))
		sb.WriteString(fmt.Sprintf("*%d→%d tokens, search %dms, llm %dms, %d memories, %d facts, %d iteration(s), recursive=%t*\n\n",
			res.TokensIn, res.TokensOut,
			res.SearchTime.Milliseconds(), res.LLMTime.Milliseconds(),
			res.MemoryCount, res.FactsUsed,
			maxInt(1, res.Iterations),
			res.Recursive,
		))
		sb.WriteString(fmt.Sprintf("Quality: headers=%s, actionable=%s, words=%d, unique_memory_refs=%d\n\n",
			boolMark(res.QualitySignals.HasHeaders),
			boolMark(res.QualitySignals.HasActionableItems),
			res.QualitySignals.WordCount,
			res.QualitySignals.UniqueMemoryRefs,
		))

		content := res.Content
		if len(content) > 600 {
			content = content[:600] + "..."
		}
		sb.WriteString("```\n" + content + "\n```\n\n")
	}
}

func boolMark(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func escapeTable(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func formatResultCost(res BenchResult) string {
	if !res.CostKnown {
		return "cost unknown"
	}
	return fmt.Sprintf("$%.4f", res.CostUSD)
}

func formatSummaryCost(cost float64, knownRuns int, unknownRuns int) string {
	if knownRuns == 0 {
		if unknownRuns > 0 {
			return "cost unknown"
		}
		return "-"
	}
	return fmt.Sprintf("$%.4f", cost)
}

func formatSummaryCostPer1K(costPer1K float64, knownRuns int) string {
	if knownRuns == 0 {
		return "cost unknown"
	}
	return fmt.Sprintf("$%.4f", costPer1K)
}
