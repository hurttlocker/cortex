// Package extract — LLM-powered fact enrichment for the import pipeline.
//
// EnrichFacts takes rule-extracted facts + the original chunk and asks an LLM
// to find what the rules missed: decision reasoning, implicit relationships,
// confidence calibration, and overlooked facts.
//
// This is additive-only — rule facts are never removed or modified.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/llm"
)

const (
	// enrichTimeout is the maximum time for a single enrichment call.
	// OpenRouter models (Grok, DeepSeek) can take 20-30s; generous headroom.
	enrichTimeout = 3 * time.Minute

	// enrichMaxChunkLen caps the chunk text sent to the LLM.
	enrichMaxChunkLen = 3000

	// enrichMaxRuleFacts caps how many rule facts are included in the prompt.
	enrichMaxRuleFacts = 30

	// DefaultEnrichModel is the recommended model for enrichment (best at finding new facts).
	// Benchmarked Feb 2026: Grok 4.1 Fast found +26 facts across 3 files; all others found ≤9.
	DefaultEnrichModel = "openrouter/x-ai/grok-4.1-fast"
)

const enrichSystemPrompt = `You are a fact enrichment system for a personal knowledge base. You receive a text chunk AND a list of facts already extracted by rule-based parsing.

Your job is to find what the rules MISSED:

1. **Decision reasoning**: WHY was something decided? ("locked ORB config" → because IEX volume filter was the problem)
2. **Implicit relationships**: Connections between entities not stated as "X is Y" ("SB needs this for Eyes Web" → SB ↔ Eyes Web ↔ health)
3. **Confidence calibration**: Tentative statements should get lower confidence ("we might try X" → 0.4) vs definitive ("X is locked" → 0.9)
4. **Missed facts**: Important information the rules skipped entirely

RULES:
- ONLY extract facts NOT already in the "existing facts" list
- Never duplicate or rephrase an existing fact
- Each fact must have a source_quote that is EXACT text from the input
- Use confidence 0.0-1.0 based on how clearly/definitively stated
- If there's nothing new to add, return an empty array
- Keep subjects short (≤50 chars) and normalized (proper nouns, no section headers)

FACT TYPES: kv, relationship, preference, temporal, identity, location, decision, state

Return ONLY a JSON object matching this schema:
{
  "facts": [
    {
      "subject": "entity name",
      "predicate": "relationship or attribute",
      "object": "value or related entity",
      "type": "one of the valid types",
      "confidence": 0.85,
      "source_quote": "exact text from the chunk"
    }
  ],
  "reasoning": "brief explanation of what you found that rules missed"
}`

// EnrichResult holds the output from LLM enrichment.
type EnrichResult struct {
	NewFacts  []ExtractedFact // Facts the LLM found that rules missed
	Reasoning string          // LLM's explanation of what it found
	Latency   time.Duration   // Time taken for LLM call
	Model     string          // Model used
}

// enrichResponse is the JSON schema the LLM returns.
type enrichResponse struct {
	Facts     []enrichFact `json:"facts"`
	Reasoning string       `json:"reasoning"`
}

type enrichFact struct {
	Subject     string  `json:"subject"`
	Predicate   string  `json:"predicate"`
	Object      string  `json:"object"`
	FactType    string  `json:"type"`
	Confidence  float64 `json:"confidence"`
	SourceQuote string  `json:"source_quote"`
}

// EnrichFacts uses an LLM to find facts that rule-based extraction missed.
// It is additive-only: ruleFacts are never modified or removed.
// On LLM error, returns nil (graceful fallback to rule-only).
func EnrichFacts(ctx context.Context, provider llm.Provider, chunk string, ruleFacts []ExtractedFact) (*EnrichResult, error) {
	if provider == nil {
		return nil, fmt.Errorf("LLM provider is nil")
	}

	start := time.Now()

	// Truncate chunk if too long
	truncatedChunk := chunk
	if len(truncatedChunk) > enrichMaxChunkLen {
		truncatedChunk = truncateAtWordBoundary(truncatedChunk, enrichMaxChunkLen)
	}

	// Build the user prompt with existing facts context
	prompt := buildEnrichPrompt(truncatedChunk, ruleFacts)

	// Call LLM with timeout
	enrichCtx, cancel := context.WithTimeout(ctx, enrichTimeout)
	defer cancel()

	response, err := provider.Complete(enrichCtx, prompt, llm.CompletionOpts{
		Temperature: 0.1,
		MaxTokens:   1024,
		System:      enrichSystemPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM enrichment call failed: %w", err)
	}

	// Parse response
	parsed, err := parseEnrichResponse(response)
	if err != nil {
		return nil, fmt.Errorf("parsing enrichment response: %w", err)
	}

	// Convert to ExtractedFact and validate
	newFacts := make([]ExtractedFact, 0, len(parsed.Facts))
	for _, f := range parsed.Facts {
		ef := ExtractedFact{
			Subject:          normalizeSubject(f.Subject, false),
			Predicate:        strings.TrimSpace(f.Predicate),
			Object:           trimFactObject(f.Object),
			FactType:         f.FactType,
			Confidence:       f.Confidence,
			SourceQuote:      f.SourceQuote,
			ExtractionMethod: "llm-enrich",
		}

		// Validate
		if ef.Predicate == "" || ef.Object == "" {
			continue
		}
		if !isValidFactType(ef.FactType) {
			ef.FactType = "kv" // fallback
		}
		if ef.Confidence <= 0 || ef.Confidence > 1.0 {
			ef.Confidence = 0.7 // reasonable default
		}

		// Assign decay rate
		if rate, ok := DecayRates[ef.FactType]; ok {
			ef.DecayRate = rate
		} else {
			ef.DecayRate = DecayRates["kv"]
		}

		// Deduplicate against existing rule facts
		if isDuplicateOfRuleFact(ef, ruleFacts) {
			continue
		}

		// Subject length check
		if len(ef.Subject) > MaxSubjectLength {
			ef.Subject = truncateAtWordBoundary(ef.Subject, MaxSubjectLength)
		}

		newFacts = append(newFacts, ef)
	}

	return &EnrichResult{
		NewFacts:  newFacts,
		Reasoning: parsed.Reasoning,
		Latency:   time.Since(start),
		Model:     provider.Name(),
	}, nil
}

// buildEnrichPrompt constructs the user message with chunk + existing facts.
func buildEnrichPrompt(chunk string, ruleFacts []ExtractedFact) string {
	var sb strings.Builder

	sb.WriteString("TEXT CHUNK:\n---\n")
	sb.WriteString(chunk)
	sb.WriteString("\n---\n\n")

	if len(ruleFacts) > 0 {
		sb.WriteString("EXISTING FACTS (already extracted by rules — do NOT duplicate these):\n")
		limit := len(ruleFacts)
		if limit > enrichMaxRuleFacts {
			limit = enrichMaxRuleFacts
		}
		for i := 0; i < limit; i++ {
			f := ruleFacts[i]
			sb.WriteString(fmt.Sprintf("- [%s] %s → %s → %s (%.1f)\n",
				f.FactType, f.Subject, f.Predicate, f.Object, f.Confidence))
		}
		if len(ruleFacts) > enrichMaxRuleFacts {
			sb.WriteString(fmt.Sprintf("... and %d more facts\n", len(ruleFacts)-enrichMaxRuleFacts))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("EXISTING FACTS: none (rules found nothing — look carefully for any facts)\n\n")
	}

	sb.WriteString("Find additional facts the rules missed. Return JSON only.")
	return sb.String()
}

// parseEnrichResponse parses the LLM's JSON (with markdown stripping).
func parseEnrichResponse(raw string) (*enrichResponse, error) {
	cleaned := strings.TrimSpace(raw)

	// Strip markdown code fences if present
	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		// Find opening and closing fence
		start, end := 0, len(lines)
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				if start == 0 {
					start = i + 1
				} else {
					end = i
					break
				}
			}
		}
		if start > 0 && end > start {
			cleaned = strings.Join(lines[start:end], "\n")
		}
	}

	cleaned = strings.TrimSpace(cleaned)

	var resp enrichResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON from LLM: %w\nraw response: %s", err, truncateForError(raw, 300))
	}

	return &resp, nil
}

// isDuplicateOfRuleFact checks if an LLM-extracted fact is a near-duplicate
// of an existing rule-extracted fact (fuzzy match on subject+predicate+object).
func isDuplicateOfRuleFact(candidate ExtractedFact, ruleFacts []ExtractedFact) bool {
	candidateSubj := strings.ToLower(strings.TrimSpace(candidate.Subject))
	candidatePred := strings.ToLower(strings.TrimSpace(candidate.Predicate))
	candidateObj := strings.ToLower(strings.TrimSpace(candidate.Object))

	for _, rf := range ruleFacts {
		ruleSubj := strings.ToLower(strings.TrimSpace(rf.Subject))
		rulePred := strings.ToLower(strings.TrimSpace(rf.Predicate))
		ruleObj := strings.ToLower(strings.TrimSpace(rf.Object))

		// Exact match on all three
		if candidateSubj == ruleSubj && candidatePred == rulePred && candidateObj == ruleObj {
			return true
		}

		// Subject + predicate match with contained object (e.g., "lives in NYC" vs "lives in New York City")
		if candidateSubj == ruleSubj && candidatePred == rulePred {
			if strings.Contains(candidateObj, ruleObj) || strings.Contains(ruleObj, candidateObj) {
				return true
			}
		}

		// Same subject, similar predicate+object (catches rephrases)
		if candidateSubj == ruleSubj {
			if (strings.Contains(candidatePred, rulePred) || strings.Contains(rulePred, candidatePred)) &&
				(strings.Contains(candidateObj, ruleObj) || strings.Contains(ruleObj, candidateObj)) {
				return true
			}
		}
	}

	return false
}

// isValidFactType checks if a fact type is one of the recognized types.
func isValidFactType(ft string) bool {
	switch ft {
	case "kv", "relationship", "preference", "temporal", "identity", "location", "decision", "state", "config":
		return true
	default:
		return false
	}
}

// truncateForError truncates a string for error messages.
func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
