package extract

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/llm"
)

// mockSummarizeProvider implements llm.Provider for testing summarization.
type mockSummarizeProvider struct {
	response string
	err      error
	calls    int
}

func (m *mockSummarizeProvider) Complete(_ context.Context, prompt string, opts llm.CompletionOpts) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockSummarizeProvider) Name() string { return "mock/summarize" }

func TestSummarizeClusters_BasicConsolidation(t *testing.T) {
	response := `{
		"summary_facts": [
			{
				"subject": "Q",
				"predicate": "lives in",
				"object": "Philadelphia, PA 19147",
				"type": "location",
				"confidence": 0.95,
				"replaces": [1, 2, 3],
				"reasoning": "Merged 3 address facts into most specific version"
			}
		],
		"kept_as_is": [4, 5],
		"reasoning": "3 address facts consolidated into 1, 2 unique facts kept"
	}`

	provider := &mockSummarizeProvider{response: response}
	clusters := []ClusterInput{
		{
			ID:   1,
			Name: "Q Personal",
			Facts: []ClusterFactInput{
				{ID: 1, Subject: "Q", Predicate: "lives in", Object: "Philadelphia", FactType: "location", Confidence: 0.8},
				{ID: 2, Subject: "Q", Predicate: "address", Object: "1001 S Broad St", FactType: "identity", Confidence: 0.9},
				{ID: 3, Subject: "Q", Predicate: "lives in", Object: "PA", FactType: "location", Confidence: 0.7},
				{ID: 4, Subject: "Q", Predicate: "DOB", Object: "12/25/1994", FactType: "identity", Confidence: 1.0},
				{ID: 5, Subject: "Q", Predicate: "phone", Object: "267-995-1461", FactType: "identity", Confidence: 1.0},
			},
		},
	}

	result, err := SummarizeClusters(context.Background(), provider, clusters, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(result.Summaries))
	}

	s := result.Summaries[0]
	if s.ClusterID != 1 {
		t.Errorf("expected cluster ID 1, got %d", s.ClusterID)
	}
	if len(s.SummaryFacts) != 1 {
		t.Fatalf("expected 1 summary fact, got %d", len(s.SummaryFacts))
	}
	if len(s.SupersededIDs) != 3 {
		t.Errorf("expected 3 superseded IDs, got %d", len(s.SupersededIDs))
	}
	if len(s.KeptAsIs) != 2 {
		t.Errorf("expected 2 kept as-is, got %d", len(s.KeptAsIs))
	}
	if s.OriginalCount != 5 {
		t.Errorf("expected 5 original facts, got %d", s.OriginalCount)
	}
	if s.NewCount != 3 { // 1 summary + 2 kept
		t.Errorf("expected 3 new count (1 summary + 2 kept), got %d", s.NewCount)
	}
	if s.Compression < 1.0 {
		t.Errorf("expected compression > 1.0, got %.2f", s.Compression)
	}
	if result.Model != "mock/summarize" {
		t.Errorf("expected model 'mock/summarize', got %q", result.Model)
	}
}

func TestSummarizeClusters_SkipsSmallClusters(t *testing.T) {
	provider := &mockSummarizeProvider{response: `{"summary_facts": [], "kept_as_is": [], "reasoning": ""}`}

	clusters := []ClusterInput{
		{
			ID:   1,
			Name: "Tiny",
			Facts: []ClusterFactInput{
				{ID: 1, Subject: "x", Predicate: "y", Object: "z", FactType: "kv"},
				{ID: 2, Subject: "a", Predicate: "b", Object: "c", FactType: "kv"},
			},
		},
	}

	result, err := SummarizeClusters(context.Background(), provider, clusters, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Summaries) != 0 {
		t.Errorf("expected 0 summaries for small cluster, got %d", len(result.Summaries))
	}
	if provider.calls != 0 {
		t.Errorf("expected 0 LLM calls for small cluster, got %d", provider.calls)
	}
}

func TestSummarizeClusters_CustomMinSize(t *testing.T) {
	provider := &mockSummarizeProvider{response: `{"summary_facts": [], "kept_as_is": [1, 2, 3], "reasoning": "all unique"}`}

	facts := make([]ClusterFactInput, 3)
	for i := range facts {
		facts[i] = ClusterFactInput{ID: int64(i + 1), Subject: "x", Predicate: "y", Object: fmt.Sprintf("z%d", i), FactType: "kv"}
	}
	clusters := []ClusterInput{{ID: 1, Name: "Small", Facts: facts}}

	opts := SummarizeOpts{MinClusterSize: 3}
	result, err := SummarizeClusters(context.Background(), provider, clusters, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Summaries) != 1 {
		t.Errorf("expected 1 summary with min size 3, got %d", len(result.Summaries))
	}
}

func TestSummarizeClusters_SpecificCluster(t *testing.T) {
	provider := &mockSummarizeProvider{response: `{"summary_facts": [], "kept_as_is": [], "reasoning": ""}`}

	clusters := []ClusterInput{
		{ID: 1, Name: "A", Facts: makeFacts(5)},
		{ID: 2, Name: "B", Facts: makeFacts(5)},
		{ID: 3, Name: "C", Facts: makeFacts(5)},
	}

	opts := SummarizeOpts{MinClusterSize: 5, ClusterID: 2}
	_, err := SummarizeClusters(context.Background(), provider, clusters, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.calls != 1 {
		t.Errorf("expected 1 LLM call (cluster 2 only), got %d", provider.calls)
	}
}

func TestSummarizeClusters_NilProvider(t *testing.T) {
	_, err := SummarizeClusters(context.Background(), nil, []ClusterInput{{ID: 1, Facts: makeFacts(5)}}, DefaultSummarizeOpts())
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestSummarizeClusters_LLMError_Continues(t *testing.T) {
	provider := &mockSummarizeProvider{err: fmt.Errorf("API error")}

	clusters := []ClusterInput{
		{ID: 1, Name: "A", Facts: makeFacts(5)},
	}

	result, err := SummarizeClusters(context.Background(), provider, clusters, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("should not return error, should skip failed cluster: %v", err)
	}

	if len(result.Summaries) != 0 {
		t.Errorf("expected 0 summaries on LLM error, got %d", len(result.Summaries))
	}
}

func TestSummarizeClusters_EmptyClusters(t *testing.T) {
	provider := &mockSummarizeProvider{}

	result, err := SummarizeClusters(context.Background(), provider, nil, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(result.Summaries))
	}
}

func TestSummarizeClusters_MarkdownFenced(t *testing.T) {
	response := "```json\n{\"summary_facts\": [{\"subject\": \"X\", \"predicate\": \"is\", \"object\": \"Y\", \"type\": \"kv\", \"confidence\": 0.9, \"replaces\": [1], \"reasoning\": \"test\"}], \"kept_as_is\": [], \"reasoning\": \"ok\"}\n```"

	provider := &mockSummarizeProvider{response: response}
	clusters := []ClusterInput{{ID: 1, Name: "Test", Facts: makeFacts(5)}}

	result, err := SummarizeClusters(context.Background(), provider, clusters, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Summaries) != 1 || len(result.Summaries[0].SummaryFacts) != 1 {
		t.Error("expected 1 summary with 1 fact from markdown-fenced response")
	}
}

func TestSummarizeClusters_InvalidFactsSkipped(t *testing.T) {
	response := `{
		"summary_facts": [
			{"subject": "good", "predicate": "has", "object": "value", "type": "kv", "confidence": 0.9, "replaces": [1], "reasoning": "ok"},
			{"subject": "bad", "predicate": "", "object": "value", "type": "kv", "confidence": 0.9, "replaces": [2], "reasoning": "no pred"},
			{"subject": "bad2", "predicate": "has", "object": "", "type": "kv", "confidence": 0.9, "replaces": [3], "reasoning": "no obj"}
		],
		"kept_as_is": [],
		"reasoning": "mixed"
	}`

	provider := &mockSummarizeProvider{response: response}
	clusters := []ClusterInput{{ID: 1, Name: "Test", Facts: makeFacts(5)}}

	result, err := SummarizeClusters(context.Background(), provider, clusters, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Summaries[0].SummaryFacts) != 1 {
		t.Errorf("expected 1 valid fact (2 invalid skipped), got %d", len(result.Summaries[0].SummaryFacts))
	}
}

func TestSummarizeClusters_TracksLatency(t *testing.T) {
	provider := &mockSummarizeProvider{response: `{"summary_facts": [], "kept_as_is": [], "reasoning": ""}`}
	clusters := []ClusterInput{{ID: 1, Name: "Test", Facts: makeFacts(5)}}

	result, err := SummarizeClusters(context.Background(), provider, clusters, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Latency <= 0 {
		t.Error("expected positive latency")
	}
}

func TestBuildSummarizePrompt(t *testing.T) {
	facts := []ClusterFactInput{
		{ID: 1, Subject: "Q", Predicate: "lives in", Object: "Philadelphia", FactType: "location", Confidence: 0.8, Source: "MEMORY.md"},
		{ID: 2, Subject: "", Predicate: "port", Object: "8090", FactType: "config", Confidence: 0.9},
	}

	prompt := buildSummarizePrompt("Q Personal", facts)

	if !strings.Contains(prompt, "CLUSTER: Q Personal") {
		t.Error("prompt should contain cluster name")
	}
	if !strings.Contains(prompt, "id:1") {
		t.Error("prompt should contain fact IDs")
	}
	if !strings.Contains(prompt, "(none)") {
		t.Error("prompt should show (none) for empty subject")
	}
	if !strings.Contains(prompt, "src:MEMORY.md") {
		t.Error("prompt should show source")
	}
	if !strings.Contains(prompt, "2 facts") {
		t.Error("prompt should show fact count")
	}
}

func TestParseSummarizeResponse_Valid(t *testing.T) {
	raw := `{"summary_facts": [{"subject": "X", "predicate": "is", "object": "Y", "type": "kv", "confidence": 0.9, "replaces": [1, 2], "reasoning": "merged"}], "kept_as_is": [3], "reasoning": "done"}`

	resp, err := parseSummarizeResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.SummaryFacts) != 1 {
		t.Fatalf("expected 1 summary fact, got %d", len(resp.SummaryFacts))
	}
	if len(resp.SummaryFacts[0].Replaces) != 2 {
		t.Errorf("expected 2 replaced IDs, got %d", len(resp.SummaryFacts[0].Replaces))
	}
	if len(resp.KeptAsIs) != 1 {
		t.Errorf("expected 1 kept, got %d", len(resp.KeptAsIs))
	}
}

func TestParseSummarizeResponse_InvalidJSON(t *testing.T) {
	_, err := parseSummarizeResponse("not json at all")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSummarizeClusters_CompressionRatio(t *testing.T) {
	// 10 facts → 3 summary + 2 kept = 5 new, compression = 10/5 = 2.0
	response := `{
		"summary_facts": [
			{"subject": "a", "predicate": "p", "object": "o1", "type": "kv", "confidence": 0.9, "replaces": [1, 2, 3], "reasoning": "merged"},
			{"subject": "b", "predicate": "p", "object": "o2", "type": "kv", "confidence": 0.9, "replaces": [4, 5, 6], "reasoning": "merged"},
			{"subject": "c", "predicate": "p", "object": "o3", "type": "kv", "confidence": 0.9, "replaces": [7, 8], "reasoning": "merged"}
		],
		"kept_as_is": [9, 10],
		"reasoning": "consolidated 3 groups"
	}`

	provider := &mockSummarizeProvider{response: response}
	clusters := []ClusterInput{{ID: 1, Name: "Test", Facts: makeFacts(10)}}

	result, err := SummarizeClusters(context.Background(), provider, clusters, DefaultSummarizeOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := result.Summaries[0]
	if s.Compression != 2.0 {
		t.Errorf("expected compression 2.0, got %.2f", s.Compression)
	}
	if result.TotalOriginal != 10 {
		t.Errorf("expected 10 original, got %d", result.TotalOriginal)
	}
	if result.TotalNew != 5 {
		t.Errorf("expected 5 new, got %d", result.TotalNew)
	}
}

// makeFacts creates N test facts for a cluster.
func makeFacts(n int) []ClusterFactInput {
	facts := make([]ClusterFactInput, n)
	for i := range facts {
		facts[i] = ClusterFactInput{
			ID:         int64(i + 1),
			Subject:    fmt.Sprintf("entity_%d", i),
			Predicate:  "has",
			Object:     fmt.Sprintf("value_%d", i),
			FactType:   "kv",
			Confidence: 0.8,
		}
	}
	return facts
}
