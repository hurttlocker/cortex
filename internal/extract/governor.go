package extract

import (
	"regexp"
	"sort"
	"strings"
)

// GovernorConfig controls fact quality filtering and caps.
type GovernorConfig struct {
	// MaxFactsPerMemory caps the number of facts kept per memory unit.
	// Facts are ranked by quality score; lowest-quality facts are dropped.
	// 0 means unlimited (default: 50).
	MaxFactsPerMemory int

	// MinObjectLength is the minimum length for a fact's Object field.
	// Facts with shorter objects are dropped as noise. Default: 2.
	MinObjectLength int

	// MinPredicateLength is the minimum length for a fact's Predicate field.
	// Default: 2.
	MinPredicateLength int

	// DropMarkdownJunk removes facts whose objects are pure markdown
	// formatting artifacts (e.g., "**", "---", "```"). Default: true.
	DropMarkdownJunk bool

	// DropGenericSubjects removes facts with subjects that carry no signal
	// (e.g., "Conversation Summary", empty subjects). Default: true.
	DropGenericSubjects bool
}

// DefaultGovernorConfig returns the recommended default governor settings.
// Tightened Feb 2026 after 537K kv garbage analysis: old config produced 250 facts/memory.
func DefaultGovernorConfig() GovernorConfig {
	return GovernorConfig{
		MaxFactsPerMemory:   20,
		MinObjectLength:     2,
		MinPredicateLength:  4,
		DropMarkdownJunk:    true,
		DropGenericSubjects: true,
	}
}

// AutoCaptureGovernorConfig returns stricter settings for conversation captures.
// Auto-capture text is noisy by nature — only high-signal facts survive.
func AutoCaptureGovernorConfig() GovernorConfig {
	return GovernorConfig{
		MaxFactsPerMemory:   15,
		MinObjectLength:     3,
		MinPredicateLength:  3,
		DropMarkdownJunk:    true,
		DropGenericSubjects: true,
	}
}

// Governor filters and ranks extracted facts to enforce quality standards.
type Governor struct {
	config GovernorConfig
}

// NewGovernor creates a Governor with the given config.
func NewGovernor(cfg GovernorConfig) *Governor {
	return &Governor{config: cfg}
}

// Apply runs all quality filters and caps on a batch of extracted facts.
// Returns the filtered, ranked facts.
func (g *Governor) Apply(facts []ExtractedFact) []ExtractedFact {
	if len(facts) == 0 {
		return facts
	}

	// Phase 1: Drop garbage facts
	filtered := make([]ExtractedFact, 0, len(facts))
	for _, f := range facts {
		if g.isNoise(f) {
			continue
		}
		filtered = append(filtered, f)
	}

	// Phase 2: Deduplicate (already handled upstream, but belt-and-suspenders)
	filtered = deduplicateByTriple(filtered)

	// Phase 3: Score and rank
	scored := make([]scoredFact, 0, len(filtered))
	for _, f := range filtered {
		scored = append(scored, scoredFact{
			fact:  f,
			score: qualityScore(f),
		})
	}

	// Sort by quality score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Phase 4: Cap
	cap := g.config.MaxFactsPerMemory
	if cap > 0 && len(scored) > cap {
		scored = scored[:cap]
	}

	// Extract back to facts slice
	result := make([]ExtractedFact, 0, len(scored))
	for _, s := range scored {
		result = append(result, s.fact)
	}

	return result
}

type scoredFact struct {
	fact  ExtractedFact
	score float64
}

// isNoise returns true if the fact should be dropped as garbage.
func (g *Governor) isNoise(f ExtractedFact) bool {
	obj := strings.TrimSpace(f.Object)
	pred := strings.TrimSpace(f.Predicate)
	subj := strings.TrimSpace(f.Subject)

	// Empty fields
	if obj == "" || pred == "" {
		return true
	}

	// Minimum length checks
	if g.config.MinObjectLength > 0 && len(obj) < g.config.MinObjectLength {
		return true
	}
	if g.config.MinPredicateLength > 0 && len(pred) < g.config.MinPredicateLength {
		return true
	}

	// Markdown junk: objects that are pure formatting artifacts
	if g.config.DropMarkdownJunk && isMarkdownJunk(obj) {
		return true
	}

	// Markdown junk in predicate
	if g.config.DropMarkdownJunk && isMarkdownJunk(pred) {
		return true
	}

	// Generic/meaningless subjects
	if g.config.DropGenericSubjects && isGenericSubject(subj) {
		return true
	}

	// Object is just a repetition of the predicate (circular fact)
	if strings.EqualFold(obj, pred) {
		return true
	}

	// Predicate is just a number or single punctuation
	if isNumericOrPunct(pred) {
		return true
	}

	// Object is just markdown bold stars or similar
	if isOnlyFormatting(obj) {
		return true
	}

	// --- v0.9.0 noise filters (Feb 2026) ---

	predLower := strings.ToLower(pred)
	objLower := strings.ToLower(obj)
	subjLower := strings.ToLower(subj)

	// Generic regex-extracted predicates that aren't real facts.
	// Note: "email" and "phone" are kept — they're valid in KV pairs like "Email: test@example.com"
	noisePredicates := map[string]bool{
		"amount": true, "url": true, "date": true, "value": true,
	}
	if noisePredicates[predLower] {
		return true
	}

	// URLs as objects (http links aren't facts — they're references)
	if strings.HasPrefix(objLower, "http://") || strings.HasPrefix(objLower, "https://") {
		return true
	}

	// URLs in predicates
	if strings.HasPrefix(predLower, "http://") || strings.HasPrefix(predLower, "https://") || strings.HasPrefix(predLower, "//") {
		return true
	}

	// Git hash predicates (e.g., "41f5d98 merge pr #5")
	if gitHashRE.MatchString(pred) {
		return true
	}

	// Numbered list items as subjects (e.g., "1307", "3)", "2BR")
	if len(subj) > 0 && subj[0] >= '0' && subj[0] <= '9' {
		return true
	}

	// Pipe-separated predicates (markdown table fragments)
	if strings.Contains(pred, "|") {
		return true
	}

	// Subjects with markdown blockquote markers
	if strings.HasPrefix(subjLower, "> ") || strings.HasPrefix(subjLower, "- ") {
		return true
	}

	// Predicate is just a markdown list marker with content (e.g., "- #68")
	if strings.HasPrefix(predLower, "- ") || strings.HasPrefix(predLower, "* ") {
		return true
	}

	// Subject is a bare markdown link reference
	if strings.HasPrefix(subjLower, "[") && strings.Contains(subjLower, "](") {
		return true
	}

	return false
}

// isMarkdownJunk detects objects/predicates that are pure markdown artifacts.
func isMarkdownJunk(s string) bool {
	stripped := strings.TrimSpace(s)
	if stripped == "" {
		return true
	}

	// Pure formatting tokens
	junk := []string{
		"**", "***", "---", "___", "```", "~~~",
		"|", "|-", "-|", "--|--", "|---|",
		"#", "##", "###", "####",
	}
	for _, j := range junk {
		if stripped == j {
			return true
		}
	}

	// All stars/dashes/pipes (table separators, horizontal rules)
	allJunkChars := true
	for _, r := range stripped {
		if r != '*' && r != '-' && r != '_' && r != '|' && r != ' ' && r != ':' {
			allJunkChars = false
			break
		}
	}
	if allJunkChars && len(stripped) > 0 {
		return true
	}

	return false
}

// gitHashRE matches predicates that are git commit hashes (6+ hex chars).
var gitHashRE = regexp.MustCompile(`^[0-9a-f]{6,}\b`)

// timestampSubjectRE matches subjects that are primarily timestamps or
// timestamp-prefixed section headers — strong signal of auto-capture noise.
var timestampSubjectRE = regexp.MustCompile(
	`^(?:` +
		`\d{4}-\d{2}-\d{2}` + // ISO date prefix
		`|\d{1,2}:\d{2}\s*(?:AM|PM)` + // Clock time prefix
		`)`,
)

// isGenericSubject detects subjects that carry no useful signal.
// Empty subjects are allowed (means source wasn't labeled, not necessarily noise).
// Generic labels, timestamp-prefixed headers, and overly long subjects are dropped.
func isGenericSubject(subj string) bool {
	trimmed := strings.TrimSpace(subj)
	lower := strings.ToLower(trimmed)

	// Empty subject is fine — just means no source_section or source_file.
	// The quality score penalizes it, but we don't hard-drop it.
	if lower == "" {
		return false
	}

	generic := []string{
		"conversation summary",
		"conversation capture",
		"conversation",
		"summary",
		"untitled",
		"unknown",
		"(unknown)",
		"none",
		"n/a",
		"assistant",
		"user",
		"system",
	}
	for _, g := range generic {
		if lower == g {
			return true
		}
	}

	// Subject that starts with "conversation" is likely auto-capture noise
	if strings.HasPrefix(lower, "conversation ") {
		return true
	}

	// Subject that starts with "send this to" is forwarding noise
	if strings.HasPrefix(lower, "send this to ") {
		return true
	}

	// Timestamp-prefixed subjects are section headers, not entities
	if timestampSubjectRE.MatchString(trimmed) {
		return true
	}

	// Subjects containing emoji section markers are usually headers, not entities
	if strings.ContainsAny(trimmed, "✅🚩📌🌙") && len(trimmed) > 30 {
		return true
	}

	// Very long subjects (>50 chars) are almost certainly not real entities.
	// Real entity names: "Q", "Cortex", "Spear", "SB", "ORB Strategy" — all short.
	if len(trimmed) > 50 {
		return true
	}

	return false
}

// isNumericOrPunct returns true if the string is purely numeric or punctuation.
func isNumericOrPunct(s string) bool {
	stripped := strings.TrimSpace(s)
	if stripped == "" {
		return true
	}
	for _, r := range stripped {
		if (r < '0' || r > '9') && r != '.' && r != ',' && r != '-' && r != '+' && r != '$' && r != '%' {
			return false
		}
	}
	return true
}

// isOnlyFormatting returns true if the string is only markdown formatting characters.
func isOnlyFormatting(s string) bool {
	stripped := strings.TrimSpace(s)
	if stripped == "" {
		return true
	}
	for _, r := range stripped {
		if r != '*' && r != '_' && r != '`' && r != '#' && r != '~' && r != ' ' {
			return false
		}
	}
	return true
}

// deduplicateByTriple removes facts with the same (subject, predicate, object) triple,
// keeping the one with highest confidence.
func deduplicateByTriple(facts []ExtractedFact) []ExtractedFact {
	type tripleKey struct {
		subject   string
		predicate string
		object    string
	}

	best := make(map[tripleKey]ExtractedFact)
	for _, f := range facts {
		key := tripleKey{
			subject:   strings.ToLower(strings.TrimSpace(f.Subject)),
			predicate: strings.ToLower(strings.TrimSpace(f.Predicate)),
			object:    strings.ToLower(strings.TrimSpace(f.Object)),
		}
		if existing, ok := best[key]; ok {
			if f.Confidence > existing.Confidence {
				best[key] = f
			}
		} else {
			best[key] = f
		}
	}

	result := make([]ExtractedFact, 0, len(best))
	for _, f := range best {
		result = append(result, f)
	}
	return result
}

// qualityScore assigns a 0-1 quality score to a fact for ranking.
// Higher = better quality, more likely to be kept when capping.
func qualityScore(f ExtractedFact) float64 {
	score := f.Confidence // Start with extraction confidence (0.0-1.0)

	// Boost non-KV types (they carry more semantic signal)
	switch f.FactType {
	case "identity":
		score += 0.15
	case "decision":
		score += 0.12
	case "relationship":
		score += 0.12
	case "preference":
		score += 0.10
	case "location":
		score += 0.08
	case "state":
		score += 0.05
	case "temporal":
		score += 0.02
	case "kv":
		// No boost — KV is baseline
	}

	// Penalize very short objects (less context)
	objLen := len(strings.TrimSpace(f.Object))
	if objLen < 5 {
		score -= 0.10
	} else if objLen < 10 {
		score -= 0.05
	}

	// Penalize empty or very short subjects
	subjLen := len(strings.TrimSpace(f.Subject))
	if subjLen == 0 {
		score -= 0.15
	} else if subjLen < 3 {
		score -= 0.08
	}

	// Penalize very short predicates
	predLen := len(strings.TrimSpace(f.Predicate))
	if predLen < 3 {
		score -= 0.08
	}

	// Boost LLM-extracted facts (generally higher quality)
	if f.ExtractionMethod == "llm" {
		score += 0.05
	}

	// Clamp to [0, 1]
	if score > 1.0 {
		score = 1.0
	}
	if score < 0.0 {
		score = 0.0
	}

	return score
}
