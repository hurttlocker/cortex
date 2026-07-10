package search

import (
	"path/filepath"
	"strings"
)

// citationTitleMaxRuleChars caps how much of a directive's rule text is used
// as a citation title (Result.Content for a directive Kind is the full rule).
const citationTitleMaxRuleChars = 80

// CitationTitleForResult derives a short, human-readable title for a citation
// purely from fields already present on Result — no LLM call, no schema
// change. First non-empty branch wins:
//
//  1. Directive results (Kind == "directive"): the rule text itself, since a
//     directive's SourceSection holds its governance scope (e.g. "global"),
//     not a section header worth surfacing as a title. Checked first so scope
//     never masks the rule.
//  2. The memory's SourceSection (a section header, when the memory was
//     imported with one).
//  3. The base filename of SourceFile (not the full path).
//
// Returns "" when nothing usable is available — callers should fall back to
// their existing locator string so a Citation's Title is never empty.
func CitationTitleForResult(r Result) string {
	if r.Kind == "directive" {
		if rule := strings.TrimSpace(r.Content); rule != "" {
			return truncateCitationTitle(rule, citationTitleMaxRuleChars)
		}
	}
	if section := strings.TrimSpace(r.SourceSection); section != "" {
		return section
	}
	if file := strings.TrimSpace(r.SourceFile); file != "" {
		return filepath.Base(file)
	}
	return ""
}

func truncateCitationTitle(s string, max int) string {
	s = strings.TrimSpace(s)
	// Truncate on runes, not bytes — a byte slice can split a UTF-8 codepoint.
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return strings.TrimSpace(string(r[:max-3])) + "..."
}
