package observe

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/llm"
	"github.com/hurttlocker/cortex/internal/store"
)

// mockLLM implements llm.Provider for testing.
type mockLLM struct {
	name     string
	response string
	err      error
	delay    time.Duration
	calls    int
}

func (m *mockLLM) Complete(ctx context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	m.calls++
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

func (m *mockLLM) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock/test"
}

func makeConflict(id1, id2 int64, subject, predicate, obj1, obj2 string) Conflict {
	now := time.Now().UTC()
	return Conflict{
		Fact1: store.Fact{
			ID:             id1,
			MemoryID:       100,
			Subject:        subject,
			Predicate:      predicate,
			Object:         obj1,
			FactType:       "kv",
			Confidence:     0.7,
			DecayRate:      0.01,
			LastReinforced: now.Add(-24 * time.Hour),
			CreatedAt:      now.Add(-48 * time.Hour),
		},
		Fact2: store.Fact{
			ID:             id2,
			MemoryID:       200,
			Subject:        subject,
			Predicate:      predicate,
			Object:         obj2,
			FactType:       "kv",
			Confidence:     0.7,
			DecayRate:      0.01,
			LastReinforced: now,
			CreatedAt:      now,
		},
		ConflictType: "attribute",
	}
}

func TestParseLLMResolution_Valid(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		winner int
		action string
		conf   float64
	}{
		{
			"clean json",
			`{"winner": 2, "action": "supersede", "confidence": 0.9, "reason": "newer fact"}`,
			2, "supersede", 0.9,
		},
		{
			"markdown wrapped",
			"```json\n{\"winner\": 1, \"action\": \"flag\", \"confidence\": 0.5, \"reason\": \"ambiguous\"}\n```",
			1, "flag", 0.5,
		},
		{
			"missing action defaults to supersede",
			`{"winner": 1, "confidence": 0.85, "reason": "clear winner"}`,
			1, "supersede", 0.85,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := parseLLMResolution(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Winner != tt.winner {
				t.Errorf("winner: got %d, want %d", res.Winner, tt.winner)
			}
			if res.Action != tt.action {
				t.Errorf("action: got %q, want %q", res.Action, tt.action)
			}
			if res.Confidence != tt.conf {
				t.Errorf("confidence: got %.2f, want %.2f", res.Confidence, tt.conf)
			}
		})
	}
}

func TestParseLLMResolution_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"not json", "I think fact 1 is better"},
		{"winner 0", `{"winner": 0, "confidence": 0.9, "reason": "test"}`},
		{"winner 3", `{"winner": 3, "confidence": 0.9, "reason": "test"}`},
		{"confidence > 1", `{"winner": 1, "confidence": 1.5, "reason": "test"}`},
		{"confidence < 0", `{"winner": 1, "confidence": -0.1, "reason": "test"}`},
		{"empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLLMResolution(tt.input)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestFormatConflictPrompt(t *testing.T) {
	c := makeConflict(1, 2, "Alpaca Paper Account", "balance", "$10,000", "$100,000")
	c.Fact1.SourceQuote = "account has $10K"
	c.Fact2.SourceQuote = "new $100K account created"

	prompt := formatConflictPrompt(c)

	// Should contain both fact values
	if !containsStr(prompt, "$10,000") {
		t.Error("prompt should contain fact1 value")
	}
	if !containsStr(prompt, "$100,000") {
		t.Error("prompt should contain fact2 value")
	}
	if !containsStr(prompt, "Alpaca Paper Account") {
		t.Error("prompt should contain subject")
	}
	if !containsStr(prompt, "account has $10K") {
		t.Error("prompt should contain source quote")
	}
}

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && contains(s, substr)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub) >= 0
}

func searchStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestResolveLLMOne_PicksWinner(t *testing.T) {
	provider := &mockLLM{
		response: `{"winner": 2, "action": "supersede", "confidence": 0.92, "reason": "Fact 2 is newer and from authoritative source"}`,
	}

	// Create a resolver with a nil store (we won't apply in this test)
	resolver := &Resolver{}
	c := makeConflict(100, 200, "Account", "balance", "$10K", "$100K")

	res := resolver.resolveLLMOne(context.Background(), c, provider)

	if res.Winner != "fact2" {
		t.Errorf("expected fact2, got %q", res.Winner)
	}
	if res.WinnerID != 200 {
		t.Errorf("expected winner ID 200, got %d", res.WinnerID)
	}
	if res.LoserID != 100 {
		t.Errorf("expected loser ID 100, got %d", res.LoserID)
	}
	if res.Strategy != StrategyLLM {
		t.Errorf("expected strategy llm, got %q", res.Strategy)
	}
}

func TestResolveLLMOne_LowConfidenceFlags(t *testing.T) {
	provider := &mockLLM{
		response: `{"winner": 1, "action": "supersede", "confidence": 0.5, "reason": "unclear which is right"}`,
	}

	resolver := &Resolver{}
	c := makeConflict(100, 200, "Config", "status", "locked", "unlocked")

	res := resolver.resolveLLMOne(context.Background(), c, provider)

	if res.Winner != "manual" {
		t.Errorf("low confidence should flag for manual, got %q", res.Winner)
	}
}

func TestResolveLLMOne_FlagAction(t *testing.T) {
	provider := &mockLLM{
		response: `{"winner": 1, "action": "flag", "confidence": 0.85, "reason": "genuinely ambiguous"}`,
	}

	resolver := &Resolver{}
	c := makeConflict(100, 200, "Strategy", "status", "active", "paused")

	res := resolver.resolveLLMOne(context.Background(), c, provider)

	if res.Winner != "manual" {
		t.Errorf("flag action should result in manual, got %q", res.Winner)
	}
}

func TestResolveLLMOne_LLMError(t *testing.T) {
	provider := &mockLLM{
		err: fmt.Errorf("rate limited"),
	}

	resolver := &Resolver{}
	c := makeConflict(100, 200, "Test", "value", "a", "b")

	res := resolver.resolveLLMOne(context.Background(), c, provider)

	if res.Winner != "manual" {
		t.Errorf("LLM error should flag for manual, got %q", res.Winner)
	}
}

func TestResolveLLMOne_BadJSON(t *testing.T) {
	provider := &mockLLM{
		response: "I think fact 1 is better because it's newer",
	}

	resolver := &Resolver{}
	c := makeConflict(100, 200, "Test", "value", "a", "b")

	res := resolver.resolveLLMOne(context.Background(), c, provider)

	if res.Winner != "manual" {
		t.Errorf("bad JSON should flag for manual, got %q", res.Winner)
	}
}

func TestResolveLLMBatch_DryRun(t *testing.T) {
	provider := &mockLLM{
		response: `{"winner": 2, "action": "supersede", "confidence": 0.9, "reason": "newer"}`,
	}

	resolver := &Resolver{}
	conflicts := []Conflict{
		makeConflict(1, 2, "A", "x", "old", "new"),
		makeConflict(3, 4, "B", "y", "old", "new"),
	}

	batch, err := resolver.ResolveLLM(context.Background(), conflicts, provider, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if batch.Total != 2 {
		t.Errorf("total: got %d, want 2", batch.Total)
	}
	// In dry run, resolved count still increments but Applied should be false
	for _, r := range batch.Results {
		if r.Applied {
			t.Error("dry run should not apply resolutions")
		}
	}
	if provider.calls != 2 {
		t.Errorf("provider should be called twice, got %d", provider.calls)
	}
}

func TestResolveLLMBatch_MixedResults(t *testing.T) {
	callCount := 0
	provider := &mockLLM{
		name: "mock/mixed",
	}

	// Override response per call
	origComplete := provider.Complete
	_ = origComplete // suppress unused
	responses := []string{
		`{"winner": 2, "action": "supersede", "confidence": 0.95, "reason": "clear update"}`,
		`{"winner": 1, "action": "flag", "confidence": 0.4, "reason": "ambiguous"}`,
		`not valid json at all`,
	}

	mockProvider := &sequentialMockLLM{responses: responses}

	resolver := &Resolver{}
	conflicts := []Conflict{
		makeConflict(1, 2, "A", "x", "old", "new"),
		makeConflict(3, 4, "B", "y", "old", "new"),
		makeConflict(5, 6, "C", "z", "old", "new"),
	}

	batch, err := resolver.ResolveLLM(context.Background(), conflicts, mockProvider, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_ = callCount

	if batch.Total != 3 {
		t.Errorf("total: got %d, want 3", batch.Total)
	}

	// First: resolved (confidence 0.95)
	if batch.Results[0].Winner != "fact2" {
		t.Errorf("result 0: expected fact2, got %q", batch.Results[0].Winner)
	}

	// Second: flagged (action=flag)
	if batch.Results[1].Winner != "manual" {
		t.Errorf("result 1: expected manual (flagged), got %q", batch.Results[1].Winner)
	}

	// Third: flagged (bad JSON)
	if batch.Results[2].Winner != "manual" {
		t.Errorf("result 2: expected manual (bad json), got %q", batch.Results[2].Winner)
	}
}

// sequentialMockLLM returns different responses for each call.
type sequentialMockLLM struct {
	responses []string
	idx       int
}

func (m *sequentialMockLLM) Complete(ctx context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	if m.idx >= len(m.responses) {
		return "", fmt.Errorf("no more responses")
	}
	resp := m.responses[m.idx]
	m.idx++
	return resp, nil
}

func (m *sequentialMockLLM) Name() string {
	return "mock/sequential"
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Error("short string should not be truncated")
	}
	if truncate("a very long string", 5) != "a ver..." {
		t.Errorf("got %q", truncate("a very long string", 5))
	}
}
