package store

import (
	"context"
	"testing"
)

func TestListProjects_Empty(t *testing.T) {
	s := newTestStore(t)

	projects, err := s.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects error: %v", err)
	}
	// Even empty store should return a result (untagged = "")
	if len(projects) != 0 {
		t.Fatalf("expected 0 projects on empty store, got %d", len(projects))
	}
}

func TestListProjects_WithProjects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memories with different projects
	mems := []*Memory{
		{Content: "trading notes 1", SourceFile: "trading/notes.md", Project: "trading"},
		{Content: "trading notes 2", SourceFile: "trading/plan.md", Project: "trading"},
		{Content: "eyes web onboarding", SourceFile: "repos/mybeautifulwife/onboarding.md", Project: "eyes-web"},
		{Content: "untagged memory", SourceFile: "random.md"},
	}
	for _, m := range mems {
		m.ContentHash = HashContentOnly(m.Content)
		if _, err := s.AddMemory(ctx, m); err != nil {
			t.Fatalf("AddMemory error: %v", err)
		}
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error: %v", err)
	}

	if len(projects) != 3 {
		t.Fatalf("expected 3 projects (trading, eyes-web, untagged), got %d", len(projects))
	}

	// Find trading project
	var tradingFound, eyesFound bool
	for _, p := range projects {
		if p.Name == "trading" {
			tradingFound = true
			if p.MemoryCount != 2 {
				t.Errorf("trading: expected 2 memories, got %d", p.MemoryCount)
			}
		}
		if p.Name == "eyes-web" {
			eyesFound = true
			if p.MemoryCount != 1 {
				t.Errorf("eyes-web: expected 1 memory, got %d", p.MemoryCount)
			}
		}
	}
	if !tradingFound {
		t.Error("trading project not found")
	}
	if !eyesFound {
		t.Error("eyes-web project not found")
	}
}

func TestTagMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add untagged memories
	mem1 := &Memory{Content: "memory one", SourceFile: "a.md", ContentHash: HashContentOnly("memory one")}
	mem2 := &Memory{Content: "memory two", SourceFile: "b.md", ContentHash: HashContentOnly("memory two")}
	id1, _ := s.AddMemory(ctx, mem1)
	id2, _ := s.AddMemory(ctx, mem2)

	// Tag both
	n, err := s.TagMemories(ctx, "trading", []int64{id1, id2})
	if err != nil {
		t.Fatalf("TagMemories error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 tagged, got %d", n)
	}

	// Verify
	m1, _ := s.GetMemory(ctx, id1)
	if m1.Project != "trading" {
		t.Errorf("memory 1 project: expected 'trading', got %q", m1.Project)
	}
}

func TestTagMemoriesBySource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memories with different sources
	for _, src := range []string{"trading/plan.md", "trading/journal.md", "other/notes.md"} {
		m := &Memory{Content: "content for " + src, SourceFile: src, ContentHash: HashContentOnly("content for " + src)}
		s.AddMemory(ctx, m)
	}

	// Tag by source pattern
	n, err := s.TagMemoriesBySource(ctx, "trading", "trading/%")
	if err != nil {
		t.Fatalf("TagMemoriesBySource error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 tagged, got %d", n)
	}
}

func TestSearchFTSWithProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memories in different projects
	mems := []*Memory{
		{Content: "QQQ trading strategy notes", SourceFile: "trading.md", Project: "trading"},
		{Content: "trading onboarding flow for eyes web", SourceFile: "eyes.md", Project: "eyes-web"},
		{Content: "untagged trading memory", SourceFile: "random.md"},
	}
	for _, m := range mems {
		m.ContentHash = HashContentOnly(m.Content)
		s.AddMemory(ctx, m)
	}

	// Search without project filter — should find all
	all, err := s.SearchFTS(ctx, "trading", 10)
	if err != nil {
		t.Fatalf("SearchFTS error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("unfiltered: expected 3 results, got %d", len(all))
	}

	// Search with project filter — should find only trading
	filtered, err := s.SearchFTSWithProject(ctx, "trading", 10, "trading")
	if err != nil {
		t.Fatalf("SearchFTSWithProject error: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("filtered by 'trading': expected 1 result, got %d", len(filtered))
	}
	if len(filtered) > 0 && filtered[0].Memory.Project != "trading" {
		t.Errorf("expected project 'trading', got %q", filtered[0].Memory.Project)
	}
}

func TestListMemories_ProjectFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mems := []*Memory{
		{Content: "mem1", SourceFile: "a.md", Project: "trading", ContentHash: HashContentOnly("mem1")},
		{Content: "mem2", SourceFile: "b.md", Project: "trading", ContentHash: HashContentOnly("mem2")},
		{Content: "mem3", SourceFile: "c.md", Project: "eyes-web", ContentHash: HashContentOnly("mem3")},
	}
	for _, m := range mems {
		s.AddMemory(ctx, m)
	}

	// Filter by project
	results, err := s.ListMemories(ctx, ListOpts{Project: "trading"})
	if err != nil {
		t.Fatalf("ListMemories error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 trading memories, got %d", len(results))
	}
}

func TestListMemories_MemoryClassFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mems := []*Memory{
		{Content: "must never push to main", SourceFile: "rules.md", MemoryClass: MemoryClassRule, ContentHash: HashContentOnly("must never push to main")},
		{Content: "we decided to use Go", SourceFile: "decisions.md", MemoryClass: MemoryClassDecision, ContentHash: HashContentOnly("we decided to use Go")},
		{Content: "brainstorm feature ideas", SourceFile: "scratch.md", MemoryClass: MemoryClassScratch, ContentHash: HashContentOnly("brainstorm feature ideas")},
	}
	for _, m := range mems {
		if _, err := s.AddMemory(ctx, m); err != nil {
			t.Fatalf("AddMemory error: %v", err)
		}
	}

	results, err := s.ListMemories(ctx, ListOpts{MemoryClasses: []string{MemoryClassRule, MemoryClassDecision}})
	if err != nil {
		t.Fatalf("ListMemories with class filter error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 filtered memories, got %d", len(results))
	}
	for _, m := range results {
		if m.MemoryClass != MemoryClassRule && m.MemoryClass != MemoryClassDecision {
			t.Fatalf("unexpected class %q", m.MemoryClass)
		}
	}
}

func TestAddMemory_InvalidMemoryClass(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.AddMemory(ctx, &Memory{
		Content:     "invalid class sample",
		SourceFile:  "invalid.md",
		MemoryClass: "not-a-class",
		ContentHash: HashContentOnly("invalid class sample"),
	})
	if err == nil {
		t.Fatal("expected error for invalid memory class")
	}
}
