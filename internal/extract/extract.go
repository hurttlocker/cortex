// Package extract provides local NLP-based fact extraction for Cortex.
//
// The extraction pipeline identifies structured information from raw text
// without requiring an LLM or external API:
// - Key-value pairs ("preferred editor: vim")
// - Relationships ("Alice works at Acme")
// - Preferences ("prefers dark mode")
// - Temporal facts ("meeting on Tuesday")
//
// Each extracted fact links back to its source memory unit for full traceability.
package extract

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ExtractedFact represents a single structured fact extracted from text.
type ExtractedFact struct {
	Subject          string  `json:"subject"`           // Who/what this is about
	Predicate        string  `json:"predicate"`         // The relationship or attribute
	Object           string  `json:"object"`            // The value or related entity
	FactType         string  `json:"type"`              // kv, relationship, preference, temporal, identity, location, decision, state
	Confidence       float64 `json:"confidence"`        // 0.0–1.0
	SourceQuote      string  `json:"source_quote"`      // Exact text this was extracted from
	ExtractionMethod string  `json:"extraction_method"` // always "rules" for Tier 1
	DecayRate        float64 `json:"decay_rate"`        // Assigned based on fact type
}

// DecayRates maps fact types to their decay rates.
// Lower decay rate means slower forgetting (longer half-life).
var DecayRates = map[string]float64{
	"identity":     0.001, // half-life: 693 days
	"decision":     0.002, // half-life: 347 days
	"relationship": 0.003, // half-life: 231 days
	"location":     0.005, // half-life: 139 days
	"preference":   0.01,  // half-life: 69 days
	"state":        0.05,  // half-life: 14 days
	"temporal":     0.1,   // half-life: 7 days
	"kv":           0.01,  // half-life: 69 days (default)
}

// Pipeline orchestrates the extraction process using rule-based extraction
// and optional LLM-assisted extraction (Tier 2).
type Pipeline struct {
	kvPatterns    []*kvPattern
	regexPatterns []*regexPattern
	llmConfig     *LLMConfig // Optional LLM configuration
	llmClient     *LLMClient // Optional LLM client
	governor      *Governor  // Quality governor (filters + caps)
}

// kvPattern represents a key-value pattern to match.
type kvPattern struct {
	regex    *regexp.Regexp
	name     string
	priority int
}

// regexPattern represents a data type pattern to match.
type regexPattern struct {
	regex    *regexp.Regexp
	factType string
	name     string
}

// PipelineOption configures the extraction pipeline.
type PipelineOption func(*Pipeline)

// WithGovernor sets a custom governor config on the pipeline.
func WithGovernor(cfg GovernorConfig) PipelineOption {
	return func(p *Pipeline) {
		p.governor = NewGovernor(cfg)
	}
}

// NewPipeline creates a new extraction pipeline with all rule-based extractors
// and optional LLM-assisted extraction.
func NewPipeline(llmConfig ...*LLMConfig) *Pipeline {
	p := &Pipeline{
		kvPatterns:    initKVPatterns(),
		regexPatterns: initRegexPatterns(),
		governor:      NewGovernor(DefaultGovernorConfig()),
	}

	// Configure LLM if provided
	if len(llmConfig) > 0 && llmConfig[0] != nil {
		p.llmConfig = llmConfig[0]
		p.llmClient = NewLLMClient(llmConfig[0])
	}

	return p
}

// NewPipelineWithOptions creates a pipeline with functional options.
func NewPipelineWithOptions(opts ...PipelineOption) *Pipeline {
	p := &Pipeline{
		kvPatterns:    initKVPatterns(),
		regexPatterns: initRegexPatterns(),
		governor:      NewGovernor(DefaultGovernorConfig()),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// MaxSubjectLength caps subjects to prevent section-header-as-subject noise.
// Real entities (people, projects, tools) are short. Long subjects are almost
// always section headers from conversation captures.
const MaxSubjectLength = 50

// timestampPrefixRE matches common timestamp prefixes at the start of section
// headers, e.g. "11:27 PM ET — ...", "2026-02-20 16:28 ET — ...",
// "Conversation Capture — 2026-02-23T04:58:24.112Z > Assistant".
var timestampPrefixRE = regexp.MustCompile(
	`^(?:` +
		// ISO timestamps: "2026-02-20T16:28:00Z" or "2026-02-20 16:28 ET"
		`\d{4}-\d{2}-\d{2}(?:T\d{2}:\d{2}[^ ]*)?\s*(?:ET|UTC|PT|CT|MT)?\s*[—–-]\s*` +
		`|` +
		// Clock timestamps: "11:27 PM ET —"
		`\d{1,2}:\d{2}\s*(?:AM|PM)?\s*(?:ET|UTC|PT|CT|MT)?\s*[—–-]\s*` +
		`|` +
		// "Conversation Capture — ..."
		`(?i)conversation\s+capture\s*[—–-]\s*[^\s]*\s*>\s*` +
		`)`,
)

// sectionTrailRE matches trailing " > SubSection" fragments from nested headers.
var sectionTrailRE = regexp.MustCompile(`\s*>\s*[^>]+$`)

// parenthesizedTimeRE matches parenthesized time ranges like "(3:45-4:45 PM)", "(9:00 PM", "(Feb 11,".
var parenthesizedTimeRE = regexp.MustCompile(`\s*\(\d{1,2}[:\-]\d{2}[^)]*\)?`)

// parenthesizedDateRE matches parenthesized dates like "(Feb 11, 2026)", "(LOCKED 2026-02-10)".
var parenthesizedDateRE = regexp.MustCompile(`\s*\((?:LOCKED\s+)?\d{4}-\d{2}-\d{2}\)`)

// emojiPrefixRE matches leading emoji + space patterns like "🧠 ", "📊 ", "💥 ".
var emojiPrefixRE = regexp.MustCompile(`^[\x{1F300}-\x{1FAF8}\x{2600}-\x{27BF}\x{FE00}-\x{FE0F}\x{200D}]+\s*`)

// inferSubject derives a subject string from extraction metadata.
//
// For auto-capture sources (conversation transcripts), subjects are aggressively
// normalized to avoid section headers becoming fact subjects. The pipeline:
//  1. Strip timestamp prefixes ("11:27 PM ET — ...")
//  2. Strip "Conversation Capture" prefixes
//  3. Strip trailing "> subsection" fragments
//  4. Cap length at MaxSubjectLength (truncate at last word boundary)
//  5. For auto-capture: fall back to filename stem if result is still long/noisy
//
// For structured documents (MEMORY.md, config), source_section is used as-is
// (capped at MaxSubjectLength).
func inferSubject(metadata map[string]string) string {
	isCapture := isAutoCaptureSource(metadata)

	if section, ok := metadata["source_section"]; ok && section != "" {
		subj := normalizeSubject(section, isCapture)
		if subj != "" {
			return subj
		}
	}
	if file, ok := metadata["source_file"]; ok && file != "" {
		// Strip directory and extension to get filename stem.
		base := file
		if idx := strings.LastIndexAny(base, "/\\"); idx >= 0 {
			base = base[idx+1:]
		}
		if idx := strings.LastIndex(base, "."); idx > 0 {
			base = base[:idx]
		}
		return truncateAtWordBoundary(base, MaxSubjectLength)
	}
	return ""
}

// normalizeSubject cleans a raw source_section into a usable fact subject.
func normalizeSubject(raw string, isCapture bool) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Strip timestamp prefixes
	s = timestampPrefixRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)

	// Strip trailing "> subsection" fragments
	s = sectionTrailRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)

	// Strip parenthesized time ranges and dates
	s = parenthesizedTimeRE.ReplaceAllString(s, "")
	s = parenthesizedDateRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)

	// Strip leading emoji
	s = emojiPrefixRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)

	// Strip leading markdown headers (## , ### , etc.)
	for strings.HasPrefix(s, "#") {
		s = strings.TrimLeft(s, "#")
		s = strings.TrimSpace(s)
	}

	// Strip common noise prefixes
	noisePrefixes := []string{
		"Send this to laptop agent next",
		"Conversation Summary",
		"Conversation Capture",
	}
	lower := strings.ToLower(s)
	for _, prefix := range noisePrefixes {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			s = strings.TrimSpace(s[len(prefix):])
			s = strings.TrimLeft(s, ">—–- ")
			s = strings.TrimSpace(s)
			break
		}
	}

	// If still empty after stripping, return empty
	if s == "" {
		return ""
	}

	// For auto-capture: if subject still looks like a long prose header, collapse it
	if isCapture && len(s) > MaxSubjectLength {
		// Try to extract just the first meaningful phrase (before any "—", ">", "(")
		for _, sep := range []string{" — ", " > ", " (", " – "} {
			if idx := strings.Index(s, sep); idx > 0 && idx <= MaxSubjectLength {
				s = s[:idx]
				break
			}
		}
	}

	return truncateAtWordBoundary(strings.TrimSpace(s), MaxSubjectLength)
}

// truncateAtWordBoundary truncates s to maxLen, cutting at the last space before maxLen.
func truncateAtWordBoundary(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find last space before maxLen
	cut := strings.LastIndex(s[:maxLen], " ")
	if cut <= 0 {
		cut = maxLen // No space found, hard truncate
	}
	return s[:cut]
}

func stripAutoCaptureScaffold(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if strings.HasPrefix(lower, "```") {
			if inFence {
				inFence = false
				continue
			}
			if len(out) > 0 {
				prev := strings.ToLower(strings.TrimSpace(out[len(out)-1]))
				if strings.Contains(prev, "untrusted metadata") {
					inFence = true
					continue
				}
			}
		}
		if inFence {
			continue
		}

		switch {
		case strings.HasPrefix(lower, "## conversation capture"):
			continue
		case strings.HasPrefix(lower, "channel:"):
			continue
		case strings.HasPrefix(lower, "### user"):
			continue
		case strings.HasPrefix(lower, "### assistant"):
			continue
		case strings.HasPrefix(lower, "current time:"):
			continue
		case strings.Contains(lower, "(untrusted metadata)"):
			continue
		}

		out = append(out, line)
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Extract runs extraction on the input text and returns structured facts.
// Uses both rule-based extraction (Tier 1) and optional LLM extraction (Tier 2).
// For auto-capture sources (conversation transcripts), a stricter governor is
// applied to suppress the high noise inherent in conversational text.
func (p *Pipeline) Extract(ctx context.Context, text string, metadata map[string]string) ([]ExtractedFact, error) {
	var facts []ExtractedFact
	isCapture := isAutoCaptureSource(metadata)

	if isCapture {
		text = stripAutoCaptureScaffold(text)
	}

	// Tier 1: Rule-based extraction
	// 1. Extract key-value patterns (with type inference)
	kvFacts := p.extractKeyValues(text, metadata)
	facts = append(facts, kvFacts...)

	// 2. Extract natural-language sentence patterns (preference/decision/relationship/state/location)
	naturalFacts := p.extractNaturalLanguagePatterns(text, metadata)
	facts = append(facts, naturalFacts...)

	// 3. Extract regex patterns (dates, emails, phones, URLs, money)
	regexFacts := p.extractRegexPatterns(text, metadata)
	facts = append(facts, regexFacts...)

	// 4. Assign decay rates and set extraction method for Tier 1 facts
	for i := range facts {
		facts[i].ExtractionMethod = "rules"
		if rate, ok := DecayRates[facts[i].FactType]; ok {
			facts[i].DecayRate = rate
		} else {
			facts[i].DecayRate = DecayRates["kv"] // default
		}
	}

	// Tier 2: LLM-assisted extraction (optional)
	var llmFacts []ExtractedFact
	if p.llmClient != nil {
		var err error
		llmFacts, err = p.extractWithLLM(ctx, text)
		if err != nil {
			// Log warning but don't fail - fall back to Tier 1 only
			fmt.Fprintf(os.Stderr, "Warning: LLM extraction failed, using rule-based extraction only: %v\n", err)
		}
	}

	// 4. Merge and deduplicate facts from both tiers
	allFacts := append(facts, llmFacts...)
	allFacts = deduplicateFacts(allFacts)

	// 5. Quality governor: filter noise, rank, and cap
	// Use stricter governor for auto-capture sources (conversation transcripts)
	gov := p.governor
	if isCapture && gov != nil {
		gov = NewGovernor(AutoCaptureGovernorConfig())
	}
	if gov != nil {
		allFacts = gov.Apply(allFacts)
	}

	return allFacts, nil
}

// initKVPatterns initializes key-value extraction patterns in priority order.
func initKVPatterns() []*kvPattern {
	patterns := []*kvPattern{
		// **Key:** Value (markdown bold with colon)
		{
			regex:    regexp.MustCompile(`(?m)^[-*•]\s+\*\*([^*:]+):\*\*\s*(.+)$`),
			name:     "bold_colon_bullet",
			priority: 1,
		},
		// **Key:** Value (markdown bold with colon, no bullet)
		{
			regex:    regexp.MustCompile(`(?m)^\*\*([^*:]+):\*\*\s*(.+)$`),
			name:     "bold_colon",
			priority: 2,
		},
		// - Key: Value (bullet with colon)
		{
			regex:    regexp.MustCompile(`(?m)^[-*•]\s+([^:]+):\s*(.+)$`),
			name:     "bullet_colon",
			priority: 3,
		},
		// Key: Value (simple colon, no bullet)
		{
			regex:    regexp.MustCompile(`(?m)^([^:]+):\s*(.+)$`),
			name:     "simple_colon",
			priority: 4,
		},
		// Key → Value (arrow)
		{
			regex:    regexp.MustCompile(`(?m)^([^→]+)\s*→\s*(.+)$`),
			name:     "arrow",
			priority: 5,
		},
		// Key = Value (equals)
		{
			regex:    regexp.MustCompile(`(?m)^([^=]+)\s*=\s*(.+)$`),
			name:     "equals",
			priority: 6,
		},
		// Key — Value (em dash)
		{
			regex:    regexp.MustCompile(`(?m)^([^—]+)\s*—\s*(.+)$`),
			name:     "em_dash",
			priority: 7,
		},
	}

	return patterns
}

// initRegexPatterns initializes regex patterns for common data types.
func initRegexPatterns() []*regexPattern {
	return []*regexPattern{
		// ISO 8601 dates (2026-01-15, 2026-01-15T10:30:00)
		{
			regex:    regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}(?:T\d{2}:\d{2}:\d{2}(?:\.\d{3})?(?:Z|[+-]\d{2}:\d{2})?)?)\b`),
			factType: "temporal",
			name:     "iso_date",
		},
		// Natural language dates (March 15, 2026; January 2024; May 2015)
		{
			regex:    regexp.MustCompile(`\b((?:January|February|March|April|May|June|July|August|September|October|November|December)(?:\s+\d{1,2})?(?:,?\s+\d{4})?)\b`),
			factType: "temporal",
			name:     "natural_date",
		},
		// Email addresses
		{
			regex:    regexp.MustCompile(`\b([a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,})\b`),
			factType: "identity",
			name:     "email",
		},
		// Phone numbers (US formats)
		{
			regex:    regexp.MustCompile(`\b(\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4})\b`),
			factType: "identity",
			name:     "phone_us",
		},
		// International phone numbers (+1-234-567-8900)
		{
			regex:    regexp.MustCompile(`\b(\+\d{1,3}[-.\s]?\(?\d{1,4}\)?[-.\s]?\d{1,4}[-.\s]?\d{1,4}[-.\s]?\d{0,9})\b`),
			factType: "identity",
			name:     "phone_intl",
		},
		// URLs (http, https)
		{
			regex:    regexp.MustCompile(`\b(https?://[^\s]+)\b`),
			factType: "kv",
			name:     "url",
		},
		// Money ($1,000, $18K, $1.5M)
		{
			regex:    regexp.MustCompile(`\$(\d+(?:\.\d+)?[KMB]|\d{1,3}(?:,\d{3})*(?:\.\d{2})?)\b`),
			factType: "kv",
			name:     "money",
		},
	}
}

// extractKeyValues finds key-value patterns in text.
func (p *Pipeline) extractKeyValues(text string, metadata map[string]string) []ExtractedFact {
	var facts []ExtractedFact
	lines := strings.Split(text, "\n")
	subject := inferSubject(metadata)
	autoCapture := isAutoCaptureSource(metadata)
	transcriptLike := isTranscriptLikeContent(text, metadata)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Try each pattern in priority order
		for _, pattern := range p.kvPatterns {
			matches := pattern.regex.FindStringSubmatch(line)
			if len(matches) >= 3 {
				key := strings.TrimSpace(matches[1])
				value := strings.TrimSpace(matches[2])

				// Skip if key or value is empty
				if key == "" || value == "" {
					continue
				}

				if shouldSkipKVExtraction(pattern.name, key, value, line, autoCapture, transcriptLike) {
					continue
				}

				// Clean up key (remove markdown formatting, etc.)
				key = cleanKey(key)

				factType := inferFactTypeFromKV(key, value, line)
				confidence := 0.9 // High confidence for explicit patterns
				if factType != "kv" {
					confidence = 0.88 // Slightly lower confidence for inferred semantic type
				}

				fact := ExtractedFact{
					Subject:     subject,
					Predicate:   key,
					Object:      value,
					FactType:    factType,
					Confidence:  confidence,
					SourceQuote: line,
				}

				facts = append(facts, fact)
				break // Only match first pattern per line
			}
		}
	}

	return facts
}

var (
	preferenceSentenceRE = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9'._ -]{0,80})\s+(prefers?|likes?|dislikes?|wants?)\s+(.+)$`)
	decisionSentenceRE   = regexp.MustCompile(`(?i)^\s*(?:we\s+)?(decided|decide|chose|choose|selected|select|approved)\s+(?:to\s+)?(.+)$`)
	engagedSentenceRE    = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9'._ -]{0,80})\s+is\s+engaged\s+to\s+([a-z][a-z0-9'._ -]{0,80})\s*$`)
	relatedSentenceRE    = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9'._ -]{0,80})\s+is\s+([a-z][a-z0-9'._ -]{0,80})'?s\s+(fianc[eé]e|manager|partner|spouse|wife|husband)\s*$`)
	stateSentenceRE      = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9'._ -]{0,80})\s+is\s+(running|active|inactive|idle|blocked|online|offline|down|up)(?:\s+on\s+port\s+(\d+))?(?:[.!])?\s*$`)
	locationSentenceRE   = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9'._ -]{0,80})\s+(?:is at|is in|located in|based in)\s+(.+?)(?:[.!])?\s*$`)
)

// extractNaturalLanguagePatterns finds facts expressed as plain sentences.
func (p *Pipeline) extractNaturalLanguagePatterns(text string, metadata map[string]string) []ExtractedFact {
	var facts []ExtractedFact
	subject := inferSubject(metadata)
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.Contains(line, ":") || strings.Contains(line, "=") || strings.Contains(line, "→") || strings.Contains(line, "—") {
			// Let explicit key/value patterns handle these.
			continue
		}

		if m := preferenceSentenceRE.FindStringSubmatch(line); len(m) == 4 {
			facts = append(facts, ExtractedFact{
				Subject:     strings.TrimSpace(m[1]),
				Predicate:   strings.ToLower(strings.TrimSpace(m[2])),
				Object:      trimFactObject(m[3]),
				FactType:    "preference",
				Confidence:  0.86,
				SourceQuote: line,
			})
			continue
		}

		if m := decisionSentenceRE.FindStringSubmatch(line); len(m) == 3 {
			decisionSubject := subject
			if decisionSubject == "" {
				decisionSubject = "decision"
			}
			facts = append(facts, ExtractedFact{
				Subject:     decisionSubject,
				Predicate:   strings.ToLower(strings.TrimSpace(m[1])),
				Object:      trimFactObject(m[2]),
				FactType:    "decision",
				Confidence:  0.84,
				SourceQuote: line,
			})
			continue
		}

		if m := engagedSentenceRE.FindStringSubmatch(line); len(m) == 3 {
			facts = append(facts, ExtractedFact{
				Subject:     strings.TrimSpace(m[1]),
				Predicate:   "engaged_to",
				Object:      strings.TrimSpace(m[2]),
				FactType:    "relationship",
				Confidence:  0.9,
				SourceQuote: line,
			})
			continue
		}

		if m := relatedSentenceRE.FindStringSubmatch(line); len(m) == 4 {
			facts = append(facts, ExtractedFact{
				Subject:     strings.TrimSpace(m[1]),
				Predicate:   strings.ToLower(strings.TrimSpace(m[3])),
				Object:      strings.TrimSpace(m[2]),
				FactType:    "relationship",
				Confidence:  0.88,
				SourceQuote: line,
			})
			continue
		}

		if m := stateSentenceRE.FindStringSubmatch(line); len(m) >= 3 {
			stateObj := strings.ToLower(strings.TrimSpace(m[2]))
			if len(m) >= 4 && strings.TrimSpace(m[3]) != "" {
				stateObj = stateObj + " on port " + strings.TrimSpace(m[3])
			}
			facts = append(facts, ExtractedFact{
				Subject:     strings.TrimSpace(m[1]),
				Predicate:   "status",
				Object:      stateObj,
				FactType:    "state",
				Confidence:  0.87,
				SourceQuote: line,
			})
			continue
		}

		if m := locationSentenceRE.FindStringSubmatch(line); len(m) == 3 {
			facts = append(facts, ExtractedFact{
				Subject:     strings.TrimSpace(m[1]),
				Predicate:   "location",
				Object:      trimFactObject(m[2]),
				FactType:    "location",
				Confidence:  0.86,
				SourceQuote: line,
			})
		}
	}

	return facts
}

func isAutoCaptureSource(metadata map[string]string) bool {
	if metadata == nil {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(metadata["source_file"]))
	if path == "" {
		return false
	}
	return strings.Contains(path, "auto-capture") || strings.Contains(path, "cortex-capture-")
}

var transcriptRoleLineRE = regexp.MustCompile(`(?im)^\s*(assistant|user|system)\s*:`)

func isTranscriptLikeContent(text string, metadata map[string]string) bool {
	if isAutoCaptureSource(metadata) {
		return true
	}

	lower := strings.ToLower(text)
	if strings.Contains(lower, "<cortex-memories>") ||
		strings.Contains(lower, "(untrusted metadata)") ||
		strings.Contains(lower, "conversation info (untrusted metadata)") ||
		strings.Contains(lower, "sender (untrusted metadata)") ||
		strings.Contains(lower, "[message_id:") ||
		strings.Contains(lower, "[queued messages while agent was busy]") {
		return true
	}

	if len(transcriptRoleLineRE.FindAllStringIndex(text, -1)) >= 2 {
		return true
	}

	return false
}

func normalizeKVKeyForFilter(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.Trim(key, "\"'`[]{}()")
	replacer := strings.NewReplacer("_", "", " ", "", "-", "")
	key = replacer.Replace(key)
	return key
}

func shouldSkipKVExtraction(patternName, key, value, sourceLine string, autoCapture, transcriptLike bool) bool {
	keyTrim := strings.TrimSpace(key)
	if keyTrim == "" || strings.TrimSpace(value) == "" {
		return true
	}

	if len(keyTrim) > 80 || strings.Count(keyTrim, " ") > 8 {
		return true
	}

	lineTrim := strings.TrimSpace(sourceLine)
	if strings.HasPrefix(lineTrim, "{") || strings.HasPrefix(lineTrim, "}") {
		return true
	}

	if strings.ContainsAny(keyTrim, "{}") {
		return true
	}

	k := normalizeKVKeyForFilter(keyTrim)
	if k == "" {
		return true
	}

	lowerLine := strings.ToLower(sourceLine)
	if strings.Contains(lowerLine, "untrusted metadata") || strings.Contains(lowerLine, "queued messages while agent was busy") {
		return true
	}

	if transcriptLike {
		// Transcript wrappers and role lines generate large amounts of low-signal KV noise.
		switch k {
		case "conversationlabel", "groupsubject", "groupchannel", "groupspace",
			"sender", "label", "username", "tag", "currenttime", "messageid",
			"assistant", "user", "system":
			return true
		}

		if strings.HasPrefix(strings.ToLower(lineTrim), "assistant:") ||
			strings.HasPrefix(strings.ToLower(lineTrim), "user:") ||
			strings.HasPrefix(strings.ToLower(lineTrim), "system:") {
			return true
		}

		// JSON metadata envelopes should not emit KV facts.
		if strings.HasPrefix(lineTrim, "\"") && strings.Contains(lineTrim, "\":") {
			return true
		}
	}

	if !autoCapture {
		return false
	}

	// In conversational auto-capture, only keep explicit markdown key-value styles.
	// Free-form separators (simple colon / arrow / em-dash / equals) are too noisy.
	switch patternName {
	case "bold_colon_bullet", "bold_colon", "bullet_colon":
		// allowed
	default:
		return true
	}

	// Auto-capture remains strict on name to avoid sender envelope bleed-through.
	if k == "name" {
		return true
	}

	return false
}

func inferFactTypeFromKV(key, value, sourceLine string) string {
	joined := strings.ToLower(strings.TrimSpace(key + " " + value + " " + sourceLine))
	keyLower := strings.ToLower(strings.TrimSpace(key))

	switch {
	case strings.Contains(joined, "prefers"), strings.Contains(joined, "preference"), strings.Contains(joined, "likes"), strings.Contains(joined, "dislikes"), strings.Contains(joined, "wants"), strings.Contains(keyLower, "favorite"):
		return "preference"
	case strings.Contains(joined, "decided"), strings.Contains(joined, "decision"), strings.Contains(joined, "chose"), strings.Contains(joined, "selected"), strings.Contains(joined, "approved"):
		return "decision"
	case strings.Contains(joined, "engaged to"), strings.Contains(joined, "married to"), strings.Contains(joined, "reports to"), strings.Contains(joined, "fiancée"), strings.Contains(joined, "fiancee"), strings.Contains(joined, "spouse"), strings.Contains(joined, "partner"), strings.Contains(joined, "manager"):
		return "relationship"
	case strings.Contains(keyLower, "status"), strings.Contains(keyLower, "state"), strings.Contains(joined, "running"), strings.Contains(joined, "blocked"), strings.Contains(joined, "idle"), strings.Contains(joined, "online"), strings.Contains(joined, "offline"):
		return "state"
	case strings.Contains(keyLower, "location"), strings.Contains(keyLower, "address"), strings.Contains(keyLower, "city"), strings.Contains(keyLower, "country"), strings.Contains(keyLower, "venue"), strings.Contains(joined, "located in"):
		return "location"
	default:
		return "kv"
	}
}

func trimFactObject(v string) string {
	out := strings.TrimSpace(v)
	out = strings.TrimRight(out, ".; ")
	return out
}

// extractRegexPatterns finds common data type patterns in text.
func (p *Pipeline) extractRegexPatterns(text string, metadata map[string]string) []ExtractedFact {
	var facts []ExtractedFact
	subject := inferSubject(metadata)

	for _, pattern := range p.regexPatterns {
		matches := pattern.regex.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				value := strings.TrimSpace(match[1])
				if value == "" {
					continue
				}

				// Create predicate based on pattern name and value
				predicate := inferPredicate(pattern.name, value)

				fact := ExtractedFact{
					Subject:     subject,
					Predicate:   predicate,
					Object:      value,
					FactType:    pattern.factType,
					Confidence:  0.7,      // Medium confidence for regex matches
					SourceQuote: match[0], // Full match as source quote
				}

				facts = append(facts, fact)
			}
		}
	}

	return facts
}

// cleanKey removes markdown formatting and normalizes key names.
func cleanKey(key string) string {
	// Remove markdown bold/italic formatting
	key = strings.ReplaceAll(key, "**", "")
	key = strings.ReplaceAll(key, "*", "")
	key = strings.ReplaceAll(key, "_", "")

	// Normalize whitespace
	key = strings.TrimSpace(key)

	// Convert to lowercase for consistency
	key = strings.ToLower(key)

	return key
}

// inferPredicate creates a meaningful predicate from the pattern type and value.
func inferPredicate(patternName, value string) string {
	switch patternName {
	case "iso_date", "natural_date":
		// Try to infer if it's a birthday, deadline, meeting, etc.
		return "date"
	case "email":
		return "email"
	case "phone_us", "phone_intl":
		return "phone"
	case "url":
		return "url"
	case "money":
		// Try to infer context (price, salary, budget, etc.)
		return "amount"
	default:
		return "value"
	}
}

// deduplicateFacts removes duplicate facts within the same extraction run.
func deduplicateFacts(facts []ExtractedFact) []ExtractedFact {
	seen := make(map[string]bool)
	var unique []ExtractedFact

	for _, fact := range facts {
		// Create a key based on subject, predicate, and object
		key := fact.Subject + "|" + fact.Predicate + "|" + fact.Object
		key = strings.ToLower(strings.TrimSpace(key))

		if !seen[key] {
			seen[key] = true
			unique = append(unique, fact)
		}
	}

	return unique
}

// normalizeWhitespace normalizes whitespace in text.
func normalizeWhitespace(text string) string {
	// Replace multiple whitespace with single space
	re := regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

// isAlpha returns true if the string contains only letters.
func isAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return len(s) > 0
}

// parseInteger safely parses an integer from a string.
func parseInteger(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}

	val, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}

	return val, true
}

// extractWithLLM runs LLM-assisted extraction on the input text.
func (p *Pipeline) extractWithLLM(ctx context.Context, text string) ([]ExtractedFact, error) {
	if p.llmClient == nil || p.llmConfig == nil {
		return nil, fmt.Errorf("LLM client not configured")
	}

	// Chunk the document if it's too large
	chunks := ChunkDocument(text, p.llmConfig.ContextWindow)

	var allFacts []ExtractedFact

	for _, chunk := range chunks {
		// Skip empty or whitespace-only chunks
		if strings.TrimSpace(chunk) == "" {
			continue
		}

		facts, err := p.llmClient.Extract(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("extracting from chunk: %w", err)
		}

		allFacts = append(allFacts, facts...)
	}

	// Deduplicate facts across chunks
	allFacts = deduplicateFacts(allFacts)

	return allFacts, nil
}
