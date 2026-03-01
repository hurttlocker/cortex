package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	dbPathFlag := fs.String("db", "", "Path to demo SQLite DB (default: temp file)")
	demoDirFlag := fs.String("dir", "", "Directory for demo markdown files (default: temp dir)")
	cleanup := fs.Bool("cleanup", false, "Delete demo files/DB after completion")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("usage: cortex demo [--db <path>] [--dir <path>] [--cleanup]")
	}

	var err error
	demoDir := strings.TrimSpace(*demoDirFlag)
	if demoDir == "" {
		demoDir, err = os.MkdirTemp("", "cortex-demo-")
		if err != nil {
			return fmt.Errorf("creating temp demo directory: %w", err)
		}
	} else {
		demoDir = expandUserPath(demoDir)
		if err := os.MkdirAll(demoDir, 0o755); err != nil {
			return fmt.Errorf("creating demo directory: %w", err)
		}
	}

	dbPath := strings.TrimSpace(*dbPathFlag)
	if dbPath == "" {
		dbPath = filepath.Join(os.TempDir(), fmt.Sprintf("cortex-demo-%d.db", time.Now().UnixNano()))
	} else {
		dbPath = expandUserPath(dbPath)
	}

	files, err := createDemoMarkdownFiles(demoDir)
	if err != nil {
		return err
	}

	fmt.Println("🧪 Cortex demo")
	fmt.Printf("Demo files: %d markdown files in %s\n", len(files), demoDir)
	fmt.Printf("Demo DB:    %s\n\n", dbPath)

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	defer func() { globalDBPath = oldDBPath }()

	fmt.Println("Step 1/3: Import + extract sample knowledge")
	if err := runImport([]string{demoDir, "--recursive", "--extract", "--no-enrich", "--no-classify"}); err != nil {
		if *cleanup {
			_ = cleanupDemoArtifacts(demoDir, dbPath)
		}
		return fmt.Errorf("demo import failed: %w", err)
	}

	fmt.Println("\nStep 2/3: Run demo searches")
	fmt.Println("\nQuery: who is leading trading strategy?")
	if err := runSearch([]string{"who", "is", "leading", "trading", "strategy", "--limit", "5"}); err != nil {
		return fmt.Errorf("demo search failed: %w", err)
	}

	fmt.Println("\nQuery: what changed in onboarding?")
	if err := runSearch([]string{"what", "changed", "in", "onboarding", "--limit", "5"}); err != nil {
		return fmt.Errorf("demo search failed: %w", err)
	}

	fmt.Println("\nStep 3/3: Doctor check on demo DB")
	if err := runDoctor([]string{}); err != nil {
		return fmt.Errorf("demo doctor failed: %w", err)
	}

	fmt.Println("\n✅ Demo complete.")
	fmt.Println("Your turn:")
	fmt.Printf("  cortex --db %s search \"your question\"\n", dbPath)
	fmt.Printf("  cortex --db %s list --facts --limit 10\n", dbPath)
	fmt.Printf("  cortex --db %s stats\n", dbPath)
	if !*cleanup {
		fmt.Println("\nInspection paths (kept):")
		fmt.Printf("  files: %s\n", demoDir)
		fmt.Printf("  db:    %s\n", dbPath)
		fmt.Println("Use --cleanup to auto-delete these next run.")
	} else {
		if err := cleanupDemoArtifacts(demoDir, dbPath); err != nil {
			return fmt.Errorf("demo cleanup failed: %w", err)
		}
		fmt.Println("\nTemporary demo files cleaned up.")
	}

	return nil
}

func createDemoMarkdownFiles(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating demo dir: %w", err)
	}

	type fileDef struct {
		name    string
		content string
	}

	defs := []fileDef{
		{
			name: "team.md",
			content: `# Team

- Q leads product direction and strategy.
- Mister coordinates architecture and merges.
- x7 implements core features in Go and runs test sweeps.
`,
		},
		{
			name: "trading.md",
			content: `# Trading Ops

- ORB strategy is active on QQQ and SPY.
- Risk note: position size increases only after 30+ validated trades.
- Next review window: Monday pre-market checklist.
`,
		},
		{
			name: "cortex.md",
			content: `# Cortex

- v1.2.1 adoption sprint delivered init wizard, error UX, and version checks.
- Obsidian export supports dashboard, topics, entities, and trading journal.
- Lifecycle run now completes with optimized query plan.
`,
		},
		{
			name: "onboarding.md",
			content: `# Onboarding

- New-user flow: install -> init -> import --extract -> search -> doctor.
- Common pitfall: env vars override config values.
- First-win demo should complete in under 60 seconds.
`,
		},
	}

	files := make([]string, 0, len(defs))
	for _, d := range defs {
		path := filepath.Join(dir, d.name)
		if err := os.WriteFile(path, []byte(d.content), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", d.name, err)
		}
		files = append(files, path)
	}

	return files, nil
}

func cleanupDemoArtifacts(demoDir, dbPath string) error {
	_ = os.RemoveAll(demoDir)

	paths := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
