package store

import (
	"context"
	"testing"
)

func TestCreateAndListWatches(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	w := &WatchQuery{
		Query:     "deployment failures",
		Threshold: 0.7,
	}
	err := s.CreateWatch(ctx, w)
	if err != nil {
		t.Fatalf("CreateWatch: %v", err)
	}
	if w.ID == 0 {
		t.Fatal("Expected watch ID to be set")
	}
	if !w.Active {
		t.Fatal("Expected watch to be active")
	}

	watches, err := s.ListWatches(ctx, true)
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("Expected 1 watch, got %d", len(watches))
	}
	if watches[0].Query != "deployment failures" {
		t.Fatalf("Expected query 'deployment failures', got %q", watches[0].Query)
	}
}

func TestCreateWatch_DefaultThreshold(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	w := &WatchQuery{Query: "test query"}
	s.CreateWatch(ctx, w)

	got, _ := s.GetWatch(ctx, w.ID)
	if got.Threshold != 0.7 {
		t.Fatalf("Expected default threshold 0.7, got %f", got.Threshold)
	}
	if got.DeliveryChannel != "alert" {
		t.Fatalf("Expected default delivery 'alert', got %q", got.DeliveryChannel)
	}
}

func TestCreateWatch_EmptyQuery(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	err := s.CreateWatch(ctx, &WatchQuery{})
	if err == nil {
		t.Fatal("Expected error for empty query")
	}
}

func TestRemoveWatch(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	w := &WatchQuery{Query: "remove me"}
	s.CreateWatch(ctx, w)

	err := s.RemoveWatch(ctx, w.ID)
	if err != nil {
		t.Fatalf("RemoveWatch: %v", err)
	}

	watches, _ := s.ListWatches(ctx, false)
	if len(watches) != 0 {
		t.Fatalf("Expected 0 watches after remove, got %d", len(watches))
	}
}

func TestRemoveWatch_NotFound(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	err := s.RemoveWatch(ctx, 999)
	if err == nil {
		t.Fatal("Expected error for nonexistent watch")
	}
}

func TestWatchPauseResume(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	w := &WatchQuery{Query: "pausable"}
	s.CreateWatch(ctx, w)

	// Pause
	s.SetWatchActive(ctx, w.ID, false)
	got, _ := s.GetWatch(ctx, w.ID)
	if got.Active {
		t.Fatal("Expected watch to be paused")
	}

	// Should not appear in active-only list
	active, _ := s.ListWatches(ctx, true)
	if len(active) != 0 {
		t.Fatalf("Expected 0 active watches, got %d", len(active))
	}

	// Resume
	s.SetWatchActive(ctx, w.ID, true)
	got, _ = s.GetWatch(ctx, w.ID)
	if !got.Active {
		t.Fatal("Expected watch to be active after resume")
	}
}

func TestBm25MatchScore(t *testing.T) {
	tests := []struct {
		content string
		query   string
		minScore float64
		maxScore float64
	}{
		{"The deployment failed at 3am", "deployment failures", 0.4, 1.0},
		{"Hello world", "deployment failures", 0, 0.01},
		{"ADA price is $0.45 today", "ADA price", 0.9, 1.0}, // Exact phrase match
		{"The quick brown fox", "lazy dog", 0, 0.01},
		{"", "anything", 0, 0.01},
		{"some content", "", 0, 0.01},
	}

	for _, tt := range tests {
		score := bm25MatchScore(tt.content, tt.query)
		if score < tt.minScore || score > tt.maxScore {
			t.Errorf("bm25MatchScore(%q, %q) = %f, expected [%f, %f]",
				tt.content, tt.query, score, tt.minScore, tt.maxScore)
		}
	}
}

func TestCheckWatchesForMemory(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	// Create a watch
	w := &WatchQuery{Query: "deployment failure", Threshold: 0.5}
	s.CreateWatch(ctx, w)

	// Create a matching memory
	memID, _ := s.AddMemory(ctx, &Memory{
		Content:    "The production deployment failed at 3am causing a major outage. Deployment failure root cause was a bad config.",
		SourceFile: "incident.md",
	})

	// Check watches
	matches, err := s.CheckWatchesForMemory(ctx, memID)
	if err != nil {
		t.Fatalf("CheckWatchesForMemory: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("Expected 1 match, got %d", len(matches))
	}
	if matches[0].WatchID != w.ID {
		t.Fatalf("Expected watch ID %d, got %d", w.ID, matches[0].WatchID)
	}

	// Verify alert was created
	unacked := false
	alerts, alertErr := s.ListAlerts(ctx, AlertFilter{Type: AlertTypeMatch, Acknowledged: &unacked})
	if alertErr != nil {
		t.Fatalf("ListAlerts: %v", alertErr)
	}
	if len(alerts) != 1 {
		t.Fatalf("Expected 1 match alert, got %d", len(alerts))
	}

	// Verify match count updated
	updated, _ := s.GetWatch(ctx, w.ID)
	if updated.MatchCount != 1 {
		t.Fatalf("Expected match_count=1, got %d", updated.MatchCount)
	}
}

func TestCheckWatchesForMemory_NoMatch(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	w := &WatchQuery{Query: "deployment failure", Threshold: 0.7}
	s.CreateWatch(ctx, w)

	memID, _ := s.AddMemory(ctx, &Memory{
		Content:    "The weather today is sunny and warm.",
		SourceFile: "weather.md",
	})

	matches, err := s.CheckWatchesForMemory(ctx, memID)
	if err != nil {
		t.Fatalf("CheckWatchesForMemory: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("Expected 0 matches for unrelated content, got %d", len(matches))
	}
}

func TestCheckWatchesForMemory_PausedWatch(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	w := &WatchQuery{Query: "failure", Threshold: 0.5}
	s.CreateWatch(ctx, w)
	s.SetWatchActive(ctx, w.ID, false)

	memID, _ := s.AddMemory(ctx, &Memory{
		Content:    "System failure detected",
		SourceFile: "alert.md",
	})

	matches, _ := s.CheckWatchesForMemory(ctx, memID)
	if len(matches) != 0 {
		t.Fatalf("Expected 0 matches (watch paused), got %d", len(matches))
	}
}

func TestExtractWatchSnippet(t *testing.T) {
	content := "This is a long document about deployment strategies. The deployment process involves multiple stages including testing, staging, and production rollout."

	snippet := extractWatchSnippet(content, "deployment", 60)
	if len(snippet) > 70 { // Allow for "..." prefix/suffix
		t.Errorf("Snippet too long: %d chars", len(snippet))
	}
	if snippet == "" {
		t.Error("Expected non-empty snippet")
	}
}

func TestCheckWatchesForMemories_Batch(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	w := &WatchQuery{Query: "error", Threshold: 0.5}
	s.CreateWatch(ctx, w)

	id1, _ := s.AddMemory(ctx, &Memory{Content: "Error in production", SourceFile: "a.md"})
	id2, _ := s.AddMemory(ctx, &Memory{Content: "All systems normal", SourceFile: "b.md"})
	id3, _ := s.AddMemory(ctx, &Memory{Content: "Another error found", SourceFile: "c.md"})

	matches, err := s.CheckWatchesForMemories(ctx, []int64{id1, id2, id3})
	if err != nil {
		t.Fatalf("CheckWatchesForMemories: %v", err)
	}
	// Should match id1 and id3 (contain "error"), not id2
	if len(matches) != 2 {
		t.Fatalf("Expected 2 batch matches, got %d", len(matches))
	}
}

func TestFactAgentID(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{
		Content: "Test content", SourceFile: "test.md",
	})

	f := &Fact{
		MemoryID: memID, Subject: "trading", Predicate: "strategy",
		Object: "ORB", FactType: "kv", Confidence: 0.9, AgentID: "mister",
	}
	id, err := s.AddFact(ctx, f)
	if err != nil {
		t.Fatalf("AddFact with agent_id: %v", err)
	}

	got, err := s.GetFact(ctx, id)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	if got.AgentID != "mister" {
		t.Fatalf("Expected agent_id 'mister', got %q", got.AgentID)
	}
}

func TestFactAgentID_DefaultEmpty(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{
		Content: "Global fact", SourceFile: "global.md",
	})

	f := &Fact{
		MemoryID: memID, Subject: "cortex", Predicate: "language",
		Object: "Go", FactType: "kv", Confidence: 0.9,
	}
	id, _ := s.AddFact(ctx, f)

	got, _ := s.GetFact(ctx, id)
	if got.AgentID != "" {
		t.Fatalf("Expected empty agent_id for global fact, got %q", got.AgentID)
	}
}

func TestListFacts_AgentFilter(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{
		Content: "Multi-agent facts", SourceFile: "test.md",
	})

	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "trading", Predicate: "strategy", Object: "ORB", FactType: "kv", AgentID: "mister"})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "code", Predicate: "language", Object: "TypeScript", FactType: "kv", AgentID: "niot"})
	s.AddFact(ctx, &Fact{MemoryID: memID, Subject: "cortex", Predicate: "type", Object: "memory", FactType: "kv", AgentID: ""})

	// Mister's view: own + global
	misterFacts, err := s.ListFacts(ctx, ListOpts{Agent: "mister"})
	if err != nil {
		t.Fatalf("ListFacts agent=mister: %v", err)
	}
	if len(misterFacts) != 2 {
		t.Fatalf("Expected 2 facts for mister (own + global), got %d", len(misterFacts))
	}

	// Niot's view: own + global
	niotFacts, _ := s.ListFacts(ctx, ListOpts{Agent: "niot"})
	if len(niotFacts) != 2 {
		t.Fatalf("Expected 2 facts for niot, got %d", len(niotFacts))
	}

	// Global view: all facts
	allFacts, _ := s.ListFacts(ctx, ListOpts{})
	if len(allFacts) != 3 {
		t.Fatalf("Expected 3 total facts, got %d", len(allFacts))
	}
}
