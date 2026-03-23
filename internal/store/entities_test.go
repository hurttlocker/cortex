package store

import (
	"context"
	"strings"
	"testing"
)

func TestEntityResolutionCascadeAndUnresolvedTracking(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{
		Content:       "Jonathan is an engineer. He likes pizza.",
		SourceFile:    "entity.md",
		SourceSection: "entity",
		Metadata: &Metadata{
			ObservedEntity: "Jonathan",
		},
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	first := &Fact{
		MemoryID:   memID,
		Subject:    "Jonathan",
		Predicate:  "role",
		Object:     "engineer",
		FactType:   "identity",
		Confidence: 0.9,
	}
	if _, err := s.AddFact(ctx, first); err != nil {
		t.Fatalf("add fact Jonathan: %v", err)
	}
	if first.EntityID == 0 {
		t.Fatal("expected Jonathan fact to resolve to a canonical entity")
	}

	second := &Fact{
		MemoryID:   memID,
		Subject:    "jonathan",
		Predicate:  "status",
		Object:     "active",
		FactType:   "state",
		Confidence: 0.8,
	}
	if _, err := s.AddFact(ctx, second); err != nil {
		t.Fatalf("add fact jonathan: %v", err)
	}
	if second.EntityID != first.EntityID {
		t.Fatalf("case-insensitive resolution = %d, want %d", second.EntityID, first.EntityID)
	}

	aliasSeed := &Fact{
		MemoryID:   memID,
		Subject:    "Jon",
		Predicate:  "nickname",
		Object:     "Jonathan",
		FactType:   "identity",
		Confidence: 0.7,
	}
	if _, err := s.AddFact(ctx, aliasSeed); err != nil {
		t.Fatalf("add alias seed fact: %v", err)
	}
	if aliasSeed.EntityID == 0 || aliasSeed.EntityID == first.EntityID {
		t.Fatalf("expected separate alias-seed entity before merge, got %d", aliasSeed.EntityID)
	}
	if err := s.MergeEntities(ctx, first.EntityID, aliasSeed.EntityID); err != nil {
		t.Fatalf("merge entities: %v", err)
	}

	aliasHit := &Fact{
		MemoryID:   memID,
		Subject:    "Jon",
		Predicate:  "works_on",
		Object:     "Cortex",
		FactType:   "relationship",
		Confidence: 0.8,
	}
	if _, err := s.AddFact(ctx, aliasHit); err != nil {
		t.Fatalf("add alias-hit fact: %v", err)
	}
	if aliasHit.EntityID != first.EntityID {
		t.Fatalf("alias resolution = %d, want %d", aliasHit.EntityID, first.EntityID)
	}

	pronoun := &Fact{
		MemoryID:   memID,
		Subject:    "he",
		Predicate:  "likes",
		Object:     "pizza",
		FactType:   "preference",
		Confidence: 0.8,
	}
	if _, err := s.AddFact(ctx, pronoun); err != nil {
		t.Fatalf("add pronoun fact: %v", err)
	}
	if pronoun.EntityID != first.EntityID {
		t.Fatalf("pronoun resolution = %d, want %d", pronoun.EntityID, first.EntityID)
	}

	numeric := &Fact{
		MemoryID:   memID,
		Subject:    "1234",
		Predicate:  "id",
		Object:     "opaque",
		FactType:   "identity",
		Confidence: 0.6,
	}
	numericID, err := s.AddFact(ctx, numeric)
	if err != nil {
		t.Fatalf("add numeric fact: %v", err)
	}
	if numeric.EntityID != 0 {
		t.Fatalf("numeric subject should stay unresolved, got entity_id=%d", numeric.EntityID)
	}

	blank := &Fact{
		MemoryID:   memID,
		Subject:    "",
		Predicate:  "owner",
		Object:     "nobody",
		FactType:   "identity",
		Confidence: 0.4,
	}
	if _, err := s.AddFact(ctx, blank); err != nil {
		t.Fatalf("add blank-subject fact: %v", err)
	}

	var unresolvedCount int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM unresolved_entities
		WHERE fact_id = ?
	`, numericID).Scan(&unresolvedCount); err != nil {
		t.Fatalf("count unresolved entities: %v", err)
	}
	if unresolvedCount != 1 {
		t.Fatalf("expected 1 unresolved row for numeric fact, got %d", unresolvedCount)
	}

	var blankCount int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM unresolved_entities
		WHERE raw_name = ''
	`).Scan(&blankCount); err != nil {
		t.Fatalf("count blank unresolved entities: %v", err)
	}
	if blankCount != 0 {
		t.Fatalf("expected blank subject to skip unresolved tracking, got %d rows", blankCount)
	}
}

func TestMergeEntitiesReassignsFactsAndAliases(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{
		Content:    "Jonathan and Jon refer to the same person.",
		SourceFile: "merge.md",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	jonathan := &Fact{MemoryID: memID, Subject: "Jonathan", Predicate: "role", Object: "engineer", FactType: "identity", Confidence: 0.9}
	jon := &Fact{MemoryID: memID, Subject: "Jon", Predicate: "focus", Object: "memory", FactType: "state", Confidence: 0.8}
	if _, err := s.AddFact(ctx, jonathan); err != nil {
		t.Fatalf("add Jonathan fact: %v", err)
	}
	if _, err := s.AddFact(ctx, jon); err != nil {
		t.Fatalf("add Jon fact: %v", err)
	}
	if jonathan.EntityID == 0 || jon.EntityID == 0 || jonathan.EntityID == jon.EntityID {
		t.Fatalf("expected two distinct entities before merge, got Jonathan=%d Jon=%d", jonathan.EntityID, jon.EntityID)
	}

	if err := s.MergeEntities(ctx, jonathan.EntityID, jon.EntityID); err != nil {
		t.Fatalf("merge entities: %v", err)
	}

	facts, err := s.GetFactsByEntityIDs(ctx, []int64{jonathan.EntityID}, false, 10)
	if err != nil {
		t.Fatalf("get facts by entity id: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts on merged entity, got %d", len(facts))
	}

	aliases, err := s.ListEntityAliases(ctx, jonathan.EntityID)
	if err != nil {
		t.Fatalf("list aliases: %v", err)
	}
	foundJon := false
	for _, alias := range aliases {
		if strings.EqualFold(alias.Alias, "Jon") {
			foundJon = true
			break
		}
	}
	if !foundJon {
		t.Fatal("expected merged entity to keep Jon as an alias")
	}

	mergedAway, err := s.GetEntity(ctx, jon.EntityID)
	if err != nil {
		t.Fatalf("get merged-away entity: %v", err)
	}
	if mergedAway != nil {
		t.Fatalf("expected merged-away entity %d to be deleted", jon.EntityID)
	}
}

func TestRebuildEntityProfileStoresMarkdown(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{
		Content:    "Alice is the project manager for Cortex.",
		SourceFile: "profile.md",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	fact := &Fact{
		MemoryID:    memID,
		Subject:     "Alice",
		Predicate:   "role",
		Object:      "project manager",
		FactType:    "relationship",
		Confidence:  0.95,
		SourceQuote: "Alice is the project manager for Cortex.",
	}
	if _, err := s.AddFact(ctx, fact); err != nil {
		t.Fatalf("add fact: %v", err)
	}

	profile, err := s.RebuildEntityProfile(ctx, fact.EntityID)
	if err != nil {
		t.Fatalf("rebuild entity profile: %v", err)
	}
	if !strings.Contains(profile, "# Alice") {
		t.Fatalf("expected markdown heading in profile, got:\n%s", profile)
	}
	if !strings.Contains(profile, "Alice role project manager") {
		t.Fatalf("expected rendered fact in profile, got:\n%s", profile)
	}

	entity, err := s.GetEntity(ctx, fact.EntityID)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if entity == nil || entity.Profile != profile {
		t.Fatalf("expected stored profile to match rebuilt profile")
	}
}

func TestBackfillFactEntitiesResolvesLegacyFacts(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{
		Content:    "Legacy Alice note",
		SourceFile: "legacy-entity.md",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO facts (memory_id, subject, predicate, object, fact_type, confidence, decay_rate, source_quote, state)
		VALUES (?, 'Alice', 'role', 'project manager', 'relationship', 0.9, 0.01, 'Alice is the project manager.', 'active')
	`, memID)
	if err != nil {
		t.Fatalf("seed legacy fact: %v", err)
	}
	factID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("legacy fact id: %v", err)
	}

	resolved, unresolved, err := s.BackfillFactEntities(ctx, 0)
	if err != nil {
		t.Fatalf("backfill fact entities: %v", err)
	}
	if resolved != 1 || unresolved != 0 {
		t.Fatalf("expected resolved=1 unresolved=0, got resolved=%d unresolved=%d", resolved, unresolved)
	}

	fact, err := s.GetFact(ctx, factID)
	if err != nil {
		t.Fatalf("get fact: %v", err)
	}
	if fact == nil || fact.EntityID == 0 {
		t.Fatalf("expected legacy fact to receive an entity id after backfill")
	}
}

func TestResolveEntityForConversationSpeakerPredicate(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	memID, err := s.AddMemory(ctx, &Memory{
		Content:    "Jon (D1:2): Lost my job as a banker yesterday.",
		SourceFile: "conversation.md",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	fact := &Fact{
		MemoryID:    memID,
		Subject:     "Session 1 - 4:04 pm on 20 January, 2023",
		Predicate:   "jon (d1",
		Object:      "2): Lost my job as a banker yesterday.",
		FactType:    "kv",
		SourceQuote: "Jon (D1:2): Lost my job as a banker yesterday.",
	}
	if _, err := s.AddFact(ctx, fact); err != nil {
		t.Fatalf("add conversation fact: %v", err)
	}
	if fact.EntityID == 0 {
		t.Fatal("expected conversation speaker heuristic to resolve an entity")
	}

	entity, err := s.GetEntity(ctx, fact.EntityID)
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	if entity == nil || entity.CanonicalName != "Jon" {
		t.Fatalf("expected canonical conversation speaker Jon, got %+v", entity)
	}
}
