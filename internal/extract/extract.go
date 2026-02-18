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

// NewPipeline creates a new extraction pipeline with all rule-based extractors
// and optional LLM-assisted extraction.
func NewPipeline(llmConfig ...*LLMConfig) *Pipeline {
	p := &Pipeline{
		kvPatterns:    initKVPatterns(),
		regexPatterns: initRegexPatterns(),
	}

	// Configure LLM if provided
	if len(llmConfig) > 0 && llmConfig[0] != nil {
		p.llmConfig = llmConfig[0]
		p.llmClient = NewLLMClient(llmConfig[0])
	}

	return p
}

// inferSubject derives a subject string from extraction metadata.
// First choice: source_section (e.g. "Wedding Planning > Vendor Contacts").
// Second choice: filename stem from source_file (e.g. "2026-02-17").
// Returns "" when neither is available.
func inferSubject(metadata map[string]string) string {
	if section, ok := metadata["source_section"]; ok && section != "" {
		return section
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
		return base
	}
	return ""
}

// Extract runs extraction on the input text and returns structured facts.
// Uses both rule-based extraction (Tier 1) and optional LLM extraction (Tier 2).
func (p *Pipeline) Extract(ctx context.Context, text string, metadata map[string]string) ([]ExtractedFact, error) {
	var facts []ExtractedFact

	// Tier 1: Rule-based extraction
	// 1. Extract key-value patterns
	kvFacts := p.extractKeyValues(text, metadata)
	facts = append(facts, kvFacts...)

	// 2. Extract regex patterns (dates, emails, phones, URLs, money)
	regexFacts := p.extractRegexPatterns(text, metadata)
	facts = append(facts, regexFacts...)

	// 3. Assign decay rates and set extraction method for Tier 1 facts
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
			// TODO: Consider using a logger interface instead of stderr
			fmt.Fprintf(os.Stderr, "Warning: LLM extraction failed, using rule-based extraction only: %v\n", err)
		}
	}

	// 4. Merge and deduplicate facts from both tiers
	allFacts := append(facts, llmFacts...)
	allFacts = deduplicateFacts(allFacts)

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

				// Clean up key (remove markdown formatting, etc.)
				key = cleanKey(key)

				fact := ExtractedFact{
					Subject:     subject,
					Predicate:   key,
					Object:      value,
					FactType:    "kv",
					Confidence:  0.9, // High confidence for explicit patterns
					SourceQuote: line,
				}

				facts = append(facts, fact)
				break // Only match first pattern per line
			}
		}
	}

	return facts
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
