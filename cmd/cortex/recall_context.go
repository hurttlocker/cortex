package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	cfgresolver "github.com/hurttlocker/cortex/internal/config"
	"github.com/hurttlocker/cortex/internal/rerank"
	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

const (
	retrievalVisibilityPromptSafe   = "prompt_safe"
	retrievalVisibilityEvidenceOnly = "evidence_only"
	retrievalVisibilityJournalOnly  = "journal_only"
	retrievalVisibilityRetired      = "retired"
)

type recallQueryOptions struct {
	Query                 string
	SearchMode            search.Mode
	Limit                 int
	MaxItems              int
	MaxTokens             int
	MinScore              float64
	EmbedFlag             string
	Project               string
	ClassFlag             string
	Agent                 string
	Channel               string
	SessionKey            string
	After                 string
	Before                string
	Source                string
	JSONOutput            bool
	AllowEvidenceFallback bool
	BoostAgent            string
	BoostChannel          string
	BoostSessionKey       string
	SourceBoostFlags      []string
	ScopeFlags            []string
	RerankMode            rerank.Mode
}

type recallFactView struct {
	FactID      int64   `json:"fact_id"`
	Subject     string  `json:"subject"`
	Predicate   string  `json:"predicate"`
	Object      string  `json:"object"`
	FactType    string  `json:"fact_type"`
	Confidence  float64 `json:"confidence"`
	State       string  `json:"state,omitempty"`
	PromptValue string  `json:"prompt_value,omitempty"`
}

type recallItem struct {
	MemoryID            int64            `json:"memory_id"`
	FactIDs             []int64          `json:"fact_ids"`
	Content             string           `json:"content"`
	Snippet             string           `json:"snippet,omitempty"`
	PromptText          string           `json:"prompt_text,omitempty"`
	SourceFile          string           `json:"source_file"`
	SourceLine          int              `json:"source_line,omitempty"`
	SourceSection       string           `json:"source_section,omitempty"`
	SourceTier          string           `json:"source_tier,omitempty"`
	Project             string           `json:"project,omitempty"`
	MemoryClass         string           `json:"memory_class,omitempty"`
	Score               float64          `json:"score"`
	MatchType           string           `json:"match_type"`
	PromptEligible      bool             `json:"prompt_eligible"`
	RetrievalVisibility string           `json:"retrieval_visibility"`
	DropReasons         []string         `json:"drop_reasons,omitempty"`
	Facts               []recallFactView `json:"facts,omitempty"`
}

type recallDiagnostics struct {
	Searched        int            `json:"searched"`
	FactBacked      int            `json:"fact_backed"`
	PromptEligible  int            `json:"prompt_eligible"`
	EvidenceOnly    int            `json:"evidence_only"`
	JournalOnly     int            `json:"journal_only"`
	Retired         int            `json:"retired"`
	DropReasonCount map[string]int `json:"drop_reason_count,omitempty"`
}

type recallResponse struct {
	Query       string            `json:"query"`
	Items       []recallItem      `json:"items"`
	Diagnostics recallDiagnostics `json:"diagnostics"`
}

type contextDiagnostics struct {
	Searched        int            `json:"searched"`
	FactBacked      int            `json:"fact_backed"`
	JournalOnly     int            `json:"journal_only"`
	DroppedByPolicy int            `json:"dropped_by_policy"`
	DroppedByBudget int            `json:"dropped_by_budget"`
	DroppedByLimit  int            `json:"dropped_by_limit"`
	Selected        int            `json:"selected"`
	FallbackUsed    bool           `json:"fallback_used"`
	DropReasonCount map[string]int `json:"drop_reason_count,omitempty"`
}

type contextResponse struct {
	Query           string             `json:"query"`
	Items           []recallItem       `json:"items"`
	Dropped         []recallItem       `json:"dropped,omitempty"`
	StructuredBlock string             `json:"structured_block"`
	TokenCount      int                `json:"token_count"`
	Diagnostics     contextDiagnostics `json:"diagnostics"`
}

func runRecall(args []string) error {
	opts, err := parseRecallQueryOptions(args, "recall")
	if err != nil {
		return err
	}

	items, diag, err := buildRecallItems(context.Background(), opts)
	if err != nil {
		return err
	}

	resp := recallResponse{
		Query:       opts.Query,
		Items:       items,
		Diagnostics: diag,
	}

	if opts.JSONOutput || !isTTY() {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("Recall for %q (%d item", opts.Query, len(items))
	if len(items) != 1 {
		fmt.Print("s")
	}
	fmt.Println(")")
	fmt.Println()

	for i, item := range items {
		fmt.Printf("  %d. [%.2f] %s  eligible:%t  #%d\n", i+1, item.Score, item.RetrievalVisibility, item.PromptEligible, item.MemoryID)
		fmt.Printf("     %s\n", truncateTTYRecall(item.PromptText, item.Content))
		if item.SourceFile != "" {
			fmt.Printf("     source: %s\n", item.SourceFile)
		}
		if len(item.DropReasons) > 0 {
			fmt.Printf("     reasons: %s\n", strings.Join(item.DropReasons, ", "))
		}
	}

	return nil
}

func runContextCommand(args []string) error {
	opts, err := parseRecallQueryOptions(args, "context")
	if err != nil {
		return err
	}

	items, diag, err := buildRecallItems(context.Background(), opts)
	if err != nil {
		return err
	}

	selected, dropped, block, tokenCount, ctxDiag := buildContextSelection(items, opts.MaxItems, opts.MaxTokens, opts.AllowEvidenceFallback)
	ctxDiag.Searched = diag.Searched
	ctxDiag.FactBacked = diag.FactBacked
	ctxDiag.JournalOnly = diag.JournalOnly
	if ctxDiag.DropReasonCount == nil {
		ctxDiag.DropReasonCount = map[string]int{}
	}

	resp := contextResponse{
		Query:           opts.Query,
		Items:           selected,
		Dropped:         dropped,
		StructuredBlock: block,
		TokenCount:      tokenCount,
		Diagnostics:     ctxDiag,
	}

	if opts.JSONOutput || !isTTY() {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if block == "" {
		fmt.Printf("No prompt-safe context for %q\n", opts.Query)
		if !opts.AllowEvidenceFallback {
			fmt.Println("Hint: retry with --allow-evidence-fallback to inject the strongest evidence-only result.")
		}
		return nil
	}

	fmt.Printf("Context for %q (%d item", opts.Query, len(selected))
	if len(selected) != 1 {
		fmt.Print("s")
	}
	fmt.Printf(", ~%d tokens)\n\n", tokenCount)
	fmt.Println(block)
	return nil
}

func parseRecallQueryOptions(args []string, command string) (recallQueryOptions, error) {
	opts := recallQueryOptions{
		SearchMode: search.ModeHybrid,
		Limit:      8,
		MaxItems:   6,
		MaxTokens:  450,
		MinScore:   -1,
		RerankMode: rerank.ModeAuto,
	}

	var queryParts []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--mode" && i+1 < len(args):
			i++
			mode, err := search.ParseMode(args[i])
			if err != nil {
				return opts, err
			}
			opts.SearchMode = mode
		case strings.HasPrefix(args[i], "--mode="):
			mode, err := search.ParseMode(strings.TrimPrefix(args[i], "--mode="))
			if err != nil {
				return opts, err
			}
			opts.SearchMode = mode
		case args[i] == "--limit" && i+1 < len(args):
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 || v > 100 {
				return opts, fmt.Errorf("--limit must be between 1 and 100")
			}
			opts.Limit = v
		case strings.HasPrefix(args[i], "--limit="):
			v, err := strconv.Atoi(strings.TrimPrefix(args[i], "--limit="))
			if err != nil || v < 1 || v > 100 {
				return opts, fmt.Errorf("--limit must be between 1 and 100")
			}
			opts.Limit = v
		case args[i] == "--max-items" && i+1 < len(args):
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 || v > 50 {
				return opts, fmt.Errorf("--max-items must be between 1 and 50")
			}
			opts.MaxItems = v
		case strings.HasPrefix(args[i], "--max-items="):
			v, err := strconv.Atoi(strings.TrimPrefix(args[i], "--max-items="))
			if err != nil || v < 1 || v > 50 {
				return opts, fmt.Errorf("--max-items must be between 1 and 50")
			}
			opts.MaxItems = v
		case args[i] == "--max-tokens" && i+1 < len(args):
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 50 || v > 20000 {
				return opts, fmt.Errorf("--max-tokens must be between 50 and 20000")
			}
			opts.MaxTokens = v
		case strings.HasPrefix(args[i], "--max-tokens="):
			v, err := strconv.Atoi(strings.TrimPrefix(args[i], "--max-tokens="))
			if err != nil || v < 50 || v > 20000 {
				return opts, fmt.Errorf("--max-tokens must be between 50 and 20000")
			}
			opts.MaxTokens = v
		case (args[i] == "--min-score" || args[i] == "--min-confidence") && i+1 < len(args):
			i++
			v, err := strconv.ParseFloat(args[i], 64)
			if err != nil {
				return opts, fmt.Errorf("invalid --min-score value: %s", args[i])
			}
			opts.MinScore = v
		case strings.HasPrefix(args[i], "--min-score="):
			v, err := strconv.ParseFloat(strings.TrimPrefix(args[i], "--min-score="), 64)
			if err != nil {
				return opts, fmt.Errorf("invalid --min-score value: %s", strings.TrimPrefix(args[i], "--min-score="))
			}
			opts.MinScore = v
		case strings.HasPrefix(args[i], "--min-confidence="):
			v, err := strconv.ParseFloat(strings.TrimPrefix(args[i], "--min-confidence="), 64)
			if err != nil {
				return opts, fmt.Errorf("invalid --min-score value: %s", strings.TrimPrefix(args[i], "--min-confidence="))
			}
			opts.MinScore = v
		case args[i] == "--embed" && i+1 < len(args):
			i++
			opts.EmbedFlag = args[i]
		case strings.HasPrefix(args[i], "--embed="):
			opts.EmbedFlag = strings.TrimPrefix(args[i], "--embed=")
		case args[i] == "--project" && i+1 < len(args):
			i++
			opts.Project = args[i]
		case strings.HasPrefix(args[i], "--project="):
			opts.Project = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--class" && i+1 < len(args):
			i++
			opts.ClassFlag = args[i]
		case strings.HasPrefix(args[i], "--class="):
			opts.ClassFlag = strings.TrimPrefix(args[i], "--class=")
		case args[i] == "--agent" && i+1 < len(args):
			i++
			opts.Agent = args[i]
		case strings.HasPrefix(args[i], "--agent="):
			opts.Agent = strings.TrimPrefix(args[i], "--agent=")
		case args[i] == "--channel" && i+1 < len(args):
			i++
			opts.Channel = args[i]
		case strings.HasPrefix(args[i], "--channel="):
			opts.Channel = strings.TrimPrefix(args[i], "--channel=")
		case args[i] == "--session-key" && i+1 < len(args):
			i++
			opts.SessionKey = args[i]
		case strings.HasPrefix(args[i], "--session-key="):
			opts.SessionKey = strings.TrimPrefix(args[i], "--session-key=")
		case args[i] == "--after" && i+1 < len(args):
			i++
			opts.After = args[i]
		case strings.HasPrefix(args[i], "--after="):
			opts.After = strings.TrimPrefix(args[i], "--after=")
		case args[i] == "--before" && i+1 < len(args):
			i++
			opts.Before = args[i]
		case strings.HasPrefix(args[i], "--before="):
			opts.Before = strings.TrimPrefix(args[i], "--before=")
		case args[i] == "--source" && i+1 < len(args):
			i++
			opts.Source = args[i]
		case strings.HasPrefix(args[i], "--source="):
			opts.Source = strings.TrimPrefix(args[i], "--source=")
		case args[i] == "--boost-agent" && i+1 < len(args):
			i++
			opts.BoostAgent = args[i]
		case strings.HasPrefix(args[i], "--boost-agent="):
			opts.BoostAgent = strings.TrimPrefix(args[i], "--boost-agent=")
		case args[i] == "--boost-channel" && i+1 < len(args):
			i++
			opts.BoostChannel = args[i]
		case strings.HasPrefix(args[i], "--boost-channel="):
			opts.BoostChannel = strings.TrimPrefix(args[i], "--boost-channel=")
		case args[i] == "--boost-session-key" && i+1 < len(args):
			i++
			opts.BoostSessionKey = args[i]
		case strings.HasPrefix(args[i], "--boost-session-key="):
			opts.BoostSessionKey = strings.TrimPrefix(args[i], "--boost-session-key=")
		case args[i] == "--scope" && i+1 < len(args):
			i++
			opts.ScopeFlags = append(opts.ScopeFlags, args[i])
		case strings.HasPrefix(args[i], "--scope="):
			opts.ScopeFlags = append(opts.ScopeFlags, strings.TrimPrefix(args[i], "--scope="))
		case args[i] == "--source-boost" && i+1 < len(args):
			i++
			opts.SourceBoostFlags = append(opts.SourceBoostFlags, args[i])
		case strings.HasPrefix(args[i], "--source-boost="):
			opts.SourceBoostFlags = append(opts.SourceBoostFlags, strings.TrimPrefix(args[i], "--source-boost="))
		case args[i] == "--allow-evidence-fallback", args[i] == "--prompt-lenient":
			opts.AllowEvidenceFallback = true
		case args[i] == "--json":
			opts.JSONOutput = true
		case args[i] == "--rerank":
			if i+1 < len(args) {
				if parsed, err := rerank.ParseMode(args[i+1]); err == nil {
					i++
					opts.RerankMode = parsed
					continue
				}
			}
			opts.RerankMode = rerank.ModeOn
		case strings.HasPrefix(args[i], "--rerank="):
			parsed, err := rerank.ParseMode(strings.TrimPrefix(args[i], "--rerank="))
			if err != nil {
				return opts, err
			}
			opts.RerankMode = parsed
		case strings.HasPrefix(args[i], "-"):
			return opts, fmt.Errorf("unknown flag: %s\nusage: cortex %s <query> [--mode keyword|semantic|hybrid|rrf] [--limit N] [--max-items N] [--max-tokens N] [--embed <provider/model>] [--rerank[=auto|on|off]] [--min-score N] [--class rule,decision] [--project <name>] [--agent <id>] [--channel <name>] [--session-key <key>] [--scope agent:<id>|entity:<id>|session:<id>|project:<id>] [--boost-agent <id>] [--boost-channel <name>] [--boost-session-key <key>] [--source <provider>] [--source-boost <prefix[:weight]>] [--after YYYY-MM-DD] [--before YYYY-MM-DD] [--allow-evidence-fallback] [--json]", args[i], command)
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	opts.Query = strings.TrimSpace(strings.Join(queryParts, " "))
	if opts.Query == "" {
		return opts, fmt.Errorf("usage: cortex %s <query> [--mode keyword|semantic|hybrid|rrf] [--limit N] [--max-items N] [--max-tokens N] [--embed <provider/model>] [--rerank[=auto|on|off]] [--min-score N] [--class rule,decision] [--project <name>] [--agent <id>] [--channel <name>] [--session-key <key>] [--scope agent:<id>|entity:<id>|session:<id>|project:<id>] [--boost-agent <id>] [--boost-channel <name>] [--boost-session-key <key>] [--source <provider>] [--source-boost <prefix[:weight]>] [--after YYYY-MM-DD] [--before YYYY-MM-DD] [--allow-evidence-fallback] [--json]", command)
	}

	return opts, nil
}

func buildRecallItems(ctx context.Context, opts recallQueryOptions) ([]recallItem, recallDiagnostics, error) {
	resolvedCfg, err := cfgresolver.ResolveConfig(cfgresolver.ResolveOptions{})
	if err != nil {
		return nil, recallDiagnostics{}, fmt.Errorf("loading config: %w", err)
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return nil, recallDiagnostics{}, fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	engine, err := newSearchEngineForMode(s, opts.SearchMode, opts.EmbedFlag)
	if err != nil {
		return nil, recallDiagnostics{}, err
	}
	if err := configureSearchReranker(engine, opts.RerankMode, true); err != nil {
		return nil, recallDiagnostics{}, err
	}

	classes, err := store.ParseMemoryClassList(opts.ClassFlag)
	if err != nil {
		return nil, recallDiagnostics{}, err
	}
	scopeFilters, err := parseSearchScopeFlags(opts.ScopeFlags)
	if err != nil {
		return nil, recallDiagnostics{}, err
	}

	parsedSourceBoosts := make([]search.SourceBoost, 0, len(resolvedCfg.Search.SourceBoosts)+len(opts.SourceBoostFlags))
	for _, boost := range resolvedCfg.Search.SourceBoosts {
		if strings.TrimSpace(boost.Prefix) == "" || boost.Weight == 0 {
			continue
		}
		parsedSourceBoosts = append(parsedSourceBoosts, search.SourceBoost{Prefix: boost.Prefix, Weight: boost.Weight})
	}
	for _, raw := range opts.SourceBoostFlags {
		boost, err := parseSourceBoostArg(raw)
		if err != nil {
			return nil, recallDiagnostics{}, err
		}
		parsedSourceBoosts = append(parsedSourceBoosts, boost)
	}

	fetchLimit := opts.Limit
	if opts.MaxItems > 0 && opts.MaxItems*3 > fetchLimit {
		fetchLimit = opts.MaxItems * 3
	}
	if fetchLimit < 8 {
		fetchLimit = 8
	}

	results, err := engine.Search(ctx, opts.Query, search.Options{
		Mode:              opts.SearchMode,
		Limit:             fetchLimit,
		MinScore:          opts.MinScore,
		Project:           opts.Project,
		Classes:           classes,
		Agent:             opts.Agent,
		Channel:           opts.Channel,
		SessionKey:        opts.SessionKey,
		After:             opts.After,
		Before:            opts.Before,
		Source:            opts.Source,
		Scope:             scopeFilters,
		SourceBoosts:      parsedSourceBoosts,
		IncludeSuperseded: true,
		BoostAgent:        opts.BoostAgent,
		BoostChannel:      opts.BoostChannel,
		BoostSessionKey:   opts.BoostSessionKey,
		RerankMode:        opts.RerankMode,
	})
	if err != nil {
		return nil, recallDiagnostics{}, err
	}

	enriched := enrichSearchResultsWithFactIDs(ctx, s, results, true)
	items, diag := classifyRecallResults(ctx, s, enriched)
	if len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	diag.Searched = len(enriched)
	return items, diag, nil
}

func classifyRecallResults(ctx context.Context, s store.Store, results []search.Result) ([]recallItem, recallDiagnostics) {
	if len(results) == 0 {
		return []recallItem{}, recallDiagnostics{DropReasonCount: map[string]int{}}
	}

	memoryIDs := make([]int64, 0, len(results))
	seen := map[int64]struct{}{}
	for _, result := range results {
		if result.MemoryID <= 0 {
			continue
		}
		if _, ok := seen[result.MemoryID]; ok {
			continue
		}
		seen[result.MemoryID] = struct{}{}
		memoryIDs = append(memoryIDs, result.MemoryID)
	}

	activeFacts, _ := s.GetFactsByMemoryIDs(ctx, memoryIDs)
	allFacts, _ := s.GetFactsByMemoryIDsIncludingSuperseded(ctx, memoryIDs)

	activeByMemory := map[int64][]*store.Fact{}
	for _, fact := range activeFacts {
		activeByMemory[fact.MemoryID] = append(activeByMemory[fact.MemoryID], fact)
	}

	allByMemory := map[int64][]*store.Fact{}
	for _, fact := range allFacts {
		allByMemory[fact.MemoryID] = append(allByMemory[fact.MemoryID], fact)
	}

	items := make([]recallItem, 0, len(results))
	diag := recallDiagnostics{DropReasonCount: map[string]int{}}

	for _, result := range results {
		item := classifyRecallResult(result, activeByMemory[result.MemoryID], allByMemory[result.MemoryID])
		if len(item.FactIDs) > 0 {
			diag.FactBacked++
		}
		switch item.RetrievalVisibility {
		case retrievalVisibilityPromptSafe:
			diag.PromptEligible++
		case retrievalVisibilityEvidenceOnly:
			diag.EvidenceOnly++
		case retrievalVisibilityJournalOnly:
			diag.JournalOnly++
		case retrievalVisibilityRetired:
			diag.Retired++
		}
		for _, reason := range item.DropReasons {
			diag.DropReasonCount[reason]++
		}
		items = append(items, item)
	}

	return items, diag
}

func classifyRecallResult(result search.Result, activeFacts []*store.Fact, allFacts []*store.Fact) recallItem {
	activeViews := summarizeFactsForRecall(activeFacts)
	allViews := summarizeFactsForRecall(allFacts)

	item := recallItem{
		MemoryID:      result.MemoryID,
		FactIDs:       extractFactIDs(activeFacts),
		Content:       strings.TrimSpace(result.Content),
		Snippet:       cleanSnippet(result.Snippet),
		SourceFile:    result.SourceFile,
		SourceLine:    result.SourceLine,
		SourceSection: result.SourceSection,
		SourceTier:    result.SourceTier,
		Project:       result.Project,
		MemoryClass:   result.MemoryClass,
		Score:         result.Score,
		MatchType:     result.MatchType,
		Facts:         trimFactViews(activeViews, 3),
	}

	item.PromptText = buildRecallPromptText(item, activeViews)

	dropReasons := []string{}
	activeFactCount := len(activeViews)
	allFactCount := len(allViews)
	maxConfidence := maxFactConfidence(activeViews)
	maxDurableConfidence := maxDurableFactConfidence(activeViews)
	stableClass := isPromptSafeMemoryClass(item.MemoryClass)
	transientSource := item.SourceTier == "capture" || item.SourceTier == "transient"

	switch {
	case maxDurableConfidence >= 0.60:
		item.PromptEligible = true
		item.RetrievalVisibility = retrievalVisibilityPromptSafe
	case maxDurableConfidence >= 0.45 && item.Score >= 0.60:
		item.PromptEligible = true
		item.RetrievalVisibility = retrievalVisibilityPromptSafe
	case stableClass && !transientSource && item.Score >= 0.45:
		item.PromptEligible = true
		item.RetrievalVisibility = retrievalVisibilityPromptSafe
	case activeFactCount > 0 && !transientSource && maxConfidence >= 0.85:
		item.PromptEligible = true
		item.RetrievalVisibility = retrievalVisibilityPromptSafe
	case activeFactCount == 0 && allFactCount > 0:
		item.RetrievalVisibility = retrievalVisibilityRetired
		dropReasons = appendUniqueString(dropReasons, "retired")
	case activeFactCount == 0 && item.SourceTier == "journal":
		item.RetrievalVisibility = retrievalVisibilityJournalOnly
		dropReasons = appendUniqueString(dropReasons, "journal_only")
	default:
		item.RetrievalVisibility = retrievalVisibilityEvidenceOnly
		dropReasons = appendUniqueString(dropReasons, "evidence_only")
		if activeFactCount == 0 {
			dropReasons = appendUniqueString(dropReasons, "no_active_facts")
		}
		if transientSource {
			dropReasons = appendUniqueString(dropReasons, "transient_source")
		}
		if item.MemoryClass == store.MemoryClassScratch {
			dropReasons = appendUniqueString(dropReasons, "scratch_class")
		}
		if activeFactCount > 0 && maxConfidence < 0.60 {
			dropReasons = appendUniqueString(dropReasons, "low_confidence")
		}
	}

	item.DropReasons = dropReasons
	return item
}

func buildContextSelection(items []recallItem, maxItems, maxTokens int, allowEvidenceFallback bool) ([]recallItem, []recallItem, string, int, contextDiagnostics) {
	if maxItems <= 0 {
		maxItems = 6
	}
	if maxTokens <= 0 {
		maxTokens = 450
	}

	diag := contextDiagnostics{DropReasonCount: map[string]int{}}
	eligible := make([]recallItem, 0, len(items))
	evidence := make([]recallItem, 0, len(items))
	dropped := make([]recallItem, 0, len(items))

	for _, item := range items {
		switch item.RetrievalVisibility {
		case retrievalVisibilityPromptSafe:
			eligible = append(eligible, item)
		case retrievalVisibilityEvidenceOnly:
			evidence = append(evidence, item)
		case retrievalVisibilityJournalOnly, retrievalVisibilityRetired:
			dropped = append(dropped, item)
			diag.DroppedByPolicy++
			incrementDropReasons(diag.DropReasonCount, item.DropReasons)
		default:
			dropped = append(dropped, item)
			diag.DroppedByPolicy++
			incrementDropReasons(diag.DropReasonCount, item.DropReasons)
		}
	}

	candidates := eligible
	if len(candidates) == 0 && allowEvidenceFallback && len(evidence) > 0 {
		candidates = evidence
		diag.FallbackUsed = true
	} else {
		for _, item := range evidence {
			dropped = append(dropped, item)
			diag.DroppedByPolicy++
			incrementDropReasons(diag.DropReasonCount, item.DropReasons)
		}
	}

	selected := make([]recallItem, 0, minInt(maxItems, len(candidates)))
	remainingDropped := make([]recallItem, 0, len(candidates))

	for _, item := range candidates {
		estimated := estimateContextItemTokens(item)
		if len(selected) >= maxItems {
			item.DropReasons = appendUniqueString(item.DropReasons, "limit")
			remainingDropped = append(remainingDropped, item)
			diag.DroppedByLimit++
			incrementDropReasons(diag.DropReasonCount, []string{"limit"})
			continue
		}
		if len(selected) == 0 && estimated > maxTokens {
			item.PromptText = truncateForPrompt(item.PromptText, max(120, maxTokens*4/2))
			estimated = estimateContextItemTokens(item)
			if estimated > maxTokens {
				item.DropReasons = appendUniqueString(item.DropReasons, "budget")
				remainingDropped = append(remainingDropped, item)
				diag.DroppedByBudget++
				incrementDropReasons(diag.DropReasonCount, []string{"budget"})
				continue
			}
		}
		nextSelected := append(append([]recallItem(nil), selected...), item)
		usedTokens := estimateContextBlockTokens(nextSelected)
		if usedTokens > maxTokens {
			item.DropReasons = appendUniqueString(item.DropReasons, "budget")
			remainingDropped = append(remainingDropped, item)
			diag.DroppedByBudget++
			incrementDropReasons(diag.DropReasonCount, []string{"budget"})
			continue
		}
		selected = append(selected, item)
	}

	dropped = append(dropped, remainingDropped...)
	sortDroppedRecallItems(dropped)

	block := formatStructuredContext(selected, diag.FallbackUsed)
	tokenCount := estimateTokens(block)
	if len(selected) == 0 {
		tokenCount = 0
	}
	diag.Selected = len(selected)
	return selected, dropped, block, tokenCount, diag
}

func formatStructuredContext(items []recallItem, fallbackUsed bool) string {
	if len(items) == 0 {
		return ""
	}
	lines := []string{"<cortex-memories>"}
	if fallbackUsed {
		lines = append(lines, `mode: evidence_fallback`)
	}
	for i, item := range items {
		lines = append(lines, fmt.Sprintf("%d. source: %s", i+1, escapePromptField(item.SourceFile)))
		if item.SourceSection != "" {
			lines = append(lines, fmt.Sprintf("   section: %s", escapePromptField(item.SourceSection)))
		}
		lines = append(lines, fmt.Sprintf("   visibility: %s", item.RetrievalVisibility))
		lines = append(lines, fmt.Sprintf("   score: %.2f", item.Score))
		lines = append(lines, fmt.Sprintf("   memory_id: %d", item.MemoryID))
		lines = append(lines, fmt.Sprintf("   content: %s", escapePromptField(item.PromptText)))
	}
	lines = append(lines, "</cortex-memories>")
	return strings.Join(lines, "\n")
}

func summarizeFactsForRecall(facts []*store.Fact) []recallFactView {
	views := make([]recallFactView, 0, len(facts))
	for _, fact := range facts {
		if fact == nil {
			continue
		}
		views = append(views, recallFactView{
			FactID:      fact.ID,
			Subject:     strings.TrimSpace(fact.Subject),
			Predicate:   strings.TrimSpace(fact.Predicate),
			Object:      strings.TrimSpace(fact.Object),
			FactType:    strings.TrimSpace(fact.FactType),
			Confidence:  fact.Confidence,
			State:       effectiveFactState(fact),
			PromptValue: strings.TrimSpace(strings.Join([]string{strings.TrimSpace(fact.Subject), strings.TrimSpace(fact.Predicate), strings.TrimSpace(fact.Object)}, " ")),
		})
	}
	sort.SliceStable(views, func(i, j int) bool {
		leftDurable := isDurableFactType(views[i].FactType)
		rightDurable := isDurableFactType(views[j].FactType)
		if leftDurable != rightDurable {
			return leftDurable
		}
		if views[i].Confidence != views[j].Confidence {
			return views[i].Confidence > views[j].Confidence
		}
		return views[i].FactID < views[j].FactID
	})
	return views
}

func buildRecallPromptText(item recallItem, facts []recallFactView) string {
	if len(facts) > 0 {
		values := make([]string, 0, minInt(2, len(facts)))
		for _, fact := range facts {
			if strings.TrimSpace(fact.PromptValue) == "" {
				continue
			}
			values = append(values, normalizePromptWhitespace(fact.PromptValue))
			if len(values) >= 2 {
				break
			}
		}
		if len(values) > 0 {
			return truncateForPrompt(strings.Join(values, " | "), 420)
		}
	}
	if snippet := strings.TrimSpace(item.Snippet); snippet != "" {
		return truncateForPrompt(normalizePromptWhitespace(snippet), 420)
	}
	return truncateForPrompt(normalizePromptWhitespace(item.Content), 420)
}

func extractFactIDs(facts []*store.Fact) []int64 {
	ids := make([]int64, 0, len(facts))
	for _, fact := range facts {
		if fact != nil {
			ids = append(ids, fact.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func effectiveFactState(fact *store.Fact) string {
	if fact == nil {
		return ""
	}
	if strings.TrimSpace(fact.State) != "" {
		return fact.State
	}
	if fact.SupersededBy != nil {
		return store.FactStateSuperseded
	}
	return store.FactStateActive
}

func isDurableFactType(factType string) bool {
	switch strings.ToLower(strings.TrimSpace(factType)) {
	case "preference", "identity", "decision", "config", "location":
		return true
	default:
		return false
	}
}

func isPromptSafeMemoryClass(memoryClass string) bool {
	switch store.NormalizeMemoryClass(memoryClass) {
	case store.MemoryClassRule, store.MemoryClassDecision, store.MemoryClassPreference, store.MemoryClassIdentity:
		return true
	default:
		return false
	}
}

func maxFactConfidence(facts []recallFactView) float64 {
	maxValue := 0.0
	for _, fact := range facts {
		if fact.Confidence > maxValue {
			maxValue = fact.Confidence
		}
	}
	return maxValue
}

func maxDurableFactConfidence(facts []recallFactView) float64 {
	maxValue := 0.0
	for _, fact := range facts {
		if isDurableFactType(fact.FactType) && fact.Confidence > maxValue {
			maxValue = fact.Confidence
		}
	}
	return maxValue
}

func trimFactViews(facts []recallFactView, limit int) []recallFactView {
	if limit <= 0 || len(facts) <= limit {
		return facts
	}
	return append([]recallFactView(nil), facts[:limit]...)
}

func cleanSnippet(snippet string) string {
	snippet = strings.ReplaceAll(snippet, "<b>", "")
	snippet = strings.ReplaceAll(snippet, "</b>", "")
	return strings.TrimSpace(snippet)
}

func estimateContextItemTokens(item recallItem) int {
	lines := 5
	chars := len(item.SourceFile) + len(item.SourceSection) + len(item.PromptText) + lines*6
	return max(1, estimateTokens(strings.Repeat("x", chars)))
}

func estimateContextBlockTokens(items []recallItem) int {
	return estimateTokens(formatStructuredContext(items, false))
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return max(1, (len(text)+3)/4)
}

func truncateTTYRecall(primary, fallback string) string {
	text := strings.TrimSpace(primary)
	if text == "" {
		text = strings.TrimSpace(fallback)
	}
	return truncateForPrompt(text, 120)
}

func truncateForPrompt(text string, maxChars int) string {
	text = normalizePromptWhitespace(text)
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return text[:maxChars]
	}
	return text[:maxChars-3] + "..."
}

func normalizePromptWhitespace(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func escapePromptField(text string) string {
	return strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(normalizePromptWhitespace(text))
}

func appendUniqueString(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return values
	}
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func incrementDropReasons(target map[string]int, reasons []string) {
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			continue
		}
		target[reason]++
	}
}

func sortDroppedRecallItems(items []recallItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].MemoryID < items[j].MemoryID
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
