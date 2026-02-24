package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/llm"
)

// mockEnrichProvider implements llm.Provider for testing enrichment.
type mockEnrichProvider struct {
	response string
	err      error
	calls    int
	lastOpts llm.CompletionOpts
}

func (m *mockEnrichProvider) Complete(_ context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	m.calls++
	m.lastOpts = opts
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockEnrichProvider) Name() string {
	return "mock/test-model"
}

func TestEnrichFacts_BasicEnrichment(t *testing.T) {
	// Mock LLM returns one new fact that rules missed
	response := `{
		"facts": [
			{
				"subject": "Q",
				"predicate": "decided because",
				"object": "IEX volume filter was blocking profitable trades",
				"type": "decision",
				"confidence": 0.9,
				"source_quote": "removing the IEX volume filter turned -$1.6K into +$22.8K"
			}
		],
		"reasoning": "The rules captured the config change but missed the WHY — the IEX filter was the root cause."
	}`

	provider := &mockEnrichProvider{response: response}

	chunk := "Q locked the ORB config because removing the IEX volume filter turned -$1.6K into +$22.8K. The volume filter only showed ~3% of real volume."
	ruleFacts := []ExtractedFact{
		{Subject: "Q", Predicate: "locked", Object: "ORB config", FactType: "decision", Confidence: 0.8},
	}

	result, err := EnrichFacts(context.Background(), provider, chunk, ruleFacts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) != 1 {
		t.Fatalf("expected 1 new fact, got %d", len(result.NewFacts))
	}

	fact := result.NewFacts[0]
	if fact.Subject != "Q" {
		t.Errorf("expected subject 'Q', got %q", fact.Subject)
	}
	if fact.ExtractionMethod != "llm-enrich" {
		t.Errorf("expected extraction_method 'llm-enrich', got %q", fact.ExtractionMethod)
	}
	if fact.FactType != "decision" {
		t.Errorf("expected type 'decision', got %q", fact.FactType)
	}
	if fact.Confidence != 0.9 {
		t.Errorf("expected confidence 0.9, got %.2f", fact.Confidence)
	}
	if fact.DecayRate != DecayRates["decision"] {
		t.Errorf("expected decay rate %.3f, got %.3f", DecayRates["decision"], fact.DecayRate)
	}
	if result.Reasoning == "" {
		t.Error("expected non-empty reasoning")
	}
	if result.Model != "mock/test-model" {
		t.Errorf("expected model 'mock/test-model', got %q", result.Model)
	}
}

func TestEnrichFacts_DeduplicatesAgainstRules(t *testing.T) {
	// LLM returns a fact that's already in rule facts — should be filtered
	response := `{
		"facts": [
			{
				"subject": "Q",
				"predicate": "locked",
				"object": "ORB config",
				"type": "decision",
				"confidence": 0.95,
				"source_quote": "Q locked the ORB config"
			},
			{
				"subject": "IEX",
				"predicate": "shows only",
				"object": "3% of real volume",
				"type": "kv",
				"confidence": 0.85,
				"source_quote": "volume filter only showed ~3% of real volume"
			}
		],
		"reasoning": "Found duplicate + one new fact"
	}`

	provider := &mockEnrichProvider{response: response}

	chunk := "Q locked the ORB config. The volume filter only showed ~3% of real volume."
	ruleFacts := []ExtractedFact{
		{Subject: "Q", Predicate: "locked", Object: "ORB config", FactType: "decision", Confidence: 0.8},
	}

	result, err := EnrichFacts(context.Background(), provider, chunk, ruleFacts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the non-duplicate fact should remain
	if len(result.NewFacts) != 1 {
		t.Fatalf("expected 1 new fact (duplicate removed), got %d", len(result.NewFacts))
	}
	if result.NewFacts[0].Subject != "IEX" {
		t.Errorf("expected subject 'IEX', got %q", result.NewFacts[0].Subject)
	}
}

func TestEnrichFacts_EmptyResponse(t *testing.T) {
	// LLM finds nothing new
	response := `{"facts": [], "reasoning": "Rules already captured everything."}`

	provider := &mockEnrichProvider{response: response}

	chunk := "The meeting is at 3pm."
	ruleFacts := []ExtractedFact{
		{Subject: "", Predicate: "meeting time", Object: "3pm", FactType: "temporal", Confidence: 0.9},
	}

	result, err := EnrichFacts(context.Background(), provider, chunk, ruleFacts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) != 0 {
		t.Errorf("expected 0 new facts, got %d", len(result.NewFacts))
	}
	if result.Reasoning == "" {
		t.Error("expected reasoning even when no new facts")
	}
}

func TestEnrichFacts_LLMError_GracefulFallback(t *testing.T) {
	provider := &mockEnrichProvider{err: fmt.Errorf("API rate limit exceeded")}

	chunk := "some text"
	result, err := EnrichFacts(context.Background(), provider, chunk, nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error should mention rate limit, got: %v", err)
	}
}

func TestEnrichFacts_NilProvider(t *testing.T) {
	_, err := EnrichFacts(context.Background(), nil, "text", nil)
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestEnrichFacts_InvalidFactType_Fallback(t *testing.T) {
	// LLM returns an invalid fact type — should fallback to "kv"
	response := `{
		"facts": [
			{
				"subject": "system",
				"predicate": "uses",
				"object": "SQLite",
				"type": "technology",
				"confidence": 0.8,
				"source_quote": "uses SQLite for storage"
			}
		],
		"reasoning": "Found technology fact"
	}`

	provider := &mockEnrichProvider{response: response}

	result, err := EnrichFacts(context.Background(), provider, "The system uses SQLite for storage.", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(result.NewFacts))
	}
	if result.NewFacts[0].FactType != "kv" {
		t.Errorf("expected fallback to 'kv', got %q", result.NewFacts[0].FactType)
	}
}

func TestEnrichFacts_InvalidConfidence_Defaults(t *testing.T) {
	response := `{
		"facts": [
			{
				"subject": "test",
				"predicate": "has",
				"object": "value",
				"type": "kv",
				"confidence": -0.5,
				"source_quote": "test has value"
			},
			{
				"subject": "test2",
				"predicate": "has",
				"object": "value2",
				"type": "kv",
				"confidence": 1.5,
				"source_quote": "test2 has value2"
			}
		],
		"reasoning": "bad confidence values"
	}`

	provider := &mockEnrichProvider{response: response}

	result, err := EnrichFacts(context.Background(), provider, "test has value. test2 has value2.", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, f := range result.NewFacts {
		if f.Confidence != 0.7 {
			t.Errorf("expected default confidence 0.7, got %.2f for %q", f.Confidence, f.Subject)
		}
	}
}

func TestEnrichFacts_SkipsEmptyPredicateOrObject(t *testing.T) {
	response := `{
		"facts": [
			{
				"subject": "good",
				"predicate": "has",
				"object": "value",
				"type": "kv",
				"confidence": 0.8,
				"source_quote": "good has value"
			},
			{
				"subject": "bad1",
				"predicate": "",
				"object": "value",
				"type": "kv",
				"confidence": 0.8,
				"source_quote": "bad1"
			},
			{
				"subject": "bad2",
				"predicate": "has",
				"object": "",
				"type": "kv",
				"confidence": 0.8,
				"source_quote": "bad2"
			}
		],
		"reasoning": "mixed validity"
	}`

	provider := &mockEnrichProvider{response: response}

	result, err := EnrichFacts(context.Background(), provider, "good has value", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) != 1 {
		t.Fatalf("expected 1 valid fact, got %d", len(result.NewFacts))
	}
	if result.NewFacts[0].Subject != "good" {
		t.Errorf("expected subject 'good', got %q", result.NewFacts[0].Subject)
	}
}

func TestEnrichFacts_MarkdownFencedResponse(t *testing.T) {
	// LLM wraps JSON in markdown code fence
	response := "```json\n" + `{
		"facts": [
			{
				"subject": "Cortex",
				"predicate": "written in",
				"object": "Go",
				"type": "kv",
				"confidence": 0.95,
				"source_quote": "Cortex is written in Go"
			}
		],
		"reasoning": "Found tech stack fact"
	}` + "\n```"

	provider := &mockEnrichProvider{response: response}

	result, err := EnrichFacts(context.Background(), provider, "Cortex is written in Go", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(result.NewFacts))
	}
}

func TestEnrichFacts_LongSubjectTruncated(t *testing.T) {
	longSubject := strings.Repeat("A", 100) // Way over MaxSubjectLength
	response := fmt.Sprintf(`{
		"facts": [
			{
				"subject": "%s",
				"predicate": "has",
				"object": "property",
				"type": "kv",
				"confidence": 0.8,
				"source_quote": "long subject has property"
			}
		],
		"reasoning": "long subject"
	}`, longSubject)

	provider := &mockEnrichProvider{response: response}

	result, err := EnrichFacts(context.Background(), provider, "long subject has property", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(result.NewFacts))
	}
	if len(result.NewFacts[0].Subject) > MaxSubjectLength {
		t.Errorf("subject should be truncated to %d, got %d", MaxSubjectLength, len(result.NewFacts[0].Subject))
	}
}

func TestEnrichFacts_TracksLatency(t *testing.T) {
	response := `{"facts": [], "reasoning": "nothing"}`
	provider := &mockEnrichProvider{response: response}

	result, err := EnrichFacts(context.Background(), provider, "text", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Latency <= 0 {
		t.Error("expected positive latency")
	}
	if result.Latency > 5*time.Second {
		t.Error("latency suspiciously high for mock")
	}
}

func TestEnrichFacts_SetsSystemPrompt(t *testing.T) {
	response := `{"facts": [], "reasoning": "n/a"}`
	provider := &mockEnrichProvider{response: response}

	_, err := EnrichFacts(context.Background(), provider, "text", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.lastOpts.System == "" {
		t.Error("expected system prompt to be set")
	}
	if !strings.Contains(provider.lastOpts.System, "fact enrichment") {
		t.Error("system prompt should mention fact enrichment")
	}
}

func TestEnrichFacts_NoRuleFacts(t *testing.T) {
	response := `{
		"facts": [
			{
				"subject": "Alice",
				"predicate": "email",
				"object": "alice@test.com",
				"type": "identity",
				"confidence": 1.0,
				"source_quote": "Alice (alice@test.com)"
			}
		],
		"reasoning": "No existing facts — found identity fact."
	}`

	provider := &mockEnrichProvider{response: response}

	result, err := EnrichFacts(context.Background(), provider, "Alice (alice@test.com) is here.", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(result.NewFacts))
	}
}

func TestBuildEnrichPrompt_WithFacts(t *testing.T) {
	facts := []ExtractedFact{
		{Subject: "Q", Predicate: "lives in", Object: "Philadelphia", FactType: "location"},
		{Subject: "SB", Predicate: "role", Object: "co-founder", FactType: "relationship"},
	}

	prompt := buildEnrichPrompt("Q and SB founded Spear in Philadelphia.", facts)

	if !strings.Contains(prompt, "TEXT CHUNK:") {
		t.Error("prompt should contain TEXT CHUNK header")
	}
	if !strings.Contains(prompt, "EXISTING FACTS") {
		t.Error("prompt should contain EXISTING FACTS header")
	}
	if !strings.Contains(prompt, "Philadelphia") {
		t.Error("prompt should include fact objects")
	}
	if !strings.Contains(prompt, "do NOT duplicate") {
		t.Error("prompt should warn against duplication")
	}
}

func TestBuildEnrichPrompt_NoFacts(t *testing.T) {
	prompt := buildEnrichPrompt("Some text.", nil)

	if !strings.Contains(prompt, "none") {
		t.Error("prompt should indicate no existing facts")
	}
}

func TestBuildEnrichPrompt_ManyFacts_Truncates(t *testing.T) {
	facts := make([]ExtractedFact, 50)
	for i := range facts {
		facts[i] = ExtractedFact{
			Subject: fmt.Sprintf("entity_%d", i), Predicate: "has", Object: "value", FactType: "kv",
		}
	}

	prompt := buildEnrichPrompt("text", facts)

	if !strings.Contains(prompt, "and 20 more facts") {
		t.Error("prompt should indicate truncated facts")
	}
}

func TestParseEnrichResponse_Valid(t *testing.T) {
	raw := `{"facts": [{"subject": "X", "predicate": "is", "object": "Y", "type": "kv", "confidence": 0.8, "source_quote": "X is Y"}], "reasoning": "test"}`

	resp, err := parseEnrichResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(resp.Facts))
	}
	if resp.Reasoning != "test" {
		t.Errorf("expected reasoning 'test', got %q", resp.Reasoning)
	}
}

func TestParseEnrichResponse_MarkdownFence(t *testing.T) {
	raw := "```json\n{\"facts\": [], \"reasoning\": \"empty\"}\n```"

	resp, err := parseEnrichResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Reasoning != "empty" {
		t.Errorf("expected reasoning 'empty', got %q", resp.Reasoning)
	}
}

func TestParseEnrichResponse_InvalidJSON(t *testing.T) {
	_, err := parseEnrichResponse("this is not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestIsDuplicateOfRuleFact(t *testing.T) {
	tests := []struct {
		name      string
		candidate ExtractedFact
		rules     []ExtractedFact
		want      bool
	}{
		{
			name:      "exact match",
			candidate: ExtractedFact{Subject: "Q", Predicate: "lives in", Object: "Philadelphia"},
			rules:     []ExtractedFact{{Subject: "Q", Predicate: "lives in", Object: "Philadelphia"}},
			want:      true,
		},
		{
			name:      "case insensitive match",
			candidate: ExtractedFact{Subject: "q", Predicate: "Lives In", Object: "philadelphia"},
			rules:     []ExtractedFact{{Subject: "Q", Predicate: "lives in", Object: "Philadelphia"}},
			want:      true,
		},
		{
			name:      "contained object",
			candidate: ExtractedFact{Subject: "Q", Predicate: "lives in", Object: "Philadelphia, PA"},
			rules:     []ExtractedFact{{Subject: "Q", Predicate: "lives in", Object: "Philadelphia"}},
			want:      true,
		},
		{
			name:      "different subject",
			candidate: ExtractedFact{Subject: "SB", Predicate: "lives in", Object: "Philadelphia"},
			rules:     []ExtractedFact{{Subject: "Q", Predicate: "lives in", Object: "Philadelphia"}},
			want:      false,
		},
		{
			name:      "different predicate and object",
			candidate: ExtractedFact{Subject: "Q", Predicate: "works at", Object: "Spear"},
			rules:     []ExtractedFact{{Subject: "Q", Predicate: "lives in", Object: "Philadelphia"}},
			want:      false,
		},
		{
			name:      "empty rules",
			candidate: ExtractedFact{Subject: "Q", Predicate: "has", Object: "thing"},
			rules:     nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDuplicateOfRuleFact(tt.candidate, tt.rules)
			if got != tt.want {
				t.Errorf("isDuplicateOfRuleFact() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsValidFactType(t *testing.T) {
	valid := []string{"kv", "relationship", "preference", "temporal", "identity", "location", "decision", "state"}
	for _, ft := range valid {
		if !isValidFactType(ft) {
			t.Errorf("expected %q to be valid", ft)
		}
	}

	invalid := []string{"technology", "misc", "", "KV", "Relationship"}
	for _, ft := range invalid {
		if isValidFactType(ft) {
			t.Errorf("expected %q to be invalid", ft)
		}
	}
}

func TestEnrichFacts_MultipleRelationships(t *testing.T) {
	// Test that enrichment can find implicit relationships
	response := `{
		"facts": [
			{
				"subject": "SB",
				"predicate": "connected to",
				"object": "Eyes Web",
				"type": "relationship",
				"confidence": 0.85,
				"source_quote": "SB needs this for Eyes Web"
			},
			{
				"subject": "Eyes Web",
				"predicate": "domain",
				"object": "health tracking",
				"type": "relationship",
				"confidence": 0.8,
				"source_quote": "personalized anti-inflammatory health companion"
			}
		],
		"reasoning": "Found implicit relationship chain: SB → Eyes Web → health"
	}`

	provider := &mockEnrichProvider{response: response}

	chunk := "SB needs this for Eyes Web, our personalized anti-inflammatory health companion."
	ruleFacts := []ExtractedFact{
		{Subject: "Eyes Web", Predicate: "is", Object: "health companion", FactType: "kv"},
	}

	result, err := EnrichFacts(context.Background(), provider, chunk, ruleFacts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewFacts) < 1 {
		t.Fatalf("expected at least 1 new relationship fact, got %d", len(result.NewFacts))
	}

	// Check that at least one is a relationship type
	foundRelationship := false
	for _, f := range result.NewFacts {
		if f.FactType == "relationship" {
			foundRelationship = true
			break
		}
	}
	if !foundRelationship {
		t.Error("expected at least one relationship fact")
	}
}

// TestEnrichResponseRoundTrip verifies our JSON schema is consistent.
func TestEnrichResponseRoundTrip(t *testing.T) {
	resp := enrichResponse{
		Facts: []enrichFact{
			{
				Subject:     "test",
				Predicate:   "has",
				Object:      "value",
				FactType:    "kv",
				Confidence:  0.8,
				SourceQuote: "test has value",
			},
		},
		Reasoning: "found a fact",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	parsed, err := parseEnrichResponse(string(data))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(parsed.Facts) != 1 || parsed.Facts[0].Subject != "test" {
		t.Error("round-trip failed")
	}
}
