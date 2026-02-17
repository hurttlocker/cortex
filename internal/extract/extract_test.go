package extract

import (
	"context"
	"testing"
)

func TestNewPipeline(t *testing.T) {
	pipeline := NewPipeline()
	if pipeline == nil {
		t.Fatal("NewPipeline() returned nil")
	}
	if len(pipeline.kvPatterns) == 0 {
		t.Error("Pipeline should have KV patterns initialized")
	}
	if len(pipeline.regexPatterns) == 0 {
		t.Error("Pipeline should have regex patterns initialized")
	}
}

// Test key-value pattern extraction
func TestExtractKeyValues_BoldColon(t *testing.T) {
	pipeline := NewPipeline()
	text := "**Name:** Alex\n**Location:** Philadelphia"

	facts := pipeline.extractKeyValues(text)

	if len(facts) != 2 {
		t.Fatalf("Expected 2 facts, got %d", len(facts))
	}

	// First fact: Name
	fact := facts[0]
	if fact.Predicate != "name" {
		t.Errorf("Expected predicate 'name', got %q", fact.Predicate)
	}
	if fact.Object != "Alex" {
		t.Errorf("Expected object 'Alex', got %q", fact.Object)
	}
	if fact.FactType != "kv" {
		t.Errorf("Expected type 'kv', got %q", fact.FactType)
	}
	if fact.Confidence != 0.9 {
		t.Errorf("Expected confidence 0.9, got %f", fact.Confidence)
	}
	if fact.SourceQuote != "**Name:** Alex" {
		t.Errorf("Expected source quote '**Name:** Alex', got %q", fact.SourceQuote)
	}

	// Second fact: Location
	fact = facts[1]
	if fact.Predicate != "location" {
		t.Errorf("Expected predicate 'location', got %q", fact.Predicate)
	}
	if fact.Object != "Philadelphia" {
		t.Errorf("Expected object 'Philadelphia', got %q", fact.Object)
	}
}

func TestExtractKeyValues_BulletColon(t *testing.T) {
	pipeline := NewPipeline()
	text := "- Broker: TradeStation\n* Strategy: QQQ options\n• Risk: Aggressive"

	facts := pipeline.extractKeyValues(text)

	if len(facts) != 3 {
		t.Fatalf("Expected 3 facts, got %d", len(facts))
	}

	expectedPairs := map[string]string{
		"broker":   "TradeStation",
		"strategy": "QQQ options",
		"risk":     "Aggressive",
	}

	for _, fact := range facts {
		expected, ok := expectedPairs[fact.Predicate]
		if !ok {
			t.Errorf("Unexpected predicate: %q", fact.Predicate)
			continue
		}
		if fact.Object != expected {
			t.Errorf("For predicate %q, expected object %q, got %q", fact.Predicate, expected, fact.Object)
		}
	}
}

func TestExtractKeyValues_Arrow(t *testing.T) {
	pipeline := NewPipeline()
	text := "Source → MEMORY.md\nEditor → vim"

	facts := pipeline.extractKeyValues(text)

	if len(facts) != 2 {
		t.Fatalf("Expected 2 facts, got %d", len(facts))
	}

	fact := facts[0]
	if fact.Predicate != "source" || fact.Object != "MEMORY.md" {
		t.Errorf("Expected source → MEMORY.md, got %s → %s", fact.Predicate, fact.Object)
	}
}

func TestExtractKeyValues_Equals(t *testing.T) {
	pipeline := NewPipeline()
	text := "theme = dark\nlanguage = go"

	facts := pipeline.extractKeyValues(text)

	if len(facts) != 2 {
		t.Fatalf("Expected 2 facts, got %d", len(facts))
	}

	fact := facts[0]
	if fact.Predicate != "theme" || fact.Object != "dark" {
		t.Errorf("Expected theme = dark, got %s = %s", fact.Predicate, fact.Object)
	}
}

func TestExtractKeyValues_EmDash(t *testing.T) {
	pipeline := NewPipeline()
	text := "Status — Active\nPriority — High"

	facts := pipeline.extractKeyValues(text)

	if len(facts) != 2 {
		t.Fatalf("Expected 2 facts, got %d", len(facts))
	}

	fact := facts[0]
	if fact.Predicate != "status" || fact.Object != "Active" {
		t.Errorf("Expected status — Active, got %s — %s", fact.Predicate, fact.Object)
	}
}

func TestExtractKeyValues_Priority(t *testing.T) {
	// Test that higher priority patterns match first
	pipeline := NewPipeline()
	text := "**Name:** Alex"

	facts := pipeline.extractKeyValues(text)

	if len(facts) != 1 {
		t.Fatalf("Expected 1 fact, got %d", len(facts))
	}

	// Should match bold_colon pattern, not simple_colon
	fact := facts[0]
	if fact.SourceQuote != "**Name:** Alex" {
		t.Errorf("Expected exact source quote, got %q", fact.SourceQuote)
	}
}

func TestExtractKeyValues_EmptyInput(t *testing.T) {
	pipeline := NewPipeline()
	facts := pipeline.extractKeyValues("")
	if len(facts) != 0 {
		t.Errorf("Expected 0 facts for empty input, got %d", len(facts))
	}
}

func TestExtractKeyValues_NoMatches(t *testing.T) {
	pipeline := NewPipeline()
	text := "This is just regular text with no patterns."
	facts := pipeline.extractKeyValues(text)
	if len(facts) != 0 {
		t.Errorf("Expected 0 facts for text with no patterns, got %d", len(facts))
	}
}

// Test regex pattern extraction
func TestExtractRegexPatterns_ISO8601Date(t *testing.T) {
	pipeline := NewPipeline()
	text := "Meeting on 2026-01-15 and another on 2026-12-31T15:30:00Z"

	facts := pipeline.extractRegexPatterns(text)

	// Should find 2 dates
	dateCount := 0
	for _, fact := range facts {
		if fact.FactType == "temporal" {
			dateCount++
		}
	}

	if dateCount != 2 {
		t.Errorf("Expected 2 temporal facts, got %d", dateCount)
	}

	// Check first date
	found := false
	for _, fact := range facts {
		if fact.Object == "2026-01-15" {
			found = true
			if fact.FactType != "temporal" {
				t.Errorf("Expected temporal fact type for date, got %q", fact.FactType)
			}
			if fact.Confidence != 0.7 {
				t.Errorf("Expected confidence 0.7 for regex match, got %f", fact.Confidence)
			}
			break
		}
	}
	if !found {
		t.Error("Did not find expected date 2026-01-15 in extracted facts")
	}
}

func TestExtractRegexPatterns_NaturalDate(t *testing.T) {
	pipeline := NewPipeline()
	text := "Born on March 15, 1992 and graduated in May 2015"

	facts := pipeline.extractRegexPatterns(text)

	dateCount := 0
	for _, fact := range facts {
		if fact.FactType == "temporal" {
			dateCount++
		}
	}

	if dateCount != 2 {
		t.Errorf("Expected 2 temporal facts, got %d", dateCount)
	}

	// Check for March date
	found := false
	for _, fact := range facts {
		if fact.Object == "March 15, 1992" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Did not find expected date 'March 15, 1992' in extracted facts")
	}
}

func TestExtractRegexPatterns_Email(t *testing.T) {
	pipeline := NewPipeline()
	text := "Contact me at john@example.com or support@company.co.uk"

	facts := pipeline.extractRegexPatterns(text)

	emailCount := 0
	emails := make(map[string]bool)
	for _, fact := range facts {
		if fact.FactType == "identity" && fact.Predicate == "email" {
			emailCount++
			emails[fact.Object] = true
		}
	}

	if emailCount != 2 {
		t.Errorf("Expected 2 email facts, got %d", emailCount)
	}

	expectedEmails := []string{"john@example.com", "support@company.co.uk"}
	for _, email := range expectedEmails {
		if !emails[email] {
			t.Errorf("Expected to find email %q", email)
		}
	}
}

func TestExtractRegexPatterns_PhoneUS(t *testing.T) {
	pipeline := NewPipeline()
	text := "Call me at (555) 123-4567 or 555.123.4567 or 555-123-4567"

	facts := pipeline.extractRegexPatterns(text)

	phoneCount := 0
	for _, fact := range facts {
		if fact.FactType == "identity" && fact.Predicate == "phone" {
			phoneCount++
		}
	}

	if phoneCount < 1 { // At least one phone number should be found
		t.Errorf("Expected at least 1 phone fact, got %d", phoneCount)
	}
}

func TestExtractRegexPatterns_URL(t *testing.T) {
	pipeline := NewPipeline()
	text := "Visit https://example.com and http://github.com/user/repo"

	facts := pipeline.extractRegexPatterns(text)

	urlCount := 0
	urls := make(map[string]bool)
	for _, fact := range facts {
		if fact.FactType == "kv" && fact.Predicate == "url" {
			urlCount++
			urls[fact.Object] = true
		}
	}

	if urlCount != 2 {
		t.Errorf("Expected 2 URL facts, got %d", urlCount)
	}

	expectedURLs := []string{"https://example.com", "http://github.com/user/repo"}
	for _, url := range expectedURLs {
		if !urls[url] {
			t.Errorf("Expected to find URL %q", url)
		}
	}
}

func TestExtractRegexPatterns_Money(t *testing.T) {
	pipeline := NewPipeline()
	text := "Budget is $1,500 or maybe $18K, but not more than $1.5M"

	facts := pipeline.extractRegexPatterns(text)

	moneyCount := 0
	amounts := make(map[string]bool)
	for _, fact := range facts {
		if fact.FactType == "kv" && fact.Predicate == "amount" {
			moneyCount++
			amounts[fact.Object] = true
		}
	}

	if moneyCount != 3 {
		t.Errorf("Expected 3 money facts, got %d", moneyCount)
	}

	expectedAmounts := []string{"1,500", "18K", "1.5M"}
	for _, amount := range expectedAmounts {
		if !amounts[amount] {
			t.Errorf("Expected to find amount %q", amount)
		}
	}
}

// Test pipeline end-to-end
func TestPipelineExtract_EndToEnd(t *testing.T) {
	pipeline := NewPipeline()
	ctx := context.Background()

	text := `# Trading Setup
**Broker:** TradeStation
- Strategy: QQQ 0DTE options
- Risk tolerance: Aggressive

Contact: john@trader.com
Started: 2026-01-15
Budget: $10,000`

	metadata := map[string]string{
		"format": "markdown",
	}

	facts, err := pipeline.Extract(ctx, text, metadata)
	if err != nil {
		t.Fatalf("Extract() failed: %v", err)
	}

	if len(facts) < 5 {
		t.Errorf("Expected at least 5 facts, got %d", len(facts))
	}

	// Check that all facts have extraction method set
	for _, fact := range facts {
		if fact.ExtractionMethod != "rules" {
			t.Errorf("Expected extraction method 'rules', got %q", fact.ExtractionMethod)
		}
		if fact.DecayRate <= 0 {
			t.Errorf("Expected positive decay rate, got %f", fact.DecayRate)
		}
	}

	// Look for specific facts
	expectedFacts := map[string]string{
		"broker":   "TradeStation",
		"strategy": "QQQ 0DTE options",
		"email":    "john@trader.com",
	}

	foundFacts := make(map[string]string)
	for _, fact := range facts {
		foundFacts[fact.Predicate] = fact.Object
	}

	for predicate, expectedObject := range expectedFacts {
		if actualObject, found := foundFacts[predicate]; !found {
			t.Errorf("Expected to find fact with predicate %q", predicate)
		} else if actualObject != expectedObject {
			t.Errorf("For predicate %q, expected object %q, got %q", predicate, expectedObject, actualObject)
		}
	}
}

func TestPipelineExtract_EmptyInput(t *testing.T) {
	pipeline := NewPipeline()
	ctx := context.Background()

	facts, err := pipeline.Extract(ctx, "", nil)
	if err != nil {
		t.Errorf("Extract() should not error on empty input: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("Expected 0 facts for empty input, got %d", len(facts))
	}
}

func TestPipelineExtract_WhitespaceOnly(t *testing.T) {
	pipeline := NewPipeline()
	ctx := context.Background()

	facts, err := pipeline.Extract(ctx, "   \n\t  \n  ", nil)
	if err != nil {
		t.Errorf("Extract() should not error on whitespace input: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("Expected 0 facts for whitespace input, got %d", len(facts))
	}
}

// Test decay rate assignment
func TestDecayRateAssignment(t *testing.T) {
	pipeline := NewPipeline()
	ctx := context.Background()

	// Create a fact with each type
	text := "email@test.com"
	facts, err := pipeline.Extract(ctx, text, nil)
	if err != nil {
		t.Fatalf("Extract() failed: %v", err)
	}

	if len(facts) == 0 {
		t.Fatal("Expected at least one fact")
	}

	// Check that decay rate was assigned
	fact := facts[0]
	if fact.FactType == "identity" {
		expectedRate := DecayRates["identity"]
		if fact.DecayRate != expectedRate {
			t.Errorf("Expected decay rate %f for identity fact, got %f", expectedRate, fact.DecayRate)
		}
	}
}

// Test deduplication
func TestDeduplicateFacts(t *testing.T) {
	facts := []ExtractedFact{
		{Subject: "", Predicate: "name", Object: "Alex", FactType: "kv"},
		{Subject: "", Predicate: "name", Object: "Alex", FactType: "kv"}, // duplicate
		{Subject: "", Predicate: "location", Object: "NYC", FactType: "kv"},
		{Subject: "", Predicate: "Name", Object: "Alex", FactType: "kv"}, // case difference
	}

	unique := deduplicateFacts(facts)

	if len(unique) != 2 { // name/Alex and location/NYC
		t.Errorf("Expected 2 unique facts after deduplication, got %d", len(unique))
	}
}

// Test real-world content
func TestExtractRealWorldContent(t *testing.T) {
	pipeline := NewPipeline()
	ctx := context.Background()

	// Sample memory content in MEMORY.md format
	text := `# Personal Info
**Name:** Q
**Location:** Philadelphia, PA
**Email:** q@example.com

# Trading
- Broker: TradeStation  
- Primary strategy: QQQ 0DTE options
- Risk tolerance: Aggressive
- Started trading: March 15, 2024
- Initial capital: $50,000

# Preferences
- Editor → vim
- Theme = dark
- Language = Go

# Important Dates
- Wedding: October 2026
- Next review: 2026-03-15

Visit https://github.com/hurttlocker/cortex for more info.
Call me at (555) 123-4567.`

	metadata := map[string]string{"format": "markdown"}

	facts, err := pipeline.Extract(ctx, text, metadata)
	if err != nil {
		t.Fatalf("Extract() failed: %v", err)
	}

	if len(facts) < 10 {
		t.Errorf("Expected at least 10 facts from real-world content, got %d", len(facts))
	}

	// Verify variety of fact types
	factTypes := make(map[string]int)
	for _, fact := range facts {
		factTypes[fact.FactType]++
	}

	expectedTypes := []string{"kv", "temporal", "identity"}
	for _, expectedType := range expectedTypes {
		if factTypes[expectedType] == 0 {
			t.Errorf("Expected at least one fact of type %q", expectedType)
		}
	}

	// Print facts for debugging (remove in final version)
	t.Logf("Extracted %d facts:", len(facts))
	for i, fact := range facts {
		t.Logf("  %d. [%s] %s: %s (confidence: %.1f, quote: %q)",
			i+1, fact.FactType, fact.Predicate, fact.Object, fact.Confidence, fact.SourceQuote)
	}
}

// Test confidence scores
func TestConfidenceScores(t *testing.T) {
	pipeline := NewPipeline()

	// KV patterns should have 0.9 confidence
	kvFacts := pipeline.extractKeyValues("**Name:** Alex")
	if len(kvFacts) > 0 && kvFacts[0].Confidence != 0.9 {
		t.Errorf("Expected KV fact confidence 0.9, got %f", kvFacts[0].Confidence)
	}

	// Regex patterns should have 0.7 confidence  
	regexFacts := pipeline.extractRegexPatterns("Contact me at john@example.com")
	for _, fact := range regexFacts {
		if fact.Confidence != 0.7 {
			t.Errorf("Expected regex fact confidence 0.7, got %f", fact.Confidence)
		}
	}
}

// Helper functions tests
func TestCleanKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"**Name**", "name"},
		{"*Location*", "location"},
		{"_Email_", "email"},
		{"  Broker  ", "broker"},
		{"Risk Tolerance", "risk tolerance"},
	}

	for _, test := range tests {
		result := cleanKey(test.input)
		if result != test.expected {
			t.Errorf("cleanKey(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestInferPredicate(t *testing.T) {
	tests := []struct {
		pattern  string
		value    string
		expected string
	}{
		{"email", "john@example.com", "email"},
		{"phone_us", "(555) 123-4567", "phone"},
		{"money", "$1,000", "amount"},
		{"url", "https://example.com", "url"},
		{"iso_date", "2026-01-15", "date"},
	}

	for _, test := range tests {
		result := inferPredicate(test.pattern, test.value)
		if result != test.expected {
			t.Errorf("inferPredicate(%q, %q) = %q, expected %q", test.pattern, test.value, result, test.expected)
		}
	}
}