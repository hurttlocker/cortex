package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/extract"
	"github.com/hurttlocker/cortex/internal/ingest"
	cortexmcp "github.com/hurttlocker/cortex/internal/mcp"
	"github.com/hurttlocker/cortex/internal/observe"
	"github.com/hurttlocker/cortex/internal/reason"
	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
	"github.com/mark3labs/mcp-go/server"
)

// version is set by goreleaser via ldflags at build time.
var version = "0.1.8"

var (
	globalDBPath   string
	globalVerbose  bool
	globalReadOnly bool
)

func main() {
	// Parse global flags and filter them out of args
	args := parseGlobalFlags(os.Args[1:])

	if len(args) < 1 {
		printUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "import":
		if err := runImport(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "extract":
		if err := runExtract(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "embed":
		if err := runEmbed(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "index":
		if err := runIndex(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "search":
		if err := runSearch(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "stats":
		if err := runStats(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "list":
		if err := runList(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "export":
		if err := runExport(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "stale":
		if err := runStale(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "conflicts":
		if err := runConflicts(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "reinforce":
		if err := runReinforce(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "reimport":
		if err := runReimport(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "cleanup":
		if err := runCleanup(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "projects":
		if err := runProjects(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "tag":
		if err := runTag(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "reason":
		if err := runReason(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "bench":
		if err := runBench(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "mcp":
		if err := runMCP(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("cortex %s\n", version)
	case "--version", "-v":
		fmt.Printf("cortex %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// parseGlobalFlags extracts global flags like --db and --verbose from args
// Returns filtered args with global flags removed
func parseGlobalFlags(args []string) []string {
	var filtered []string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--db" && i+1 < len(args):
			globalDBPath = args[i+1]
			i++ // skip the value
		case strings.HasPrefix(args[i], "--db="):
			globalDBPath = strings.TrimPrefix(args[i], "--db=")
		case args[i] == "--verbose" || args[i] == "-v":
			globalVerbose = true
		case args[i] == "--read-only" || args[i] == "--readonly":
			globalReadOnly = true
		case strings.HasPrefix(args[i], "-"):
			// Skip unknown flags but keep them for subcommand processing
			filtered = append(filtered, args[i])
		default:
			filtered = append(filtered, args[i])
		}
	}

	return filtered
}

// getDBPath returns the database path using the resolution order:
// --db flag > CORTEX_DB env var > default path
func getDBPath() string {
	if globalDBPath != "" {
		return globalDBPath
	}
	if envPath := os.Getenv("CORTEX_DB"); envPath != "" {
		return envPath
	}
	return "" // Let store.NewStore use its default
}

// getStoreConfig returns a StoreConfig with the global DB path and read-only flag.
func getStoreConfig() store.StoreConfig {
	return store.StoreConfig{DBPath: getDBPath(), ReadOnly: globalReadOnly}
}

// getHNSWPath returns the path for the persisted HNSW index file.
// Stored alongside the database in ~/.cortex/hnsw.idx
func getHNSWPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cortex", "hnsw.idx")
}

func runImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex import <path> [--recursive] [--dry-run] [--extract] [--project <name>] [--auto-tag] [--metadata <json>] [--llm <provider/model>] [--embed <provider/model>]")
	}

	// Parse flags
	var paths []string
	opts := ingest.ImportOptions{}
	enableExtraction := false
	llmFlag := ""
	embedFlag := ""
	projectFlag := ""
	metadataFlag := ""
	autoTag := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--recursive" || args[i] == "-r":
			opts.Recursive = true
		case args[i] == "--dry-run" || args[i] == "-n":
			opts.DryRun = true
		case args[i] == "--extract":
			enableExtraction = true
		case args[i] == "--project" && i+1 < len(args):
			i++
			projectFlag = args[i]
		case strings.HasPrefix(args[i], "--project="):
			projectFlag = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--metadata" && i+1 < len(args):
			i++
			metadataFlag = args[i]
		case strings.HasPrefix(args[i], "--metadata="):
			metadataFlag = strings.TrimPrefix(args[i], "--metadata=")
		case args[i] == "--auto-tag":
			autoTag = true
		case args[i] == "--llm" && i+1 < len(args):
			i++
			llmFlag = args[i]
		case strings.HasPrefix(args[i], "--llm="):
			llmFlag = strings.TrimPrefix(args[i], "--llm=")
		case args[i] == "--embed" && i+1 < len(args):
			i++
			embedFlag = args[i]
		case strings.HasPrefix(args[i], "--embed="):
			embedFlag = strings.TrimPrefix(args[i], "--embed=")
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			paths = append(paths, args[i])
		}
	}

	// Set project on import options
	opts.Project = projectFlag
	opts.AutoTag = autoTag

	// Parse metadata JSON if provided
	if metadataFlag != "" {
		meta, err := store.ParseMetadataJSON(metadataFlag)
		if err != nil {
			return fmt.Errorf("invalid --metadata: %w", err)
		}
		opts.Metadata = meta
	}

	if len(paths) == 0 {
		return fmt.Errorf("no path specified")
	}

	// Open store
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	engine := ingest.NewEngine(s)
	ctx := context.Background()

	if opts.DryRun {
		fmt.Println("Dry run mode — no changes will be written")
		fmt.Println()
	}

	totalResult := &ingest.ImportResult{}

	for _, path := range paths {
		fmt.Printf("Importing %s...\n", path)

		opts.ProgressFn = func(current, total int, file string) {
			fmt.Printf("  [%d/%d] %s\n", current, total, file)
		}

		result, err := engine.ImportFile(ctx, path, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			continue
		}

		totalResult.Add(result)
	}

	// Run extraction if requested
	if enableExtraction && !opts.DryRun && totalResult.MemoriesNew > 0 {
		fmt.Println("\nRunning extraction...")
		extractionStats, err := runExtractionOnImportedMemories(ctx, s, llmFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Extraction error: %v\n", err)
		} else {
			if extractionStats.LLMFactsExtracted > 0 {
				fmt.Printf("  Facts extracted: %d (%d rules, %d LLM)\n",
					extractionStats.FactsExtracted,
					extractionStats.RulesFactsExtracted,
					extractionStats.LLMFactsExtracted)
			} else {
				fmt.Printf("  Facts extracted: %d (rules only)\n", extractionStats.FactsExtracted)
			}
		}
	}

	// Run embedding if requested
	if embedFlag != "" && !opts.DryRun && totalResult.MemoriesNew > 0 {
		fmt.Println("\nGenerating embeddings...")
		embedStats, err := runEmbeddingOnImportedMemories(ctx, s, embedFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Embedding error: %v\n", err)
		} else {
			fmt.Printf("  Embeddings generated: %d\n", embedStats.EmbeddingsAdded)
		}
	}

	fmt.Println()
	fmt.Print(ingest.FormatImportResult(totalResult))
	return nil
}

func runSearch(args []string) error {
	// Parse flags and query
	var queryParts []string
	mode := "keyword"
	limit := 10
	minScore := -1.0 // -1 = use mode-dependent defaults (BM25: 0.05, semantic: 0.25, hybrid: 0.05)
	jsonOutput := false
	embedFlag := ""
	projectFlag := ""
	agentFlag := ""
	channelFlag := ""
	afterFlag := ""
	beforeFlag := ""
	showMetadata := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--project" && i+1 < len(args):
			i++
			projectFlag = args[i]
		case strings.HasPrefix(args[i], "--project="):
			projectFlag = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--agent" && i+1 < len(args):
			i++
			agentFlag = args[i]
		case strings.HasPrefix(args[i], "--agent="):
			agentFlag = strings.TrimPrefix(args[i], "--agent=")
		case args[i] == "--channel" && i+1 < len(args):
			i++
			channelFlag = args[i]
		case strings.HasPrefix(args[i], "--channel="):
			channelFlag = strings.TrimPrefix(args[i], "--channel=")
		case args[i] == "--after" && i+1 < len(args):
			i++
			afterFlag = args[i]
		case strings.HasPrefix(args[i], "--after="):
			afterFlag = strings.TrimPrefix(args[i], "--after=")
		case args[i] == "--before" && i+1 < len(args):
			i++
			beforeFlag = args[i]
		case strings.HasPrefix(args[i], "--before="):
			beforeFlag = strings.TrimPrefix(args[i], "--before=")
		case args[i] == "--show-metadata":
			showMetadata = true
		case args[i] == "--mode" && i+1 < len(args):
			i++
			mode = args[i]
		case strings.HasPrefix(args[i], "--mode="):
			mode = strings.TrimPrefix(args[i], "--mode=")
		case args[i] == "--limit" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid --limit value: %s", args[i])
			}
			limit = n
		case strings.HasPrefix(args[i], "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(args[i], "--limit="))
			if err != nil {
				return fmt.Errorf("invalid --limit value: %s", args[i])
			}
			limit = n
		case (args[i] == "--min-score" || args[i] == "--min-confidence") && i+1 < len(args):
			i++
			f, err := strconv.ParseFloat(args[i], 64)
			if err != nil {
				return fmt.Errorf("invalid --min-score value: %s", args[i])
			}
			minScore = f
		case strings.HasPrefix(args[i], "--min-score=") || strings.HasPrefix(args[i], "--min-confidence="):
			val := args[i]
			if strings.HasPrefix(val, "--min-score=") {
				val = strings.TrimPrefix(val, "--min-score=")
			} else {
				val = strings.TrimPrefix(val, "--min-confidence=")
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid --min-score value: %s", val)
			}
			minScore = f
		case args[i] == "--json":
			jsonOutput = true
		case args[i] == "--embed" && i+1 < len(args):
			i++
			embedFlag = args[i]
		case strings.HasPrefix(args[i], "--embed="):
			embedFlag = strings.TrimPrefix(args[i], "--embed=")
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		return fmt.Errorf("usage: cortex search <query> [--mode keyword|semantic|hybrid] [--limit N] [--embed <provider/model>] [--json] [--agent <id>] [--channel <name>] [--after YYYY-MM-DD] [--before YYYY-MM-DD] [--show-metadata]")
	}

	searchMode, err := search.ParseMode(mode)
	if err != nil {
		return err
	}

	// Open store
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	// Create search engine with optional embedder
	var engine *search.Engine
	if embedFlag != "" {
		// Configure embedder
		embedConfig, err := embed.ResolveEmbedConfig(embedFlag)
		if err != nil {
			return fmt.Errorf("configuring embedder: %w", err)
		}
		if embedConfig == nil {
			return fmt.Errorf("no embedding configuration found")
		}
		if err := embedConfig.Validate(); err != nil {
			return fmt.Errorf("invalid embedding configuration: %w", err)
		}

		embedder, err := embed.NewClient(embedConfig)
		if err != nil {
			return fmt.Errorf("creating embedder: %w", err)
		}

		engine = search.NewEngineWithEmbedder(s, embedder)

		// Load or build HNSW index for fast semantic search
		hnswPath := getHNSWPath()
		if count, err := engine.LoadOrBuildHNSW(context.Background(), hnswPath, 3600); err == nil && count > 0 {
			if globalVerbose {
				fmt.Fprintf(os.Stderr, "  HNSW index: %d vectors loaded\n", count)
			}
		}
	} else {
		engine = search.NewEngine(s)
	}

	ctx := context.Background()

	opts := search.Options{
		Mode:     searchMode,
		Limit:    limit,
		MinScore: minScore,
		Project:  projectFlag,
		Agent:    agentFlag,
		Channel:  channelFlag,
		After:    afterFlag,
		Before:   beforeFlag,
	}

	results, err := engine.Search(ctx, query, opts)
	if err != nil {
		return err
	}

	// Determine output format
	if jsonOutput || !isTTY() {
		return outputJSON(results)
	}

	return outputTTYSearch(query, results, showMetadata)
}

func runStats(args []string) error {
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		}
	}

	// Open store
	cfg := getStoreConfig()
	s, err := store.NewStore(cfg)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Use observe engine for enhanced stats
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = store.DefaultDBPath
	}
	engine := observe.NewEngine(s, dbPath)
	observeStats, err := engine.GetStats(ctx)
	if err != nil {
		return fmt.Errorf("getting observability stats: %w", err)
	}

	// Get additional info: date range
	// TODO: Consider merging this with observe engine stats to avoid duplicate queries
	_, dateRange, err := getExtendedStats(ctx, s)
	if err != nil {
		return fmt.Errorf("getting extended stats: %w", err)
	}

	if jsonOutput || !isTTY() {
		return outputEnhancedStatsJSON(observeStats, dateRange)
	}

	return outputEnhancedStatsTTY(observeStats, dateRange)
}

func runStale(args []string) error {
	opts := observe.StaleOpts{
		MaxConfidence: 0.5,
		MaxDays:       30,
		Limit:         50,
	}
	jsonOutput := false

	// Parse flags
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--days" && i+1 < len(args):
			i++
			days, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid --days value: %s", args[i])
			}
			opts.MaxDays = days
		case strings.HasPrefix(args[i], "--days="):
			days, err := strconv.Atoi(strings.TrimPrefix(args[i], "--days="))
			if err != nil {
				return fmt.Errorf("invalid --days value: %s", args[i])
			}
			opts.MaxDays = days
		case args[i] == "--min-confidence" && i+1 < len(args):
			i++
			conf, err := strconv.ParseFloat(args[i], 64)
			if err != nil {
				return fmt.Errorf("invalid --min-confidence value: %s", args[i])
			}
			opts.MaxConfidence = conf
		case strings.HasPrefix(args[i], "--min-confidence="):
			conf, err := strconv.ParseFloat(strings.TrimPrefix(args[i], "--min-confidence="), 64)
			if err != nil {
				return fmt.Errorf("invalid --min-confidence value: %s", args[i])
			}
			opts.MaxConfidence = conf
		case args[i] == "--json":
			jsonOutput = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			// Accept positional number as --days shorthand (e.g. "cortex stale 30")
			days, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("unexpected argument: %s", args[i])
			}
			opts.MaxDays = days
		}
	}

	// Open store
	cfg := getStoreConfig()
	s, err := store.NewStore(cfg)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = store.DefaultDBPath
	}
	engine := observe.NewEngine(s, dbPath)

	staleFacts, err := engine.GetStaleFacts(ctx, opts)
	if err != nil {
		return fmt.Errorf("getting stale facts: %w", err)
	}

	if jsonOutput || !isTTY() {
		return outputStaleJSON(staleFacts)
	}

	// Get total fact count for the "no stale facts" message context.
	storeStats, _ := s.Stats(ctx)
	totalFacts := 0
	if storeStats != nil {
		totalFacts = int(storeStats.FactCount)
	}

	return outputStaleTTY(staleFacts, opts, totalFacts)
}

func runConflicts(args []string) error {
	jsonOutput := false
	resolveStrategy := ""
	dryRun := false
	limitFlag := 100
	keepFlag := int64(0)
	dropFlag := int64(0)

	// Parse flags
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			jsonOutput = true
		case args[i] == "--dry-run" || args[i] == "-n":
			dryRun = true
		case args[i] == "--resolve" && i+1 < len(args):
			i++
			resolveStrategy = args[i]
		case strings.HasPrefix(args[i], "--resolve="):
			resolveStrategy = strings.TrimPrefix(args[i], "--resolve=")
		case args[i] == "--limit" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid --limit: %s", args[i])
			}
			limitFlag = n
		case strings.HasPrefix(args[i], "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(args[i], "--limit="))
			if err != nil {
				return fmt.Errorf("invalid --limit: %s", args[i])
			}
			limitFlag = n
		case args[i] == "--keep" && i+1 < len(args):
			i++
			n, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid --keep: %s", args[i])
			}
			keepFlag = n
		case args[i] == "--drop" && i+1 < len(args):
			i++
			n, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid --drop: %s", args[i])
			}
			dropFlag = n
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s", args[i])
			}
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	// Open store
	cfg := getStoreConfig()
	s, err := store.NewStore(cfg)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = store.DefaultDBPath
	}
	engine := observe.NewEngine(s, dbPath)
	resolver := observe.NewResolver(s, engine)

	// Manual resolution: --keep X --drop Y
	if keepFlag > 0 && dropFlag > 0 {
		res, err := resolver.ResolveByID(ctx, keepFlag, dropFlag)
		if err != nil {
			return fmt.Errorf("manual resolve: %w", err)
		}
		if jsonOutput || !isTTY() {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(res)
		}
		fmt.Printf("✅ Resolved: kept fact %d, suppressed fact %d\n", res.WinnerID, res.LoserID)
		fmt.Printf("   %s\n", res.Reason)
		return nil
	}

	// Auto-resolution with strategy
	if resolveStrategy != "" {
		strategy, err := observe.ParseStrategy(resolveStrategy)
		if err != nil {
			return err
		}

		batch, err := resolver.DetectAndResolve(ctx, strategy, dryRun)
		if err != nil {
			return fmt.Errorf("resolving conflicts: %w", err)
		}

		if jsonOutput || !isTTY() {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(batch)
		}

		prefix := ""
		if dryRun {
			prefix = "[DRY RUN] "
		}
		fmt.Printf("%sConflict Resolution: %s\n", prefix, strategy)
		fmt.Printf("  Total:    %d\n", batch.Total)
		fmt.Printf("  Resolved: %d\n", batch.Resolved)
		fmt.Printf("  Skipped:  %d\n", batch.Skipped)
		fmt.Printf("  Errors:   %d\n", batch.Errors)
		fmt.Println()

		for _, r := range batch.Results {
			status := "✅"
			if r.Winner == "manual" {
				status = "🔍"
			} else if !r.Applied && !dryRun {
				status = "❌"
			}
			fmt.Printf("  %s %s.%s: %s\n", status, r.Conflict.Fact1.Subject, r.Conflict.Fact1.Predicate, r.Reason)
		}
		return nil
	}

	// Detection only (default)
	conflicts, err := engine.GetConflictsLimit(ctx, limitFlag)
	if err != nil {
		return fmt.Errorf("getting conflicts: %w", err)
	}

	if jsonOutput || !isTTY() {
		return outputConflictsJSON(conflicts)
	}

	return outputConflictsTTY(conflicts)
}

func runExtract(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex extract <file> [--json] [--llm <provider/model>]")
	}

	// Parse flags
	var filepath string
	jsonOutput := false
	llmFlag := ""

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			jsonOutput = true
		case args[i] == "--llm" && i+1 < len(args):
			i++
			llmFlag = args[i]
		case strings.HasPrefix(args[i], "--llm="):
			llmFlag = strings.TrimPrefix(args[i], "--llm=")
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			if filepath != "" {
				return fmt.Errorf("only one file path allowed")
			}
			filepath = args[i]
		}
	}

	if filepath == "" {
		return fmt.Errorf("no file path specified")
	}

	// Read file
	content, err := os.ReadFile(filepath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	// Configure LLM if requested
	var llmConfig *extract.LLMConfig
	if llmFlag != "" {
		var err error
		llmConfig, err = extract.ResolveLLMConfig(llmFlag)
		if err != nil {
			return fmt.Errorf("configuring LLM: %w", err)
		}
		if llmConfig != nil {
			if err := llmConfig.Validate(); err != nil {
				return fmt.Errorf("invalid LLM configuration: %w", err)
			}
		}
	}

	// Run extraction
	pipeline := extract.NewPipeline(llmConfig)
	ctx := context.Background()

	metadata := map[string]string{
		"source_file": filepath,
	}
	// Detect format from extension
	if strings.HasSuffix(strings.ToLower(filepath), ".md") {
		metadata["format"] = "markdown"
	}

	facts, err := pipeline.Extract(ctx, string(content), metadata)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Output results
	if jsonOutput || !isTTY() {
		return outputExtractJSON(facts)
	}

	return outputExtractTTY(filepath, facts)
}

func runList(args []string) error {
	// Parse flags
	var limit int = 20
	var sourceFile, factType string
	var listFacts, jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--facts":
			listFacts = true
		case args[i] == "--limit" && i+1 < len(args):
			i++
			var err error
			if limit, err = strconv.Atoi(args[i]); err != nil {
				return fmt.Errorf("invalid --limit value: %s", args[i])
			}
		case strings.HasPrefix(args[i], "--limit="):
			var err error
			if limit, err = strconv.Atoi(strings.TrimPrefix(args[i], "--limit=")); err != nil {
				return fmt.Errorf("invalid --limit value: %s", args[i])
			}
		case args[i] == "--source" && i+1 < len(args):
			i++
			sourceFile = args[i]
		case strings.HasPrefix(args[i], "--source="):
			sourceFile = strings.TrimPrefix(args[i], "--source=")
		case args[i] == "--type" && i+1 < len(args):
			i++
			factType = args[i]
		case strings.HasPrefix(args[i], "--type="):
			factType = strings.TrimPrefix(args[i], "--type=")
		case args[i] == "--json":
			jsonOutput = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	// Open store
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	opts := store.ListOpts{
		Limit:      limit,
		Offset:     0,
		SourceFile: sourceFile,
		FactType:   factType,
	}

	if listFacts {
		facts, err := s.ListFacts(ctx, opts)
		if err != nil {
			return fmt.Errorf("listing facts: %w", err)
		}

		if jsonOutput || !isTTY() {
			return outputListFactsJSON(facts)
		}
		return outputListFactsTTY(facts, opts)
	} else {
		memories, err := s.ListMemories(ctx, opts)
		if err != nil {
			return fmt.Errorf("listing memories: %w", err)
		}

		if jsonOutput || !isTTY() {
			return outputListMemoriesJSON(memories)
		}
		return outputListMemoriesTTY(memories, opts)
	}
}

func runExport(args []string) error {
	// Parse flags
	var format string = "json"
	var outputFile string
	var exportFacts bool

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--facts":
			exportFacts = true
		case args[i] == "--format" && i+1 < len(args):
			i++
			format = args[i]
		case strings.HasPrefix(args[i], "--format="):
			format = strings.TrimPrefix(args[i], "--format=")
		case args[i] == "--output" && i+1 < len(args):
			i++
			outputFile = args[i]
		case strings.HasPrefix(args[i], "--output="):
			outputFile = strings.TrimPrefix(args[i], "--output=")
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	// Validate format
	if format != "json" && format != "markdown" && format != "csv" {
		return fmt.Errorf("unsupported format: %s (supported: json, markdown, csv)", format)
	}

	// Open store
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Set output destination
	output := os.Stdout
	if outputFile != "" {
		file, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}
		defer file.Close()
		output = file
	}

	if exportFacts {
		facts, err := s.ListFacts(ctx, store.ListOpts{Limit: math.MaxInt32}) // TODO: Add pagination for v0.2
		if err != nil {
			return fmt.Errorf("listing facts: %w", err)
		}
		return exportFactsInFormat(facts, format, output)
	} else {
		memories, err := s.ListMemories(ctx, store.ListOpts{Limit: math.MaxInt32}) // TODO: Add pagination for v0.2
		if err != nil {
			return fmt.Errorf("listing memories: %w", err)
		}
		return exportMemoriesInFormat(memories, format, output)
	}
}

// ExtractionStats holds statistics about extraction run.
type ExtractionStats struct {
	FactsExtracted      int
	RulesFactsExtracted int
	LLMFactsExtracted   int
}

// runExtractionOnImportedMemories runs extraction on recently imported memories.
func runExtractionOnImportedMemories(ctx context.Context, s store.Store, llmFlag string) (*ExtractionStats, error) {
	// Get recently imported memories (limit to reasonable batch size)
	memories, err := s.ListMemories(ctx, store.ListOpts{Limit: 1000, SortBy: "date"})
	if err != nil {
		return nil, fmt.Errorf("listing memories: %w", err)
	}

	// Configure LLM if requested
	var llmConfig *extract.LLMConfig
	if llmFlag != "" {
		llmConfig, err = extract.ResolveLLMConfig(llmFlag)
		if err != nil {
			return nil, fmt.Errorf("configuring LLM: %w", err)
		}
		if llmConfig != nil {
			if err := llmConfig.Validate(); err != nil {
				return nil, fmt.Errorf("invalid LLM configuration: %w", err)
			}
		}
	}

	pipeline := extract.NewPipeline(llmConfig)
	stats := &ExtractionStats{}

	for _, memory := range memories {
		// Build metadata
		metadata := map[string]string{
			"source_file": memory.SourceFile,
		}
		if strings.HasSuffix(strings.ToLower(memory.SourceFile), ".md") {
			metadata["format"] = "markdown"
		}
		if memory.SourceSection != "" {
			metadata["source_section"] = memory.SourceSection
		}

		// Extract facts
		facts, err := pipeline.Extract(ctx, memory.Content, metadata)
		if err != nil {
			continue // Skip errors, continue with next memory
		}

		// Store facts and track extraction method
		for _, extractedFact := range facts {
			fact := &store.Fact{
				MemoryID:    memory.ID,
				Subject:     extractedFact.Subject,
				Predicate:   extractedFact.Predicate,
				Object:      extractedFact.Object,
				FactType:    extractedFact.FactType,
				Confidence:  extractedFact.Confidence,
				DecayRate:   extractedFact.DecayRate,
				SourceQuote: extractedFact.SourceQuote,
			}

			_, err := s.AddFact(ctx, fact)
			if err != nil {
				continue // Skip storage errors
			}

			stats.FactsExtracted++
			if extractedFact.ExtractionMethod == "llm" {
				stats.LLMFactsExtracted++
			} else {
				stats.RulesFactsExtracted++
			}
		}
	}

	return stats, nil
}

// EmbeddingStats holds statistics about embedding run.
type EmbeddingStats struct {
	EmbeddingsAdded int
}

// runEmbeddingOnImportedMemories runs embedding on recently imported memories.
func runEmbeddingOnImportedMemories(ctx context.Context, s store.Store, embedFlag string) (*EmbeddingStats, error) {
	// Configure embedder
	embedConfig, err := embed.ResolveEmbedConfig(embedFlag)
	if err != nil {
		return nil, fmt.Errorf("configuring embedder: %w", err)
	}
	if embedConfig == nil {
		return nil, fmt.Errorf("no embedding configuration found")
	}
	if err := embedConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid embedding configuration: %w", err)
	}

	embedder, err := embed.NewClient(embedConfig)
	if err != nil {
		return nil, fmt.Errorf("creating embedder: %w", err)
	}

	// Create embedding engine
	embedEngine := ingest.NewEmbedEngine(s, embedder)

	// Embed only recently imported memories (filter for ones without embeddings)
	opts := ingest.DefaultEmbedOptions()
	opts.ProgressFn = func(current, total int) {
		fmt.Printf("  [%d/%d] Embedding memories...\n", current, total)
	}

	result, err := embedEngine.EmbedMemories(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("embedding memories: %w", err)
	}

	return &EmbeddingStats{
		EmbeddingsAdded: result.EmbeddingsAdded,
	}, nil
}

func runReinforce(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex reinforce <fact_id> [fact_id...]")
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	reinforced := 0

	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Skipping invalid ID %q: %v\n", arg, err)
			continue
		}

		if err := s.ReinforceFact(ctx, id); err != nil {
			fmt.Fprintf(os.Stderr, "  Error reinforcing fact %d: %v\n", id, err)
			continue
		}
		reinforced++
	}

	fmt.Printf("Reinforced %d fact(s)\n", reinforced)
	return nil
}

func runReimport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex reimport <path> [--recursive] [--extract] [--embed <provider/model>] [--force]")
	}

	// Parse flags
	var paths []string
	recursive := false
	enableExtraction := false
	embedFlag := ""
	llmFlag := ""
	force := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--recursive" || args[i] == "-r":
			recursive = true
		case args[i] == "--extract":
			enableExtraction = true
		case args[i] == "--embed" && i+1 < len(args):
			i++
			embedFlag = args[i]
		case strings.HasPrefix(args[i], "--embed="):
			embedFlag = strings.TrimPrefix(args[i], "--embed=")
		case args[i] == "--llm" && i+1 < len(args):
			i++
			llmFlag = args[i]
		case strings.HasPrefix(args[i], "--llm="):
			llmFlag = strings.TrimPrefix(args[i], "--llm=")
		case args[i] == "--force" || args[i] == "-f":
			force = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			paths = append(paths, args[i])
		}
	}

	if len(paths) == 0 {
		return fmt.Errorf("no path specified")
	}

	// Confirmation prompt (unless --force)
	if !force {
		fmt.Println("⚠️  This will WIPE the existing database and reimport from scratch.")
		fmt.Printf("   Database: %s\n", getDBPath())
		fmt.Printf("   Source:   %s\n", strings.Join(paths, ", "))
		fmt.Print("\nContinue? [y/N] ")

		var answer string
		fmt.Scanln(&answer)
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Step 1: Wipe the database
	dbPath := getDBPath()
	if dbPath == "" {
		dbPath = store.DefaultDBPath
	}
	// Expand ~ if needed
	if strings.HasPrefix(dbPath, "~/") {
		home, _ := os.UserHomeDir()
		dbPath = home + dbPath[1:]
	}

	fmt.Println("Step 1/3: Wiping database...")
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing database: %w", err)
	}
	// Also remove WAL and SHM files
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	fmt.Println("  ✓ Database wiped")

	// Step 2: Import
	fmt.Println("Step 2/3: Importing...")
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	engine := ingest.NewEngine(s)
	ctx := context.Background()

	opts := ingest.ImportOptions{
		Recursive: recursive,
		ProgressFn: func(current, total int, file string) {
			fmt.Printf("  [%d/%d] %s\n", current, total, file)
		},
	}

	totalResult := &ingest.ImportResult{}
	for _, path := range paths {
		result, err := engine.ImportFile(ctx, path, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Error importing %s: %v\n", path, err)
			continue
		}
		totalResult.Add(result)
	}
	fmt.Printf("  ✓ Imported %d files (%d new memories, %d unchanged)\n",
		totalResult.FilesImported, totalResult.MemoriesNew, totalResult.MemoriesUnchanged)

	// Run extraction if requested
	if enableExtraction && totalResult.MemoriesNew > 0 {
		fmt.Println("  Extracting facts...")
		extractionStats, err := runExtractionOnImportedMemories(ctx, s, llmFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Extraction error: %v\n", err)
		} else {
			fmt.Printf("  ✓ Extracted %d facts\n", extractionStats.FactsExtracted)
		}
	}

	// Step 3: Embed (if requested)
	if embedFlag != "" && totalResult.MemoriesNew > 0 {
		fmt.Println("Step 3/3: Generating embeddings...")
		embedStats, err := runEmbeddingOnImportedMemories(ctx, s, embedFlag)
		if err != nil {
			return fmt.Errorf("embedding error: %w", err)
		}
		fmt.Printf("  ✓ Generated %d embeddings\n", embedStats.EmbeddingsAdded)
	} else if embedFlag == "" {
		fmt.Println("Step 3/3: Skipped (no --embed flag)")
	} else {
		fmt.Println("Step 3/3: Skipped (no new memories)")
	}

	fmt.Println()
	fmt.Print(ingest.FormatImportResult(totalResult))
	fmt.Println("\n✅ Reimport complete!")
	return nil
}

func runCleanup(args []string) error {
	// Parse flags (none currently, reserved for future use)
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("unknown flag: %s", arg)
		}
		return fmt.Errorf("unexpected argument: %s", arg)
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()

	// SQLiteStore.ExecContext provides direct SQL access.
	ss, ok := s.(*store.SQLiteStore)
	if !ok {
		return fmt.Errorf("cleanup requires SQLiteStore backend")
	}

	// 1. Delete short memories (likely garbage chunks).
	res, err := ss.ExecContext(ctx, `DELETE FROM memories WHERE LENGTH(content) < 20`)
	if err != nil {
		return fmt.Errorf("deleting short memories: %w", err)
	}
	shortDeleted, _ := res.RowsAffected()

	// 2. Delete purely numeric memories.
	res, err = ss.ExecContext(ctx, `DELETE FROM memories WHERE content GLOB '[0-9]*' AND content NOT GLOB '*[^0-9]*'`)
	if err != nil {
		return fmt.Errorf("deleting numeric memories: %w", err)
	}
	numericDeleted, _ := res.RowsAffected()

	// 3. Delete headless facts (subject is null or empty).
	res, err = ss.ExecContext(ctx, `DELETE FROM facts WHERE subject IS NULL OR subject = ''`)
	if err != nil {
		return fmt.Errorf("deleting headless facts: %w", err)
	}
	factsDeleted, _ := res.RowsAffected()

	fmt.Printf("Cleanup complete:\n")
	fmt.Printf("  Short memories deleted:   %d\n", shortDeleted)
	fmt.Printf("  Numeric memories deleted: %d\n", numericDeleted)
	fmt.Printf("  Headless facts deleted:   %d\n", factsDeleted)

	return nil
}

func runMCP(args []string) error {
	var port int
	var embedModel string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--port" && i+1 < len(args):
			p, err := strconv.Atoi(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid port: %s", args[i+1])
			}
			port = p
			i++
		case strings.HasPrefix(args[i], "--port="):
			p, err := strconv.Atoi(strings.TrimPrefix(args[i], "--port="))
			if err != nil {
				return fmt.Errorf("invalid port: %s", strings.TrimPrefix(args[i], "--port="))
			}
			port = p
		case args[i] == "--embed" && i+1 < len(args):
			embedModel = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--embed="):
			embedModel = strings.TrimPrefix(args[i], "--embed=")
		case args[i] == "--help" || args[i] == "-h":
			fmt.Println(`cortex mcp — Start Model Context Protocol server

Usage:
  cortex mcp                         Start MCP server (stdio transport)
  cortex mcp --port 8080             Start MCP server (HTTP+SSE transport)

Flags:
  --port <N>                         HTTP+SSE port (default: stdio)
  --embed <provider/model>           Enable semantic/hybrid search
  -h, --help                         Show this help

Tools exposed:
  cortex_search    Hybrid search across memories
  cortex_import    Add new memories from text
  cortex_stats     Get memory statistics
  cortex_facts     Query extracted facts
  cortex_stale     Get stale facts

Resources:
  cortex://stats   Memory statistics
  cortex://recent  Recently imported memories`)
			return nil
		default:
			return fmt.Errorf("unknown argument: %s", args[i])
		}
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	mcpCfg := cortexmcp.ServerConfig{
		Store:   s,
		DBPath:  getDBPath(),
		Version: version,
	}

	// Wire up embedder if requested
	if embedModel != "" {
		embedCfg, err := embed.NewEmbedConfig(embedModel)
		if err != nil {
			return fmt.Errorf("invalid embed model: %w", err)
		}
		embedder, err := embed.NewClient(embedCfg)
		if err != nil {
			return fmt.Errorf("creating embedder: %w", err)
		}
		mcpCfg.Embedder = embedder
	}

	mcpServer := cortexmcp.NewServer(mcpCfg)

	if port > 0 {
		// HTTP+SSE transport
		sseServer := server.NewSSEServer(mcpServer)
		addr := fmt.Sprintf(":%d", port)
		fmt.Fprintf(os.Stderr, "Cortex MCP server listening on http://localhost%s/sse\n", addr)
		return sseServer.Start(addr)
	}

	// Default: stdio transport
	return server.ServeStdio(mcpServer)
}

func runIndex(args []string) error {
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	engine := search.NewEngine(s)

	hnswPath := getHNSWPath()
	fmt.Println("Building HNSW index from stored embeddings...")

	start := time.Now()
	count, err := engine.BuildHNSW(ctx)
	if err != nil {
		return fmt.Errorf("building HNSW index: %w", err)
	}

	if count == 0 {
		fmt.Println("No embeddings found. Run 'cortex embed <provider/model>' first.")
		return nil
	}

	buildTime := time.Since(start)

	// Save to disk
	if err := engine.SaveHNSW(hnswPath); err != nil {
		return fmt.Errorf("saving HNSW index: %w", err)
	}

	info, _ := os.Stat(hnswPath)
	sizeMB := float64(info.Size()) / (1024 * 1024)

	fmt.Printf("HNSW index built:\n")
	fmt.Printf("  Vectors: %d\n", count)
	fmt.Printf("  Build time: %s\n", buildTime.Round(time.Millisecond))
	fmt.Printf("  File: %s (%.1f MB)\n", hnswPath, sizeMB)
	fmt.Printf("  Search: O(log N) vs O(N) brute-force\n")
	return nil
}

func runEmbed(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex embed <provider/model> [--batch-size N] [--force]")
	}

	embedFlag := args[0]
	batchSize := 10 // Default: 10 for local models (Ollama), increase for API providers
	forceReembed := false
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--batch-size" && i+1 < len(args):
			i++
			fmt.Sscanf(args[i], "%d", &batchSize)
		case strings.HasPrefix(args[i], "--batch-size="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--batch-size="), "%d", &batchSize)
		case args[i] == "--force" || args[i] == "-f":
			forceReembed = true
		}
	}

	// Open store
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()

	// If --force, delete all existing embeddings first so they get regenerated
	// with context-enriched content (Issue #26).
	if forceReembed {
		fmt.Println("Force mode: deleting all existing embeddings for re-generation with context enrichment...")
		result, err := s.DeleteAllEmbeddings(ctx)
		if err != nil {
			return fmt.Errorf("deleting embeddings: %w", err)
		}
		fmt.Printf("  Deleted %d existing embeddings\n", result)
	}

	// Configure embedder
	embedConfig, err := embed.ResolveEmbedConfig(embedFlag)
	if err != nil {
		return fmt.Errorf("configuring embedder: %w", err)
	}
	if embedConfig == nil {
		return fmt.Errorf("no embedding configuration found")
	}
	if err := embedConfig.Validate(); err != nil {
		return fmt.Errorf("invalid embedding configuration: %w", err)
	}

	embedder, err := embed.NewClient(embedConfig)
	if err != nil {
		return fmt.Errorf("creating embedder: %w", err)
	}

	// Create embedding engine
	embedEngine := ingest.NewEmbedEngine(s, embedder)

	if forceReembed {
		fmt.Println("Re-generating all embeddings with context-enriched content...")
	} else {
		fmt.Println("Generating embeddings for memories without embeddings...")
	}

	// Embed all memories without embeddings
	opts := ingest.DefaultEmbedOptions()
	opts.BatchSize = batchSize
	opts.ProgressFn = func(current, total int) {
		pct := 0
		if total > 0 {
			pct = current * 100 / total
		}
		fmt.Printf("\r  Embedding... [%d/%d] %d%%", current, total, pct)
	}

	result, err := embedEngine.EmbedMemories(ctx, opts)
	if err != nil {
		return fmt.Errorf("embedding memories: %w", err)
	}

	fmt.Printf("\nEmbedding complete:\n")
	fmt.Printf("  Memories processed: %d\n", result.MemoriesProcessed)
	fmt.Printf("  Embeddings added: %d\n", result.EmbeddingsAdded)
	fmt.Printf("  Already had embeddings: %d\n", result.EmbeddingsSkipped)

	if len(result.Errors) > 0 {
		fmt.Printf("  Errors: %d\n", len(result.Errors))
		if globalVerbose {
			for _, err := range result.Errors {
				fmt.Printf("    Memory %d: %s\n", err.MemoryID, err.Message)
			}
		}
	}

	return nil
}

func outputExtractJSON(facts []extract.ExtractedFact) error {
	if facts == nil {
		facts = []extract.ExtractedFact{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(facts)
}

func outputExtractTTY(filepath string, facts []extract.ExtractedFact) error {
	if len(facts) == 0 {
		fmt.Printf("No facts extracted from %s\n", filepath)
		return nil
	}

	// Count facts by extraction method
	rulesCount := 0
	llmCount := 0
	for _, fact := range facts {
		if fact.ExtractionMethod == "llm" {
			llmCount++
		} else {
			rulesCount++
		}
	}

	fmt.Printf("Extracted %d fact", len(facts))
	if len(facts) != 1 {
		fmt.Print("s")
	}
	fmt.Printf(" from %s", filepath)

	if llmCount > 0 {
		fmt.Printf(" (%d rules, %d LLM)", rulesCount, llmCount)
	}
	fmt.Println()
	fmt.Println()

	// Group facts by type for better display
	factsByType := make(map[string][]extract.ExtractedFact)
	for _, fact := range facts {
		factsByType[fact.FactType] = append(factsByType[fact.FactType], fact)
	}

	for factType, typeFacts := range factsByType {
		fmt.Printf("%s (%d):\n", strings.ToUpper(factType), len(typeFacts))
		for _, fact := range typeFacts {
			if fact.Subject != "" {
				fmt.Printf("  • %s %s %s", fact.Subject, fact.Predicate, fact.Object)
			} else {
				fmt.Printf("  • %s: %s", fact.Predicate, fact.Object)
			}
			fmt.Printf(" [%.1f]", fact.Confidence)

			// Show extraction method if LLM was used
			if fact.ExtractionMethod == "llm" {
				fmt.Printf(" (LLM)")
			}

			if fact.SourceQuote != "" && len(fact.SourceQuote) < 50 {
				fmt.Printf(" (%q)", fact.SourceQuote)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	return nil
}

func outputListMemoriesJSON(memories []*store.Memory) error {
	if memories == nil {
		memories = []*store.Memory{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(memories)
}

func outputListMemoriesTTY(memories []*store.Memory, opts store.ListOpts) error {
	if len(memories) == 0 {
		fmt.Println("No memories found")
		return nil
	}

	fmt.Printf("Recent Memories (%d", len(memories))
	if opts.Limit > 0 {
		fmt.Printf(" of latest %d", opts.Limit)
	}
	fmt.Println(")")
	fmt.Println()

	for i, memory := range memories {
		// Format date
		date := memory.ImportedAt.Format("2006-01-02")

		// Truncate content
		content := memory.Content
		if len(content) > 60 {
			content = content[:57] + "..."
		}
		// Replace newlines with spaces for display
		content = strings.ReplaceAll(content, "\n", " ")

		fmt.Printf("  %d. [%s] %s\n", i+1, date, content)

		// Add source info if available
		if memory.SourceFile != "" {
			fmt.Printf("     📁 %s", memory.SourceFile)
			if memory.SourceLine > 0 {
				fmt.Printf(":%d", memory.SourceLine)
			}
			if memory.SourceSection != "" {
				fmt.Printf(" · %s", memory.SourceSection)
			}
			fmt.Println()
		}

		// Add verbose details if requested
		if globalVerbose && len(memory.Content) > 60 {
			fmt.Printf("     Full content: %s\n", memory.Content)
		}

		fmt.Println()
	}

	return nil
}

func outputListFactsJSON(facts []*store.Fact) error {
	if facts == nil {
		facts = []*store.Fact{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(facts)
}

func outputListFactsTTY(facts []*store.Fact, opts store.ListOpts) error {
	if len(facts) == 0 {
		fmt.Println("No facts found")
		return nil
	}

	fmt.Printf("Facts (%d", len(facts))
	if opts.Limit > 0 {
		fmt.Printf(" of latest %d", opts.Limit)
	}
	fmt.Println(")")
	fmt.Println()

	for i, fact := range facts {
		// Format fact content
		factContent := ""
		if fact.Subject != "" {
			factContent = fmt.Sprintf("%s %s %s", fact.Subject, fact.Predicate, fact.Object)
		} else {
			factContent = fmt.Sprintf("%s: %s", fact.Predicate, fact.Object)
		}

		// Truncate if too long and not verbose
		if !globalVerbose && len(factContent) > 60 {
			factContent = factContent[:57] + "..."
		}

		fmt.Printf("  %d. [%s] %s\n", i+1, fact.FactType, factContent)
		fmt.Printf("     Confidence: %.2f · Decay: %.3f/day\n",
			fact.Confidence, fact.DecayRate)

		// Add source quote if available and verbose
		if globalVerbose && fact.SourceQuote != "" {
			fmt.Printf("     Source: %q\n", fact.SourceQuote)
		}

		fmt.Println()
	}

	return nil
}

func exportMemoriesInFormat(memories []*store.Memory, format string, output *os.File) error {
	switch format {
	case "json":
		return exportMemoriesJSON(memories, output)
	case "markdown":
		return exportMemoriesMarkdown(memories, output)
	case "csv":
		return exportMemoriesCSV(memories, output)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func exportFactsInFormat(facts []*store.Fact, format string, output *os.File) error {
	switch format {
	case "json":
		return exportFactsJSON(facts, output)
	case "markdown":
		return exportFactsMarkdown(facts, output)
	case "csv":
		return exportFactsCSV(facts, output)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func exportMemoriesJSON(memories []*store.Memory, output *os.File) error {
	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	return enc.Encode(memories)
}

func exportMemoriesMarkdown(memories []*store.Memory, output *os.File) error {
	fmt.Fprintf(output, "# Cortex Memory Export\n\n")

	// Group by source file
	sourceGroups := make(map[string][]*store.Memory)
	for _, memory := range memories {
		sourceFile := memory.SourceFile
		if sourceFile == "" {
			sourceFile = "Unknown Source"
		}
		sourceGroups[sourceFile] = append(sourceGroups[sourceFile], memory)
	}

	for sourceFile, sourceMemories := range sourceGroups {
		fmt.Fprintf(output, "## Source: %s\n\n", sourceFile)

		for _, memory := range sourceMemories {
			if memory.SourceSection != "" {
				fmt.Fprintf(output, "### %s", memory.SourceSection)
				if memory.SourceLine > 0 {
					fmt.Fprintf(output, " (line %d)", memory.SourceLine)
				}
				fmt.Fprintf(output, "\n\n")
			}

			fmt.Fprintf(output, "%s\n\n", memory.Content)
		}
	}

	return nil
}

func exportMemoriesCSV(memories []*store.Memory, output *os.File) error {
	// Write CSV header
	fmt.Fprintf(output, "id,content,source_file,source_line,source_section,imported_at\n")

	for _, memory := range memories {
		// Escape quotes in content
		content := strings.ReplaceAll(memory.Content, `"`, `""`)
		sourceFile := strings.ReplaceAll(memory.SourceFile, `"`, `""`)
		sourceSection := strings.ReplaceAll(memory.SourceSection, `"`, `""`)

		fmt.Fprintf(output, `%d,"%s","%s",%d,"%s",%s`+"\n",
			memory.ID,
			content,
			sourceFile,
			memory.SourceLine,
			sourceSection,
			memory.ImportedAt.Format("2006-01-02T15:04:05Z07:00"))
	}

	return nil
}

func exportFactsJSON(facts []*store.Fact, output *os.File) error {
	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	return enc.Encode(facts)
}

func exportFactsMarkdown(facts []*store.Fact, output *os.File) error {
	fmt.Fprintf(output, "# Cortex Facts Export\n\n")

	// Group by fact type
	typeGroups := make(map[string][]*store.Fact)
	for _, fact := range facts {
		typeGroups[fact.FactType] = append(typeGroups[fact.FactType], fact)
	}

	for factType, typeFacts := range typeGroups {
		fmt.Fprintf(output, "## %s Facts\n\n", strings.Title(factType))

		for _, fact := range typeFacts {
			if fact.Subject != "" {
				fmt.Fprintf(output, "**%s** %s %s", fact.Subject, fact.Predicate, fact.Object)
			} else {
				fmt.Fprintf(output, "**%s**: %s", fact.Predicate, fact.Object)
			}

			fmt.Fprintf(output, " *(confidence: %.2f)*\n", fact.Confidence)

			if fact.SourceQuote != "" {
				fmt.Fprintf(output, "> %s\n", fact.SourceQuote)
			}

			fmt.Fprintf(output, "\n")
		}
	}

	return nil
}

func exportFactsCSV(facts []*store.Fact, output *os.File) error {
	// Write CSV header
	fmt.Fprintf(output, "id,memory_id,subject,predicate,object,fact_type,confidence,decay_rate,source_quote,created_at\n")

	for _, fact := range facts {
		// Escape quotes
		subject := strings.ReplaceAll(fact.Subject, `"`, `""`)
		predicate := strings.ReplaceAll(fact.Predicate, `"`, `""`)
		object := strings.ReplaceAll(fact.Object, `"`, `""`)
		sourceQuote := strings.ReplaceAll(fact.SourceQuote, `"`, `""`)

		fmt.Fprintf(output, `%d,%d,"%s","%s","%s",%s,%.6f,%.6f,"%s",%s`+"\n",
			fact.ID,
			fact.MemoryID,
			subject,
			predicate,
			object,
			fact.FactType,
			fact.Confidence,
			fact.DecayRate,
			sourceQuote,
			fact.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	}

	return nil
}

// ExtendedStats holds additional statistics not available from store.Stats().
type ExtendedStats struct {
	SourceFiles int
	DateRange   string
}

// getExtendedStats fetches source file count and import date range via SQL.
func getExtendedStats(ctx context.Context, s store.Store) (int, string, error) {
	// Use the store's ExtendedStats method if available (efficient SQL queries).
	if es, ok := s.(interface {
		ExtendedStats(ctx context.Context) (int, string, string, error)
	}); ok {
		sourceFiles, earliest, latest, err := es.ExtendedStats(ctx)
		if err != nil {
			return 0, "", err
		}
		if sourceFiles == 0 {
			return 0, "N/A", nil
		}
		dateRange := earliest
		if earliest != latest {
			dateRange = earliest + " → " + latest
		}
		return sourceFiles, dateRange, nil
	}

	// Fallback: list memories (slower, for interface compatibility).
	memories, err := s.ListMemories(ctx, store.ListOpts{Limit: math.MaxInt32}) // TODO: Add pagination for v0.2
	if err != nil {
		return 0, "", err
	}

	if len(memories) == 0 {
		return 0, "N/A", nil
	}

	files := make(map[string]bool)
	var earliest, latest string

	for _, m := range memories {
		if m.SourceFile != "" {
			files[m.SourceFile] = true
		}
		ts := m.ImportedAt.Format("2006-01-02")
		if earliest == "" || ts < earliest {
			earliest = ts
		}
		if latest == "" || ts > latest {
			latest = ts
		}
	}

	dateRange := "N/A"
	if earliest != "" {
		if earliest == latest {
			dateRange = earliest
		} else {
			dateRange = earliest + " → " + latest
		}
	}

	return len(files), dateRange, nil
}

func outputJSON(results []search.Result) error {
	if results == nil {
		results = []search.Result{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

func outputTTY(query string, results []search.Result) error {
	return outputTTYSearch(query, results, false)
}

func outputTTYSearch(query string, results []search.Result, showMetadata bool) error {
	if len(results) == 0 {
		fmt.Printf("No results for %q\n", query)
		return nil
	}

	fmt.Printf("Results for %q (%d match", query, len(results))
	if len(results) != 1 {
		fmt.Print("es")
	}
	fmt.Println(")")
	fmt.Println()

	for i, r := range results {
		content := search.TruncateContent(r.Content, 200)
		// Replace newlines with spaces for display
		content = strings.ReplaceAll(content, "\n", " ")

		fmt.Printf("  %d. [%.2f] %s\n", i+1, r.Score, content)
		if r.SourceFile != "" {
			fmt.Printf("     📁 %s", r.SourceFile)
			if r.SourceLine > 0 {
				fmt.Printf(":%d", r.SourceLine)
			}
			if r.Project != "" {
				fmt.Printf("  🏷️ %s", r.Project)
			}
			fmt.Println()
		}
		// Show metadata if requested (--show-metadata flag, Issue #30)
		if showMetadata && r.Metadata != nil {
			meta := r.Metadata
			fmt.Print("     📋")
			if meta.AgentID != "" {
				fmt.Printf(" agent:%s", meta.AgentID)
			}
			if meta.Channel != "" {
				fmt.Printf(" channel:%s", meta.Channel)
			}
			if meta.ChannelName != "" {
				fmt.Printf("(%s)", meta.ChannelName)
			}
			if meta.Model != "" {
				fmt.Printf(" model:%s", meta.Model)
			}
			if meta.InputTokens > 0 || meta.OutputTokens > 0 {
				fmt.Printf(" tokens:%d/%d", meta.InputTokens, meta.OutputTokens)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	return nil
}

type statsJSON struct {
	Memories    int64  `json:"memories"`
	Facts       int64  `json:"facts"`
	Embeddings  int64  `json:"embeddings"`
	Events      int64  `json:"events"`
	SourceFiles int    `json:"source_files"`
	DBSizeBytes int64  `json:"db_size_bytes"`
	DateRange   string `json:"date_range"`
}

func outputStatsJSON(stats *store.StoreStats, sourceFiles int, dateRange string) error {
	s := statsJSON{
		Memories:    stats.MemoryCount,
		Facts:       stats.FactCount,
		Embeddings:  stats.EmbeddingCount,
		Events:      stats.EventCount,
		SourceFiles: sourceFiles,
		DBSizeBytes: stats.DBSizeBytes,
		DateRange:   dateRange,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func outputStatsTTY(stats *store.StoreStats, sourceFiles int, dateRange string) error {
	fmt.Println("Cortex Memory Statistics")
	fmt.Println("========================")
	fmt.Println()
	fmt.Printf("  Memories:     %d\n", stats.MemoryCount)
	fmt.Printf("  Facts:        %d\n", stats.FactCount)
	fmt.Printf("  Embeddings:   %d\n", stats.EmbeddingCount)
	fmt.Printf("  Events:       %d\n", stats.EventCount)
	fmt.Printf("  Source Files: %d\n", sourceFiles)
	fmt.Println()
	fmt.Printf("  Date Range:   %s\n", dateRange)

	if stats.DBSizeBytes > 0 {
		fmt.Printf("  DB Size:      %s\n", formatBytes(stats.DBSizeBytes))
	}
	fmt.Println()

	return nil
}

// Enhanced stats output functions
func outputEnhancedStatsJSON(stats *observe.Stats, dateRange string) error {
	type enhancedStatsJSON struct {
		*observe.Stats
		DateRange string `json:"date_range"`
	}

	s := enhancedStatsJSON{
		Stats:     stats,
		DateRange: dateRange,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func outputEnhancedStatsTTY(stats *observe.Stats, dateRange string) error {
	fmt.Println("╭─────────────────────────────────────────────╮")
	fmt.Println("│              Cortex Memory Stats             │")
	fmt.Println("├─────────────────────────────────────────────┤")
	fmt.Printf("│ Memories:        %-27d │\n", stats.TotalMemories)
	fmt.Printf("│ Facts:           %-27d │\n", stats.TotalFacts)
	fmt.Printf("│ Sources:         %-27d │\n", stats.TotalSources)
	if stats.StorageBytes > 0 {
		fmt.Printf("│ Storage:         %-27s │\n", formatBytes(stats.StorageBytes))
	}
	fmt.Printf("│ Avg confidence:  %.2f%-22s │\n", stats.AvgConfidence, "")

	if len(stats.FactsByType) > 0 {
		fmt.Println("├─────────────────────────────────────────────┤")
		fmt.Println("│ Facts by Type                                │")

		// Calculate percentages and show top types
		total := stats.TotalFacts
		for factType, count := range stats.FactsByType {
			if total > 0 {
				percent := float64(count) * 100.0 / float64(total)
				bars := int(percent / 10)
				if bars > 10 {
					bars = 10
				}
				barStr := strings.Repeat("█", bars) + strings.Repeat("░", 10-bars)
				fmt.Printf("│   %-12s %5d (%4.1f%%)  %s │\n", factType+":", count, percent, barStr)
			} else {
				fmt.Printf("│   %-12s %5d             %10s │\n", factType+":", count, "")
			}
		}
	}

	if stats.ConfidenceDistribution != nil && stats.ConfidenceDistribution.Total > 0 {
		fmt.Println("├─────────────────────────────────────────────┤")
		fmt.Println("│ Confidence Health (Ebbinghaus decay)         │")
		cd := stats.ConfidenceDistribution
		total := float64(cd.Total)
		highPct := float64(cd.High) * 100.0 / total
		medPct := float64(cd.Medium) * 100.0 / total
		lowPct := float64(cd.Low) * 100.0 / total
		fmt.Printf("│   🟢 High (≥0.7):   %5d (%4.1f%%)            │\n", cd.High, highPct)
		fmt.Printf("│   🟡 Medium:        %5d (%4.1f%%)            │\n", cd.Medium, medPct)
		fmt.Printf("│   🔴 Low (<0.3):    %5d (%4.1f%%)            │\n", cd.Low, lowPct)
	}

	fmt.Println("├─────────────────────────────────────────────┤")
	fmt.Println("│ Freshness                                    │")
	fmt.Printf("│   Today:           %-25d │\n", stats.Freshness.Today)
	fmt.Printf("│   This week:       %-25d │\n", stats.Freshness.ThisWeek)
	fmt.Printf("│   This month:      %-25d │\n", stats.Freshness.ThisMonth)
	fmt.Printf("│   Older:           %-25d │\n", stats.Freshness.Older)

	if dateRange != "N/A" {
		fmt.Println("├─────────────────────────────────────────────┤")
		fmt.Printf("│ Date Range:   %-29s │\n", dateRange)
	}

	fmt.Println("╰─────────────────────────────────────────────╯")
	return nil
}

// Stale facts output functions
func outputStaleJSON(staleFacts []observe.StaleFact) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(staleFacts)
}

func outputStaleTTY(staleFacts []observe.StaleFact, opts observe.StaleOpts, totalFacts int) error {
	if len(staleFacts) == 0 {
		if totalFacts > 0 {
			fmt.Printf("No stale facts found. All %d facts were reinforced within the last %d days.\n", totalFacts, opts.MaxDays)
		} else {
			fmt.Printf("No stale facts found (confidence < %.2f, not reinforced in %d+ days)\n", opts.MaxConfidence, opts.MaxDays)
		}
		return nil
	}

	fmt.Printf("Stale Facts (confidence < %.2f, not reinforced in %d+ days)\n\n", opts.MaxConfidence, opts.MaxDays)

	for i, sf := range staleFacts {
		if i >= opts.Limit {
			break
		}

		// Format fact content
		factContent := ""
		if sf.Fact.Subject != "" {
			factContent = fmt.Sprintf("%s %s %s", sf.Fact.Subject, sf.Fact.Predicate, sf.Fact.Object)
		} else {
			factContent = fmt.Sprintf("%s: %s", sf.Fact.Predicate, sf.Fact.Object)
		}

		fmt.Printf("⚠️  %.2f  \"%s\"\n", sf.EffectiveConfidence, factContent)
		fmt.Printf("         %s · %d days old · original confidence: %.2f\n",
			sf.Fact.FactType, sf.DaysSinceReinforced, sf.Fact.Confidence)

		if sf.Fact.SourceQuote != "" {
			fmt.Printf("         Source: %q\n", sf.Fact.SourceQuote)
		}
		fmt.Println()
	}

	fmt.Printf("✅  %d stale fact", len(staleFacts))
	if len(staleFacts) != 1 {
		fmt.Print("s")
	}
	fmt.Println(" found.")
	return nil
}

// Conflicts output functions
func outputConflictsJSON(conflicts []observe.Conflict) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(conflicts)
}

func outputConflictsTTY(conflicts []observe.Conflict) error {
	if len(conflicts) == 0 {
		fmt.Println("No conflicts detected.")
		return nil
	}

	fmt.Printf("Conflicts Detected: %d\n\n", len(conflicts))

	for _, c := range conflicts {
		fmt.Println("❌ Attribute Conflict")

		fact1Content := ""
		if c.Fact1.Subject != "" {
			fact1Content = fmt.Sprintf("%s %s %s", c.Fact1.Subject, c.Fact1.Predicate, c.Fact1.Object)
		} else {
			fact1Content = fmt.Sprintf("%s: %s", c.Fact1.Predicate, c.Fact1.Object)
		}

		fact2Content := ""
		if c.Fact2.Subject != "" {
			fact2Content = fmt.Sprintf("%s %s %s", c.Fact2.Subject, c.Fact2.Predicate, c.Fact2.Object)
		} else {
			fact2Content = fmt.Sprintf("%s: %s", c.Fact2.Predicate, c.Fact2.Object)
		}

		fmt.Printf("   \"%s\" (confidence: %.2f)\n", fact1Content, c.Fact1.Confidence)
		fmt.Printf("   \"%s\" (confidence: %.2f)\n", fact2Content, c.Fact2.Confidence)
		fmt.Printf("   Similarity: %.2f\n", c.Similarity)
		fmt.Println()
	}

	return nil
}

// isTTY returns true if stdout is a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func runReason(args []string) error {
	// Parse flags
	var queryParts []string
	presetName := ""
	modelFlag := ""
	projectFlag := ""
	maxTokens := 0
	maxContext := 8000
	jsonOutput := false
	embedFlag := ""
	listPresets := false
	recursive := false
	maxIterations := 8
	maxDepth := 1
	verbose := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--preset" && i+1 < len(args):
			i++
			presetName = args[i]
		case strings.HasPrefix(args[i], "--preset="):
			presetName = strings.TrimPrefix(args[i], "--preset=")
		case args[i] == "--model" && i+1 < len(args):
			i++
			modelFlag = args[i]
		case strings.HasPrefix(args[i], "--model="):
			modelFlag = strings.TrimPrefix(args[i], "--model=")
		case args[i] == "--project" && i+1 < len(args):
			i++
			projectFlag = args[i]
		case strings.HasPrefix(args[i], "--project="):
			projectFlag = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--max-tokens" && i+1 < len(args):
			i++
			if v, err := strconv.Atoi(args[i]); err == nil {
				maxTokens = v
			}
		case args[i] == "--max-context" && i+1 < len(args):
			i++
			if v, err := strconv.Atoi(args[i]); err == nil {
				maxContext = v
			}
		case args[i] == "--embed" && i+1 < len(args):
			i++
			embedFlag = args[i]
		case strings.HasPrefix(args[i], "--embed="):
			embedFlag = strings.TrimPrefix(args[i], "--embed=")
		case args[i] == "--json":
			jsonOutput = true
		case args[i] == "--list":
			listPresets = true
		case args[i] == "--recursive", args[i] == "-R":
			recursive = true
		case args[i] == "--max-iterations" && i+1 < len(args):
			i++
			if v, err := strconv.Atoi(args[i]); err == nil {
				maxIterations = v
			}
		case args[i] == "--max-depth" && i+1 < len(args):
			i++
			if v, err := strconv.Atoi(args[i]); err == nil {
				maxDepth = v
			}
		case args[i] == "--verbose", args[i] == "-v":
			verbose = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	// Handle --list
	if listPresets {
		configDir := getConfigDir()
		presets := reason.ListPresets(configDir)
		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(presets)
		}
		fmt.Println("Available presets:")
		for _, p := range presets {
			fmt.Printf("  %-20s %s\n", p.Name, p.Description)
		}
		return nil
	}

	query := strings.Join(queryParts, " ")
	if query == "" && presetName == "" {
		return fmt.Errorf("usage: cortex reason <query> [--preset <name>] [--model <provider/model>] [--project <name>] [--list]")
	}

	// Smart model defaults based on preset and available API keys:
	//   Interactive (daily-digest, conflict-check, agent-review): gemini-2.5-flash
	//   Deep analysis (weekly-dive, fact-audit): deepseek-v3.2
	//   No API key: phi4-mini local
	// Users can always override with --model.
	if modelFlag == "" {
		if os.Getenv("OPENROUTER_API_KEY") != "" {
			switch presetName {
			case "weekly-dive", "fact-audit":
				modelFlag = reason.DefaultCronModel
			default:
				modelFlag = reason.DefaultInteractiveModel
			}
		} else {
			modelFlag = reason.DefaultLocalModel
		}
	}

	// Parse provider/model
	provider, model := reason.ParseProviderModel(modelFlag)

	// Create LLM client
	llm, err := reason.NewLLM(reason.LLMConfig{
		Provider: provider,
		Model:    model,
	})
	if err != nil {
		return err
	}

	// Open store
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	// Create search engine
	var searchEngine *search.Engine
	if embedFlag != "" {
		cfg, err := embed.ParseEmbedFlag(embedFlag)
		if err != nil {
			return fmt.Errorf("parsing embed provider: %w", err)
		}
		client, err := embed.NewClient(cfg)
		if err != nil {
			return fmt.Errorf("creating embedder: %w", err)
		}
		searchEngine = search.NewEngineWithEmbedder(s, client)
	} else {
		searchEngine = search.NewEngine(s)
	}
	configDir := getConfigDir()

	// Create reason engine
	engine := reason.NewEngine(reason.EngineConfig{
		SearchEngine: searchEngine,
		Store:        s,
		LLM:         llm,
		ConfigDir:    configDir,
	})

	// Run reasoning
	ctx := context.Background()

	if recursive {
		// Recursive mode — iterative loop with actions
		if verbose {
			fmt.Printf("🔄 Recursive reasoning: max %d iterations, depth %d\n\n", maxIterations, maxDepth)
		}
		rResult, err := engine.ReasonRecursive(ctx, reason.RecursiveOptions{
			Query:         query,
			Preset:        presetName,
			Project:       projectFlag,
			MaxIterations: maxIterations,
			MaxDepth:      maxDepth,
			MaxTokens:     maxTokens,
			MaxContext:     maxContext,
			JSONOutput:    jsonOutput,
			Verbose:       verbose,
		})
		if err != nil {
			return err
		}

		// Output
		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rResult)
		}

		// TTY output
		fmt.Println(rResult.Content)
		fmt.Println()
		fmt.Printf("─── %s/%s | %d iterations, %d calls | %d memories, %d facts | search %s, llm %s | %d→%d tokens",
			rResult.Provider, rResult.Model,
			rResult.Iterations, rResult.TotalCalls,
			rResult.MemoriesUsed, rResult.FactsUsed,
			rResult.SearchTime.Round(time.Millisecond),
			rResult.LLMTime.Round(time.Millisecond),
			rResult.TokensIn, rResult.TokensOut,
		)
		if len(rResult.SubQueries) > 0 {
			fmt.Printf(" | %d sub-queries", len(rResult.SubQueries))
		}
		fmt.Println(" ───")

		return nil
	}

	// Single-pass mode (default)
	result, err := engine.Reason(ctx, reason.ReasonOptions{
		Query:      query,
		Preset:     presetName,
		Project:    projectFlag,
		MaxTokens:  maxTokens,
		MaxContext:  maxContext,
		JSONOutput: jsonOutput,
	})
	if err != nil {
		return err
	}

	// Output
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// TTY output
	fmt.Println(result.Content)
	fmt.Println()
	fmt.Printf("─── %s/%s | %d memories, %d facts | search %s, llm %s | %d→%d tokens ───\n",
		result.Provider, result.Model,
		result.MemoriesUsed, result.FactsUsed,
		result.SearchTime.Round(time.Millisecond),
		result.LLMTime.Round(time.Millisecond),
		result.TokensIn, result.TokensOut,
	)

	return nil
}

func runBench(args []string) error {
	embedFlag := ""
	includeLocal := false
	jsonOutput := false
	outputFile := ""
	var customModels []string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--embed" && i+1 < len(args):
			i++
			embedFlag = args[i]
		case strings.HasPrefix(args[i], "--embed="):
			embedFlag = strings.TrimPrefix(args[i], "--embed=")
		case args[i] == "--local":
			includeLocal = true
		case args[i] == "--json":
			jsonOutput = true
		case args[i] == "--output" && i+1 < len(args):
			i++
			outputFile = args[i]
		case strings.HasPrefix(args[i], "--output="):
			outputFile = strings.TrimPrefix(args[i], "--output=")
		case args[i] == "--models" && i+1 < len(args):
			i++
			customModels = strings.Split(args[i], ",")
		case strings.HasPrefix(args[i], "--models="):
			customModels = strings.Split(strings.TrimPrefix(args[i], "--models="), ",")
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s\nUsage: cortex bench [--embed <provider/model>] [--local] [--models m1,m2] [--output file.md] [--json]", args[i])
		}
	}

	// Open store
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	// Create search engine
	var searchEngine *search.Engine
	if embedFlag != "" {
		cfg, err := embed.ParseEmbedFlag(embedFlag)
		if err != nil {
			return fmt.Errorf("parsing embed provider: %w", err)
		}
		client, err := embed.NewClient(cfg)
		if err != nil {
			return fmt.Errorf("creating embedder: %w", err)
		}
		searchEngine = search.NewEngineWithEmbedder(s, client)
	} else {
		searchEngine = search.NewEngine(s)
	}

	// Create a placeholder LLM (bench creates its own per model)
	placeholderLLM, _ := reason.NewLLM(reason.LLMConfig{Provider: "ollama", Model: "phi4-mini"})

	engine := reason.NewEngine(reason.EngineConfig{
		SearchEngine: searchEngine,
		Store:        s,
		LLM:          placeholderLLM,
		ConfigDir:    getConfigDir(),
	})

	// Build model list
	var models []reason.BenchModel
	if len(customModels) > 0 {
		for _, m := range customModels {
			m = strings.TrimSpace(m)
			provider, model := reason.ParseProviderModel(m)
			models = append(models, reason.BenchModel{
				Label:    m,
				Provider: provider,
				Model:    model,
			})
		}
	}

	fmt.Println("🧪 Cortex Reason Benchmark")
	fmt.Println("─────────────────────────")

	opts := reason.BenchOptions{
		Models:       models, // nil = defaults
		IncludeLocal: includeLocal,
		MaxContext:   8000,
		ProgressFn: func(model, preset string, i, total int) {
			fmt.Printf("  [%d/%d] %s × %s...\n", i, total, model, preset)
		},
	}

	ctx := context.Background()
	report, err := engine.RunBenchmark(ctx, opts)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	// Markdown output
	md := report.FormatMarkdown()
	fmt.Println()
	fmt.Println(md)

	// Save to file if requested
	if outputFile != "" {
		if err := os.WriteFile(outputFile, []byte(md), 0644); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
		fmt.Printf("📄 Report saved to %s\n", outputFile)
	}

	return nil
}

func getConfigDir() string {
	home, _ := os.UserHomeDir()
	return home + "/.cortex"
}

func runProjects(args []string) error {
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		}
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	projects, err := s.ListProjects(context.Background())
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(projects)
	}

	if len(projects) == 0 {
		fmt.Println("No projects found. Use --project or --auto-tag when importing.")
		return nil
	}

	fmt.Printf("%-20s  %8s  %8s\n", "PROJECT", "MEMORIES", "FACTS")
	fmt.Println(strings.Repeat("─", 42))
	for _, p := range projects {
		name := p.Name
		if name == "" {
			name = "(untagged)"
		}
		fmt.Printf("%-20s  %8d  %8d\n", name, p.MemoryCount, p.FactCount)
	}
	return nil
}

func runTag(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex tag --project <name> [--source <pattern>] [--id <id>...] [--auto]")
	}

	project := ""
	sourcePattern := ""
	autoTag := false
	var memoryIDs []int64

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--project" && i+1 < len(args):
			i++
			project = args[i]
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--source" && i+1 < len(args):
			i++
			sourcePattern = args[i]
		case strings.HasPrefix(args[i], "--source="):
			sourcePattern = strings.TrimPrefix(args[i], "--source=")
		case args[i] == "--id" && i+1 < len(args):
			i++
			id, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid --id value: %s", args[i])
			}
			memoryIDs = append(memoryIDs, id)
		case args[i] == "--auto":
			autoTag = true
		default:
			// Try parsing as memory ID
			id, err := strconv.ParseInt(args[i], 10, 64)
			if err == nil {
				memoryIDs = append(memoryIDs, id)
			} else {
				return fmt.Errorf("unknown argument: %s", args[i])
			}
		}
	}

	if !autoTag && project == "" {
		return fmt.Errorf("--project is required (or use --auto for auto-tagging)")
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()

	if autoTag {
		return runAutoTag(ctx, s)
	}

	var totalTagged int64

	if sourcePattern != "" {
		// Convert glob-style pattern to SQL LIKE
		likePattern := strings.ReplaceAll(sourcePattern, "*", "%")
		n, err := s.TagMemoriesBySource(ctx, project, likePattern)
		if err != nil {
			return err
		}
		totalTagged += n
		fmt.Printf("Tagged %d memories matching %q → project %q\n", n, sourcePattern, project)
	}

	if len(memoryIDs) > 0 {
		n, err := s.TagMemories(ctx, project, memoryIDs)
		if err != nil {
			return err
		}
		totalTagged += n
		fmt.Printf("Tagged %d memories by ID → project %q\n", n, project)
	}

	if totalTagged == 0 && sourcePattern == "" && len(memoryIDs) == 0 {
		return fmt.Errorf("specify --source <pattern> or --id <id> to tag memories")
	}

	return nil
}

// runAutoTag applies default project rules to all untagged memories.
// Uses path-based rules first, then content-keyword matching as fallback.
func runAutoTag(ctx context.Context, s store.Store) error {
	// Get all untagged memories
	memories, err := s.ListMemories(ctx, store.ListOpts{
		Limit: 100000, // Get all
	})
	if err != nil {
		return fmt.Errorf("listing memories: %w", err)
	}

	tagged := 0
	byProject := make(map[string]int)
	byMethod := map[string]int{"path": 0, "content": 0}

	for _, m := range memories {
		if m.Project != "" {
			continue // Already tagged
		}

		// Try path rules first, then content keywords
		inferred := store.InferProjectFull(m.SourceFile, m.Content, store.DefaultProjectRules, store.DefaultContentRules)
		if inferred == "" {
			continue // No matching rule
		}

		// Track which method matched
		if store.InferProject(m.SourceFile, store.DefaultProjectRules) != "" {
			byMethod["path"]++
		} else {
			byMethod["content"]++
		}

		_, err := s.TagMemories(ctx, inferred, []int64{m.ID})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to tag memory %d: %v\n", m.ID, err)
			continue
		}
		tagged++
		byProject[inferred]++
	}

	if tagged == 0 {
		fmt.Println("No memories matched auto-tag rules. All may already be tagged.")
		return nil
	}

	fmt.Printf("Auto-tagged %d memories:\n", tagged)
	for project, count := range byProject {
		fmt.Printf("  %s: %d\n", project, count)
	}
	fmt.Printf("\nBy method: %d path-based, %d content-based\n", byMethod["path"], byMethod["content"])
	return nil
}

func printUsage() {
	fmt.Printf(`cortex %s — Import-first memory layer for AI agents

Usage:
  cortex [global-flags] <command> [arguments]

Commands:
  import <path>       Import memory from a file or directory
  reimport <path>     Wipe database and reimport from scratch (--embed to include embeddings)
  extract <file>      Extract facts from a single file (without importing)
  embed <provider/model> Generate embeddings for all memories without embeddings
  search <query>      Search memory (keyword, semantic, or hybrid)
  reinforce <id>      Reinforce a fact (reset its decay timer)
  stats               Show memory statistics and health
  list                List memories or facts from the store
  export              Export the full memory store in different formats
  stale               Find outdated memory entries
  conflicts           Detect contradictory facts
  cleanup             Remove garbage memories and headless facts
  reason <query>      LLM reasoning over memories (search → prompt → analyze)
  bench               Benchmark LLM models for reason (speed, cost, quality)
  projects            List all project tags with memory/fact counts
  tag                 Tag memories by project (--project, --source, --id, --auto)
  mcp                 Start MCP (Model Context Protocol) server
  version             Print version

Global Flags:
  --db <path>         Database path (overrides CORTEX_DB env var)
  --read-only         Open database in read-only mode (no schema changes)
  --verbose, -v       Show detailed output
  -h, --help          Show this help message

Search Flags:
  --mode <mode>       Search mode: keyword, semantic, hybrid (default: keyword)
  --limit <N>         Maximum results (default: 10)
  --min-score <F>     Minimum search score threshold (default: mode-dependent; --min-confidence still works)
  --embed <provider/model> Embedding provider for semantic/hybrid search (e.g., --embed ollama/all-minilm)
  --project <name>    Scope search to a specific project (e.g., --project trading)
  --json              Force JSON output even in TTY

Import Flags:
  -r, --recursive     Recursively import from directories
  -n, --dry-run       Show what would be imported without writing
  --extract           Extract facts from imported memories and store them
  --project <name>    Tag imported memories with a project (e.g., --project trading)
  --auto-tag          Infer project from file paths using built-in rules
  --embed <provider/model> Generate embeddings during import (e.g., --embed ollama/all-minilm)
  --llm <provider/model>  Enable LLM-assisted extraction (e.g., --llm openai/gpt-4o-mini)

Projects/Tag Flags:
  cortex projects [--json]                    List all projects
  cortex tag --project <name> --source <pat>  Tag memories by source file pattern
  cortex tag --project <name> --id <id>       Tag specific memories by ID
  cortex tag --auto                           Auto-tag untagged memories using path rules

Extract Flags:
  --json              Force JSON output even in TTY
  --llm <provider/model>  Enable LLM-assisted extraction (e.g., --llm ollama/gemma2:2b)

List Flags:
  --facts             List facts instead of memories
  --limit <N>         Maximum results (default: 20)
  --source <file>     Filter by source file
  --type <fact_type>  Filter facts by type (kv, temporal, identity, etc.)
  --json              Force JSON output even in TTY

Export Flags:
  --format <fmt>      Output format: json, markdown, csv (default: json)
  --output <file>     Write to file instead of stdout
  --facts             Export facts instead of memories

Stats Flags:
  --json              Force JSON output even in TTY

Reimport Flags:
  -r, --recursive     Recursively import from directories
  --extract           Extract facts from imported memories
  --embed <provider/model> Generate embeddings after import
  --llm <provider/model>  Enable LLM-assisted extraction
  -f, --force         Skip confirmation prompt

Reinforce:
  cortex reinforce <fact_id> [fact_id...]   Reset decay timer for specified facts

MCP Flags:
  --port <N>          Start HTTP+SSE transport on port (default: stdio)
  --embed <provider/model> Enable semantic/hybrid search via embeddings

Examples:
  cortex list --limit 50
  cortex list --facts --type kv
  cortex export --format markdown --output memories.md
  cortex --db ~/my-cortex.db list --source ~/notes.md
  cortex mcp                          # Start MCP server (stdio, for Claude Desktop/Cursor)
  cortex mcp --port 8080              # Start MCP server (HTTP+SSE)

Documentation:
  https://github.com/hurttlocker/cortex
`, version)
}
