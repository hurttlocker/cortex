package search

import (
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestApplyMetadataBoosts_AgentMatch(t *testing.T) {
	results := []Result{
		{Content: "from main agent", Score: 1.0, Metadata: &store.Metadata{AgentID: "main"}},
		{Content: "from ace agent", Score: 1.0, Metadata: &store.Metadata{AgentID: "ace"}},
		{Content: "no metadata", Score: 1.0},
	}

	opts := Options{BoostAgent: "main"}
	boosted := applyMetadataBoosts(results, opts)

	if boosted[0].Content != "from main agent" {
		t.Errorf("expected main agent result first, got %q", boosted[0].Content)
	}
	if boosted[0].Score <= 1.0 {
		t.Errorf("expected boosted score > 1.0, got %f", boosted[0].Score)
	}
	if boosted[1].Score != 1.0 {
		t.Errorf("expected non-matched score unchanged at 1.0, got %f", boosted[1].Score)
	}
}

func TestApplyMetadataBoosts_ChannelMatch(t *testing.T) {
	results := []Result{
		{Content: "telegram msg", Score: 1.0, Metadata: &store.Metadata{Channel: "telegram"}},
		{Content: "discord msg", Score: 1.0, Metadata: &store.Metadata{Channel: "discord"}},
	}

	opts := Options{BoostChannel: "discord"}
	boosted := applyMetadataBoosts(results, opts)

	if boosted[0].Content != "discord msg" {
		t.Errorf("expected discord result first, got %q", boosted[0].Content)
	}
}

func TestApplyMetadataBoosts_NoBoostContext(t *testing.T) {
	results := []Result{
		{Content: "first", Score: 0.9},
		{Content: "second", Score: 0.8},
	}

	opts := Options{} // No boost context
	boosted := applyMetadataBoosts(results, opts)

	if boosted[0].Score != 0.9 || boosted[1].Score != 0.8 {
		t.Error("scores should be unchanged when no boost context")
	}
}

func TestApplyRecencyBoost_TodayBoosted(t *testing.T) {
	now := time.Now()
	results := []Result{
		{Content: "old memory", Score: 1.0, ImportedAt: now.AddDate(0, -2, 0)},
		{Content: "today memory", Score: 1.0, ImportedAt: now},
		{Content: "week ago", Score: 1.0, ImportedAt: now.AddDate(0, 0, -3)},
	}

	boosted := applyRecencyBoost(results, false)

	if boosted[0].Content != "today memory" {
		t.Errorf("expected today's memory first, got %q", boosted[0].Content)
	}
	if boosted[0].Score <= 1.0 {
		t.Errorf("expected today's score > 1.0, got %f", boosted[0].Score)
	}
}

func TestApplyRecencyBoost_WeekBoostedMoreThanMonth(t *testing.T) {
	now := time.Now()
	results := []Result{
		{Content: "3 weeks ago", Score: 1.0, ImportedAt: now.AddDate(0, 0, -21)},
		{Content: "3 days ago", Score: 1.0, ImportedAt: now.AddDate(0, 0, -3)},
	}

	boosted := applyRecencyBoost(results, false)

	weekScore := boosted[0].Score
	monthScore := boosted[1].Score

	// 3 days ago should be boosted more than 3 weeks ago
	if weekScore <= monthScore {
		t.Errorf("expected week-old score (%f) > month-old score (%f)", weekScore, monthScore)
	}
}

func TestApplyRecencyBoost_OldNotBoosted(t *testing.T) {
	now := time.Now()
	results := []Result{
		{Content: "ancient", Score: 1.0, ImportedAt: now.AddDate(-1, 0, 0)},
	}

	boosted := applyRecencyBoost(results, false)

	if boosted[0].Score != 1.0 {
		t.Errorf("expected ancient memory score unchanged at 1.0, got %f", boosted[0].Score)
	}
}

func TestApplyMetadataBoosts_CaseInsensitive(t *testing.T) {
	results := []Result{
		{Content: "from Main", Score: 1.0, Metadata: &store.Metadata{AgentID: "Main"}},
	}

	opts := Options{BoostAgent: "main"}
	boosted := applyMetadataBoosts(results, opts)

	if boosted[0].Score <= 1.0 {
		t.Errorf("expected case-insensitive match to boost, got %f", boosted[0].Score)
	}
}

func TestApplyMetadataBoosts_BothAgentAndChannel(t *testing.T) {
	results := []Result{
		{Content: "both match", Score: 1.0, Metadata: &store.Metadata{AgentID: "main", Channel: "discord"}},
		{Content: "agent only", Score: 1.0, Metadata: &store.Metadata{AgentID: "main", Channel: "telegram"}},
		{Content: "neither", Score: 1.0, Metadata: &store.Metadata{AgentID: "ace", Channel: "telegram"}},
	}

	opts := Options{BoostAgent: "main", BoostChannel: "discord"}
	boosted := applyMetadataBoosts(results, opts)

	if boosted[0].Content != "both match" {
		t.Errorf("expected double-boosted result first, got %q", boosted[0].Content)
	}
	// Double boost should be > single boost
	if boosted[0].Score <= boosted[1].Score {
		t.Errorf("expected double boost (%f) > single boost (%f)", boosted[0].Score, boosted[1].Score)
	}
}

func TestFilterBySource(t *testing.T) {
	results := []Result{
		{Content: "github issue", SourceFile: "github:issues/123"},
		{Content: "gmail message", SourceFile: "gmail:inbox/msg-1"},
		{Content: "local file", SourceFile: "memory/2026-02-23.md"},
		{Content: "github PR", SourceFile: "github:pulls/456"},
	}

	filtered := filterBySource(results, "github")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 github results, got %d", len(filtered))
	}
	if filtered[0].Content != "github issue" {
		t.Errorf("expected github issue first, got %q", filtered[0].Content)
	}
	if filtered[1].Content != "github PR" {
		t.Errorf("expected github PR second, got %q", filtered[1].Content)
	}
}

func TestFilterBySource_CaseInsensitive(t *testing.T) {
	results := []Result{
		{Content: "github", SourceFile: "GitHub:issues/1"},
		{Content: "other", SourceFile: "memory/notes.md"},
	}

	filtered := filterBySource(results, "github")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 result, got %d", len(filtered))
	}
}

func TestFilterBySource_NoMatch(t *testing.T) {
	results := []Result{
		{Content: "local", SourceFile: "memory/notes.md"},
	}

	filtered := filterBySource(results, "slack")
	if len(filtered) != 0 {
		t.Fatalf("expected 0 results, got %d", len(filtered))
	}
}

func TestIsConnectorSource(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"github:issues/123", true},
		{"gmail:inbox/msg-1", true},
		{"calendar:events/abc", true},
		{"drive:docs/file", true},
		{"slack:channels/general", true},
		{"discord:messages/456", true},
		{"telegram:chats/789", true},
		{"notion:pages/abc", true},
		{"memory/2026-02-23.md", false},
		{"MEMORY.md", false},
		{"/Users/q/notes.md", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isConnectorSource(tt.source)
		if got != tt.want {
			t.Errorf("isConnectorSource(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestApplySourceWeight(t *testing.T) {
	results := []Result{
		{Content: "manual import", SourceFile: "memory/notes.md", Score: 1.0},
		{Content: "connector import", SourceFile: "github:issues/1", Score: 1.0},
	}

	weighted := applySourceWeight(results, false)

	// Manual should be boosted, connector should be penalized
	manualScore := weighted[0].Score
	connectorScore := weighted[1].Score

	// After sorting, manual (1.05) should be first, connector (0.97) second
	if weighted[0].SourceFile != "memory/notes.md" {
		t.Errorf("expected manual import first after source weighting, got %q", weighted[0].SourceFile)
	}
	if manualScore <= connectorScore {
		t.Errorf("expected manual score (%f) > connector score (%f)", manualScore, connectorScore)
	}
}

func TestApplySourceWeight_Explain(t *testing.T) {
	results := []Result{
		{Content: "connector", SourceFile: "github:issues/1", Score: 1.0},
	}

	weighted := applySourceWeight(results, true)

	if weighted[0].Explain == nil {
		t.Fatal("expected explain details when explain=true")
	}
	if weighted[0].Explain.RankComponents.SourceWeight != sourceWeightConnector {
		t.Errorf("expected source weight %f, got %f", sourceWeightConnector, weighted[0].Explain.RankComponents.SourceWeight)
	}
}
