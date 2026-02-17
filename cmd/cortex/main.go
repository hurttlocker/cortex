package main

import (
	"fmt"
	"os"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "import":
		fmt.Println("cortex import: not yet implemented")
		fmt.Println("Usage: cortex import <path> [--recursive]")
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

Flags:
  -h, --help          Show this help message
  -v, --version       Print version

Documentation:
  https://github.com/LavonTMCQ/cortex
`, version)
}
