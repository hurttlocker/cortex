# PRD-005: CLI

**Status:** Draft  
**Priority:** P0  
**Phase:** 1  
**Depends On:** PRD-001 (Storage), PRD-002 (Import), PRD-003 (Extraction), PRD-004 (Search), PRD-006 (Observability)  
**Package:** `cmd/cortex/`

---

## Overview

The CLI is the primary interface to Cortex. Built with Cobra, it provides commands for importing, searching, listing, exporting, and observing memory. Output is formatted as clean tables for interactive use and JSON for piped/scripted use.

## Problem

Users need a fast, intuitive command-line interface that follows Unix conventions. The CLI must auto-detect whether output is going to a terminal (pretty tables, colors) or a pipe (JSON, no colors). It must be discoverable (`--help` is useful) and composable (works in shell scripts).

---

## Requirements

### Must Have (P0)

- **Commands**

  | Command | Description | Flags |
  |---------|-------------|-------|
  | `cortex import <path>` | Import memory from file or directory | `--recursive`, `--llm <provider/model>`, `--dry-run`, `--max-size <bytes>` |
  | `cortex search <query>` | Search memory | `--mode keyword\|semantic\|hybrid`, `--limit N`, `--lens <name>`, `--min-confidence F` |
  | `cortex list` | List all memory entries | `--sort date\|confidence\|recalls`, `--limit N`, `--type <fact_type>` |
  | `cortex export` | Export memory | `--format json\|markdown\|csv`, `--output <path>` |
  | `cortex stats` | Memory statistics | (none) |
  | `cortex stale` | Find outdated entries | `--days N`, `--min-confidence F` |
  | `cortex conflicts` | Detect contradictions | (none) |
  | `cortex version` | Print version | (none) |

- **Global flags**
  - `--db <path>` — override database path (default: `~/.cortex/cortex.db`)
  - `--verbose` / `-v` — enable verbose/debug output
  - `--json` — force JSON output (auto-detected when stdout is not a TTY)
  - `--no-color` — disable color output (also respects `NO_COLOR` env var)
  - `--help` / `-h` — command help (Cobra built-in)

- **Output formatting**
  - **TTY detected:** clean tables with aligned columns, color-coded confidence scores, human-readable sizes
  - **Non-TTY (piped):** JSON Lines (one JSON object per line) or full JSON array
  - **Detection:** use `os.IsTerminal(os.Stdout.Fd())` or `isatty` equivalent
  - **Color:** ANSI colors for TTY output, respect `NO_COLOR` env var

- **Import command behavior**
  - Accept file path or directory path
  - If directory: require `--recursive` flag (don't silently recurse)
  - Show progress: files processed, memories extracted, facts extracted
  - Show summary: `12 files imported, 847 memories created, 2,340 facts extracted`
  - With `--dry-run`: show what would be imported without writing to DB
  - With `--llm`: pass LLM config to extraction pipeline

- **Search command behavior**
  - Accept query as positional argument (rest of args joined)
  - Default mode: hybrid
  - Show results as a ranked list with score, snippet, source file, confidence
  - Example output (TTY):
    ```
    # Results for "deployment process" (hybrid, 5 results)
    
    1. [0.94] Project Alpha deployment pipeline          (MEMORY.md:42)
       "...the deployment process uses a blue-green strategy with..."
       Confidence: 0.87 ████████░░
    
    2. [0.82] CI/CD configuration                        (notes/devops.md:15)
       "...automated deployment triggered by merge to main..."
       Confidence: 0.95 █████████░
    ```

- **Error handling**
  - All errors go to stderr
  - Exit code 0 for success, 1 for errors
  - Error messages are clear and actionable:
    - ✅ `Error: database not found at ~/.cortex/cortex.db. Run 'cortex import' to create it.`
    - ❌ `Error: no such file or directory`

### Should Have (P1)

- **Progress bars** for long operations
  - Import: show file-level progress bar for directories
  - Embedding generation: show progress for batch embedding
  - Use a library like `cheggaaa/pb` or `schollz/progressbar`

- **Interactive stale/conflicts**
  - `cortex stale`: show stale facts, prompt `[r]einforce / [d]elete / [s]kip` for each
  - `cortex conflicts`: show conflicts, prompt `[m]erge / [k]eep both / [d]elete one`
  - Skip interactive prompts when stdout is not a TTY (just list)

- **Shell completion**
  - Bash, Zsh, Fish completions via Cobra's built-in support
  - `cortex completion bash > /etc/bash_completion.d/cortex`

### Future (P2)

- **`cortex serve`** — start MCP server or HTTP API
- **`cortex dashboard`** — open web dashboard
- **`cortex config`** — manage `~/.cortex/config.yaml`
- **`cortex lens`** — manage memory lenses
- **`cortex diff`** — show memory changes over time

---

## Technical Design

### Command Structure (Cobra)

```go
package main

import (
    "github.com/spf13/cobra"
)

var (
    dbPath  string
    verbose bool
    jsonOut bool
    noColor bool
)

func main() {
    root := &cobra.Command{
        Use:   "cortex",
        Short: "Import-first memory layer for AI agents",
        Long:  "Cortex is an import-first, zero-dependency, observable memory layer for AI agents.\nMemory that thinks like you do.",
    }
    
    // Global flags
    root.PersistentFlags().StringVar(&dbPath, "db", "", "database path (default: ~/.cortex/cortex.db)")
    root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
    root.PersistentFlags().BoolVar(&jsonOut, "json", false, "JSON output")
    root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")
    
    // Register commands
    root.AddCommand(
        importCmd(),
        searchCmd(),
        listCmd(),
        exportCmd(),
        statsCmd(),
        staleCmd(),
        conflictsCmd(),
        versionCmd(),
    )
    
    if err := root.Execute(); err != nil {
        os.Exit(1)
    }
}
```

### Import Command

```go
func importCmd() *cobra.Command {
    var (
        recursive bool
        llm       string
        dryRun    bool
        maxSize   int64
    )
    
    cmd := &cobra.Command{
        Use:   "import <path>",
        Short: "Import memory from a file or directory",
        Long:  "Parse and ingest memory from files you already have.\nSupports: Markdown, JSON, YAML, CSV, plain text.",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            path := args[0]
            
            // 1. Resolve database path
            // 2. Open store
            // 3. Create import engine
            // 4. Configure LLM if --llm provided
            // 5. If directory: require --recursive, call ImportDir
            // 6. If file: call ImportFile
            // 7. Print summary
            
            return nil
        },
    }
    
    cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recursively import directory")
    cmd.Flags().StringVar(&llm, "llm", "", "LLM for extraction (e.g., ollama/gemma2:2b)")
    cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be imported")
    cmd.Flags().Int64Var(&maxSize, "max-size", 10*1024*1024, "max file size in bytes")
    
    return cmd
}
```

### Search Command

```go
func searchCmd() *cobra.Command {
    var (
        mode          string
        limit         int
        lens          string
        minConfidence float64
    )
    
    cmd := &cobra.Command{
        Use:   "search <query>",
        Short: "Search memory",
        Long:  "Search your memory store with keyword, semantic, or hybrid search.\nDefault: hybrid (BM25 + semantic via Reciprocal Rank Fusion).",
        Args:  cobra.MinimumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            query := strings.Join(args, " ")
            
            // 1. Open store + search engine
            // 2. Build search options from flags
            // 3. Execute search
            // 4. Format and display results
            
            return nil
        },
    }
    
    cmd.Flags().StringVar(&mode, "mode", "hybrid", "search mode: keyword, semantic, hybrid")
    cmd.Flags().IntVar(&limit, "limit", 20, "max results")
    cmd.Flags().StringVar(&lens, "lens", "", "memory lens to apply")
    cmd.Flags().Float64Var(&minConfidence, "min-confidence", 0.1, "minimum confidence threshold")
    
    return cmd
}
```

### Database Path Resolution

```go
func resolveDBPath() string {
    // Priority: CLI flag > env var > default
    if dbPath != "" {
        return dbPath
    }
    if envPath := os.Getenv("CORTEX_DB"); envPath != "" {
        return envPath
    }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".cortex", "cortex.db")
}
```

### Output Formatting

```go
// isTerminal returns true if stdout is a terminal.
func isTerminal() bool {
    if jsonOut {
        return false // --json flag overrides
    }
    fi, _ := os.Stdout.Stat()
    return (fi.Mode() & os.ModeCharDevice) != 0
}

// useColor returns true if color output is appropriate.
func useColor() bool {
    if noColor || os.Getenv("NO_COLOR") != "" {
        return false
    }
    return isTerminal()
}

// printResults formats search results for the appropriate output.
func printResults(results []search.Result, query string) {
    if !isTerminal() {
        // JSON output
        json.NewEncoder(os.Stdout).Encode(results)
        return
    }
    
    // Pretty table output
    fmt.Printf("# Results for %q (%d found)\n\n", query, len(results))
    for i, r := range results {
        fmt.Printf("%d. [%.2f] %s\n", i+1, r.Score, truncate(r.Memory.Content, 60))
        if r.Memory.SourceFile != "" {
            fmt.Printf("   (%s:%d)\n", r.Memory.SourceFile, r.Memory.SourceLine)
        }
        if r.Snippet != "" {
            fmt.Printf("   %q\n", r.Snippet)
        }
        if r.EffectiveConfidence > 0 {
            fmt.Printf("   Confidence: %.2f %s\n", r.EffectiveConfidence, confidenceBar(r.EffectiveConfidence))
        }
        fmt.Println()
    }
}
```

### Version Command

```go
var (
    Version   = "dev"     // Set by goreleaser
    Commit    = "none"    // Set by goreleaser
    BuildDate = "unknown" // Set by goreleaser
)

func versionCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "version",
        Short: "Print version information",
        Run: func(cmd *cobra.Command, args []string) {
            fmt.Printf("cortex %s\n", Version)
            if verbose {
                fmt.Printf("  commit:  %s\n", Commit)
                fmt.Printf("  built:   %s\n", BuildDate)
                fmt.Printf("  go:      %s\n", runtime.Version())
                fmt.Printf("  os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
            }
        },
    }
}
```

---

## Test Strategy

### Unit Tests

- **TestResolveDBPath_Flag** — `--db /tmp/test.db` returns `/tmp/test.db`
- **TestResolveDBPath_EnvVar** — `CORTEX_DB` env var used when no flag
- **TestResolveDBPath_Default** — returns `~/.cortex/cortex.db` when no flag or env
- **TestResolveDBPath_Priority** — flag > env > default
- **TestIsTerminal_WithFlag** — `--json` forces non-terminal
- **TestUseColor_NoColorEnv** — `NO_COLOR` disables color
- **TestUseColor_NoColorFlag** — `--no-color` disables color

### Integration Tests (CLI-level)

- **TestImportCommand_File** — `cortex import sample.md` creates memories
- **TestImportCommand_Directory** — `cortex import dir/ --recursive` walks tree
- **TestImportCommand_DryRun** — `--dry-run` shows plan without writing
- **TestImportCommand_MissingFile** — clear error message
- **TestImportCommand_DirWithoutRecursive** — error telling user to add `--recursive`
- **TestSearchCommand_Basic** — `cortex search "query"` returns results
- **TestSearchCommand_JSONOutput** — `cortex search --json "query"` returns valid JSON
- **TestSearchCommand_Modes** — `--mode keyword`, `--mode semantic`, `--mode hybrid`
- **TestListCommand** — `cortex list` shows entries
- **TestExportCommand_JSON** — `cortex export --format json` outputs valid JSON
- **TestExportCommand_Markdown** — `cortex export --format markdown` outputs valid Markdown
- **TestStatsCommand** — `cortex stats` shows statistics
- **TestVersionCommand** — `cortex version` prints version string
- **TestVersionCommand_Verbose** — `cortex version -v` shows commit and build info

### CLI Test Helper

```go
func runCLI(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
    t.Helper()
    // Create temp DB, set CORTEX_DB, execute command, capture output
}
```

---

## Open Questions

1. **Cobra vs. Kong:** Cobra is the standard but Kong has nicer syntax. Stick with Cobra? (Current decision: yes, ecosystem is larger)
2. **Table library:** Use `tablewriter`, `lipgloss`, or custom formatting?
3. **Config file format:** YAML or TOML for `~/.cortex/config.yaml`? (YAML matches existing decision)
4. **Signal handling:** Should we handle SIGINT gracefully during long imports? (Yes — commit what we have, report partial results)
