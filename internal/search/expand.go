package search

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
	// expandTimeout is the maximum time to wait for LLM query expansion.
	expandTimeout = 5 * time.Second

	// expandCacheSize is the max number of cached expansions.
	expandCacheSize = 100

	// expandMaxQueries is the max number of expanded queries to generate.
	expandMaxQueries = 5
)

const expandSystemPrompt = `You are a search query expansion assistant for a personal knowledge base containing notes, decisions, daily logs, and technical documentation.

Given a vague or natural language query, generate 3-5 precise search queries that would find relevant results.

Rules:
- Each query should target different aspects of the user's intent
- Use specific keywords and proper nouns, not natural language
- Include dates, technical terms, and abbreviations when inferable
- Keep queries short (2-6 words each)
- Return ONLY a JSON array of strings, nothing else

Examples:
Input: "what was that trading thing we decided"
Output: ["ORB strategy decision locked", "trading configuration change", "options trading config February", "strategy parameters update"]

Input: "SB health stuff"  
Output: ["SB scleritis treatment", "Eyes Web health tracker", "anti-inflammatory diet", "SB medical conditions"]

Input: "when did we change the agent setup"
Output: ["agent architecture change", "Sage retired", "3-agent architecture", "agent model upgrade"]`

// ExpandResult holds the expansion output and metadata.
type ExpandResult struct {
	Original string   // The original query
	Expanded []string // 3-5 expanded queries (includes original)
	Latency  time.Duration
	Cached   bool
}

// expandCache is a simple LRU cache for query expansions.
type expandCache struct {
	mu      sync.Mutex
	entries map[string]*expandCacheEntry
	order   []string // oldest first
	maxSize int
}

type expandCacheEntry struct {
	expanded []string
	created  time.Time
}

func newExpandCache(maxSize int) *expandCache {
	return &expandCache{
		entries: make(map[string]*expandCacheEntry),
		maxSize: maxSize,
	}
}

func (c *expandCache) get(query string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(strings.TrimSpace(query))
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	// Expire after 1 hour
	if time.Since(entry.created) > time.Hour {
		delete(c.entries, key)
		return nil, false
	}
	return entry.expanded, true
}

func (c *expandCache) put(query string, expanded []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.ToLower(strings.TrimSpace(query))

	// Evict oldest if at capacity
	if len(c.entries) >= c.maxSize {
		if len(c.order) > 0 {
			oldest := c.order[0]
			delete(c.entries, oldest)
			c.order = c.order[1:]
		}
	}

	c.entries[key] = &expandCacheEntry{
		expanded: expanded,
		created:  time.Now(),
	}
	c.order = append(c.order, key)
}

// Global expansion cache (shared across calls within the same process).
var globalExpandCache = newExpandCache(expandCacheSize)

// ExpandQuery uses an LLM to expand a vague query into multiple precise search queries.
// Returns the original query prepended to the expansions.
// On error or timeout, returns just the original query (graceful fallback).
func ExpandQuery(ctx context.Context, provider llm.Provider, query string) (*ExpandResult, error) {
	result := &ExpandResult{Original: query}

	// Check cache first
	if cached, ok := globalExpandCache.get(query); ok {
		result.Expanded = cached
		result.Cached = true
		return result, nil
	}

	// Apply expansion timeout
	expandCtx, cancel := context.WithTimeout(ctx, expandTimeout)
	defer cancel()

	start := time.Now()

	resp, err := provider.Complete(expandCtx, query, llm.CompletionOpts{
		System:      expandSystemPrompt,
		MaxTokens:   200,
		Temperature: 0.3,
		// NOTE: We do NOT use Format:"json" here because thinking models
		// (Gemini 2.5/3) consume the JSON in their thinking phase and return
		// empty/placeholder text. The prompt instructs JSON-only output, and
		// parseExpandResponse handles markdown-wrapped responses.
	})

	result.Latency = time.Since(start)

	if err != nil {
		// Graceful fallback: return original query only
		result.Expanded = []string{query}
		return result, nil
	}

	// Parse JSON array response
	expanded, parseErr := parseExpandResponse(resp)
	if parseErr != nil {
		// Fallback on bad JSON
		result.Expanded = []string{query}
		return result, nil
	}

	// Prepend original query and deduplicate
	allQueries := []string{query}
	seen := map[string]bool{strings.ToLower(query): true}
	for _, q := range expanded {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		lower := strings.ToLower(q)
		if !seen[lower] {
			allQueries = append(allQueries, q)
			seen[lower] = true
		}
		if len(allQueries) >= expandMaxQueries+1 { // +1 for original
			break
		}
	}

	result.Expanded = allQueries

	// Cache the result
	globalExpandCache.put(query, allQueries)

	return result, nil
}

// parseExpandResponse parses the LLM response into a string slice.
// Handles both clean JSON arrays and markdown-wrapped responses.
func parseExpandResponse(resp string) ([]string, error) {
	resp = strings.TrimSpace(resp)

	// Strip markdown code fences if present
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

	// Try parsing as JSON array
	var queries []string
	if err := json.Unmarshal([]byte(resp), &queries); err != nil {
		// Try extracting from a JSON object with a "queries" key
		var obj map[string]json.RawMessage
		if err2 := json.Unmarshal([]byte(resp), &obj); err2 == nil {
			for _, key := range []string{"queries", "expansions", "results", "search_queries"} {
				if raw, ok := obj[key]; ok {
					if err3 := json.Unmarshal(raw, &queries); err3 == nil {
						return queries, nil
					}
				}
			}
		}
		return nil, fmt.Errorf("failed to parse expansion response as JSON array: %w", err)
	}

	return queries, nil
}

// ResetExpandCache clears the expansion cache. Used in testing.
func ResetExpandCache() {
	globalExpandCache = newExpandCache(expandCacheSize)
}
