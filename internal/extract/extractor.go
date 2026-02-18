package extract

import "strings"

// AutoClassifyMemoryClass applies lightweight heuristics to classify a memory chunk.
// Returns empty string when confidence is too low (treated as unclassified).
func AutoClassifyMemoryClass(content, sourceSection string) string {
	text := strings.ToLower(strings.TrimSpace(sourceSection + "\n" + content))
	if text == "" {
		return ""
	}

	scores := map[string]int{
		"rule":       0,
		"decision":   0,
		"preference": 0,
		"identity":   0,
		"status":     0,
		"scratch":    0,
	}

	// Rule / hard constraints
	ruleSignals := []string{
		"must", "must not", "never", "always", "do not", "don't", "non-negotiable",
		"rule:", "guardrail", "mandatory", "capital instruction", "no exceptions",
	}
	for _, s := range ruleSignals {
		if strings.Contains(text, s) {
			scores["rule"] += 2
		}
	}

	// Decisions / chosen paths
	decisionSignals := []string{
		"decided", "decision", "we chose", "chosen", "going with", "selected",
		"final decision", "ship this", "approved approach",
	}
	for _, s := range decisionSignals {
		if strings.Contains(text, s) {
			scores["decision"] += 2
		}
	}

	// Preferences
	preferenceSignals := []string{
		"prefer", "preferred", "likes", "dislikes", "wants", "style", "tone",
		"format", "preference",
	}
	for _, s := range preferenceSignals {
		if strings.Contains(text, s) {
			scores["preference"] += 2
		}
	}

	// Identity / profile-like facts
	identitySignals := []string{
		"name:", "role:", "username", "email", "i am", "he is", "she is", "they are",
		"works at", "reports to", "title:",
	}
	for _, s := range identitySignals {
		if strings.Contains(text, s) {
			scores["identity"] += 2
		}
	}

	// Status / transient updates
	statusSignals := []string{
		"status", "currently", "in progress", "working on", "blocked", "todo", "next up",
		"started", "completed", "idle",
	}
	for _, s := range statusSignals {
		if strings.Contains(text, s) {
			scores["status"] += 1
		}
	}

	// Scratch / brainstorm
	scratchSignals := []string{
		"brainstorm", "scratch", "rough idea", "draft", "maybe", "might", "could",
		"experiment", "parking lot",
	}
	for _, s := range scratchSignals {
		if strings.Contains(text, s) {
			scores["scratch"] += 1
		}
	}

	bestClass := ""
	bestScore := 0
	for class, score := range scores {
		if score > bestScore {
			bestClass = class
			bestScore = score
		}
	}

	if bestScore <= 0 {
		return ""
	}
	return bestClass
}
