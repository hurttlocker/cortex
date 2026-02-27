package extract

import (
	"testing"
)

func TestGovernor_DropMarkdownJunk(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	junkFacts := []ExtractedFact{
		{Subject: "test", Predicate: "note", Object: "**", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "note", Object: "---", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "note", Object: "|---|---|", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "note", Object: "***", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "note", Object: "```", FactType: "kv", Confidence: 0.9},
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
		{Subject: "Conversation Summary", Predicate: "setting", Object: "some value here", FactType: "kv", Confidence: 0.9},
		{Subject: "conversation capture", Predicate: "topic_note", Object: "some other value", FactType: "kv", Confidence: 0.9},
		{Subject: "", Predicate: "notes", Object: "empty subject is fine", FactType: "kv", Confidence: 0.9},
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
		{Subject: "Q", Predicate: "engaged_to", Object: "SB (Lemons)", FactType: "relationship", Confidence: 0.9},
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
		{Subject: "test", Predicate: "label", Object: "actual value here", FactType: "kv", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (circular dropped), got %d", len(result))
	}
}

func TestGovernor_MinLengthFilters(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "test", Predicate: "k", Object: "a value here", FactType: "kv", Confidence: 0.9},          // pred too short (1 char)
		{Subject: "test", Predicate: "key", Object: "val", FactType: "kv", Confidence: 0.9},                 // pred too short (3 chars, min is 5)
		{Subject: "test", Predicate: "name", Object: "ab", FactType: "kv", Confidence: 0.9},                 // pred too short (4 chars, min is 5) + obj too short (2 chars, min is 3)
		{Subject: "test", Predicate: "status", Object: "valid value here", FactType: "kv", Confidence: 0.9}, // good (pred=6, obj=16)
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (min length filters), got %d: %+v", len(result), result)
	}
}

func TestGovernor_NumericPredicate(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "test", Predicate: "123", Object: "some value here", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "$50.00", Object: "some value here", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "price", Object: "$50.00 per unit", FactType: "kv", Confidence: 0.9}, // "price" is 5 chars, passes
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
		{Subject: "test", Predicate: "label", Object: "*** __ ``", FactType: "kv", Confidence: 0.9},
		{Subject: "test", Predicate: "label", Object: "real content here", FactType: "kv", Confidence: 0.9},
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

func TestGovernor_SectionHeaderSubjects(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "## Trading", Predicate: "status", Object: "active trading here", FactType: "kv", Confidence: 0.9},
		{Subject: "### Key Dates", Predicate: "section", Object: "important dates listed", FactType: "kv", Confidence: 0.9},
		{Subject: "# MEMORY.md", Predicate: "title_info", Object: "long term memory file", FactType: "kv", Confidence: 0.9},
		{Subject: "Q", Predicate: "email", Object: "test@example.com", FactType: "identity", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (section headers dropped), got %d: %+v", len(result), result)
	}
	if len(result) > 0 && result[0].Subject != "Q" {
		t.Errorf("expected subject 'Q', got %q", result[0].Subject)
	}
}

func TestGovernor_BoldSubjects(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "**Key Dates**", Predicate: "section", Object: "important dates listed", FactType: "kv", Confidence: 0.9},
		{Subject: "**Active Projects**", Predicate: "header", Object: "projects are listed below", FactType: "kv", Confidence: 0.9},
		{Subject: "Cortex", Predicate: "version", Object: "v0.9.0 released", FactType: "state", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (bold subjects dropped), got %d: %+v", len(result), result)
	}
}

func TestGovernor_FilePathPredicates(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "script", Predicate: "scripts/cortex.sh search", Object: "search command here", FactType: "kv", Confidence: 0.9},
		{Subject: "binary", Predicate: "/usr/local/bin/engram", Object: "Go v0.1.0 binary", FactType: "kv", Confidence: 0.9},
		{Subject: "Cortex", Predicate: "status", Object: "running normally here", FactType: "state", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (file path predicates dropped), got %d: %+v", len(result), result)
	}
}

func TestGovernor_LongObjects(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	longObj := "This is a very long sentence fragment that contains more than two hundred characters of text which is almost certainly a paragraph or sentence accidentally captured by the regex extractor rather than a proper factual triple that we want to store in the knowledge base."
	facts := []ExtractedFact{
		{Subject: "test", Predicate: "details", Object: longObj, FactType: "kv", Confidence: 0.9},
		{Subject: "Q", Predicate: "email", Object: "test@example.com", FactType: "identity", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (long object dropped), got %d: %+v", len(result), result)
	}
}

func TestGovernor_CheckboxSubjects(t *testing.T) {
	gov := NewGovernor(DefaultGovernorConfig())

	facts := []ExtractedFact{
		{Subject: "[x] Build enrichment", Predicate: "status", Object: "completed successfully", FactType: "kv", Confidence: 0.9},
		{Subject: "[ ] Tag v0.9.0", Predicate: "status", Object: "pending release work", FactType: "kv", Confidence: 0.9},
		{Subject: "Cortex", Predicate: "status", Object: "running fine here", FactType: "state", Confidence: 0.9},
	}

	result := gov.Apply(facts)
	if len(result) != 1 {
		t.Errorf("expected 1 fact (checkbox subjects dropped), got %d: %+v", len(result), result)
	}
}

func TestGovernor_TighterDefaults(t *testing.T) {
	// Verify the #227 tightened defaults
	cfg := DefaultGovernorConfig()
	if cfg.MaxFactsPerMemory != 10 {
		t.Errorf("expected MaxFactsPerMemory=10, got %d", cfg.MaxFactsPerMemory)
	}
	if cfg.MinObjectLength != 3 {
		t.Errorf("expected MinObjectLength=3, got %d", cfg.MinObjectLength)
	}
	if cfg.MinPredicateLength != 5 {
		t.Errorf("expected MinPredicateLength=5, got %d", cfg.MinPredicateLength)
	}

	acfg := AutoCaptureGovernorConfig()
	if acfg.MaxFactsPerMemory != 5 {
		t.Errorf("expected AutoCapture MaxFactsPerMemory=5, got %d", acfg.MaxFactsPerMemory)
	}
	if acfg.MinObjectLength != 4 {
		t.Errorf("expected AutoCapture MinObjectLength=4, got %d", acfg.MinObjectLength)
	}
}
