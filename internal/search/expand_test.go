package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/llm"
)

// mockProvider implements llm.Provider for testing.
type mockProvider struct {
	name     string
	response string
	err      error
	delay    time.Duration
	called   int
}

func (m *mockProvider) Complete(ctx context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	m.called++
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock/test"
}

func TestExpandQueryBasic(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: `["ORB strategy decision locked", "trading configuration change", "options trading config"]`,
	}

	result, err := ExpandQuery(context.Background(), provider, "what was that trading thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Original != "what was that trading thing" {
		t.Errorf("original query wrong: %q", result.Original)
	}

	// Should include original + 3 expanded
	if len(result.Expanded) != 4 {
		t.Errorf("expected 4 queries (1 original + 3 expanded), got %d: %v", len(result.Expanded), result.Expanded)
	}

	if result.Expanded[0] != "what was that trading thing" {
		t.Errorf("first query should be original, got %q", result.Expanded[0])
	}

	if result.Cached {
		t.Error("should not be cached on first call")
	}

	if result.Latency <= 0 {
		t.Error("latency should be positive")
	}
}

func TestExpandQueryCache(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: `["expanded 1", "expanded 2"]`,
	}

	// First call
	result1, _ := ExpandQuery(context.Background(), provider, "test query")
	if result1.Cached {
		t.Error("first call should not be cached")
	}

	// Second call — should hit cache
	result2, _ := ExpandQuery(context.Background(), provider, "test query")
	if !result2.Cached {
		t.Error("second call should be cached")
	}

	// Provider should only be called once
	if provider.called != 1 {
		t.Errorf("provider called %d times, expected 1", provider.called)
	}

	// Case-insensitive cache
	result3, _ := ExpandQuery(context.Background(), provider, "Test Query")
	if !result3.Cached {
		t.Error("case-insensitive lookup should hit cache")
	}
}

func TestExpandQueryTimeout(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: `["slow result"]`,
		delay:    10 * time.Second, // longer than expandTimeout
	}

	result, err := ExpandQuery(context.Background(), provider, "slow query")
	if err != nil {
		t.Fatalf("should not return error on timeout, got: %v", err)
	}

	// Should fall back to original query only
	if len(result.Expanded) != 1 {
		t.Errorf("expected 1 query (original only) on timeout, got %d", len(result.Expanded))
	}
	if result.Expanded[0] != "slow query" {
		t.Errorf("fallback should be original query, got %q", result.Expanded[0])
	}
}

func TestExpandQueryLLMError(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		err: fmt.Errorf("API error: rate limited"),
	}

	result, err := ExpandQuery(context.Background(), provider, "error query")
	if err != nil {
		t.Fatalf("should not propagate LLM errors, got: %v", err)
	}

	if len(result.Expanded) != 1 || result.Expanded[0] != "error query" {
		t.Errorf("should fall back to original on error: %v", result.Expanded)
	}
}

func TestExpandQueryBadJSON(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: "not valid json at all",
	}

	result, err := ExpandQuery(context.Background(), provider, "bad json query")
	if err != nil {
		t.Fatalf("should not error on bad JSON: %v", err)
	}

	if len(result.Expanded) != 1 || result.Expanded[0] != "bad json query" {
		t.Errorf("should fall back on bad JSON: %v", result.Expanded)
	}
}

func TestExpandQueryMarkdownWrapped(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: "```json\n[\"query 1\", \"query 2\"]\n```",
	}

	result, _ := ExpandQuery(context.Background(), provider, "markdown test")
	// original + 2 expanded
	if len(result.Expanded) != 3 {
		t.Errorf("expected 3 queries, got %d: %v", len(result.Expanded), result.Expanded)
	}
}

func TestExpandQueryObjectResponse(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: `{"queries": ["from object 1", "from object 2"]}`,
	}

	result, _ := ExpandQuery(context.Background(), provider, "object response test")
	if len(result.Expanded) != 3 { // original + 2
		t.Errorf("expected 3 queries, got %d: %v", len(result.Expanded), result.Expanded)
	}
}

func TestExpandQueryDedup(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: `["dedup test", "Dedup Test", "unique one", "dedup test"]`,
	}

	result, _ := ExpandQuery(context.Background(), provider, "dedup test")
	// "dedup test" is the original, all duplicates should be removed
	// Should be: ["dedup test", "unique one"]
	if len(result.Expanded) != 2 {
		t.Errorf("expected 2 queries after dedup, got %d: %v", len(result.Expanded), result.Expanded)
	}
}

func TestExpandQueryMaxQueries(t *testing.T) {
	ResetExpandCache()

	// Return more than expandMaxQueries
	many := make([]string, 10)
	for i := range many {
		many[i] = fmt.Sprintf("query %d", i)
	}
	resp, _ := json.Marshal(many)

	provider := &mockProvider{
		response: string(resp),
	}

	result, _ := ExpandQuery(context.Background(), provider, "max test")
	// Should be capped at expandMaxQueries + 1 (original)
	if len(result.Expanded) > expandMaxQueries+1 {
		t.Errorf("expected max %d queries, got %d", expandMaxQueries+1, len(result.Expanded))
	}
}

func TestParseExpandResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"clean array", `["a", "b", "c"]`, 3, false},
		{"markdown wrapped", "```json\n[\"a\"]\n```", 1, false},
		{"object with queries key", `{"queries": ["a", "b"]}`, 2, false},
		{"object with expansions key", `{"expansions": ["a"]}`, 1, false},
		{"invalid json", "not json", 0, true},
		{"empty array", "[]", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseExpandResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != tt.want {
				t.Errorf("got %d results, want %d", len(result), tt.want)
			}
		})
	}
}

func TestExpandCacheEviction(t *testing.T) {
	cache := newExpandCache(3)

	cache.put("q1", []string{"a"})
	cache.put("q2", []string{"b"})
	cache.put("q3", []string{"c"})

	// All should be present
	if _, ok := cache.get("q1"); !ok {
		t.Error("q1 should be in cache")
	}

	// Adding q4 should evict q1
	cache.put("q4", []string{"d"})
	if _, ok := cache.get("q1"); ok {
		t.Error("q1 should have been evicted")
	}
	if _, ok := cache.get("q4"); !ok {
		t.Error("q4 should be in cache")
	}
}

func TestExpandQueryEmptyAndWhitespace(t *testing.T) {
	ResetExpandCache()

	provider := &mockProvider{
		response: `["  ", "", "valid query", "  "]`,
	}

	result, _ := ExpandQuery(context.Background(), provider, "whitespace test")
	// Should filter empty/whitespace, keep original + "valid query"
	for _, q := range result.Expanded {
		if strings.TrimSpace(q) == "" {
			t.Errorf("should not include empty/whitespace query: %q", q)
		}
	}
}
