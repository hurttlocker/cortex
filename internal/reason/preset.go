package reason

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Preset defines a reusable reasoning template.
type Preset struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	System      string `yaml:"system" json:"system"`           // System prompt
	Template    string `yaml:"template" json:"template"`       // User prompt template ({{context}} and {{query}} are replaced)
	MaxTokens   int    `yaml:"max_tokens" json:"max_tokens"`   // Max output tokens (default: 1024)
	SearchLimit int    `yaml:"search_limit" json:"search_limit"` // How many memories to search for context (default: 20)
	SearchMode  string `yaml:"search_mode" json:"search_mode"` // "hybrid", "bm25", "semantic" (default: "hybrid")
}

// BuiltinPresets are the presets that ship with Cortex.
var BuiltinPresets = map[string]Preset{
	"daily-digest": {
		Name:        "daily-digest",
		Description: "Generate a concise daily summary of recent activity, decisions, and priorities",
		System:      "You are a concise analyst reviewing an AI agent's memory system. Be direct, specific, and actionable. No filler.",
		Template: `Analyze these memories and facts from the past day. Create a daily digest covering:

1. **Key Decisions** — what was decided and why
2. **Active Work** — what's in progress
3. **Blockers** — anything stuck or needing attention
4. **Tomorrow's Focus** — top 3 priorities

Context (sorted by confidence — lower scores may be stale):
{{context}}

{{if .Query}}Focus area: {{.Query}}{{end}}

Write the digest in markdown. Be concise — aim for 200-300 words.`,
		MaxTokens:   512,
		SearchLimit: 30,
		SearchMode:  "hybrid",
	},
	"fact-audit": {
		Name:        "fact-audit",
		Description: "Audit extracted facts for quality, staleness, and contradictions",
		System:      "You are a data quality auditor. Identify problems: stale facts, contradictions, misclassified types, missing context. Be precise.",
		Template: `Audit these facts from the Cortex knowledge base. For each issue found, state:
- The problematic fact(s)
- Why it's a problem (stale, contradictory, mistyped, vague)
- Suggested fix (update, merge, delete, reclassify)

Facts to audit:
{{context}}

{{if .Query}}Focus on: {{.Query}}{{end}}

List issues as numbered items. If everything looks clean, say so.`,
		MaxTokens:   512,
		SearchLimit: 40,
		SearchMode:  "bm25",
	},
	"weekly-dive": {
		Name:        "weekly-dive",
		Description: "Deep analysis of a topic using all available context",
		System:      "You are a senior analyst doing a deep-dive review. Synthesize patterns, identify risks, and surface non-obvious insights. Think like a strategist.",
		Template: `Deep dive analysis requested. Search the full knowledge base and synthesize:

1. **Pattern Summary** — what recurring themes emerge?
2. **Risk Assessment** — what could go wrong that hasn't been addressed?
3. **Blind Spots** — what's missing from the knowledge base on this topic?
4. **Recommendations** — 3-5 specific, actionable next steps

Topic: {{.Query}}

Available context:
{{context}}

Write a thorough analysis (400-600 words). Cite specific facts with their confidence scores when relevant.`,
		MaxTokens:   1024,
		SearchLimit: 40,
		SearchMode:  "hybrid",
	},
	"conflict-check": {
		Name:        "conflict-check",
		Description: "Find contradictory or inconsistent facts",
		System:      "You are a consistency checker. Find facts that contradict each other. Only report real contradictions, not facts that are simply about different topics.",
		Template: `Review these facts for contradictions. A contradiction is when two facts make incompatible claims about the same subject.

Facts:
{{context}}

{{if .Query}}Focus on: {{.Query}}{{end}}

For each contradiction found:
- Fact A: [quote it]
- Fact B: [quote it]
- Conflict: [explain the contradiction]
- Resolution: [which is likely correct and why]

If no contradictions found, say "No contradictions detected."`,
		MaxTokens:   512,
		SearchLimit: 50,
		SearchMode:  "bm25",
	},
	"agent-review": {
		Name:        "agent-review",
		Description: "Review agent activity, performance, and improvement opportunities",
		System:      "You are reviewing an AI agent team's recent performance. Be constructive but honest. Focus on patterns, not individual mistakes.",
		Template: `Review agent activity from the knowledge base:

1. **Activity Summary** — what did each agent accomplish?
2. **Coordination** — are agents communicating effectively? Any duplication?
3. **Quality** — any recurring errors or missed opportunities?
4. **Recommendations** — how to improve the team's effectiveness

Context:
{{context}}

{{if .Query}}Focus on: {{.Query}}{{end}}

Be specific and cite evidence from the context.`,
		MaxTokens:   512,
		SearchLimit: 30,
		SearchMode:  "hybrid",
	},
}

// LoadCustomPresets reads user-defined presets from ~/.cortex/presets.yaml.
func LoadCustomPresets(configDir string) (map[string]Preset, error) {
	path := filepath.Join(configDir, "presets.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No custom presets
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var presets map[string]Preset
	if err := yaml.Unmarshal(data, &presets); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return presets, nil
}

// GetPreset returns a preset by name, checking custom presets first.
func GetPreset(name string, customDir string) (*Preset, error) {
	// Check custom presets first (allows overriding builtins)
	custom, err := LoadCustomPresets(customDir)
	if err != nil {
		return nil, err
	}
	if custom != nil {
		if p, ok := custom[name]; ok {
			return &p, nil
		}
	}

	// Check builtins
	if p, ok := BuiltinPresets[name]; ok {
		return &p, nil
	}

	// List available presets in error
	var names []string
	for n := range BuiltinPresets {
		names = append(names, n)
	}
	for n := range custom {
		names = append(names, n+"*")
	}
	return nil, fmt.Errorf("unknown preset %q (available: %s)", name, strings.Join(names, ", "))
}

// ListPresets returns all available preset names and descriptions.
func ListPresets(customDir string) []Preset {
	var result []Preset
	for _, p := range BuiltinPresets {
		result = append(result, p)
	}

	custom, err := LoadCustomPresets(customDir)
	if err == nil && custom != nil {
		for _, p := range custom {
			result = append(result, p)
		}
	}
	return result
}
