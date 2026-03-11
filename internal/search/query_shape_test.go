package search

import (
	"context"
	"strings"
	"testing"
)

func TestShapeOperatorIntentQuery_LongWorkflowPrompt(t *testing.T) {
	query := strings.Join([]string{
		"Read HEARTBEAT.md if it exists (workspace context). Follow it strictly.",
		"Do not infer or repeat old tasks from prior chats.",
		"If nothing needs attention, reply HEARTBEAT_OK.",
		"Current time: Monday, March 9th, 2026 — 4:30 PM.",
		"Need operator intent query shaping for long workflow prompts in cortex search.",
		"Keep the slice bounded and dependency safe.",
	}, "\n")

	got := shapeOperatorIntentQuery(query)
	if !got.Applied {
		t.Fatalf("expected query shaping to apply for long workflow prompt")
	}
	if got.RemovedTokens <= 0 {
		t.Fatalf("expected removed tokens > 0, got %d", got.RemovedTokens)
	}
	if strings.Contains(got.Shaped, "heartbeat") {
		t.Fatalf("expected shaped query to remove heartbeat boilerplate, got %q", got.Shaped)
	}
	if !strings.Contains(got.Shaped, "operator") || !strings.Contains(got.Shaped, "workflow") {
		t.Fatalf("expected shaped query to retain core intent, got %q", got.Shaped)
	}
}

func TestShapeOperatorIntentQuery_ShortQueryNoop(t *testing.T) {
	query := "operator intent query shaping"
	got := shapeOperatorIntentQuery(query)
	if got.Applied {
		t.Fatalf("expected short query not to be shaped")
	}
	if got.Shaped != query {
		t.Fatalf("expected unchanged query %q, got %q", query, got.Shaped)
	}
}

func TestSearchExplain_AttachesQueryShape(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)
	engine := NewEngine(s)

	query := strings.Join([]string{
		"System post-compaction context refresh.",
		"Read HEARTBEAT.md if it exists and follow it strictly.",
		"Do not infer old tasks. Reply HEARTBEAT_OK if idle.",
		"Need Go memory management guidance for garbage collector workflow debugging.",
		"Operator wants long workflow prompt retrieval shaping.",
	}, "\n")

	results, err := engine.Search(context.Background(), query, Options{Mode: ModeKeyword, Limit: 5, Explain: true})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results")
	}
	if results[0].Explain == nil || results[0].Explain.QueryShape == nil {
		t.Fatalf("expected explain.query_shape payload")
	}
	qs := results[0].Explain.QueryShape
	if !qs.Applied {
		t.Fatalf("expected query_shape.applied=true")
	}
	if qs.Raw == "" || qs.Shaped == "" {
		t.Fatalf("expected raw and shaped query values, got raw=%q shaped=%q", qs.Raw, qs.Shaped)
	}
	if strings.Contains(strings.ToLower(qs.Shaped), "heartbeat") {
		t.Fatalf("expected shaped query without heartbeat boilerplate, got %q", qs.Shaped)
	}
	if !strings.Contains(results[0].Explain.Why, "query shaping applied") {
		t.Fatalf("expected explain.why to mention query shaping, got %q", results[0].Explain.Why)
	}
}
