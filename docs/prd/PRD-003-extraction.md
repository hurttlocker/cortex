# PRD-003: Extraction Pipeline

**Status:** Draft  
**Priority:** P0  
**Phase:** 1 (Tier 1 + Tier 2 basic)  
**Depends On:** PRD-001 (Storage), PRD-002 (Import)  
**Package:** `internal/extract/`

---

## Overview

The extraction pipeline identifies structured facts from raw imported text. It has two tiers: Tier 1 (rule-based, zero dependencies) handles structured input like Markdown and JSON. Tier 2 (LLM-assist, optional) handles unstructured text like conversation logs. Both tiers produce identical `[]ExtractedFact` output. The storage layer doesn't know or care which tier produced a fact.

## Problem

Raw imported text is useful for search, but agents need structured facts — key:value pairs, relationships, preferences, temporal information. Extracting these facts locally (without an LLM) is essential for the zero-dependency promise. But being honest about what local extraction can't do (coreference resolution, relationship extraction from prose) means offering optional LLM-assist from day one.

---

## Requirements

### Must Have (P0)

- **Tier 1: Rule-based extraction**

  - **Key:value pattern matching** — detect and extract patterns with these separators:
    - Colon: `Key: Value`, `**Key:** Value`, `Key — Value`
    - Arrow: `Key → Value`
    - Equals: `Key = Value`
    - Must handle whitespace variations and markdown formatting
    - Output: `{subject: "", predicate: key, object: value, type: "kv"}`

  - **Markdown structure extraction**
    - Headers (`## `, `### `) → category tags
    - Header nesting → hierarchy (e.g., `## Work > ### Projects > #### Alpha`)
    - Bullet lists → individual facts
    - Numbered lists → ordered facts

  - **Regex patterns for common data types**
    - Dates: ISO 8601 (`2026-01-15`), natural language (`March 15, 1992`, `January 2024`)
    - Email addresses: standard email regex
    - Phone numbers: US formats, international with `+` prefix
    - URLs: `http://`, `https://`, `www.` prefixed
    - Money: `$X,XXX.XX`, `$XK`, `$X.XM` patterns
    - Addresses: basic street address patterns

  - **Basic NER via prose library**
    - Person names (proper nouns in person-name contexts)
    - Location names (cities, states, countries)
    - Organization names (company names, team names)
    - Note: prose NER quality is limited — this is best-effort

  - **Output format**: `[]ExtractedFact` with consistent structure
    - Every fact includes `source_quote` — the exact text it was extracted from
    - Every fact includes `confidence` score (0.0–1.0)
    - Tier 1 confidence: 0.9 for explicit key:value, 0.7 for regex matches, 0.5 for NER

- **Tier 2: LLM-Assist extraction** (optional, opt-in)

  - **Instructor pattern implementation** — schema + prompt → validated JSON
    - Define extraction JSON schema (see Technical Design)
    - Build prompt: system prompt + schema + document text
    - Send to any OpenAI-compatible chat completions endpoint
    - Parse JSON response
    - Validate against schema
    - Retry up to 3 times on validation failure
    - Fall back to Tier 1 on complete failure (never lose data)

  - **Provider support** — any OpenAI-compatible API
    - Parse `--llm <provider>/<model>` format
    - Resolve endpoint URL from provider name:
      - `ollama/*` → `http://localhost:11434/v1/chat/completions`
      - `openai/*` → `https://api.openai.com/v1/chat/completions`
      - `deepseek/*` → `https://api.deepseek.com/v1/chat/completions`
      - `openrouter/*` → `https://openrouter.ai/api/v1/chat/completions`
      - Custom endpoint via config file or env var
    - API key from: config file → env var → prompt user

  - **Privacy guarantee**: LLM only sees the document being imported, never the existing memory store

  - **Extraction prompts versioned** in `internal/extract/prompts/`
    - System prompt: extraction rules, output format
    - User prompt template: schema + document chunk
    - Prompts stored as Go embed files for reproducibility

- **Unified output**: both tiers produce `[]ExtractedFact`
  - Downstream code (storage, search) doesn't know or care which tier extracted a fact
  - Facts from Tier 2 include `extraction_method: "llm"` metadata
  - Facts from Tier 1 include `extraction_method: "rules"` metadata

### Should Have (P1)

- **Chunking for large documents**
  - If document exceeds model context window, split into chunks
  - Overlap chunks by 50 tokens for continuity
  - Merge extracted facts across chunks (dedup by similarity)

- **Cost estimation** (`--dry-run`)
  - Count tokens in document (approximate)
  - Estimate cost based on model pricing
  - Show before proceeding: "Estimated: 4,200 tokens, ~$0.0004 with gpt-4.1-nano"

- **Config file support**
  - `~/.cortex/config.yaml` with LLM defaults:
    ```yaml
    llm:
      default: ollama/gemma2:2b
      endpoint: http://localhost:11434/v1
      api_key: ""  # not needed for Ollama
    ```

### Future (P2)

- GLiNER for zero-shot NER (~400MB ONNX model)
- Fine-tuned T5-small for structured extraction (~60MB)
- Constrained decoding via Outlines for guaranteed valid JSON from local models
- Coreference resolution
- Relationship extraction from prose

---

## Technical Design

### ExtractedFact Schema

```go
package extract

// ExtractedFact represents a single structured fact extracted from text.
type ExtractedFact struct {
    Subject          string  `json:"subject"`           // Who/what this is about
    Predicate        string  `json:"predicate"`         // The relationship or attribute
    Object           string  `json:"object"`            // The value or related entity
    FactType         string  `json:"type"`              // kv, relationship, preference, temporal, identity, location, decision, state
    Confidence       float64 `json:"confidence"`        // 0.0–1.0
    Temporal         string  `json:"temporal,omitempty"` // When this was true (if mentioned)
    SourceQuote      string  `json:"source_quote"`      // Exact text this was extracted from
    ExtractionMethod string  `json:"extraction_method"` // "rules" or "llm"
}

// Extractor processes raw text and returns structured facts.
type Extractor interface {
    Extract(ctx context.Context, text string, metadata map[string]string) ([]ExtractedFact, error)
}

// Pipeline runs extraction through both tiers.
type Pipeline struct {
    tier1 *RuleExtractor
    tier2 *LLMExtractor // nil if not configured
}

// NewPipeline creates an extraction pipeline.
// If llmConfig is nil, only Tier 1 (rules) is used.
func NewPipeline(llmConfig *LLMConfig) *Pipeline { ... }

// Extract runs the appropriate tier(s) on the input text.
func (p *Pipeline) Extract(ctx context.Context, text string, metadata map[string]string) ([]ExtractedFact, error) {
    // Always run Tier 1
    facts := p.tier1.Extract(ctx, text, metadata)
    
    // If Tier 2 is configured, also run LLM extraction
    if p.tier2 != nil {
        llmFacts, err := p.tier2.Extract(ctx, text, metadata)
        if err != nil {
            // Log warning, fall back to Tier 1 results only
            return facts, nil
        }
        // Merge: prefer LLM facts where they overlap, keep unique facts from both
        facts = mergeFacts(facts, llmFacts)
    }
    
    return facts, nil
}
```

### Tier 1: Rule-Based Extractor

```go
type RuleExtractor struct{}

func (r *RuleExtractor) Extract(ctx context.Context, text string, metadata map[string]string) ([]ExtractedFact, error) {
    var facts []ExtractedFact
    
    // 1. Key:value pattern matching
    facts = append(facts, r.extractKeyValues(text)...)
    
    // 2. Regex patterns (dates, emails, phones, URLs, money)
    facts = append(facts, r.extractPatterns(text)...)
    
    // 3. NER via prose (persons, locations, organizations)
    facts = append(facts, r.extractEntities(text)...)
    
    // 4. Markdown structure (if metadata indicates markdown source)
    if metadata["format"] == "markdown" {
        facts = append(facts, r.extractMarkdownStructure(text)...)
    }
    
    // Deduplicate within this extraction run
    facts = dedup(facts)
    
    return facts, nil
}
```

### Key:Value Pattern Matching

```go
// Patterns to match (in priority order):
var kvPatterns = []struct {
    regex *regexp.Regexp
    sep   string
}{
    {regexp.MustCompile(`^[-*]\s+\*\*([^*]+)\*\*:\s*(.+)$`), ":"},   // - **Key:** Value
    {regexp.MustCompile(`^[-*]\s+([^:]+):\s+(.+)$`), ":"},            // - Key: Value
    {regexp.MustCompile(`^([^→]+)\s*→\s*(.+)$`), "→"},                // Key → Value
    {regexp.MustCompile(`^([^=]+)\s*=\s*(.+)$`), "="},                // Key = Value
    {regexp.MustCompile(`^([^—]+)\s*—\s*(.+)$`), "—"},                // Key — Value
}
```

### Tier 2: LLM Extractor

```go
type LLMConfig struct {
    Provider string // "ollama", "openai", "deepseek", "openrouter", "custom"
    Model    string // "gemma2:2b", "gpt-4.1-nano", etc.
    Endpoint string // Full API URL
    APIKey   string
    MaxRetries int  // Default: 3
}

type LLMExtractor struct {
    config LLMConfig
    client *http.Client
}

func (l *LLMExtractor) Extract(ctx context.Context, text string, metadata map[string]string) ([]ExtractedFact, error) {
    // 1. Build prompt from template
    prompt := l.buildPrompt(text)
    
    // 2. Send to LLM endpoint (OpenAI-compatible chat completions)
    response, err := l.callLLM(ctx, prompt)
    if err != nil {
        return nil, fmt.Errorf("LLM call failed: %w", err)
    }
    
    // 3. Parse JSON response
    facts, err := l.parseResponse(response)
    if err != nil {
        // Retry up to MaxRetries times
        for i := 0; i < l.config.MaxRetries; i++ {
            response, err = l.callLLM(ctx, prompt)
            if err != nil { continue }
            facts, err = l.parseResponse(response)
            if err == nil { break }
        }
        if err != nil {
            return nil, fmt.Errorf("LLM extraction failed after %d retries: %w", l.config.MaxRetries, err)
        }
    }
    
    // 4. Set extraction method on all facts
    for i := range facts {
        facts[i].ExtractionMethod = "llm"
    }
    
    return facts, nil
}
```

### Extraction Prompt (versioned in `internal/extract/prompts/`)

```
// prompts/system_v1.txt
You are a fact extraction engine. Extract structured facts from the text below.

Rules:
- Extract ONLY facts explicitly stated in the text
- Do NOT infer or assume facts not present
- Include the exact source quote for each fact
- Assign confidence based on how explicitly the fact is stated (0.0–1.0)
- For temporal facts, include when the fact applies
- Classify each fact into one of these types: kv, relationship, preference, temporal, identity, location, decision, state

Return valid JSON matching this schema:
{
  "facts": [
    {
      "subject": "who/what this is about",
      "predicate": "the relationship or attribute",
      "object": "the value or related entity",
      "type": "one of: kv, relationship, preference, temporal, identity, location, decision, state",
      "confidence": 0.0-1.0,
      "temporal": "when this was true, if mentioned (optional)",
      "source_quote": "exact text this was extracted from"
    }
  ]
}

Return ONLY the JSON object. No markdown, no explanation, no preamble.
```

### Fact Merging (Tier 1 + Tier 2)

```go
func mergeFacts(tier1, tier2 []ExtractedFact) []ExtractedFact {
    // 1. Build a set of tier2 facts (keyed by subject + predicate)
    // 2. For each tier1 fact:
    //    a. If matching tier2 fact exists: prefer tier2 (usually higher quality)
    //    b. If no match: keep tier1 fact
    // 3. Add any tier2 facts not matched by tier1
    // 4. Return merged set
}
```

### Decay Rate Assignment

When storing extracted facts, assign decay rates based on `fact_type`:

```go
var decayRates = map[string]float64{
    "identity":     0.001, // half-life: 693 days
    "decision":     0.002, // half-life: 347 days
    "relationship": 0.003, // half-life: 231 days
    "location":     0.005, // half-life: 139 days
    "preference":   0.01,  // half-life: 69 days
    "state":        0.05,  // half-life: 14 days
    "temporal":     0.1,   // half-life: 7 days
    "kv":           0.01,  // half-life: 69 days (default)
}
```

---

## Test Strategy

### Unit Tests

**Rule Extractor:**
- **TestExtractKV_Colon** — `"Name: Alex"` → `{predicate: "Name", object: "Alex", type: "kv"}`
- **TestExtractKV_BoldColon** — `"**Name:** Alex"` → same output
- **TestExtractKV_Arrow** — `"Source → MEMORY.md"` → correct extraction
- **TestExtractKV_Equals** — `"theme = dark"` → correct extraction
- **TestExtractKV_MultiplePerLine** — only first separator matched
- **TestExtractPattern_Dates** — ISO dates, natural language dates
- **TestExtractPattern_Emails** — various email formats
- **TestExtractPattern_URLs** — http, https, www prefixed
- **TestExtractPattern_Money** — `$1,000`, `$18K`, `$1.5M`
- **TestExtractPattern_PhoneNumbers** — US and international formats
- **TestExtractEntities_Persons** — proper nouns identified as persons
- **TestExtractEntities_Locations** — city/state/country names
- **TestExtractMarkdown_Headers** — headers become category metadata
- **TestExtract_EmptyInput** — returns empty slice, no error
- **TestExtract_NoFacts** — text with no extractable facts returns empty

**LLM Extractor:**
- **TestLLMExtract_ValidResponse** — mock LLM returns valid JSON, facts extracted
- **TestLLMExtract_InvalidJSON** — mock LLM returns garbage, retry logic fires
- **TestLLMExtract_RetrySuccess** — fails twice, succeeds on third try
- **TestLLMExtract_RetryExhausted** — fails all retries, returns error
- **TestLLMExtract_Timeout** — context deadline exceeded, returns error
- **TestParseProviderModel** — `"ollama/gemma2:2b"` → provider + model + endpoint

**Pipeline:**
- **TestPipeline_Tier1Only** — no LLM config, only rule extraction runs
- **TestPipeline_BothTiers** — both tiers run, results merged
- **TestPipeline_LLMFallback** — LLM fails, Tier 1 results returned
- **TestMergeFacts_NoOverlap** — all facts kept from both tiers
- **TestMergeFacts_Overlap** — overlapping facts, LLM version preferred
- **TestMergeFacts_Dedup** — identical facts deduplicated

### Integration Tests

- **TestExtractSampleMemory** — extract from `tests/testdata/sample-memory.md`, verify >20 facts
- **TestExtractSampleJSON** — extract from `tests/testdata/sample-data.json`, verify structure
- **TestDecayRateAssignment** — verify correct decay rates assigned by fact type

---

## Open Questions

1. **Fact type classification accuracy:** How do we handle ambiguous types? (e.g., "Prefers Go" — is it `preference` or `kv`?)
2. **Extraction aggressiveness:** High recall + noise vs. high precision + missed facts? (Start conservative, tune based on feedback)
3. **Multi-language support:** Should regex patterns handle non-English dates, money formats? (v1: English only)
4. **Prompt versioning:** How do we handle prompt version changes that change extraction output? (Stamp version on facts)
