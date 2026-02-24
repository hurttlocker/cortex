// Package extract — LLM-powered fact cluster summarization for Cortex.
//
// SummarizeClusters takes fact clusters (from v0.8.0 topic clustering) and
// consolidates redundant/overlapping facts into authoritative summaries.
// Superseded originals are marked, not deleted, preserving audit trail.
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
	// summarizeTimeout is the max time for a single cluster summarization call.
	summarizeTimeout = 45 * time.Second

	// DefaultMinClusterSize is the minimum facts to consider for summarization.
	DefaultMinClusterSize = 5

	// summarizeMaxFacts caps facts sent to LLM per cluster.
	summarizeMaxFacts = 100
)

const summarizeSystemPrompt = `You are a fact consolidation system for a personal knowledge base. You receive a cluster of related facts and must produce a minimal, non-redundant set.

RULES:
1. NEVER drop important information — consolidate, don't delete
2. Merge facts that say the same thing differently into ONE authoritative fact
3. Keep distinct facts separate (don't merge unrelated information)
4. Preserve the MOST SPECIFIC version when merging (e.g., "lives in Philadelphia, PA" > "lives in PA")
5. Preserve temporal context (dates, "as of", "since") when present
6. Each output fact must list which input fact IDs it replaces
7. If a fact is unique and can't be merged, keep it as-is

FACT TYPES: kv, relationship, preference, temporal, identity, location, decision, state, config

Return ONLY a JSON object:
{
  "summary_facts": [
    {
      "subject": "entity",
      "predicate": "attribute",
      "object": "consolidated value",
      "type": "fact_type",
      "confidence": 0.9,
      "replaces": [1, 2, 3],
      "reasoning": "merged 3 duplicate address facts"
    }
  ],
  "kept_as_is": [4, 5],
  "reasoning": "brief overview of consolidation decisions"
}`

// SummaryFact represents one consolidated fact from cluster summarization.
type SummaryFact struct {
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	FactType   string  `json:"type"`
	Confidence float64 `json:"confidence"`
	Replaces   []int64 `json:"replaces"`
	Reasoning  string  `json:"reasoning"`
}

// ClusterSummary holds the result of summarizing one cluster.
type ClusterSummary struct {
	ClusterID     int64         `json:"cluster_id"`
	ClusterName   string        `json:"cluster_name"`
	SummaryFacts  []SummaryFact `json:"summary_facts"`
	KeptAsIs      []int64       `json:"kept_as_is"`
	SupersededIDs []int64       `json:"superseded_ids"`
	OriginalCount int           `json:"original_count"`
	NewCount      int           `json:"new_count"`
	Compression   float64       `json:"compression_ratio"`
	Reasoning     string        `json:"reasoning"`
	Latency       time.Duration `json:"latency"`
}

// SummarizeResult holds the output from a full summarization run.
type SummarizeResult struct {
	Summaries      []ClusterSummary `json:"summaries"`
	TotalOriginal  int              `json:"total_original"`
	TotalNew       int              `json:"total_new"`
	TotalSupersede int              `json:"total_superseded"`
	Latency        time.Duration    `json:"latency"`
	Model          string           `json:"model"`
}

// SummarizeOpts configures the summarization run.
type SummarizeOpts struct {
	MinClusterSize int   // Min facts in cluster to consider (default: 5)
	ClusterID      int64 // Summarize specific cluster (0 = all)
	DryRun         bool  // Show plan without applying
}

// DefaultSummarizeOpts returns sensible defaults.
func DefaultSummarizeOpts() SummarizeOpts {
	return SummarizeOpts{
		MinClusterSize: DefaultMinClusterSize,
	}
}

// ClusterInput is the minimal cluster data needed for summarization.
type ClusterInput struct {
	ID    int64
	Name  string
	Facts []ClusterFactInput
}

// ClusterFactInput is a fact within a cluster for summarization.
type ClusterFactInput struct {
	ID         int64
	Subject    string
	Predicate  string
	Object     string
	FactType   string
	Confidence float64
	Source     string
}

// summarizeResponse is the JSON the LLM returns.
type summarizeResponse struct {
	SummaryFacts []SummaryFact `json:"summary_facts"`
	KeptAsIs     []int64       `json:"kept_as_is"`
	Reasoning    string        `json:"reasoning"`
}

// SummarizeClusters processes clusters and generates consolidated summaries.
func SummarizeClusters(ctx context.Context, provider llm.Provider, clusters []ClusterInput, opts SummarizeOpts) (*SummarizeResult, error) {
	if provider == nil {
		return nil, fmt.Errorf("LLM provider is nil")
	}

	if opts.MinClusterSize <= 0 {
		opts.MinClusterSize = DefaultMinClusterSize
	}

	start := time.Now()
	result := &SummarizeResult{
		Model: provider.Name(),
	}

	for _, cluster := range clusters {
		// Skip small clusters
		if len(cluster.Facts) < opts.MinClusterSize {
			continue
		}

		// Filter to specific cluster if requested
		if opts.ClusterID > 0 && cluster.ID != opts.ClusterID {
			continue
		}

		summary, err := summarizeCluster(ctx, provider, cluster)
		if err != nil {
			// Log but continue with next cluster
			continue
		}

		result.Summaries = append(result.Summaries, *summary)
		result.TotalOriginal += summary.OriginalCount
		result.TotalNew += summary.NewCount
		result.TotalSupersede += len(summary.SupersededIDs)
	}

	result.Latency = time.Since(start)
	return result, nil
}

// summarizeCluster processes a single cluster.
func summarizeCluster(ctx context.Context, provider llm.Provider, cluster ClusterInput) (*ClusterSummary, error) {
	start := time.Now()

	// Cap facts sent to LLM
	facts := cluster.Facts
	if len(facts) > summarizeMaxFacts {
		facts = facts[:summarizeMaxFacts]
	}

	prompt := buildSummarizePrompt(cluster.Name, facts)

	sumCtx, cancel := context.WithTimeout(ctx, summarizeTimeout)
	defer cancel()

	response, err := provider.Complete(sumCtx, prompt, llm.CompletionOpts{
		Temperature: 0.1,
		MaxTokens:   4096,
		System:      summarizeSystemPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM summarize call: %w", err)
	}

	parsed, err := parseSummarizeResponse(response)
	if err != nil {
		return nil, fmt.Errorf("parsing summarize response: %w", err)
	}

	// Validate summary facts
	validFacts := make([]SummaryFact, 0, len(parsed.SummaryFacts))
	supersededSet := make(map[int64]bool)

	for _, sf := range parsed.SummaryFacts {
		// Basic validation
		if sf.Predicate == "" || sf.Object == "" {
			continue
		}
		if !isValidFactType(sf.FactType) && sf.FactType != "config" {
			sf.FactType = "kv"
		}
		if sf.Confidence <= 0 || sf.Confidence > 1.0 {
			sf.Confidence = 0.8
		}
		if len(sf.Subject) > MaxSubjectLength {
			sf.Subject = truncateAtWordBoundary(sf.Subject, MaxSubjectLength)
		}

		// Track superseded IDs
		for _, id := range sf.Replaces {
			supersededSet[id] = true
		}

		validFacts = append(validFacts, sf)
	}

	supersededIDs := make([]int64, 0, len(supersededSet))
	for id := range supersededSet {
		supersededIDs = append(supersededIDs, id)
	}

	originalCount := len(facts)
	newCount := len(validFacts) + len(parsed.KeptAsIs)
	compression := 0.0
	if originalCount > 0 {
		compression = float64(originalCount) / float64(maxInt(newCount, 1))
	}

	return &ClusterSummary{
		ClusterID:     cluster.ID,
		ClusterName:   cluster.Name,
		SummaryFacts:  validFacts,
		KeptAsIs:      parsed.KeptAsIs,
		SupersededIDs: supersededIDs,
		OriginalCount: originalCount,
		NewCount:      newCount,
		Compression:   compression,
		Reasoning:     parsed.Reasoning,
		Latency:       time.Since(start),
	}, nil
}

// buildSummarizePrompt constructs the user message with cluster facts.
func buildSummarizePrompt(clusterName string, facts []ClusterFactInput) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("CLUSTER: %s (%d facts)\n\n", clusterName, len(facts)))
	sb.WriteString("FACTS:\n")

	for _, f := range facts {
		subj := f.Subject
		if subj == "" {
			subj = "(none)"
		}
		sb.WriteString(fmt.Sprintf("- id:%d [%s] %s → %s → %s (conf:%.2f",
			f.ID, f.FactType, subj, f.Predicate, truncateForPrompt(f.Object, 100), f.Confidence))
		if f.Source != "" {
			sb.WriteString(fmt.Sprintf(", src:%s", truncateForPrompt(f.Source, 40)))
		}
		sb.WriteString(")\n")
	}

	sb.WriteString("\nConsolidate redundant facts. Preserve all unique information. Return JSON only.")
	return sb.String()
}

// parseSummarizeResponse parses the LLM's JSON (with markdown stripping).
func parseSummarizeResponse(raw string) (*summarizeResponse, error) {
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

	var resp summarizeResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w\nraw: %s", err, truncateForError(raw, 300))
	}

	return &resp, nil
}

// maxInt returns the larger of two ints.
// Named to avoid conflict with Go 1.21+ builtin max.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
