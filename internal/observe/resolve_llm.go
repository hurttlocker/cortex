package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/llm"
	"github.com/hurttlocker/cortex/internal/store"
)

const (
	// StrategyLLM uses an LLM to evaluate and resolve conflicts.
	StrategyLLM Strategy = "llm"

	// llmResolveTimeout per conflict pair.
	llmResolveTimeout = 15 * time.Second

	// llmConfidenceThreshold: below this, flag for human review.
	llmConfidenceThreshold = 0.7
)

const resolveSystemPrompt = `You are a fact conflict resolver for a personal knowledge base.

Given two conflicting facts (same subject and predicate but different values), decide which one is correct.

Evaluate based on:
1. RECENCY: Newer facts usually reflect updates (check created_at timestamps)
2. SOURCE AUTHORITY: Curated files (MEMORY.md, USER.md, capital-instructions.md) > daily notes > auto-captures
3. SPECIFICITY: More specific/detailed values are usually more accurate
4. CONTEXT: Is this a genuine update (old value replaced) or an error?

Return ONLY a JSON object:
{
  "winner": 1 or 2,
  "action": "supersede" or "merge" or "flag",
  "confidence": 0.0 to 1.0,
  "reason": "brief explanation"
}

- "supersede": keep winner, mark loser as outdated
- "merge": both contain partial truth (explain how to combine)
- "flag": genuinely ambiguous, needs human review

If unsure, set confidence < 0.7 and action to "flag".`

// llmResolution is the expected JSON response from the LLM.
type llmResolution struct {
	Winner     int     `json:"winner"`
	Action     string  `json:"action"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// ResolveLLM resolves conflicts using an LLM provider.
// Each conflict is sent individually to the LLM for evaluation.
// Conflicts where the LLM confidence < threshold are flagged for manual review.
func (r *Resolver) ResolveLLM(ctx context.Context, conflicts []Conflict, provider llm.Provider, dryRun bool) (*ResolveBatch, error) {
	batch := &ResolveBatch{
		Total:   len(conflicts),
		Results: make([]Resolution, 0, len(conflicts)),
	}

	for _, c := range conflicts {
		resolution := r.resolveLLMOne(ctx, c, provider)

		if !dryRun && resolution.Winner != "manual" && resolution.LoserID > 0 {
			err := r.store.SupersedeFact(ctx, resolution.LoserID, resolution.WinnerID,
				fmt.Sprintf("strategy:llm provider:%s", provider.Name()))
			if err != nil {
				batch.Errors++
				resolution.Reason += fmt.Sprintf(" (apply error: %v)", err)
			} else {
				resolution.Applied = true
				_ = r.store.ReinforceFact(ctx, resolution.WinnerID)
			}
			batch.Resolved++
		} else if resolution.Winner == "manual" {
			batch.Skipped++
		} else {
			batch.Resolved++
		}

		batch.Results = append(batch.Results, resolution)
	}

	return batch, nil
}

// resolveLLMOne sends a single conflict to the LLM and parses the response.
func (r *Resolver) resolveLLMOne(ctx context.Context, c Conflict, provider llm.Provider) Resolution {
	res := Resolution{
		Conflict: c,
		Strategy: StrategyLLM,
	}

	prompt := formatConflictPrompt(c)

	resolveCtx, cancel := context.WithTimeout(ctx, llmResolveTimeout)
	defer cancel()

	resp, err := provider.Complete(resolveCtx, prompt, llm.CompletionOpts{
		System:      resolveSystemPrompt,
		MaxTokens:   200,
		Temperature: 0.1, // Low temp for consistent judgment
	})
	if err != nil {
		// Fallback to last-write-wins on LLM error
		res.Winner = "manual"
		res.Reason = fmt.Sprintf("LLM error (flagged for review): %v", err)
		return res
	}

	llmRes, parseErr := parseLLMResolution(resp)
	if parseErr != nil {
		res.Winner = "manual"
		res.Reason = fmt.Sprintf("LLM response unparseable (flagged for review): %v", parseErr)
		return res
	}

	// Apply confidence threshold
	if llmRes.Confidence < llmConfidenceThreshold || llmRes.Action == "flag" {
		res.Winner = "manual"
		res.Reason = fmt.Sprintf("LLM confidence %.2f < threshold (flagged): %s", llmRes.Confidence, llmRes.Reason)
		return res
	}

	// Map winner
	switch llmRes.Winner {
	case 1:
		res.Winner = "fact1"
		res.WinnerID = c.Fact1.ID
		res.LoserID = c.Fact2.ID
	case 2:
		res.Winner = "fact2"
		res.WinnerID = c.Fact2.ID
		res.LoserID = c.Fact1.ID
	default:
		res.Winner = "manual"
		res.Reason = fmt.Sprintf("LLM returned invalid winner %d (flagged)", llmRes.Winner)
		return res
	}

	res.Reason = fmt.Sprintf("[LLM %.0f%% %s] %s", llmRes.Confidence*100, provider.Name(), llmRes.Reason)
	return res
}

// formatConflictPrompt builds the prompt for a single conflict.
func formatConflictPrompt(c Conflict) string {
	var sb strings.Builder

	sb.WriteString("CONFLICT: Two facts with same subject+predicate have different values.\n\n")

	sb.WriteString("FACT 1:\n")
	writeFactDetail(&sb, c.Fact1, 1)

	sb.WriteString("\nFACT 2:\n")
	writeFactDetail(&sb, c.Fact2, 2)

	sb.WriteString("\nWhich fact is correct? Return JSON only.")
	return sb.String()
}

func writeFactDetail(sb *strings.Builder, f store.Fact, num int) {
	fmt.Fprintf(sb, "  Subject: %s\n", f.Subject)
	fmt.Fprintf(sb, "  Predicate: %s\n", f.Predicate)
	fmt.Fprintf(sb, "  Value: %s\n", f.Object)
	fmt.Fprintf(sb, "  Type: %s\n", f.FactType)
	fmt.Fprintf(sb, "  Confidence: %.2f\n", f.Confidence)
	fmt.Fprintf(sb, "  Created: %s\n", f.CreatedAt.Format(time.RFC3339))
	if f.SourceQuote != "" {
		quote := f.SourceQuote
		if len(quote) > 100 {
			quote = quote[:100] + "..."
		}
		fmt.Fprintf(sb, "  Source quote: %q\n", quote)
	}
	if f.AgentID != "" {
		fmt.Fprintf(sb, "  Agent: %s\n", f.AgentID)
	}
}

// parseLLMResolution parses the LLM's JSON response.
func parseLLMResolution(resp string) (*llmResolution, error) {
	resp = strings.TrimSpace(resp)

	// Strip markdown code fences
	if strings.HasPrefix(resp, "```") {
		lines := strings.Split(resp, "\n")
		var cleaned []string
		inBlock := false
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock {
				cleaned = append(cleaned, line)
			}
		}
		resp = strings.Join(cleaned, "\n")
	}

	var res llmResolution
	if err := json.Unmarshal([]byte(resp), &res); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w (raw: %s)", err, truncate(resp, 200))
	}

	if res.Winner < 1 || res.Winner > 2 {
		return nil, fmt.Errorf("winner must be 1 or 2, got %d", res.Winner)
	}
	if res.Confidence < 0 || res.Confidence > 1 {
		return nil, fmt.Errorf("confidence must be 0-1, got %.2f", res.Confidence)
	}
	if res.Action == "" {
		res.Action = "supersede"
	}

	return &res, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
