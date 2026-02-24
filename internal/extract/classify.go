// Package extract — LLM-powered fact classification for Cortex.
//
// ClassifyFacts reclassifies facts from the generic "kv" bucket into
// proper semantic types (decision, preference, identity, relationship, etc.)
// using batch LLM processing.
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
	// classifyTimeout is the max time for a single batch classification call.
	classifyTimeout = 30 * time.Second

	// DefaultClassifyBatchSize is the default number of facts per LLM batch.
	DefaultClassifyBatchSize = 50

	// classifyMinConfidence is the threshold below which reclassifications are skipped.
	classifyMinConfidence = 0.8
)

const classifySystemPrompt = `You are a fact classification system for a personal knowledge base. Each fact has a subject, predicate, and object. Your job is to assign the most accurate TYPE to each fact.

AVAILABLE TYPES:
- decision: Choices, commitments, locked configs ("Q locked ORB config", "decided to use Alpaca")
- preference: Likes, dislikes, style choices ("prefers dark mode", "dislikes tedious UI debugging")
- identity: Personal identifiers, credentials, roles ("email is alice@test.com", "Q's DOB is 12/25/1994")
- relationship: Connections between entities ("Niot works on Eyes Web", "SB is co-founder")
- temporal: Time-bound facts, deadlines, expiry dates ("eBay token expires 2027-07-28", "meeting on Tuesday")
- state: Current conditions, statuses ("ADA bot is LIVE", "disk usage at 92%")
- location: Geographic or path references ("lives in Philadelphia", "binary at ~/bin/cortex")
- config: Technical settings, parameters ("model is Haiku 4.5", "port 8090")
- kv: Generic key-value when no better type fits (LAST RESORT only)

RULES:
- Classify based on the SEMANTIC MEANING, not just keyword matching
- Use "decision" when someone CHOSE or LOCKED something (even if it looks like config)
- Use "relationship" when two entities are connected (even if stated as key-value)
- Use "temporal" when a date/time is the key information
- Only use "kv" when genuinely no other type fits
- Return confidence 0.0-1.0 for each classification

Return ONLY a JSON object:
{
  "classifications": [
    {"id": 123, "type": "decision", "confidence": 0.9},
    {"id": 456, "type": "relationship", "confidence": 0.85}
  ]
}`

// FactClassification holds the result of reclassifying a single fact.
type FactClassification struct {
	FactID     int64   `json:"id"`
	OldType    string  `json:"old_type"`
	NewType    string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// ClassifyResult holds the output from a batch classification run.
type ClassifyResult struct {
	Classified  []FactClassification // Facts that were reclassified
	Unchanged   int                  // Facts where type didn't change or confidence too low
	Errors      int                  // Facts where classification failed
	TotalFacts  int                  // Total facts processed
	Latency     time.Duration        // Total time
	Model       string               // Model used
	BatchCount  int                  // Number of LLM batches
}

// ClassifyOpts configures the classification run.
type ClassifyOpts struct {
	BatchSize     int     // Facts per LLM batch (default: 50)
	MinConfidence float64 // Min confidence to apply reclassification (default: 0.8)
	Limit         int     // Max facts to process (0 = all)
	DryRun        bool    // Show changes without applying
}

// DefaultClassifyOpts returns sensible defaults.
func DefaultClassifyOpts() ClassifyOpts {
	return ClassifyOpts{
		BatchSize:     DefaultClassifyBatchSize,
		MinConfidence: classifyMinConfidence,
	}
}

// classifyResponse is the JSON the LLM returns.
type classifyResponse struct {
	Classifications []classifyEntry `json:"classifications"`
}

type classifyEntry struct {
	ID         int64   `json:"id"`
	FactType   string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// ClassifyableFact is a minimal fact struct for classification input.
type ClassifyableFact struct {
	ID        int64
	Subject   string
	Predicate string
	Object    string
	FactType  string
}

// ClassifyFacts reclassifies facts using an LLM. Only processes facts
// currently typed as "kv" unless the input already contains mixed types.
// Returns classifications where the new type differs and confidence >= threshold.
func ClassifyFacts(ctx context.Context, provider llm.Provider, facts []ClassifyableFact, opts ClassifyOpts) (*ClassifyResult, error) {
	if provider == nil {
		return nil, fmt.Errorf("LLM provider is nil")
	}
	if len(facts) == 0 {
		return &ClassifyResult{}, nil
	}

	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultClassifyBatchSize
	}
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = classifyMinConfidence
	}

	start := time.Now()
	result := &ClassifyResult{
		TotalFacts: len(facts),
		Model:      provider.Name(),
	}

	// Process in batches
	for i := 0; i < len(facts); i += opts.BatchSize {
		end := i + opts.BatchSize
		if end > len(facts) {
			end = len(facts)
		}
		batch := facts[i:end]

		classifications, err := classifyBatch(ctx, provider, batch)
		if err != nil {
			result.Errors += len(batch)
			continue
		}

		result.BatchCount++

		// Build lookup for this batch
		factMap := make(map[int64]*ClassifyableFact, len(batch))
		for idx := range batch {
			factMap[batch[idx].ID] = &batch[idx]
		}

		for _, c := range classifications {
			original, ok := factMap[c.ID]
			if !ok {
				continue // LLM returned an ID not in the batch
			}

			// Skip if type didn't change
			if c.FactType == original.FactType {
				result.Unchanged++
				continue
			}

			// Skip invalid types
			if !isValidFactType(c.FactType) && c.FactType != "config" {
				result.Errors++
				continue
			}

			// Skip low confidence
			if c.Confidence < opts.MinConfidence {
				result.Unchanged++
				continue
			}

			result.Classified = append(result.Classified, FactClassification{
				FactID:     c.ID,
				OldType:    original.FactType,
				NewType:    c.FactType,
				Confidence: c.Confidence,
			})
		}

		// Facts in batch not returned by LLM
		returned := make(map[int64]bool, len(classifications))
		for _, c := range classifications {
			returned[c.ID] = true
		}
		for _, f := range batch {
			if !returned[f.ID] {
				result.Unchanged++
			}
		}
	}

	result.Latency = time.Since(start)
	return result, nil
}

// classifyBatch sends one batch of facts to the LLM for classification.
func classifyBatch(ctx context.Context, provider llm.Provider, facts []ClassifyableFact) ([]classifyEntry, error) {
	prompt := buildClassifyPrompt(facts)

	classifyCtx, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()

	response, err := provider.Complete(classifyCtx, prompt, llm.CompletionOpts{
		Temperature: 0.1,
		MaxTokens:   2048,
		System:      classifySystemPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM classify call: %w", err)
	}

	return parseClassifyResponse(response)
}

// buildClassifyPrompt constructs the user message with a batch of facts.
func buildClassifyPrompt(facts []ClassifyableFact) string {
	var sb strings.Builder
	sb.WriteString("Classify each fact into the correct type. Return JSON only.\n\n")
	sb.WriteString("FACTS:\n")

	for _, f := range facts {
		subj := f.Subject
		if subj == "" {
			subj = "(none)"
		}
		sb.WriteString(fmt.Sprintf("- id:%d | %s → %s → %s (current: %s)\n",
			f.ID, subj, f.Predicate, truncateForPrompt(f.Object, 80), f.FactType))
	}

	return sb.String()
}

// parseClassifyResponse parses the LLM's JSON (with markdown stripping).
func parseClassifyResponse(raw string) ([]classifyEntry, error) {
	cleaned := strings.TrimSpace(raw)

	// Strip markdown code fences
	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
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

	var resp classifyResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON from LLM: %w\nraw: %s", err, truncateForError(raw, 300))
	}

	return resp.Classifications, nil
}

// truncateForPrompt truncates a string for prompt inclusion.
func truncateForPrompt(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
