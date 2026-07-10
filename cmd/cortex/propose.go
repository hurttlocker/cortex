package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
)

// runPropose dispatches the `cortex propose <subcommand>` group — the proposer
// loop (v2 M3). Scanning surfaces recurring ledger fix patterns as candidate
// directives; only an explicit `accept` ever writes a directive.
func runPropose(args []string) error {
	if len(args) < 1 {
		return proposeUsageErr()
	}

	switch args[0] {
	case "scan":
		return runProposeScan(args[1:])
	case "list", "ls":
		return runProposeList(args[1:])
	case "accept":
		return runProposeAccept(args[1:])
	case "dismiss":
		return runProposeDismiss(args[1:])
	case "help", "--help", "-h":
		fmt.Println(proposeUsageText())
		return nil
	default:
		return fmt.Errorf("unknown propose subcommand %q\n\n%s", args[0], proposeUsageText())
	}
}

func proposeUsageText() string {
	return strings.TrimSpace(`Usage: cortex propose <command>

Commands:
  scan [--min-occurrences 3] [--window 14d] [--dry-run]   Scan the ledger for recurring
                                                          fix patterns and record candidate
                                                          proposals (--dry-run persists nothing)
  list [--all] [--json]                                   List proposals (pending by default)
  accept <id>                                             Accept a proposal → creates a directive
  dismiss <id>                                            Dismiss a proposal (writes no directive)`)
}

func proposeUsageErr() error {
	return fmt.Errorf("%s", proposeUsageText())
}

// openProposeStore opens the store as a *SQLiteStore, which the proposal API
// lives on (mirrors openLedgerStore).
func openProposeStore() (*store.SQLiteStore, func(), error) {
	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return nil, nil, fmt.Errorf("opening store: %w", err)
	}
	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		s.Close()
		return nil, nil, fmt.Errorf("propose requires SQLiteStore")
	}
	return sqlStore, func() { s.Close() }, nil
}

func runProposeScan(args []string) error {
	windowFlag := "14d"
	minOccur := store.DefaultProposalMinOccurrences
	dryRun := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--window" && i+1 < len(args):
			i++
			windowFlag = args[i]
		case strings.HasPrefix(args[i], "--window="):
			windowFlag = strings.TrimPrefix(args[i], "--window=")
		case args[i] == "--min-occurrences" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("invalid --min-occurrences value %q", args[i])
			}
			minOccur = n
		case strings.HasPrefix(args[i], "--min-occurrences="):
			n, err := strconv.Atoi(strings.TrimPrefix(args[i], "--min-occurrences="))
			if err != nil {
				return fmt.Errorf("invalid --min-occurrences value")
			}
			minOccur = n
		case args[i] == "--dry-run":
			dryRun = true
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	if minOccur < 1 {
		return fmt.Errorf("--min-occurrences must be at least 1")
	}

	window, err := parseSinceDuration(windowFlag)
	if err != nil {
		return fmt.Errorf("invalid --window value: %w", err)
	}

	sqlStore, closeStore, err := openProposeStore()
	if err != nil {
		return err
	}
	defer closeStore()

	res, err := sqlStore.ScanForProposals(context.Background(), store.ScanOptions{
		Window:         window,
		MinOccurrences: minOccur,
		DryRun:         dryRun,
	})
	if err != nil {
		return err
	}

	if dryRun {
		if len(res.Candidates) == 0 {
			fmt.Printf("Dry run: no fix pattern recurred ≥ %d× in the last %s. Nothing to propose.\n", minOccur, windowFlag)
			return nil
		}
		fmt.Printf("Dry run (persisted nothing): %d candidate proposal(s) — patterns recurring ≥ %d× in %s\n\n", len(res.Candidates), minOccur, windowFlag)
		for _, c := range res.Candidates {
			fmt.Printf("  · %s  (seen %d×)\n", c.PatternKey, c.Occurrences)
		}
		return nil
	}

	if len(res.Created) == 0 {
		if len(res.SkippedExisting) > 0 {
			fmt.Printf("No new proposals: %d recurring pattern(s) already have an overlapping proposal.\n", len(res.SkippedExisting))
			return nil
		}
		fmt.Printf("No new proposals: no fix pattern recurred ≥ %d× in the last %s.\n", minOccur, windowFlag)
		return nil
	}

	fmt.Printf("Recorded %d proposal(s):\n\n", len(res.Created))
	for _, c := range res.Created {
		fmt.Printf("  [%d] %s  (seen %d×)\n", c.ID, c.PatternKey, c.Occurrences)
	}
	if len(res.SkippedExisting) > 0 {
		fmt.Printf("\n(%d recurring pattern(s) skipped — already proposed.)\n", len(res.SkippedExisting))
	}
	fmt.Printf("\nReview with `cortex propose list`, then `cortex propose accept <id>` or `dismiss <id>`.\n")
	return nil
}

func runProposeList(args []string) error {
	opts := store.ProposalListOpts{}
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--all":
			opts.Status = "all"
		case args[i] == "--json":
			jsonOutput = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	sqlStore, closeStore, err := openProposeStore()
	if err != nil {
		return err
	}
	defer closeStore()

	proposals, err := sqlStore.ListProposals(context.Background(), opts)
	if err != nil {
		return err
	}

	if jsonOutput || !isTTY() {
		if proposals == nil {
			proposals = []*store.DirectiveProposal{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(proposals)
	}

	if len(proposals) == 0 {
		fmt.Println("No proposals found. Run `cortex propose scan` to look for recurring fix patterns.")
		return nil
	}

	for _, p := range proposals {
		marker := "○"
		switch p.Status {
		case store.ProposalStatusPending:
			marker = "●"
		case store.ProposalStatusAccepted:
			marker = "✓"
		case store.ProposalStatusDismissed:
			marker = "✗"
		}
		fmt.Printf("%s [%d] %s\n", marker, p.ID, p.CandidateRule)
		meta := fmt.Sprintf("%s · seen %d× · %s", p.Status, p.Occurrences, p.WindowStart.Format("2006-01-02"))
		if p.WindowEnd.After(p.WindowStart) {
			meta += "→" + p.WindowEnd.Format("2006-01-02")
		}
		if p.CreatedDirectiveID != nil {
			meta += fmt.Sprintf(" · directive #%d", *p.CreatedDirectiveID)
		}
		fmt.Printf("      %s\n", meta)
	}
	fmt.Printf("\n%d proposal(s)\n", len(proposals))
	return nil
}

func runProposeAccept(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex propose accept <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid proposal id %q", args[0])
	}

	sqlStore, closeStore, err := openProposeStore()
	if err != nil {
		return err
	}
	defer closeStore()

	directiveID, err := sqlStore.AcceptProposal(context.Background(), id)
	if err != nil {
		return fmt.Errorf("accepting proposal %d: %w", id, err)
	}
	fmt.Printf("Accepted proposal %d → created directive %d\n", id, directiveID)
	return nil
}

func runProposeDismiss(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex propose dismiss <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid proposal id %q", args[0])
	}

	sqlStore, closeStore, err := openProposeStore()
	if err != nil {
		return err
	}
	defer closeStore()

	if err := sqlStore.DismissProposal(context.Background(), id); err != nil {
		return fmt.Errorf("dismissing proposal %d: %w", id, err)
	}
	fmt.Printf("Dismissed proposal %d (no directive written)\n", id)
	return nil
}
