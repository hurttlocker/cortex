// Package extract — LLM-powered conflict resolution for Cortex.
//
// ResolveConflicts evaluates contradictory fact pairs using an LLM to determine
// which fact should win based on recency, source authority, and semantic context.
// Low-confidence resolutions are flagged for human review.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hurttlocker/cortex/internal/llm"
)

const (
	// resolveTimeout is the max time for a single conflict resolution LLM call.
	resolveTimeout = 90 * time.Second

	// DefaultResolveModel is the recommended model for conflict resolution.
	DefaultResolveModel = "openrouter/deepseek/deepseek-v3.2"

	// resolveMinConfidence gates auto-resolution. Below this → flag for human.
	resolveMinConfidence = 0.7

	// DefaultResolveConcurrency is the default parallel LLM requests for batch resolution.
	DefaultResolveConcurrency = 5
)

const resolveSystemPrompt = `You are a conflict resolution system for a personal knowledge base. You receive two contradictory facts about the same subject+predicate and must decide which one to keep.

EVALUATION CRITERIA (in order of importance):
1. RECENCY: Newer information usually supersedes older (check timestamps, "as of" dates)
2. SOURCE AUTHORITY: Some sources are more authoritative:
   - MEMORY.md > USER.md > IDENTITY.md > daily notes > imports
   - Manual entries > auto-captured > connector imports
3. SPECIFICITY: More specific/detailed facts are usually better
4. CONTEXT: Does one fact make more sense given what you know about the other facts?

ACTIONS:
- "supersede": Keep winner, mark loser as superseded (clear winner exists)
- "merge": Combine both into a single better fact (complementary information)
- "flag-human": Cannot determine winner with confidence (genuinely ambiguous)

RULES:
- If both facts are essentially the same with minor wording differences → supersede the less precise one
- If both facts contain unique information → merge
- If genuinely contradictory with no clear winner → flag-human
- Confidence must reflect your certainty: 0.9+ = very sure, 0.7-0.9 = reasonably sure, <0.7 = unsure

Return ONLY a JSON object:
{
  "resolutions": [
    {
      "pair_index": 0,
      "action": "supersede",
      "winner_id": 123,
      "loser_id": 456,
      "reason": "Fact 123 is newer (Feb 2026 vs Jan 2026) and from MEMORY.md",
      "confidence": 0.92,
      "merged_fact": null
    }
  ]
}

For merge actions, include the merged fact:
{
  "pair_index": 1,
  "action": "merge",
  "winner_id": 0,
  "loser_id": 0,
  "reason": "Both facts contain unique details about the same config",
  "confidence": 0.85,
  "merged_fact": {
    "subject": "entity",
    "predicate": "attribute",
    "object": "combined value from both facts",
    "type": "config"
  }
}`

// ConflictPair represents two contradictory facts for resolution.
type ConflictPair struct {
	Index int
	Fact1 ConflictFact
	Fact2 ConflictFact
}

// ConflictFact is a fact with metadata needed for resolution.
type ConflictFact struct {
	ID             int64
	Subject        string
	Predicate      string
	Object         string
	FactType       string
	Confidence     float64
	DecayRate      float64
	Source         string
	CreatedAt      time.Time
	LastReinforced time.Time
}

// ConflictResolution is the LLM's decision for one conflict pair.
type ConflictResolution struct {
	PairIndex  int         `json:"pair_index"`
	Action     string      `json:"action"` // "supersede", "merge", "flag-human"
	WinnerID   int64       `json:"winner_id"`
	LoserID    int64       `json:"loser_id"`
	Reason     string      `json:"reason"`
	Confidence float64     `json:"confidence"`
	MergedFact *MergedFact `json:"merged_fact,omitempty"`
}

// MergedFact is a new fact combining two conflicting originals.
type MergedFact struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	FactType  string `json:"type"`
}

// ResolveResult holds the output from a batch LLM resolution run.
type ResolveResult struct {
	Resolutions []ConflictResolution
	Superseded  int // Facts that will be superseded
	Merged      int // Conflict pairs resolved by merging
	Flagged     int // Conflicts flagged for human review
	Errors      int // Errors during resolution
	TotalPairs  int // Total conflict pairs processed
	Latency     time.Duration
	Model       string
}

// ResolveOpts configures the LLM resolution run.
type ResolveOpts struct {
	MinConfidence float64 // Below this → flag for human (default: 0.7)
	DryRun        bool    // Show resolutions without applying
	Concurrency   int     // Parallel LLM requests (default: 5)
	BatchSize     int     // Pairs per LLM call (default: 5)
}

// DefaultResolveOpts returns sensible defaults.
func DefaultResolveOpts() ResolveOpts {
	return ResolveOpts{
		MinConfidence: resolveMinConfidence,
		Concurrency:   DefaultResolveConcurrency,
		BatchSize:     5,
	}
}

// resolveResponse is the JSON the LLM returns.
type resolveResponse struct {
	Resolutions []resolveEntry `json:"resolutions"`
}

type resolveEntry struct {
	PairIndex  int         `json:"pair_index"`
	Action     string      `json:"action"`
	WinnerID   int64       `json:"winner_id"`
	LoserID    int64       `json:"loser_id"`
	Reason     string      `json:"reason"`
	Confidence float64     `json:"confidence"`
	MergedFact *MergedFact `json:"merged_fact"`
}

// ResolveConflictsLLM uses an LLM to evaluate and resolve conflict pairs.
// Pairs with low-confidence resolutions are flagged for human review.
func ResolveConflictsLLM(ctx context.Context, provider llm.Provider, pairs []ConflictPair, opts ResolveOpts) (*ResolveResult, error) {
	if provider == nil {
		return nil, fmt.Errorf("LLM provider is nil")
	}
	if len(pairs) == 0 {
		return &ResolveResult{}, nil
	}

	if opts.MinConfidence <= 0 {
		opts.MinConfidence = resolveMinConfidence
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultResolveConcurrency
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5
	}

	start := time.Now()
	result := &ResolveResult{
		TotalPairs: len(pairs),
		Model:      provider.Name(),
	}

	// Build batches of pairs
	var batches [][]ConflictPair
	for i := 0; i < len(pairs); i += opts.BatchSize {
		end := i + opts.BatchSize
		if end > len(pairs) {
			end = len(pairs)
		}
		batches = append(batches, pairs[i:end])
	}

	// Process concurrently
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.Concurrency)

	for _, batch := range batches {
		wg.Add(1)
		sem <- struct{}{}

		go func(batch []ConflictPair) {
			defer wg.Done()
			defer func() { <-sem }()

			resolutions, err := resolveBatch(ctx, provider, batch)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				result.Errors += len(batch)
				return
			}

			for _, r := range resolutions {
				// Validate action
				switch r.Action {
				case "supersede", "merge", "flag-human":
					// ok
				default:
					result.Errors++
					continue
				}

				// Force flag-human if confidence too low
				if r.Confidence < opts.MinConfidence && r.Action != "flag-human" {
					r.Action = "flag-human"
					r.Reason += " (confidence below threshold, flagged for review)"
				}

				resolution := ConflictResolution{
					PairIndex:  r.PairIndex,
					Action:     r.Action,
					WinnerID:   r.WinnerID,
					LoserID:    r.LoserID,
					Reason:     r.Reason,
					Confidence: r.Confidence,
					MergedFact: r.MergedFact,
				}

				// Validate merged fact if merge action
				if r.Action == "merge" && r.MergedFact != nil {
					if !isValidFactType(r.MergedFact.FactType) {
						r.MergedFact.FactType = "kv"
					}
				}

				result.Resolutions = append(result.Resolutions, resolution)

				switch r.Action {
				case "supersede":
					result.Superseded++
				case "merge":
					result.Merged++
				case "flag-human":
					result.Flagged++
				}
			}
		}(batch)
	}

	wg.Wait()
	result.Latency = time.Since(start)
	return result, nil
}

// resolveBatch sends a batch of conflict pairs to the LLM.
func resolveBatch(ctx context.Context, provider llm.Provider, pairs []ConflictPair) ([]resolveEntry, error) {
	prompt := buildResolvePrompt(pairs)

	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	response, err := provider.Complete(resolveCtx, prompt, llm.CompletionOpts{
		Temperature: 0.1,
		MaxTokens:   4096,
		System:      resolveSystemPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM resolve call: %w", err)
	}

	return parseResolveResponse(response)
}

// buildResolvePrompt constructs the user message with conflict pairs.
func buildResolvePrompt(pairs []ConflictPair) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Resolve %d conflict pairs. Return JSON only.\n\n", len(pairs)))

	for i, p := range pairs {
		sb.WriteString(fmt.Sprintf("--- CONFLICT PAIR %d ---\n", i))
		sb.WriteString(fmt.Sprintf("FACT A (id:%d): %s → %s → %s\n",
			p.Fact1.ID, p.Fact1.Subject, p.Fact1.Predicate, truncateForPrompt(p.Fact1.Object, 120)))
		sb.WriteString(fmt.Sprintf("  type:%s, conf:%.2f, source:%s, created:%s\n",
			p.Fact1.FactType, p.Fact1.Confidence, truncateForPrompt(p.Fact1.Source, 50),
			p.Fact1.CreatedAt.Format("2006-01-02")))
		sb.WriteString(fmt.Sprintf("FACT B (id:%d): %s → %s → %s\n",
			p.Fact2.ID, p.Fact2.Subject, p.Fact2.Predicate, truncateForPrompt(p.Fact2.Object, 120)))
		sb.WriteString(fmt.Sprintf("  type:%s, conf:%.2f, source:%s, created:%s\n\n",
			p.Fact2.FactType, p.Fact2.Confidence, truncateForPrompt(p.Fact2.Source, 50),
			p.Fact2.CreatedAt.Format("2006-01-02")))
	}

	return sb.String()
}

// parseResolveResponse parses the LLM's JSON response.
func parseResolveResponse(raw string) ([]resolveEntry, error) {
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

	var resp resolveResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON from LLM: %w\nraw: %s", err, truncateForError(raw, 300))
	}

	return resp.Resolutions, nil
}
