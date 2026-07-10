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

// runDirective dispatches the `cortex directive <subcommand>` group — the CLI
// surface for the v2 governance layer (explicit, human-authored rules).
func runDirective(args []string) error {
	if len(args) == 0 {
		return directiveUsageErr()
	}

	switch args[0] {
	case "add":
		return runDirectiveAdd(args[1:])
	case "list", "ls":
		return runDirectiveList(args[1:])
	case "edit":
		return runDirectiveEdit(args[1:])
	case "archive":
		return runDirectiveArchive(args[1:])
	case "rm", "delete":
		return runDirectiveRemove(args[1:])
	case "help", "--help", "-h":
		printDirectiveUsage()
		return nil
	default:
		return fmt.Errorf("unknown directive subcommand %q\n\n%s", args[0], directiveUsageText())
	}
}

func directiveUsageText() string {
	return strings.TrimSpace(`Usage: cortex directive <command>

Commands:
  add "<rule>" [--scope S] [--author A]   Add a governance directive
  list [--all|--archived] [--scope S] [--json]   List directives (active by default)
  edit <id> [--rule "<text>"] [--scope S]   Edit a directive's rule and/or scope
  archive <id>                             Archive a directive (drops from retrieval)
  rm <id>                                  Permanently delete a directive`)
}

func printDirectiveUsage() {
	fmt.Println(directiveUsageText())
}

func directiveUsageErr() error {
	return fmt.Errorf("%s", directiveUsageText())
}

func runDirectiveAdd(args []string) error {
	var rule, scope, author string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--scope" && i+1 < len(args):
			i++
			scope = args[i]
		case strings.HasPrefix(args[i], "--scope="):
			scope = strings.TrimPrefix(args[i], "--scope=")
		case args[i] == "--author" && i+1 < len(args):
			i++
			author = args[i]
		case strings.HasPrefix(args[i], "--author="):
			author = strings.TrimPrefix(args[i], "--author=")
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			if rule != "" {
				return fmt.Errorf("unexpected argument: %s (quote the rule text)", args[i])
			}
			rule = args[i]
		}
	}
	if strings.TrimSpace(rule) == "" {
		return fmt.Errorf(`usage: cortex directive add "<rule>" [--scope S] [--author A]`)
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	id, err := s.AddDirective(ctx, &store.Directive{Rule: rule, Scope: scope, Author: author})
	if err != nil {
		return fmt.Errorf("adding directive: %w", err)
	}

	created, err := s.GetDirective(ctx, id)
	if err != nil {
		return fmt.Errorf("reading directive %d: %w", id, err)
	}
	fmt.Printf("Added directive %d (scope: %s)\n", created.ID, created.Scope)
	return nil
}

func runDirectiveList(args []string) error {
	opts := store.DirectiveListOpts{}
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--all":
			opts.Status = "all"
		case args[i] == "--archived":
			opts.Status = store.DirectiveStatusArchived
		case args[i] == "--scope" && i+1 < len(args):
			i++
			opts.Scope = args[i]
		case strings.HasPrefix(args[i], "--scope="):
			opts.Scope = strings.TrimPrefix(args[i], "--scope=")
		case args[i] == "--json":
			jsonOutput = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	directives, err := s.ListDirectives(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing directives: %w", err)
	}

	if jsonOutput || !isTTY() {
		if directives == nil {
			directives = []*store.Directive{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(directives)
	}

	if len(directives) == 0 {
		fmt.Println("No directives found.")
		return nil
	}

	for _, d := range directives {
		marker := "●"
		if d.Status == store.DirectiveStatusArchived {
			marker = "○"
		}
		fmt.Printf("%s [%d] (%s) %s\n", marker, d.ID, d.Scope, d.Rule)
		meta := d.Status
		if d.Author != "" {
			meta += " · " + d.Author
		}
		meta += " · " + d.CreatedAt.Format("2006-01-02")
		fmt.Printf("      %s\n", meta)
	}
	fmt.Printf("\n%d directive(s)\n", len(directives))
	return nil
}

func runDirectiveEdit(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`usage: cortex directive edit <id> [--rule "<text>"] [--scope S]`)
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid directive id %q", args[0])
	}

	var upd store.DirectiveUpdate
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--rule" && i+1 < len(rest):
			i++
			v := rest[i]
			upd.Rule = &v
		case strings.HasPrefix(rest[i], "--rule="):
			v := strings.TrimPrefix(rest[i], "--rule=")
			upd.Rule = &v
		case rest[i] == "--scope" && i+1 < len(rest):
			i++
			v := rest[i]
			upd.Scope = &v
		case strings.HasPrefix(rest[i], "--scope="):
			v := strings.TrimPrefix(rest[i], "--scope=")
			upd.Scope = &v
		default:
			return fmt.Errorf("unknown argument: %s", rest[i])
		}
	}
	if upd.Rule == nil && upd.Scope == nil {
		return fmt.Errorf("nothing to edit: provide --rule and/or --scope")
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.UpdateDirective(ctx, id, upd); err != nil {
		return fmt.Errorf("editing directive %d: %w", id, err)
	}
	fmt.Printf("Updated directive %d\n", id)
	return nil
}

func runDirectiveArchive(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex directive archive <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid directive id %q", args[0])
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.ArchiveDirective(ctx, id); err != nil {
		return fmt.Errorf("archiving directive %d: %w", id, err)
	}
	fmt.Printf("Archived directive %d\n", id)
	return nil
}

func runDirectiveRemove(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cortex directive rm <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid directive id %q", args[0])
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.DeleteDirective(ctx, id); err != nil {
		return fmt.Errorf("deleting directive %d: %w", id, err)
	}
	fmt.Printf("Deleted directive %d\n", id)
	return nil
}
