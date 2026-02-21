package reason

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

// Action types the LLM can request during recursive reasoning.
const (
	ActionSearch   = "SEARCH"    // Run a new Cortex search with different terms
	ActionFacts    = "FACTS"     // Search facts specifically
	ActionPeek     = "PEEK"      // Get full content of a memory by ID
	ActionSubQuery = "SUB_QUERY" // Recursive sub-call (depth-limited)
	ActionFinal    = "FINAL"     // Terminal — return this as the answer
)

// RecursiveOptions configures a recursive reasoning run.
type RecursiveOptions struct {
	Query         string // Root query
	Preset        string // Preset name
	Project       string // Project scope
	MaxIterations int    // Max loop iterations (default: 8)
	MaxDepth      int    // Max recursion depth for SUB_QUERY (default: 1)
	MaxTokens     int    // Max tokens per LLM call
	MaxContext    int    // Max context chars
	JSONOutput    bool   // Output as JSON
	Verbose       bool   // Print iteration progress
}

// RecursiveResult extends ReasonResult with recursion metadata.
type RecursiveResult struct {
	ReasonResult                  // Embed base result
	Iterations   int              `json:"iterations"`
	TotalCalls   int              `json:"total_calls"`
	Actions      []ActionRecord   `json:"actions"`
	SubQueries   []SubQueryResult `json:"sub_queries,omitempty"`
	Depth        int              `json:"depth"`
}

// ActionRecord logs each action the LLM took during reasoning.
type ActionRecord struct {
	Iteration int           `json:"iteration"`
	Action    string        `json:"action"`
	Argument  string        `json:"argument"`
	ResultLen int           `json:"result_len"`
	Duration  time.Duration `json:"duration"`
}

// SubQueryResult holds the result of a recursive sub-query.
type SubQueryResult struct {
	Query    string `json:"query"`
	Response string `json:"response"`
	Depth    int    `json:"depth"`
}

// Regex patterns to parse LLM action responses.
var (
	reAction = regexp.MustCompile(`(?m)^(SEARCH|FACTS|PEEK|SUB_QUERY|FINAL)\((.+?)\)\s*$`)
	// Also match multi-line FINAL with closing paren on same or next lines
	reFinalBlock = regexp.MustCompile(`(?s)FINAL\((.+)\)`)
)

// recursiveSystemPrompt instructs the LLM on the recursive protocol.
const recursiveSystemPrompt = `You are a recursive reasoning engine. You MUST follow the action protocol below exactly.

## MANDATORY: Every response MUST end with exactly one action call

Available actions:
- SEARCH(new search query) — search memories with different terms
- FACTS(keyword) — search extracted facts
- PEEK(memory_id) — get full content of a memory by its numeric ID
- SUB_QUERY(sub-question) — recursive sub-call for component questions
- FINAL(your complete answer here) — your final synthesized answer

## STRICT FORMAT RULES

1. Think briefly, then end with ONE action.
2. The action MUST be the last thing in your response.
3. Do NOT repeat previous search queries.
4. For complex queries: use 3-6 SEARCH calls to gather context from different angles, THEN use FINAL.
5. For simple queries with enough initial context: go straight to FINAL.
6. Confidence scores [0.85] show memory freshness. Below [0.50] = stale.

## EXAMPLE (3-iteration run)

User provides initial context...

Response 1:
The initial context covers X but I need more about Y.
SEARCH(Y specific details)

[system feeds back new results]

Response 2:
Now I have X and Y. Let me check for Z.
SEARCH(Z related information)

[system feeds back new results]

Response 3:
I now have comprehensive context. Here's my synthesis.
FINAL(## Complete Analysis

1. **Finding one**: ...
2. **Finding two**: ...
3. **Recommendation**: ...)

## CRITICAL: Your FINAL() content is returned verbatim to the user. Make it complete and well-formatted.
`

// ReasonRecursive executes the full recursive reasoning loop.
func (e *Engine) ReasonRecursive(ctx context.Context, opts RecursiveOptions) (*RecursiveResult, error) {
	return e.reasonRecursiveAtDepth(ctx, opts, 0)
}

func (e *Engine) reasonRecursiveAtDepth(ctx context.Context, opts RecursiveOptions, depth int) (*RecursiveResult, error) {
	start := time.Now()

	// Defaults
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = 8
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 1
	}
	maxContext := opts.MaxContext
	if maxContext <= 0 {
		maxContext = 8000
	}

	// Load preset
	presetName := opts.Preset
	if presetName == "" {
		presetName = "daily-digest"
	}
	preset, err := GetPreset(presetName, e.configDir)
	if err != nil {
		return nil, err
	}

	maxTokens := preset.MaxTokens
	if opts.MaxTokens > 0 {
		maxTokens = opts.MaxTokens
	}

	// Initial search
	searchStart := time.Now()
	searchOpts := search.Options{
		Limit:   preset.SearchLimit,
		Project: opts.Project,
	}
	if preset.SearchMode != "" {
		mode, err := search.ParseMode(preset.SearchMode)
		if err == nil {
			searchOpts.Mode = mode
		}
	}

	query := opts.Query
	if query == "" {
		query = preset.Name
	}

	initialResults, err := e.searchEngine.Search(ctx, query, searchOpts)
	if err != nil {
		return nil, fmt.Errorf("initial search failed: %w", err)
	}
	searchTime := time.Since(searchStart)

	// Build initial context
	contextStr, memoriesUsed := buildConfidenceContext(ctx, e.store, initialResults, maxContext)
	factsStr, factsUsed := gatherFacts(ctx, e.store, initialResults, maxContext-len(contextStr))

	initialContext := contextStr
	if factsStr != "" {
		initialContext += "\n\n--- Extracted Facts ---\n" + factsStr
	}

	// Build system prompt: combine preset system + recursive protocol + response contract
	systemPrompt := recursiveSystemPrompt
	if preset.System != "" {
		systemPrompt = preset.System + "\n\n" + recursiveSystemPrompt
	}
	systemPrompt += "\n\n" + responseQualityContract

	// Build the conversation history for the recursive loop
	analysisPrompt := expandTemplate(preset.Template, initialContext, query)
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf(
			"## Query\n%s\n\n## Task\n%s\n\n"+
				"Use actions (SEARCH/FACTS/PEEK/SUB_QUERY) as needed, then end with FINAL(your complete answer). "+
				"Do not stop at partial notes. FINAL must satisfy the required section format and include concrete source/evidence, uncertainty/trade-offs, and owner/timeline recommendations.",
			query, analysisPrompt,
		)},
	}

	// Track state
	var actions []ActionRecord
	var subQueries []SubQueryResult
	var totalTokensIn, totalTokensOut int
	var totalLLMTime time.Duration
	var finalContent string
	totalCalls := 0
	seenSearches := map[string]bool{normalizeSearchArgument(query): true} // Don't repeat initial query
	iteration := 0

	// === THE RECURSIVE LOOP ===
	for iteration = 0; iteration < maxIter; iteration++ {
		if opts.Verbose {
			fmt.Printf("  [%d/%d] ", iteration+1, maxIter)
		}

		// Call LLM
		llmStart := time.Now()
		llmResult, err := e.llm.Chat(ctx, messages, maxTokens)
		if err != nil {
			return nil, fmt.Errorf("LLM call %d failed: %w", iteration+1, err)
		}
		callDuration := time.Since(llmStart)
		totalLLMTime += callDuration
		totalCalls++
		totalTokensIn += llmResult.PromptTokens
		totalTokensOut += llmResult.CompletionTokens

		response := strings.TrimSpace(llmResult.Content)

		// Parse the action from the response
		action, argument := parseAction(response)

		if action == "" {
			action = "IMPLICIT_FINAL"
			argument = response
		}

		if opts.Verbose {
			if action == ActionFinal || action == "IMPLICIT_FINAL" {
				fmt.Printf("%s — %.1fs (%d chars)\n", action, callDuration.Seconds(), len(argument))
			} else {
				fmt.Printf("%s(%s) — %.1fs\n", action, truncateArg(argument, 60), callDuration.Seconds())
			}
		}

		switch action {
		case ActionFinal:
			// Terminal — we're done
			finalContent = argument
			actions = append(actions, ActionRecord{
				Iteration: iteration + 1,
				Action:    ActionFinal,
				Argument:  truncateArg(argument, 200),
				Duration:  callDuration,
			})
			goto done

		case ActionSearch:
			// Run a new search with different terms
			searchKey := normalizeSearchArgument(argument)
			if seenSearches[searchKey] {
				if opts.Verbose {
					fmt.Printf("  ⚠️ Duplicate search, requesting a different query\n")
				}
				messages = append(messages,
					ChatMessage{Role: "assistant", Content: response},
					ChatMessage{Role: "user", Content: fmt.Sprintf(
						"⚠️ SEARCH(%q) was already used. Use a different search angle, or finalize now with FINAL(complete structured answer).",
						argument,
					)},
				)
				continue
			}
			seenSearches[searchKey] = true

			actionStart := time.Now()
			newResults, err := e.searchEngine.Search(ctx, argument, searchOpts)
			actionDur := time.Since(actionStart)
			searchTime += actionDur

			var newContext string
			var newMem int
			if err != nil {
				newContext = fmt.Sprintf("(search error: %v)", err)
			} else {
				newContext, newMem = buildConfidenceContext(ctx, e.store, newResults, maxContext/2)
				memoriesUsed += newMem
			}

			actions = append(actions, ActionRecord{
				Iteration: iteration + 1,
				Action:    ActionSearch,
				Argument:  argument,
				ResultLen: len(newContext),
				Duration:  actionDur,
			})

			// Feed results back into the conversation
			messages = append(messages,
				ChatMessage{Role: "assistant", Content: response},
				ChatMessage{Role: "user", Content: fmt.Sprintf(
					"## SEARCH Results for: %q\n%s\n\nContinue your analysis. Use another action or FINAL(answer) when ready.",
					argument, newContext,
				)},
			)

		case ActionFacts:
			actionStart := time.Now()
			// Search facts via store
			facts, err := e.store.ListFacts(ctx, store.ListOpts{Limit: 50})
			actionDur := time.Since(actionStart)

			var factsResult string
			if err != nil {
				factsResult = fmt.Sprintf("(facts error: %v)", err)
			} else {
				// Filter facts by keyword match on argument
				var matched []string
				argLower := strings.ToLower(argument)
				for _, f := range facts {
					combined := strings.ToLower(f.Subject + " " + f.Predicate + " " + f.Object)
					if strings.Contains(combined, argLower) || fuzzyMatch(argLower, combined) {
						matched = append(matched, fmt.Sprintf("[%.2f] %s: %s %s %s",
							f.Confidence, f.FactType, f.Subject, f.Predicate, f.Object))
					}
				}
				if len(matched) == 0 {
					factsResult = "(no matching facts found)"
				} else {
					if len(matched) > 20 {
						matched = matched[:20]
					}
					factsResult = strings.Join(matched, "\n")
					factsUsed += len(matched)
				}
			}

			actions = append(actions, ActionRecord{
				Iteration: iteration + 1,
				Action:    ActionFacts,
				Argument:  argument,
				ResultLen: len(factsResult),
				Duration:  actionDur,
			})

			messages = append(messages,
				ChatMessage{Role: "assistant", Content: response},
				ChatMessage{Role: "user", Content: fmt.Sprintf(
					"## FACTS Results for: %q\n%s\n\nContinue your analysis. Use another action or FINAL(answer) when ready.",
					argument, factsResult,
				)},
			)

		case ActionPeek:
			actionStart := time.Now()
			// Try to parse memory ID from argument
			var peekResult string
			var memID int64
			if _, err := fmt.Sscanf(argument, "%d", &memID); err == nil {
				mem, err := e.store.GetMemory(ctx, memID)
				if err != nil {
					peekResult = fmt.Sprintf("(memory %d not found: %v)", memID, err)
				} else if mem == nil {
					peekResult = fmt.Sprintf("(memory %d not found)", memID)
				} else {
					peekResult = truncateContent(mem.Content, maxContext/2)
				}
			} else {
				peekResult = fmt.Sprintf("(invalid memory ID: %q — use numeric ID from search results)", argument)
			}
			actionDur := time.Since(actionStart)

			actions = append(actions, ActionRecord{
				Iteration: iteration + 1,
				Action:    ActionPeek,
				Argument:  argument,
				ResultLen: len(peekResult),
				Duration:  actionDur,
			})

			messages = append(messages,
				ChatMessage{Role: "assistant", Content: response},
				ChatMessage{Role: "user", Content: fmt.Sprintf(
					"## PEEK Result (Memory %s)\n%s\n\nContinue your analysis. Use another action or FINAL(answer) when ready.",
					argument, peekResult,
				)},
			)

		case ActionSubQuery:
			if depth >= maxDepth {
				// At max depth — tell the LLM it can't recurse further
				messages = append(messages,
					ChatMessage{Role: "assistant", Content: response},
					ChatMessage{Role: "user", Content: fmt.Sprintf(
						"⚠️ SUB_QUERY depth limit reached (%d/%d). Cannot recurse further. Please answer %q with available context, or use SEARCH/FACTS to gather more, then FINAL(answer).",
						depth+1, maxDepth, argument,
					)},
				)
			} else {
				// Recursive call!
				subOpts := RecursiveOptions{
					Query:         argument,
					Preset:        opts.Preset,
					Project:       opts.Project,
					MaxIterations: maxIter / 2, // Sub-queries get half the budget
					MaxDepth:      maxDepth,
					MaxTokens:     maxTokens,
					MaxContext:    maxContext,
					Verbose:       opts.Verbose,
				}

				if opts.Verbose {
					fmt.Printf("  ↳ Recursing into sub-query (depth %d): %s\n", depth+1, truncateArg(argument, 80))
				}

				subResult, err := e.reasonRecursiveAtDepth(ctx, subOpts, depth+1)
				var subResponse string
				if err != nil {
					subResponse = fmt.Sprintf("(sub-query error: %v)", err)
				} else {
					subResponse = subResult.Content
					totalTokensIn += subResult.TokensIn
					totalTokensOut += subResult.TokensOut
					totalCalls += subResult.TotalCalls
					totalLLMTime += subResult.LLMTime
					searchTime += subResult.SearchTime
					memoriesUsed += subResult.MemoriesUsed
					factsUsed += subResult.FactsUsed
				}

				subQueries = append(subQueries, SubQueryResult{
					Query:    argument,
					Response: truncateContent(subResponse, 500),
					Depth:    depth + 1,
				})

				actions = append(actions, ActionRecord{
					Iteration: iteration + 1,
					Action:    ActionSubQuery,
					Argument:  argument,
					ResultLen: len(subResponse),
				})

				messages = append(messages,
					ChatMessage{Role: "assistant", Content: response},
					ChatMessage{Role: "user", Content: fmt.Sprintf(
						"## SUB_QUERY Result for: %q\n%s\n\nIntegrate this with your analysis. Use another action or FINAL(answer) when ready.",
						argument, subResponse,
					)},
				)
			}

		case "IMPLICIT_FINAL":
			// LLM didn't output a valid action — treat the whole response as the final answer
			finalContent = argument
			actions = append(actions, ActionRecord{
				Iteration: iteration + 1,
				Action:    "IMPLICIT_FINAL",
				ResultLen: len(argument),
				Duration:  callDuration,
			})
			goto done

		default:
			// Unknown action — shouldn't happen but handle gracefully
			finalContent = response
			actions = append(actions, ActionRecord{
				Iteration: iteration + 1,
				Action:    "UNKNOWN:" + action,
				ResultLen: len(response),
				Duration:  callDuration,
			})
			goto done
		}
	}

	// Max iterations reached without FINAL
	if opts.Verbose {
		fmt.Printf("  ⚠️ Max iterations (%d) reached — using last response\n", maxIter)
	}
	if finalContent == "" {
		// Use the last assistant message content
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				finalContent = extractPreActionText(messages[i].Content)
				if finalContent == "" {
					finalContent = messages[i].Content
				}
				break
			}
		}
	}

done:
	finalContent = enforceResponseQualityContract(finalContent, query)

	return &RecursiveResult{
		ReasonResult: ReasonResult{
			Content:      finalContent,
			Preset:       presetName,
			Query:        query,
			Project:      opts.Project,
			Model:        e.llm.model,
			Provider:     e.llm.provider,
			MemoriesUsed: memoriesUsed,
			FactsUsed:    factsUsed,
			Duration:     time.Since(start),
			SearchTime:   searchTime,
			LLMTime:      totalLLMTime,
			TokensIn:     totalTokensIn,
			TokensOut:    totalTokensOut,
		},
		Iterations: iteration + 1,
		TotalCalls: totalCalls,
		Actions:    actions,
		SubQueries: subQueries,
		Depth:      depth,
	}, nil
}

// parseAction extracts the action type and argument from the LLM response.
func parseAction(response string) (string, string) {
	// First: try single-line actions (SEARCH, FACTS, PEEK, SUB_QUERY)
	// These are simpler and should be matched first since they're unambiguous.
	for _, action := range []string{ActionSearch, ActionFacts, ActionPeek, ActionSubQuery} {
		prefix := action + "("
		// Search from the end of the response (action should be last)
		lastIdx := strings.LastIndex(response, prefix)
		if lastIdx >= 0 {
			rest := response[lastIdx+len(prefix):]
			// Find closing paren (single-line actions don't have nested parens typically)
			if closeIdx := strings.Index(rest, ")"); closeIdx >= 0 {
				return action, strings.TrimSpace(rest[:closeIdx])
			}
			// No close paren — take to end of line
			if nlIdx := strings.Index(rest, "\n"); nlIdx >= 0 {
				return action, strings.TrimSpace(rest[:nlIdx])
			}
			return action, strings.TrimSpace(rest)
		}
	}

	// FINAL — the most complex case. Content can be multi-line with markdown.
	if idx := strings.LastIndex(response, "FINAL("); idx >= 0 {
		rest := response[idx+6:]
		// Find matching close paren with depth tracking
		depth := 1
		for i, c := range rest {
			switch c {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					content := strings.TrimSpace(rest[:i])
					if content != "" {
						return ActionFinal, content
					}
				}
			}
		}
		// No matching close paren — take everything after FINAL(
		content := strings.TrimSpace(rest)
		// Strip trailing ) if it's the very last character (common formatting)
		content = strings.TrimRight(content, ")")
		content = strings.TrimSpace(content)
		if content != "" {
			return ActionFinal, content
		}
	}

	// No action found
	return "", ""
}

// extractPreActionText gets any reasoning text before the action call.
func extractPreActionText(response string) string {
	// Find the first action-like line
	for _, action := range []string{"SEARCH(", "FACTS(", "PEEK(", "SUB_QUERY(", "FINAL("} {
		if idx := strings.Index(response, action); idx > 0 {
			text := strings.TrimSpace(response[:idx])
			if text != "" {
				return text
			}
		}
	}
	return ""
}

// fuzzyMatch does a simple word-overlap check for fact filtering.
func fuzzyMatch(query, text string) bool {
	words := strings.Fields(query)
	matches := 0
	for _, w := range words {
		if len(w) > 2 && strings.Contains(text, w) {
			matches++
		}
	}
	return len(words) > 0 && float64(matches)/float64(len(words)) >= 0.5
}

func normalizeSearchArgument(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

func truncateArg(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
