package extract

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/llm"
)

// mockClassifyProvider implements llm.Provider for testing classification.
type mockClassifyProvider struct {
	response string
	err      error
	calls    int
}

func (m *mockClassifyProvider) Complete(_ context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockClassifyProvider) Name() string { return "mock/classify" }

func TestClassifyFacts_BasicReclassification(t *testing.T) {
	response := `{
		"classifications": [
			{"id": 1, "type": "decision", "confidence": 0.95},
			{"id": 2, "type": "relationship", "confidence": 0.9},
			{"id": 3, "type": "kv", "confidence": 0.85}
		]
	}`

	provider := &mockClassifyProvider{response: response}
	facts := []ClassifyableFact{
		{ID: 1, Subject: "Q", Predicate: "locked", Object: "ORB config", FactType: "kv"},
		{ID: 2, Subject: "Niot", Predicate: "works on", Object: "Eyes Web", FactType: "kv"},
		{ID: 3, Subject: "port", Predicate: "is", Object: "8090", FactType: "kv"},
	}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Facts 1 and 2 should be reclassified, 3 stays kv (same type)
	if len(result.Classified) != 2 {
		t.Fatalf("expected 2 reclassified, got %d", len(result.Classified))
	}
	if result.Unchanged != 1 {
		t.Errorf("expected 1 unchanged, got %d", result.Unchanged)
	}
	if result.TotalFacts != 3 {
		t.Errorf("expected 3 total, got %d", result.TotalFacts)
	}

	// Check specific reclassifications
	for _, c := range result.Classified {
		if c.OldType != "kv" {
			t.Errorf("expected old type 'kv', got %q", c.OldType)
		}
		switch c.FactID {
		case 1:
			if c.NewType != "decision" {
				t.Errorf("fact 1: expected 'decision', got %q", c.NewType)
			}
		case 2:
			if c.NewType != "relationship" {
				t.Errorf("fact 2: expected 'relationship', got %q", c.NewType)
			}
		}
	}
}

func TestClassifyFacts_LowConfidence_Skipped(t *testing.T) {
	response := `{
		"classifications": [
			{"id": 1, "type": "decision", "confidence": 0.6},
			{"id": 2, "type": "temporal", "confidence": 0.95}
		]
	}`

	provider := &mockClassifyProvider{response: response}
	facts := []ClassifyableFact{
		{ID: 1, Subject: "Q", Predicate: "locked", Object: "config", FactType: "kv"},
		{ID: 2, Subject: "token", Predicate: "expires", Object: "2027-07-28", FactType: "kv"},
	}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Classified) != 1 {
		t.Fatalf("expected 1 reclassified (low confidence skipped), got %d", len(result.Classified))
	}
	if result.Classified[0].FactID != 2 {
		t.Errorf("expected fact 2 to be classified, got fact %d", result.Classified[0].FactID)
	}
}

func TestClassifyFacts_EmptyInput(t *testing.T) {
	provider := &mockClassifyProvider{response: "{}"}

	result, err := ClassifyFacts(context.Background(), provider, nil, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalFacts != 0 {
		t.Errorf("expected 0 total facts, got %d", result.TotalFacts)
	}
	if provider.calls != 0 {
		t.Errorf("expected 0 LLM calls for empty input, got %d", provider.calls)
	}
}

func TestClassifyFacts_NilProvider(t *testing.T) {
	_, err := ClassifyFacts(context.Background(), nil, []ClassifyableFact{{ID: 1}}, DefaultClassifyOpts())
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestClassifyFacts_LLMError_GracefulHandling(t *testing.T) {
	provider := &mockClassifyProvider{err: fmt.Errorf("API rate limit")}
	facts := []ClassifyableFact{
		{ID: 1, Subject: "Q", Predicate: "has", Object: "thing", FactType: "kv"},
	}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("should not return error, should count as batch error: %v", err)
	}

	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
}

func TestClassifyFacts_InvalidType_Counted(t *testing.T) {
	response := `{
		"classifications": [
			{"id": 1, "type": "technology", "confidence": 0.9}
		]
	}`

	provider := &mockClassifyProvider{response: response}
	facts := []ClassifyableFact{
		{ID: 1, Subject: "Go", Predicate: "is", Object: "language", FactType: "kv"},
	}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Errors != 1 {
		t.Errorf("expected 1 error (invalid type), got %d", result.Errors)
	}
}

func TestClassifyFacts_ConfigType_Accepted(t *testing.T) {
	response := `{
		"classifications": [
			{"id": 1, "type": "config", "confidence": 0.9}
		]
	}`

	provider := &mockClassifyProvider{response: response}
	facts := []ClassifyableFact{
		{ID: 1, Subject: "port", Predicate: "is", Object: "8090", FactType: "kv"},
	}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Classified) != 1 {
		t.Fatalf("expected 1 classified (config is valid), got %d", len(result.Classified))
	}
	if result.Classified[0].NewType != "config" {
		t.Errorf("expected 'config', got %q", result.Classified[0].NewType)
	}
}

func TestClassifyFacts_BatchProcessing(t *testing.T) {
	callCount := 0
	provider := &mockClassifyProvider{}

	// Create 120 facts — should trigger 3 batches of 50
	facts := make([]ClassifyableFact, 120)
	for i := range facts {
		facts[i] = ClassifyableFact{
			ID: int64(i + 1), Subject: fmt.Sprintf("e%d", i),
			Predicate: "has", Object: "value", FactType: "kv",
		}
	}

	// Return empty classifications (all unchanged)
	provider.response = `{"classifications": []}`

	result, err := ClassifyFacts(context.Background(), provider, facts, ClassifyOpts{
		BatchSize:     50,
		MinConfidence: 0.8,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	callCount = provider.calls
	if callCount != 3 {
		t.Errorf("expected 3 batches, got %d LLM calls", callCount)
	}
	if result.BatchCount != 3 {
		t.Errorf("expected batch count 3, got %d", result.BatchCount)
	}
}

func TestClassifyFacts_MarkdownFencedResponse(t *testing.T) {
	response := "```json\n{\"classifications\": [{\"id\": 1, \"type\": \"decision\", \"confidence\": 0.9}]}\n```"

	provider := &mockClassifyProvider{response: response}
	facts := []ClassifyableFact{
		{ID: 1, Subject: "Q", Predicate: "locked", Object: "config", FactType: "kv"},
	}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Classified) != 1 {
		t.Fatalf("expected 1 classified, got %d", len(result.Classified))
	}
}

func TestClassifyFacts_UnknownID_Ignored(t *testing.T) {
	response := `{
		"classifications": [
			{"id": 999, "type": "decision", "confidence": 0.9},
			{"id": 1, "type": "temporal", "confidence": 0.85}
		]
	}`

	provider := &mockClassifyProvider{response: response}
	facts := []ClassifyableFact{
		{ID: 1, Subject: "token", Predicate: "expires", Object: "2027", FactType: "kv"},
	}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Classified) != 1 {
		t.Fatalf("expected 1 classified (unknown ID ignored), got %d", len(result.Classified))
	}
}

func TestClassifyFacts_CustomBatchSize(t *testing.T) {
	provider := &mockClassifyProvider{response: `{"classifications": []}`}

	facts := make([]ClassifyableFact, 10)
	for i := range facts {
		facts[i] = ClassifyableFact{ID: int64(i + 1), Subject: "x", Predicate: "y", Object: "z", FactType: "kv"}
	}

	_, err := ClassifyFacts(context.Background(), provider, facts, ClassifyOpts{
		BatchSize:     3,
		MinConfidence: 0.8,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 10 facts / 3 per batch = 4 batches (3+3+3+1)
	if provider.calls != 4 {
		t.Errorf("expected 4 batches for batch size 3, got %d", provider.calls)
	}
}

func TestBuildClassifyPrompt(t *testing.T) {
	facts := []ClassifyableFact{
		{ID: 42, Subject: "Q", Predicate: "locked", Object: "ORB config", FactType: "kv"},
		{ID: 43, Subject: "", Predicate: "port", Object: "8090", FactType: "kv"},
	}

	prompt := buildClassifyPrompt(facts)

	if !strings.Contains(prompt, "id:42") {
		t.Error("prompt should contain fact ID")
	}
	if !strings.Contains(prompt, "Q → locked → ORB config") {
		t.Error("prompt should contain fact triple")
	}
	if !strings.Contains(prompt, "(none)") {
		t.Error("prompt should show (none) for empty subject")
	}
	if !strings.Contains(prompt, "current: kv") {
		t.Error("prompt should show current type")
	}
}

func TestParseClassifyResponse_Valid(t *testing.T) {
	raw := `{"classifications": [{"id": 1, "type": "decision", "confidence": 0.9}]}`
	entries, err := parseClassifyResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].FactType != "decision" {
		t.Errorf("expected 'decision', got %q", entries[0].FactType)
	}
}

func TestParseClassifyResponse_InvalidJSON(t *testing.T) {
	_, err := parseClassifyResponse("not json")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClassifyFacts_TracksLatencyAndModel(t *testing.T) {
	provider := &mockClassifyProvider{response: `{"classifications": []}`}
	facts := []ClassifyableFact{{ID: 1, Subject: "x", Predicate: "y", Object: "z", FactType: "kv"}}

	result, err := ClassifyFacts(context.Background(), provider, facts, DefaultClassifyOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Latency <= 0 {
		t.Error("expected positive latency")
	}
	if result.Model != "mock/classify" {
		t.Errorf("expected model 'mock/classify', got %q", result.Model)
	}
}

func TestTruncateForPrompt(t *testing.T) {
	short := "hello"
	if got := truncateForPrompt(short, 10); got != short {
		t.Errorf("short string should be unchanged, got %q", got)
	}

	long := strings.Repeat("a", 100)
	got := truncateForPrompt(long, 50)
	if len(got) > 53 { // 50 + "…" (3 bytes UTF-8)
		t.Errorf("truncated string too long: %d", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated string should end with ellipsis")
	}
}
