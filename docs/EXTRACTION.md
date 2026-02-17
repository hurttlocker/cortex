# Extraction Pipeline — Deep Design

> The extraction layer is the make-or-break component. This doc covers the full pipeline design, what's easy, what's hard, and exactly how we solve it.

---

## Pipeline Overview

```
Document comes in
       │
   ┌───┴───┐
   │ PARSE │ ← Format-specific (MD parser, JSON decoder, etc.)
   └───┬───┘
       │
   ┌───┴────┐
   │ CHUNK  │ ← Break into memory-sized units
   └───┬────┘
       │
   ┌───┴─────────┐
   │  EXTRACT     │ ← Two tiers (see below)
   │  (Tier 1/2)  │
   └───┬──────────┘
       │
   ┌───┴──────┐
   │ DEDUP    │ ← Compare against existing memory
   └───┬──────┘
       │
   ┌───┴──────┐
   │ STORE    │ ← SQLite + FTS5 + embeddings
   └──────────┘
```

Each step is an interface. You can swap any layer independently.

---

## Tier 1: Rule-Based Extraction (Zero Dependencies)

### What It Handles Well (Structured Input)

**Markdown with headers and bullets:**
```markdown
## Trading
- Broker: TradeStation
- Strategy: QQQ/SPY 0DTE options
- Risk tolerance: Aggressive
```

Extraction is trivial:
```
Header "Trading" → category tag
"Broker: TradeStation" → {key: "broker", value: "TradeStation", type: "kv"}
"Strategy: QQQ/SPY..." → {key: "strategy", value: "QQQ/SPY 0DTE options", type: "kv"}
```

**Pattern matching on separators:** `:`, `→`, `=`, `—`
**Markdown headers** become category tags.
**Bullet nesting** becomes hierarchy.
**This handles 80% of what OpenClaw/Claude Code users have** because their memory IS structured.

**JSON:**
```json
{"name": "Q", "location": "Philadelphia", "broker": "TradeStation"}
```
Every key-value pair is a fact. Nested objects become relationships.

**CSV:**
Headers become keys, each row becomes a fact set.

**Regex patterns** catch structured data types:
- Dates: ISO 8601, natural language ("March 15", "next Tuesday")
- Emails: standard email regex
- Phone numbers: various formats
- URLs: http/https patterns
- Money: $X,XXX.XX patterns
- Addresses: street number + name patterns

### Where Tier 1 FAILS (Unstructured Input)

```
"Yesterday I talked to SB about moving the wedding to October 
instead of September because her mom can't make it in September. 
We also decided to go with the mid-tier budget, around $18K."
```

A human extracts:
- Wedding month changed: September → October (DECISION)
- Reason: SB's mom unavailability in September (RELATIONSHIP + TEMPORAL)
- Budget decision: mid-tier, ~$18K (DECISION)
- People involved: Q, SB, SB's mom (ENTITIES + RELATIONSHIPS)

Tier 1 gets: "SB" (person), "October" (date), "September" (date), "$18K" (money).
**It misses:** Relationships, decisions, coreference ("her mom" → SB's mom), temporal reasoning ("instead of" implies change).

### The Go NLP Reality

Go's NLP ecosystem (`prose` library) provides:
- ✅ Tokenization
- ✅ Sentence splitting
- ✅ Basic NER (person, location, organization)
- ❌ Coreference resolution ("his" → who?)
- ❌ Relationship extraction ("Alice manages Bob")
- ❌ Complex fact patterns (temporal changes, implicit decisions)

Python has spaCy, transformers, NLTK — a decade of tooling. Go has almost nothing.

**This is why we need Tier 2 from day one.**

---

## Tier 2: LLM-Assist Extraction (Any Provider)

### The Instructor Pattern

We piggyback on the pattern established by [Instructor](https://github.com/jxnl/instructor) (25K+ GitHub stars, by Jason Liu): **schema + prompt → validated structured JSON.**

We don't need the Python library. We need the PATTERN:
1. Define a JSON schema for facts
2. Send schema + raw text to any LLM
3. Validate the response against the schema
4. Retry on validation failure

**This is ~100 lines of Go.** The innovation isn't the LLM call — it's the schema validation loop.

### Extraction Schema

```json
{
  "facts": [
    {
      "subject": "string — who/what this is about",
      "predicate": "string — the relationship or attribute type",
      "object": "string — the value or related entity",
      "type": "enum: preference | decision | relationship | temporal | identity | location | kv | state",
      "confidence": "float 0-1 — how confident the extraction is",
      "temporal": "string? — when this was true, if mentioned",
      "source_quote": "string — exact text this was extracted from"
    }
  ]
}
```

### Extraction Prompt Template

```
You are a fact extraction engine. Extract structured facts from the text below.

Rules:
- Extract ONLY facts explicitly stated in the text
- Do NOT infer or assume facts not present
- Include the exact source quote for each fact
- Assign confidence based on how explicitly the fact is stated
- For temporal facts, include when the fact applies

Schema: [JSON schema above]

Text:
---
{document_text}
---

Return valid JSON matching the schema. Nothing else.
```

### Provider Support

Any OpenAI-compatible API works:

```bash
# Free, local
cortex import chat-log.txt --llm ollama/gemma2:2b

# Almost free cloud
cortex import chat-log.txt --llm openai/gpt-4.1-nano     # ~$0.10/M tokens
cortex import chat-log.txt --llm deepseek/v3              # Dirt cheap

# High quality
cortex import chat-log.txt --llm anthropic/haiku           
cortex import chat-log.txt --llm anthropic/sonnet          

# Any provider via OpenRouter
cortex import chat-log.txt --llm openrouter/any-model     
```

### Constrained Decoding (Outlines)

For local models via Ollama, [Outlines](https://github.com/outlines-dev/outlines) enables **constrained decoding** — modifying the model's sampling to ONLY generate tokens that produce valid JSON matching our schema. The model literally CANNOT output garbage.

This is the cutting edge of local LLM reliability. When someone runs Cortex with a local model, we can guarantee structured output.

### Key Design Decisions

- LLM-assist is OPTIONAL. Local extraction is always default.
- Supports ANY OpenAI-compatible API endpoint (no vendor lock-in)
- The LLM is used for extraction ONLY — search stays 100% local
- The LLM NEVER sees your full memory store — only the document being imported
- Extraction prompts are versioned and reproducible
- Failed extractions fall back to Tier 1 (never lose data)

---

## Deduplication Strategy

### The Problem

You import MEMORY.md. Then daily notes from 30 days. Then old conversation logs.

Now you have:
- "Q is in Philadelphia" (from MEMORY.md)
- "User located in Philly, PA" (from a daily note)
- "I'm based in Philadelphia" (from a conversation log)

Same fact. Three different wordings. Three different sources.

### The Approach

1. **Embedding similarity** — If two facts have >0.92 cosine similarity, flag as potential duplicate
2. **Entity-attribute clustering** — If both facts mention the same subject + similar attribute, likely same fact
3. **Temporal analysis** — If facts are about the same attribute but different values at different times, it's an UPDATE, not a duplicate
4. **Conservative by default** — Flag for user review rather than auto-merging

```bash
cortex conflicts
# ⚠️  Potential duplicates:
#   [1] "Q is in Philadelphia" (MEMORY.md:4, imported 2026-01-15)
#   [2] "User located in Philly, PA" (2026-01-20.md:12, imported 2026-02-16)
#   Similarity: 0.94 — Likely same fact
#   Action: [m]erge / [k]eep both / [d]elete one
```

---

## Future Extraction Improvements (v1.1+)

### GLiNER — Zero-Shot NER

[GLiNER](https://github.com/urchade/GLiNER) does Named Entity Recognition without fine-tuning. You pass ANY entity types:

```
entities: ["person", "location", "preference", "decision", "date", "tool", "project"]
text: "Q decided to use TradeStation for trading QQQ options"
→ {person: "Q", tool: "TradeStation", decision: "use TradeStation for trading QQQ options"}
```

~400MB ONNX model. Runs locally. Could be our Tier 1.5 — better than regex, no LLM needed.

### Fine-Tuned T5-Small

A ~60MB T5-small model fine-tuned on structured extraction. Produces JSON facts from text. Trained on our extraction schema. Local, fast, no API keys.

This would bridge the gap between rule-based and LLM-assisted extraction.
