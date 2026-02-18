package store

import (
	"path/filepath"
	"strings"
)

// DefaultProjectRules maps file path patterns to project names.
// Used by InferProject to auto-tag memories based on their source file.
var DefaultProjectRules = []ProjectRule{
	// Trading
	{Pattern: "trading", Project: "trading"},
	{Pattern: "mister-trading", Project: "trading"},
	{Pattern: "trading_journal", Project: "trading"},
	{Pattern: "crypto", Project: "trading"},
	{Pattern: "pre-market", Project: "trading"},

	// Eyes Web
	{Pattern: "mybeautifulwife", Project: "eyes-web"},
	{Pattern: "eyes-web", Project: "eyes-web"},

	// Spear
	{Pattern: "spear", Project: "spear"},
	{Pattern: "rustdesk", Project: "spear"},

	// Cortex (this repo)
	{Pattern: "cortex", Project: "cortex"},

	// Wedding
	{Pattern: "wedding", Project: "wedding"},

	// YouTube
	{Pattern: "youtube", Project: "youtube"},
	{Pattern: "mister_youtube", Project: "youtube"},
}

// ContentRule matches keywords in memory content to a project.
// MinHits is the minimum number of distinct keywords that must appear
// for the rule to fire (prevents false positives from single-word matches).
type ContentRule struct {
	Keywords []string // Lowercase keywords to look for in content
	MinHits  int      // Minimum distinct keyword matches required (default: 2)
	Project  string   // Project to assign when matched
}

// DefaultContentRules maps content keywords to project names.
// Requires multiple keyword hits to reduce false positives on mixed-topic daily notes.
var DefaultContentRules = []ContentRule{
	// Trading — needs 2+ of these terms
	{
		Keywords: []string{"qqq", "spy", "pre-market", "premarket", "orb ", "opening range",
			"puts", "calls", "options", "strike", "expiry", "0dte", "ema ",
			"vwap", "macd", "rsi ", "candle", "bearish", "bullish", "scalp",
			"trading session", "trading plan", "entry point", "stop loss",
			"take profit", "alpaca", "finnhub", "ada perp", "coinbase",
			"short position", "long position", "breakout", "breakdown"},
		MinHits: 2,
		Project: "trading",
	},
	// Wedding — needs 2+ of these terms
	{
		Keywords: []string{"wedding", "ceremony", "villa", "cabrera", "dominican republic",
			"guest list", "vendor", "catering", "photographer", "officiant",
			"reception", "bridal", "groom", "bridesmaid", "flower girl",
			"rehearsal dinner", "save the date", "rsvp", "seating chart",
			"honeymoon", "engagement", "flor de cabrera", "destination wedding"},
		MinHits: 2,
		Project: "wedding",
	},
	// Spear — needs 2+ of these terms
	{
		Keywords: []string{"spear restoration", "spear customer", "rustdesk", "device fleet",
			"customer ticket", "remote support", "spear agent", "customer ops",
			"paypal dispute", "mrr", "spear revenue", "repair request"},
		MinHits: 2,
		Project: "spear",
	},
	// Eyes Web — needs 2+ of these terms
	{
		Keywords: []string{"eyes web", "onboarding flow", "mybeautifulwife", "lulu",
			"user onboarding", "eyes app", "signup flow", "waitlist",
			"eyes platform"},
		MinHits: 2,
		Project: "eyes-web",
	},
	// YouTube — needs 2+ of these terms
	{
		Keywords: []string{"youtube video", "thumbnail", "youtube upload", "video render",
			"remotion", "mister youtube", "video pipeline", "tts audio",
			"video script", "pre-market video", "youtube channel"},
		MinHits: 2,
		Project: "youtube",
	},
	// Cortex — needs 2+ of these terms
	{
		Keywords: []string{"cortex search", "cortex import", "cortex stats", "cortex fact",
			"bm25", "fts5", "embedding", "hybrid search", "semantic search",
			"confidence decay", "ebbinghaus", "cortex reimport", "cortex mcp",
			"fact extraction", "cortex db", "goreleaser"},
		MinHits: 2,
		Project: "cortex",
	},
}

// ProjectRule maps a path substring to a project name.
type ProjectRule struct {
	Pattern string // Substring to match in file path (case-insensitive)
	Project string // Project to assign when matched
}

// InferProject attempts to determine the project from a source file path.
// Returns empty string if no rule matches.
func InferProject(sourceFile string, rules []ProjectRule) string {
	if sourceFile == "" {
		return ""
	}

	// Normalize: lowercase, forward slashes
	normalized := strings.ToLower(filepath.ToSlash(sourceFile))

	for _, rule := range rules {
		if strings.Contains(normalized, strings.ToLower(rule.Pattern)) {
			return rule.Project
		}
	}
	return ""
}

// InferProjectFromContent scans memory content for keyword clusters.
// Returns the project with the highest keyword hit count (above its MinHits threshold).
// Returns empty string if no rule fires.
func InferProjectFromContent(content string, rules []ContentRule) string {
	if content == "" {
		return nil_project
	}

	lower := strings.ToLower(content)

	bestProject := ""
	bestHits := 0

	for _, rule := range rules {
		minHits := rule.MinHits
		if minHits <= 0 {
			minHits = 2 // default
		}

		hits := 0
		for _, kw := range rule.Keywords {
			if strings.Contains(lower, kw) {
				hits++
			}
		}

		if hits >= minHits && hits > bestHits {
			bestHits = hits
			bestProject = rule.Project
		}
	}

	return bestProject
}

// nil_project is the empty string constant for readability.
const nil_project = ""

// InferProjectFull tries path-based rules first, then content-based rules.
// Path rules take priority since they're more precise.
func InferProjectFull(sourceFile, content string, pathRules []ProjectRule, contentRules []ContentRule) string {
	// Path rules first — more precise
	if p := InferProject(sourceFile, pathRules); p != "" {
		return p
	}
	// Content rules as fallback
	return InferProjectFromContent(content, contentRules)
}
