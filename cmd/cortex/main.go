package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/LavonTMCQ/cortex/internal/ingest"
	"github.com/LavonTMCQ/cortex/internal/store"
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
	case "search":
		fmt.Println("cortex search: not yet implemented")
		fmt.Println("Usage: cortex search <query> [--mode keyword|semantic|hybrid]")
	case "list":
		fmt.Println("cortex list: not yet implemented")
		fmt.Println("Usage: cortex list [--source <file>] [--since <date>]")
	case "export":
		fmt.Println("cortex export: not yet implemented")
		fmt.Println("Usage: cortex export [--format json|markdown]")
	case "stats":
		fmt.Println("cortex stats: not yet implemented")
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
		return fmt.Errorf("usage: cortex import <path> [--recursive] [--dry-run]")
	}

	// Parse flags
	var paths []string
	opts := ingest.ImportOptions{}

	for _, arg := range args {
		switch {
		case arg == "--recursive" || arg == "-r":
			opts.Recursive = true
		case arg == "--dry-run" || arg == "-n":
			opts.DryRun = true
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

	fmt.Println()
	fmt.Print(ingest.FormatImportResult(totalResult))
	return nil
}

func printUsage() {
	fmt.Printf(`cortex %s — Import-first memory layer for AI agents

Usage:
  cortex <command> [arguments]

Commands:
  import <path>       Import memory from a file or directory
  search <query>      Search memory (hybrid BM25 + semantic by default)
  list                List all memory entries
  export              Export memory in standard formats
  stats               Show memory statistics and health
  stale               Find outdated memory entries
  conflicts           Detect contradictory facts
  version             Print version

Import Flags:
  -r, --recursive     Recursively import from directories
  -n, --dry-run       Show what would be imported without writing

Flags:
  -h, --help          Show this help message
  -v, --version       Print version

Documentation:
  https://github.com/LavonTMCQ/cortex
`, version)
}
