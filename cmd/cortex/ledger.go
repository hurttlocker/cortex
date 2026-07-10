package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

func runLedger(args []string) error {
	if len(args) < 1 {
		fmt.Println(`Usage: cortex ledger <subcommand>

Subcommands:
  record   Append a session outcome row (end-of-task)
  list     List recorded session outcomes`)
		return nil
	}

	switch args[0] {
	case "record":
		return runLedgerRecord(args[1:])
	case "list":
		return runLedgerList(args[1:])
	default:
		return fmt.Errorf("unknown ledger subcommand: %s", args[0])
	}
}

func openLedgerStore() (*store.SQLiteStore, func(), error) {
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return nil, nil, fmt.Errorf("opening store: %w", err)
	}
	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		s.Close()
		return nil, nil, fmt.Errorf("ledger requires SQLiteStore")
	}
	return sqlStore, func() { s.Close() }, nil
}

func runLedgerRecord(args []string) error {
	var summary, outcome, filesFlag, pattern, sessionID, agentID, project string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--summary" && i+1 < len(args):
			i++
			summary = args[i]
		case strings.HasPrefix(args[i], "--summary="):
			summary = strings.TrimPrefix(args[i], "--summary=")
		case args[i] == "--outcome" && i+1 < len(args):
			i++
			outcome = args[i]
		case strings.HasPrefix(args[i], "--outcome="):
			outcome = strings.TrimPrefix(args[i], "--outcome=")
		case args[i] == "--files" && i+1 < len(args):
			i++
			filesFlag = args[i]
		case strings.HasPrefix(args[i], "--files="):
			filesFlag = strings.TrimPrefix(args[i], "--files=")
		case args[i] == "--pattern" && i+1 < len(args):
			i++
			pattern = args[i]
		case strings.HasPrefix(args[i], "--pattern="):
			pattern = strings.TrimPrefix(args[i], "--pattern=")
		case args[i] == "--session" && i+1 < len(args):
			i++
			sessionID = args[i]
		case strings.HasPrefix(args[i], "--session="):
			sessionID = strings.TrimPrefix(args[i], "--session=")
		case args[i] == "--agent" && i+1 < len(args):
			i++
			agentID = args[i]
		case strings.HasPrefix(args[i], "--agent="):
			agentID = strings.TrimPrefix(args[i], "--agent=")
		case args[i] == "--project" && i+1 < len(args):
			i++
			project = args[i]
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	if strings.TrimSpace(summary) == "" || strings.TrimSpace(outcome) == "" {
		return fmt.Errorf(`usage: cortex ledger record --summary "<s>" --outcome success|partial|failure [--files a.go,b.go] [--pattern "<fix pattern>"] [--session S] [--agent A] [--project P]`)
	}

	var files []string
	for _, f := range strings.Split(filesFlag, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			files = append(files, f)
		}
	}

	sqlStore, closeStore, err := openLedgerStore()
	if err != nil {
		return err
	}
	defer closeStore()

	entry := &store.LedgerEntry{
		SessionID:    sessionID,
		TaskSummary:  summary,
		Outcome:      outcome,
		FilesTouched: files,
		FixPattern:   pattern,
		AgentID:      agentID,
		Project:      project,
	}

	id, err := sqlStore.RecordLedgerEntry(context.Background(), entry)
	if err != nil {
		return err
	}

	fmt.Printf("Recorded session ledger entry #%d (%s)\n", id, entry.Outcome)
	return nil
}

func runLedgerList(args []string) error {
	sinceFlag := ""
	project := ""
	jsonOutput := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--since" && i+1 < len(args):
			i++
			sinceFlag = args[i]
		case strings.HasPrefix(args[i], "--since="):
			sinceFlag = strings.TrimPrefix(args[i], "--since=")
		case args[i] == "--project" && i+1 < len(args):
			i++
			project = args[i]
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--json":
			jsonOutput = true
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	var since time.Time
	if sinceFlag != "" {
		d, err := parseSinceDuration(sinceFlag)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
		since = time.Now().UTC().Add(-d)
	}

	sqlStore, closeStore, err := openLedgerStore()
	if err != nil {
		return err
	}
	defer closeStore()

	entries, err := sqlStore.ListLedgerEntries(context.Background(), since, project, 0)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	if len(entries) == 0 {
		fmt.Println("No session ledger entries found.")
		return nil
	}

	fmt.Printf("%-6s  %-16s  %-8s  %-14s  %s\n", "ID", "CREATED", "OUTCOME", "AGENT", "SUMMARY")
	fmt.Println(strings.Repeat("─", 80))
	for _, e := range entries {
		fmt.Printf("%-6d  %-16s  %-8s  %-14s  %s\n",
			e.ID, e.CreatedAt.Format("2006-01-02 15:04"), e.Outcome, e.AgentID, truncateString(e.TaskSummary, 60))
	}
	return nil
}

// parseSinceDuration parses Nd/Nw/Nh window strings (e.g. "14d", "2w", "12h"),
// falling back to Go's stdlib duration syntax for other forms (e.g. "90m").
func parseSinceDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	unit := s[len(s)-1]
	numPart := s[:len(s)-1]
	switch unit {
	case 'd', 'D':
		n, err := strconv.ParseFloat(numPart, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	case 'w', 'W':
		n, err := strconv.ParseFloat(numPart, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid week count %q", s)
		}
		return time.Duration(n * float64(7*24*time.Hour)), nil
	}

	return time.ParseDuration(s)
}
