package reason

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

// Engine orchestrates the search → prompt → LLM → output pipeline.
type Engine struct {
	searchEngine *search.Engine
	store        store.Store
	llm          *LLM
	configDir    string // ~/.cortex
}

// EngineConfig configures the reason engine.
type EngineConfig struct {
	SearchEngine *search.Engine
	Store        store.Store
	LLM         *LLM
	ConfigDir    string
}

// ReasonOptions configures a reasoning request.
type ReasonOptions struct {
	Query      string // The question or topic
	Preset     string // Preset name (default: "daily-digest")
	Project    string // Scope to project (empty = all)
	MaxTokens  int    // Override preset max_tokens
	MaxContext int    // Max context chars to send to LLM (default: 8000)
	JSONOutput bool   // Output as JSON
}

// ReasonResult holds the output of a reasoning run.
type ReasonResult struct {
	Content      string        `json:"content"`
	Preset       string        `json:"preset"`
	Query        string        `json:"query"`
	Project      string        `json:"project,omitempty"`
	Model        string        `json:"model"`
	Provider     string        `json:"provider"`
	MemoriesUsed int           `json:"memories_used"`
	FactsUsed    int           `json:"facts_used"`
	Duration     time.Duration `json:"duration"`
	SearchTime   time.Duration `json:"search_time"`
	LLMTime      time.Duration `json:"llm_time"`
	TokensIn     int           `json:"tokens_in"`
	TokensOut    int           `json:"tokens_out"`
}

// NewEngine creates a new reasoning engine.
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		searchEngine: cfg.SearchEngine,
		store:        cfg.Store,
		llm:          cfg.LLM,
		configDir:    cfg.ConfigDir,
	}
}

// Reason executes the full search → prompt → LLM → output pipeline.
func (e *Engine) Reason(ctx context.Context, opts ReasonOptions) (*ReasonResult, error) {
	start := time.Now()

	// 1. Load preset
	presetName := opts.Preset
	if presetName == "" {
		presetName = "daily-digest"
	}

	preset, err := GetPreset(presetName, e.configDir)
	if err != nil {
		return nil, err
	}

	maxTokens := preset.MaxTokens
	if opts.MaxTokens > 0 {
		maxTokens = opts.MaxTokens
	}

	maxContext := opts.MaxContext
	if maxContext <= 0 {
		maxContext = 8000 // Safe default for small models
	}

	// 2. Search for relevant context
	searchStart := time.Now()
	searchOpts := search.Options{
		Limit:   preset.SearchLimit,
		Project: opts.Project,
	}

	// Parse search mode
	if preset.SearchMode != "" {
		mode, err := search.ParseMode(preset.SearchMode)
		if err == nil {
			searchOpts.Mode = mode
		}
	}

	query := opts.Query
	if query == "" {
		query = preset.Name // Use preset name as default query
	}

	results, err := e.searchEngine.Search(ctx, query, searchOpts)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	searchTime := time.Since(searchStart)

	// 3. Build confidence-aware context
	contextStr, memoriesUsed := buildConfidenceContext(ctx, e.store, results, maxContext)

	// 4. Gather relevant facts
	factsStr, factsUsed := gatherFacts(ctx, e.store, results, maxContext-len(contextStr))

	// 5. Build the prompt
	fullContext := contextStr
	if factsStr != "" {
		fullContext += "\n\n--- Extracted Facts ---\n" + factsStr
	}

	userPrompt := expandTemplate(preset.Template, fullContext, query)

	// 6. Call LLM
	messages := []ChatMessage{
		{Role: "system", Content: preset.System},
		{Role: "user", Content: userPrompt},
	}

	llmStart := time.Now()
	llmResult, err := e.llm.Chat(ctx, messages, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}
	llmTime := time.Since(llmStart)

	return &ReasonResult{
		Content:      llmResult.Content,
		Preset:       presetName,
		Query:        query,
		Project:      opts.Project,
		Model:        llmResult.Model,
		Provider:     llmResult.Provider,
		MemoriesUsed: memoriesUsed,
		FactsUsed:    factsUsed,
		Duration:     time.Since(start),
		SearchTime:   searchTime,
		LLMTime:      llmTime,
		TokensIn:     llmResult.PromptTokens,
		TokensOut:    llmResult.CompletionTokens,
	}, nil
}

// buildConfidenceContext creates a context string with confidence annotations.
// This is the key differentiator — the LLM sees confidence scores and can
// weight its reasoning accordingly.
func buildConfidenceContext(ctx context.Context, st store.Store, results []search.Result, maxChars int) (string, int) {
	if len(results) == 0 {
		return "(no memories found)", 0
	}

	var sb strings.Builder
	used := 0

	for _, r := range results {
		// Get confidence for this memory's facts
		confidence := estimateConfidence(ctx, st, r.MemoryID)

		// Format with confidence indicator
		var prefix string
		switch {
		case confidence >= 0.8:
			prefix = fmt.Sprintf("[%.2f] ", confidence)
		case confidence >= 0.5:
			prefix = fmt.Sprintf("[%.2f] ⚡ ", confidence)
		default:
			prefix = fmt.Sprintf("[%.2f] ⚠️ STALE: ", confidence)
		}

		entry := fmt.Sprintf("%s%s\n  Source: %s", prefix, truncateContent(r.Content, 300), r.SourceFile)
		if r.Project != "" {
			entry += fmt.Sprintf(" | Project: %s", r.Project)
		}
		entry += "\n\n"

		if sb.Len()+len(entry) > maxChars {
			break
		}
		sb.WriteString(entry)
		used++
	}

	return sb.String(), used
}

// estimateConfidence gets the average confidence of facts linked to a memory.
func estimateConfidence(ctx context.Context, st store.Store, memoryID int64) float64 {
	facts, err := st.ListFacts(ctx, store.ListOpts{
		Limit: 10,
	})
	if err != nil || len(facts) == 0 {
		return 0.85 // Default if we can't look up facts
	}

	// Find facts for this memory
	var total float64
	var count int
	for _, f := range facts {
		if f.MemoryID == memoryID {
			total += f.Confidence
			count++
		}
	}
	if count == 0 {
		return 0.85 // Default
	}
	return total / float64(count)
}

// gatherFacts collects relevant extracted facts for additional context.
func gatherFacts(ctx context.Context, st store.Store, results []search.Result, maxChars int) (string, int) {
	if maxChars <= 0 || len(results) == 0 {
		return "", 0
	}

	// Collect memory IDs from search results
	memIDs := make(map[int64]bool)
	for _, r := range results {
		memIDs[r.MemoryID] = true
	}

	// Get facts for these memories
	facts, err := st.ListFacts(ctx, store.ListOpts{Limit: 200})
	if err != nil {
		return "", 0
	}

	// Filter to relevant facts and sort by confidence
	type scoredFact struct {
		fact *store.Fact
	}
	var relevant []scoredFact
	for _, f := range facts {
		if memIDs[f.MemoryID] {
			relevant = append(relevant, scoredFact{fact: f})
		}
	}

	sort.Slice(relevant, func(i, j int) bool {
		return relevant[i].fact.Confidence > relevant[j].fact.Confidence
	})

	var sb strings.Builder
	used := 0
	for _, sf := range relevant {
		entry := fmt.Sprintf("[%.2f] %s: %s %s %s\n",
			sf.fact.Confidence, sf.fact.FactType, sf.fact.Subject, sf.fact.Predicate, sf.fact.Object)
		if sb.Len()+len(entry) > maxChars {
			break
		}
		sb.WriteString(entry)
		used++
	}

	return sb.String(), used
}

// expandTemplate replaces {{context}} and {{.Query}} in the template.
func expandTemplate(tmpl, contextStr, query string) string {
	result := strings.ReplaceAll(tmpl, "{{context}}", contextStr)
	result = strings.ReplaceAll(result, "{{.Query}}", query)

	// Handle conditional {{if .Query}}...{{end}} blocks
	if query != "" {
		result = strings.ReplaceAll(result, "{{if .Query}}", "")
		result = strings.ReplaceAll(result, "{{end}}", "")
	} else {
		// Remove conditional blocks when no query
		for {
			start := strings.Index(result, "{{if .Query}}")
			if start == -1 {
				break
			}
			end := strings.Index(result[start:], "{{end}}")
			if end == -1 {
				break
			}
			result = result[:start] + result[start+end+len("{{end}}"):]
		}
	}

	return result
}

func truncateContent(s string, max int) string {
	// Replace newlines for readability
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
