package extract

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/llm"
)

// mockResolveProvider implements llm.Provider for testing.
type mockResolveProvider struct {
	response string
	err      error
	name     string
}

func (m *mockResolveProvider) Complete(ctx context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockResolveProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock/resolve-test"
}

func makePair(idx int, id1, id2 int64, obj1, obj2 string) ConflictPair {
	now := time.Now().UTC()
	return ConflictPair{
		Index: idx,
		Fact1: ConflictFact{
			ID:        id1,
			Subject:   "test",
			Predicate: "value",
			Object:    obj1,
			FactType:  "kv",
			Confidence: 0.9,
			Source:    "MEMORY.md",
			CreatedAt: now.Add(-24 * time.Hour),
		},
		Fact2: ConflictFact{
			ID:        id2,
			Subject:   "test",
			Predicate: "value",
			Object:    obj2,
			FactType:  "kv",
			Confidence: 0.85,
			Source:    "memory/2026-02-24.md",
			CreatedAt: now,
		},
	}
}

func TestResolveConflictsLLM_Supersede(t *testing.T) {
	provider := &mockResolveProvider{
		response: `{"resolutions": [{"pair_index": 0, "action": "supersede", "winner_id": 200, "loser_id": 100, "reason": "Fact 200 is newer", "confidence": 0.92}]}`,
	}

	pairs := []ConflictPair{makePair(0, 100, 200, "old value", "new value")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalPairs != 1 {
		t.Errorf("expected 1 total pair, got %d", result.TotalPairs)
	}
	if result.Superseded != 1 {
		t.Errorf("expected 1 superseded, got %d", result.Superseded)
	}
	if len(result.Resolutions) != 1 {
		t.Fatalf("expected 1 resolution, got %d", len(result.Resolutions))
	}
	r := result.Resolutions[0]
	if r.Action != "supersede" {
		t.Errorf("expected action supersede, got %s", r.Action)
	}
	if r.WinnerID != 200 {
		t.Errorf("expected winner 200, got %d", r.WinnerID)
	}
	if r.LoserID != 100 {
		t.Errorf("expected loser 100, got %d", r.LoserID)
	}
}

func TestResolveConflictsLLM_Merge(t *testing.T) {
	provider := &mockResolveProvider{
		response: `{"resolutions": [{"pair_index": 0, "action": "merge", "winner_id": 0, "loser_id": 0, "reason": "Both contain unique info", "confidence": 0.85, "merged_fact": {"subject": "test", "predicate": "value", "object": "combined", "type": "config"}}]}`,
	}

	pairs := []ConflictPair{makePair(0, 100, 200, "part A", "part B")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Merged != 1 {
		t.Errorf("expected 1 merged, got %d", result.Merged)
	}
	r := result.Resolutions[0]
	if r.MergedFact == nil {
		t.Fatal("expected merged fact, got nil")
	}
	if r.MergedFact.Object != "combined" {
		t.Errorf("expected merged object 'combined', got %q", r.MergedFact.Object)
	}
}

func TestResolveConflictsLLM_FlagHuman(t *testing.T) {
	provider := &mockResolveProvider{
		response: `{"resolutions": [{"pair_index": 0, "action": "flag-human", "winner_id": 0, "loser_id": 0, "reason": "Genuinely ambiguous", "confidence": 0.45}]}`,
	}

	pairs := []ConflictPair{makePair(0, 100, 200, "value A", "value B")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Flagged != 1 {
		t.Errorf("expected 1 flagged, got %d", result.Flagged)
	}
}

func TestResolveConflictsLLM_LowConfidence_ForcesFlag(t *testing.T) {
	// LLM says supersede but with confidence below threshold → should be flagged
	provider := &mockResolveProvider{
		response: `{"resolutions": [{"pair_index": 0, "action": "supersede", "winner_id": 200, "loser_id": 100, "reason": "Slightly newer", "confidence": 0.55}]}`,
	}

	pairs := []ConflictPair{makePair(0, 100, 200, "old", "new")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Flagged != 1 {
		t.Errorf("expected 1 flagged (low confidence), got %d", result.Flagged)
	}
	if result.Superseded != 0 {
		t.Errorf("expected 0 superseded (should be flagged), got %d", result.Superseded)
	}
	if result.Resolutions[0].Action != "flag-human" {
		t.Errorf("expected action flag-human, got %s", result.Resolutions[0].Action)
	}
}

func TestResolveConflictsLLM_NilProvider(t *testing.T) {
	_, err := ResolveConflictsLLM(context.Background(), nil, []ConflictPair{makePair(0, 1, 2, "a", "b")}, DefaultResolveOpts())
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestResolveConflictsLLM_EmptyPairs(t *testing.T) {
	provider := &mockResolveProvider{response: "{}"}
	result, err := ResolveConflictsLLM(context.Background(), provider, nil, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalPairs != 0 {
		t.Errorf("expected 0 pairs, got %d", result.TotalPairs)
	}
}

func TestResolveConflictsLLM_LLMError(t *testing.T) {
	provider := &mockResolveProvider{err: fmt.Errorf("API timeout")}

	pairs := []ConflictPair{makePair(0, 100, 200, "a", "b")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
}

func TestResolveConflictsLLM_InvalidJSON(t *testing.T) {
	provider := &mockResolveProvider{response: "not json at all"}

	pairs := []ConflictPair{makePair(0, 100, 200, "a", "b")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error, got %d", result.Errors)
	}
}

func TestResolveConflictsLLM_InvalidAction(t *testing.T) {
	provider := &mockResolveProvider{
		response: `{"resolutions": [{"pair_index": 0, "action": "delete", "winner_id": 100, "loser_id": 200, "reason": "bad action", "confidence": 0.9}]}`,
	}

	pairs := []ConflictPair{makePair(0, 100, 200, "a", "b")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Errors != 1 {
		t.Errorf("expected 1 error for invalid action, got %d", result.Errors)
	}
}

func TestResolveConflictsLLM_MarkdownFenced(t *testing.T) {
	provider := &mockResolveProvider{
		response: "```json\n{\"resolutions\": [{\"pair_index\": 0, \"action\": \"supersede\", \"winner_id\": 200, \"loser_id\": 100, \"reason\": \"newer\", \"confidence\": 0.9}]}\n```",
	}

	pairs := []ConflictPair{makePair(0, 100, 200, "old", "new")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Superseded != 1 {
		t.Errorf("expected 1 superseded from fenced JSON, got %d", result.Superseded)
	}
}

func TestResolveConflictsLLM_MultiplePairs(t *testing.T) {
	provider := &mockResolveProvider{
		response: `{"resolutions": [
			{"pair_index": 0, "action": "supersede", "winner_id": 2, "loser_id": 1, "reason": "newer", "confidence": 0.9},
			{"pair_index": 1, "action": "flag-human", "winner_id": 0, "loser_id": 0, "reason": "ambiguous", "confidence": 0.5},
			{"pair_index": 2, "action": "merge", "winner_id": 0, "loser_id": 0, "reason": "complementary", "confidence": 0.85, "merged_fact": {"subject": "x", "predicate": "y", "object": "z", "type": "state"}}
		]}`,
	}

	pairs := []ConflictPair{
		makePair(0, 1, 2, "old", "new"),
		makePair(1, 3, 4, "unclear A", "unclear B"),
		makePair(2, 5, 6, "part 1", "part 2"),
	}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalPairs != 3 {
		t.Errorf("expected 3 pairs, got %d", result.TotalPairs)
	}
	if result.Superseded != 1 {
		t.Errorf("expected 1 superseded, got %d", result.Superseded)
	}
	if result.Flagged != 1 {
		t.Errorf("expected 1 flagged, got %d", result.Flagged)
	}
	if result.Merged != 1 {
		t.Errorf("expected 1 merged, got %d", result.Merged)
	}
}

func TestResolveConflictsLLM_Concurrency(t *testing.T) {
	provider := &mockResolveProvider{
		response: `{"resolutions": [{"pair_index": 0, "action": "supersede", "winner_id": 2, "loser_id": 1, "reason": "newer", "confidence": 0.9}]}`,
	}

	// 10 pairs with batch size 2, concurrency 3 → should complete
	pairs := make([]ConflictPair, 10)
	for i := range pairs {
		pairs[i] = makePair(i, int64(i*2+1), int64(i*2+2), "old", "new")
	}

	opts := DefaultResolveOpts()
	opts.Concurrency = 3
	opts.BatchSize = 2

	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each batch returns 1 resolution (matching pair_index 0 only per batch)
	if result.Errors > 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}
	if result.Superseded == 0 {
		t.Error("expected some superseded resolutions")
	}
}

func TestResolveConflictsLLM_MergedFactInvalidType(t *testing.T) {
	provider := &mockResolveProvider{
		response: `{"resolutions": [{"pair_index": 0, "action": "merge", "winner_id": 0, "loser_id": 0, "reason": "combine", "confidence": 0.85, "merged_fact": {"subject": "x", "predicate": "y", "object": "z", "type": "invalid_type"}}]}`,
	}

	pairs := []ConflictPair{makePair(0, 1, 2, "a", "b")}
	result, err := ResolveConflictsLLM(context.Background(), provider, pairs, DefaultResolveOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Merged != 1 {
		t.Errorf("expected 1 merged, got %d", result.Merged)
	}
	// Invalid type should be fixed to "kv"
	if result.Resolutions[0].MergedFact.FactType != "kv" {
		t.Errorf("expected fallback type 'kv', got %q", result.Resolutions[0].MergedFact.FactType)
	}
}

func TestBuildResolvePrompt(t *testing.T) {
	pairs := []ConflictPair{makePair(0, 100, 200, "old value", "new value")}
	prompt := buildResolvePrompt(pairs)

	if !containsStr(prompt, "CONFLICT PAIR 0") {
		t.Error("prompt missing pair header")
	}
	if !containsStr(prompt, "id:100") {
		t.Error("prompt missing fact A ID")
	}
	if !containsStr(prompt, "id:200") {
		t.Error("prompt missing fact B ID")
	}
	if !containsStr(prompt, "MEMORY.md") {
		t.Error("prompt missing source metadata")
	}
}

func TestParseResolveResponse_Valid(t *testing.T) {
	raw := `{"resolutions": [{"pair_index": 0, "action": "supersede", "winner_id": 1, "loser_id": 2, "reason": "test", "confidence": 0.9}]}`
	entries, err := parseResolveResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Action != "supersede" {
		t.Errorf("expected supersede, got %s", entries[0].Action)
	}
}

func TestParseResolveResponse_InvalidJSON(t *testing.T) {
	_, err := parseResolveResponse("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && contains(s, substr)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
