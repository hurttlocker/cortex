package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hurttlocker/cortex/internal/extract"
	"github.com/hurttlocker/cortex/internal/ingest"
	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "import":
		if err := runImport(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "extract":
		if err := runExtract(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "search":
		if err := runSearch(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "stats":
		if err := runStats(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "list":
		fmt.Println("cortex list: not yet implemented")
		fmt.Println("Usage: cortex list [--source <file>] [--since <date>]")
	case "export":
		fmt.Println("cortex export: not yet implemented")
		fmt.Println("Usage: cortex export [--format json|markdown]")
	case "stale":
		fmt.Println("cortex stale: not yet implemented")
		fmt.Println("Usage: cortex stale [--days 30]")
	case "conflicts":
		fmt.Println("cortex conflicts: not yet implemented")
	case "version":
		fmt.Printf("cortex %s\n", version)
	case "--version", "-v":
		fmt.Printf("cortex %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex import <path> [--recursive] [--dry-run] [--extract]")
	}

	// Parse flags
	var paths []string
	opts := ingest.ImportOptions{}
	enableExtraction := false

	for _, arg := range args {
		switch {
		case arg == "--recursive" || arg == "-r":
			opts.Recursive = true
		case arg == "--dry-run" || arg == "-n":
			opts.DryRun = true
		case arg == "--extract":
			enableExtraction = true
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag: %s", arg)
		default:
			paths = append(paths, arg)
		}
	}

	if len(paths) == 0 {
		return fmt.Errorf("no path specified")
	}

	// Open store
	s, err := store.NewStore(store.StoreConfig{})
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
		extractionStats, err := runExtractionOnImportedMemories(ctx, s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Extraction error: %v\n", err)
		} else {
			fmt.Printf("  Facts extracted: %d\n", extractionStats.FactsExtracted)
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
	minConfidence := 0.0
	jsonOutput := false

	for i := 0; i < len(args); i++ {
		switch {
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
		case args[i] == "--min-confidence" && i+1 < len(args):
			i++
			f, err := strconv.ParseFloat(args[i], 64)
			if err != nil {
				return fmt.Errorf("invalid --min-confidence value: %s", args[i])
			}
			minConfidence = f
		case strings.HasPrefix(args[i], "--min-confidence="):
			f, err := strconv.ParseFloat(strings.TrimPrefix(args[i], "--min-confidence="), 64)
			if err != nil {
				return fmt.Errorf("invalid --min-confidence value: %s", args[i])
			}
			minConfidence = f
		case args[i] == "--json":
			jsonOutput = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		return fmt.Errorf("usage: cortex search <query> [--mode keyword|semantic|hybrid] [--limit N] [--json]")
	}

	searchMode, err := search.ParseMode(mode)
	if err != nil {
		return err
	}

	// Open store
	s, err := store.NewStore(store.StoreConfig{})
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	engine := search.NewEngine(s)
	ctx := context.Background()

	opts := search.Options{
		Mode:          searchMode,
		Limit:         limit,
		MinConfidence: minConfidence,
	}

	results, err := engine.Search(ctx, query, opts)
	if err != nil {
		return err
	}

	// Determine output format
	if jsonOutput || !isTTY() {
		return outputJSON(results)
	}

	// Hybrid mode note in Phase 1
	if searchMode == search.ModeHybrid {
		fmt.Println("Note: hybrid mode currently uses keyword search only (semantic search coming in Phase 2)")
		fmt.Println()
	}

	return outputTTY(query, results)
}

func runStats(args []string) error {
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		}
	}

	// Open store
	s, err := store.NewStore(store.StoreConfig{})
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()

	stats, err := s.Stats(ctx)
	if err != nil {
		return fmt.Errorf("getting stats: %w", err)
	}

	// Get additional info: source file count and date range
	sourceFiles, dateRange, err := getExtendedStats(ctx, s)
	if err != nil {
		return fmt.Errorf("getting extended stats: %w", err)
	}

	if jsonOutput || !isTTY() {
		return outputStatsJSON(stats, sourceFiles, dateRange)
	}

	return outputStatsTTY(stats, sourceFiles, dateRange)
}

func runExtract(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex extract <file> [--json]")
	}

	// Parse flags
	var filepath string
	jsonOutput := false

	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOutput = true
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag: %s", arg)
		default:
			if filepath != "" {
				return fmt.Errorf("only one file path allowed")
			}
			filepath = arg
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

	// Run extraction
	pipeline := extract.NewPipeline()
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

// ExtractionStats holds statistics about extraction run.
type ExtractionStats struct {
	FactsExtracted int
}

// runExtractionOnImportedMemories runs extraction on recently imported memories.
func runExtractionOnImportedMemories(ctx context.Context, s store.Store) (*ExtractionStats, error) {
	// Get recently imported memories (limit to reasonable batch size)
	memories, err := s.ListMemories(ctx, store.ListOpts{Limit: 1000, SortBy: "date"})
	if err != nil {
		return nil, fmt.Errorf("listing memories: %w", err)
	}

	pipeline := extract.NewPipeline()
	stats := &ExtractionStats{}

	for _, memory := range memories {
		// Build metadata
		metadata := map[string]string{
			"source_file": memory.SourceFile,
		}
		if strings.HasSuffix(strings.ToLower(memory.SourceFile), ".md") {
			metadata["format"] = "markdown"
		}

		// Extract facts
		facts, err := pipeline.Extract(ctx, memory.Content, metadata)
		if err != nil {
			continue // Skip errors, continue with next memory
		}

		// Store facts
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
		}
	}

	return stats, nil
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

	fmt.Printf("Extracted %d fact", len(facts))
	if len(facts) != 1 {
		fmt.Print("s")
	}
	fmt.Printf(" from %s\n\n", filepath)

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
			if fact.SourceQuote != "" && len(fact.SourceQuote) < 50 {
				fmt.Printf(" (%q)", fact.SourceQuote)
			}
			fmt.Println()
		}
		fmt.Println()
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
	memories, err := s.ListMemories(ctx, store.ListOpts{Limit: 100000})
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

func printUsage() {
	fmt.Printf(`cortex %s — Import-first memory layer for AI agents

Usage:
  cortex <command> [arguments]

Commands:
  import <path>       Import memory from a file or directory
  extract <file>      Extract facts from a single file (without importing)
  search <query>      Search memory (keyword by default, hybrid coming in Phase 2)
  stats               Show memory statistics and health
  list                List all memory entries
  export              Export memory in standard formats
  stale               Find outdated memory entries
  conflicts           Detect contradictory facts
  version             Print version

Search Flags:
  --mode <mode>       Search mode: keyword, semantic, hybrid (default: keyword)
  --limit <N>         Maximum results (default: 10)
  --min-confidence <F> Minimum confidence threshold (default: 0.0)
  --json              Force JSON output even in TTY

Import Flags:
  -r, --recursive     Recursively import from directories
  -n, --dry-run       Show what would be imported without writing
  --extract           Extract facts from imported memories and store them

Extract Flags:
  --json              Force JSON output even in TTY

Stats Flags:
  --json              Force JSON output even in TTY

Flags:
  -h, --help          Show this help message
  -v, --version       Print version

Documentation:
  https://github.com/hurttlocker/cortex
`, version)
}
