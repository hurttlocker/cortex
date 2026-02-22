package extract

import (
	"testing"
)

func TestGovernor_DropMarkdownJunk(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	junkFacts := []ExtractedFact{
		{Subject: "test", Predicate: "key", Object: "**", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "key", Object: "---", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "key", Object: "|---|---|", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "key", Object: "***", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "key", Object: "```", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "---", Object: "value", FactType: "kv", Confidence: 0.9},
	}

	result := gov.Apply(junkFacts)
	if len(result) != 0 {
		t.Errorf("expected 0 facts after filtering junk, got %d: %+v", len(result), result)
	}
}

func TestGovernor_DropGenericSubjects(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "Conversation Summary", Predicate: "key", Object: "some value here", FactType: "kv", Confidence: 0.9},
		{Subject: "conversation capture", Predicate: "key2", Object: "some other value", FactType: "kv", Confidence: 0.9},
		{Subject: "", Predicate: "key3", Object: "empty subject is fine", FactType: "kv", Confidence: 0.9},
		{Subject: "Q", Predicate: "email", Object: "test@example.com", FactType: "identity", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	// "Conversation Summary" and "conversation capture" dropped, but "" and "Q" kept
	if len(result) != 2 {
		t.Errorf("expected 2 facts (generic subjects dropped, empty kept), got %d: %+v", len(result), result)
	}
}

func TestGovernor_PerMemoryCap(t *testing.T) {
	cfg := DefaultGovernorConfig()
	cfg.MaxFactsPerMemory = 5
	gov := NewGovernor(cfg)

	// Generate 20 facts with varying quality
	facts := make([]ExtractedFact, 20)
	for i := range facts {
		facts[i] = ExtractedFact{
			Subject:    "test-subject",
			Predicate:  "key-" + string(rune('a'+i)),
			Object:     "value with enough length to pass filters easily here",
			FactType:   "kv",
			Confidence: 0.5 + float64(i)*0.025, // 0.5, 0.525, 0.55, ... 0.975
		}
	}

	result := gov.Apply(facts)
	if len(result) != 5 {
		t.Errorf("expected 5 facts (capped), got %d", len(result))
	}

	// Should keep the highest-confidence facts
	if len(result) > 0 && result[0].Confidence < 0.9 {
		t.Errorf("expected highest-confidence fact first, got confidence %f", result[0].Confidence)
	}
}

func TestGovernor_PreservesHighValueFacts(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "Q", Predicate: "email", Object: "test@example.com", FactType: "identity", Confidence: 0.9},
		{Subject: "Q", Predicate: "prefers", Object: "dark mode over light", FactType: "preference", Confidence: 0.86},
		{Subject: "team", Predicate: "decided", Object: "to use Go for the backend", FactType: "decision", Confidence: 0.84},
		{Subject: "Q", Predicate: "location", Object: "Philadelphia, PA", FactType: "location", Confidence: 0.86},
		{Subject: "Q", Predicate: "engaged_to", Object: "SB", FactType: "relationship", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 5 {
		t.Errorf("expected all 5 high-value facts preserved, got %d", len(result))
	}
}

func TestGovernor_CircularFacts(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "test", Predicate: "status", Object: "status", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "name", Object: "actual value here", FactType: "kv", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (circular dropped), got %d", len(result))
	}
}

func TestGovernor_MinLengthFilters(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "test", Predicate: "k", Object: "a value", FactType: "kv", Confidence: 0.9},            // pred too short
		{Subject: "test", Predicate: "key", Object: "v", FactType: "kv", Confidence: 0.9},                // obj too short
		{Subject: "test", Predicate: "key", Object: "valid value here", FactType: "kv", Confidence: 0.9}, // good
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (min length filters), got %d", len(result))
	}
}

func TestGovernor_NumericPredicate(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "test", Predicate: "123", Object: "some value here", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "$50.00", Object: "some value here", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "price", Object: "$50.00 per unit", FactType: "kv", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (numeric predicates dropped), got %d", len(result))
	}
	if len(result) > 0 && result[0].Predicate != "price" {
		t.Errorf("expected predicate 'price', got %q", result[0].Predicate)
	}
}

func TestGovernor_QualityScoreRanking(t *testing.T) {
	// Identity facts should rank higher than KV facts at same confidence
	identityFact := ExtractedFact{
		Subject: "Q", Predicate: "email", Object: "q@example.com",
		FactType: "identity", Confidence: 0.9,
	}
	kvFact := ExtractedFact{
		Subject: "config", Predicate: "port", Object: "8080",
		FactType: "kv", Confidence: 0.9,
	}

	identityScore := qualityScore(identityFact)
	kvScore := qualityScore(kvFact)

	if identityScore <= kvScore {
		t.Errorf("identity score (%f) should be > kv score (%f)", identityScore, kvScore)
	}
}

func TestGovernor_UnlimitedCap(t *testing.T) {
	cfg := DefaultGovernorConfig()
	cfg.MaxFactsPerMemory = 0 // Unlimited
	gov := NewGovernor(cfg)

	facts := make([]ExtractedFact, 100)
	for i := range facts {
		facts[i] = ExtractedFact{
			Subject:    "test-subject",
			Predicate:  "key-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Object:     "unique value long enough to pass filters",
			FactType:   "kv",
			Confidence: 0.9,
		}
	}

	result := gov.Apply(facts)
	if len(result) != 100 {
		t.Errorf("expected 100 facts (unlimited cap), got %d", len(result))
	}
}

func TestGovernor_OnlyFormattingObject(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "test", Predicate: "note", Object: "*** __ ``", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "note", Object: "real content here", FactType: "kv", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (formatting-only object dropped), got %d", len(result))
	}
}

func TestQualityScore_EmptySubjectPenalty(t *testing.T) {
	withSubject := ExtractedFact{
		Subject: "Q", Predicate: "email", Object: "test@example.com",
		FactType: "kv", Confidence: 0.9,
	}
	withoutSubject := ExtractedFact{
		Subject: "", Predicate: "email", Object: "test@example.com",
		FactType: "kv", Confidence: 0.9,
	}

	scoreWith := qualityScore(withSubject)
	scoreWithout := qualityScore(withoutSubject)

	if scoreWithout >= scoreWith {
		t.Errorf("empty subject score (%f) should be < subject score (%f)", scoreWithout, scoreWith)
	}
}
