# Novel Ideas — What Nobody Else Is Doing

> These ideas differentiate Cortex from every existing memory tool. Some are MVP-ready, some are v2+. All should influence architecture from day one.

---

## 1. Memory Provenance Chains (Citation Graphs for Facts)

### The Concept

Every memory tool stores facts. Nobody tracks what those facts DID.

Current approach:
```
{fact: "Q lives in Philadelphia", source: "MEMORY.md"}
```

Cortex approach:
```
{fact: "Q lives in Philadelphia", source: "MEMORY.md:4"}
  ├── Confirmed by: conversation on 2025-09-22
  ├── Used in: wedding venue search → influenced PHL→DR flight routing
  ├── Used in: timezone detection → EST assumption in all scheduling
  ├── Related to: {fact: "Q's address: 1001 S Broad St"}
  ├── Recall count: 47
  ├── Last recalled: 2026-02-16T21:30:00Z
  └── Confidence: 0.98 (recently confirmed, frequently used)
```

### Why This Matters

You can ask questions nobody else can answer:
- **"What decisions were influenced by the fact that Q is in Philadelphia?"**
- **"If Q moved to a new city, what else breaks?"** (cascade analysis)
- **"Which facts are load-bearing vs decorative?"** (high recall count = important)
- **"What's the full chain from raw data → decision?"** (audit trail)

### Implementation

Not hard — when the search function retrieves a fact for a conversation, log the retrieval with context. Build the provenance graph over time.

```sql
CREATE TABLE recall_log (
    id          INTEGER PRIMARY KEY,
    fact_id     INTEGER REFERENCES facts(id),
    recalled_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    context     TEXT,  -- what query/conversation triggered the recall
    session_id  TEXT   -- which agent session used this fact
);
```

Query: "What decisions used this fact?"
```sql
SELECT f.*, r.context, r.recalled_at 
FROM facts f 
JOIN recall_log r ON f.id = r.fact_id 
WHERE f.id = ? 
ORDER BY r.recalled_at DESC;
```

### Phase: MVP (recall logging) → v1.1 (provenance UI) → v2 (cascade analysis)

---

## 2. Confidence Decay (Ebbinghaus Forgetting Curve for AI)

### The Concept

Human memory decays unless reinforced. AI memory should too.

Every fact has a confidence score that decays over time based on its TYPE — unless the fact is reinforced (re-confirmed in conversation, re-imported, or recalled and not contradicted).

### Decay Rates by Fact Type

```
Identity facts (name, birthday):     decay = 0.001/day → 50% after 693 days
Location facts (lives in X):         decay = 0.005/day → 50% after 139 days
Preference facts (likes Y):          decay = 0.01/day  → 50% after 69 days
Temporal facts (meeting Tuesday):    decay = 0.1/day   → 50% after 7 days
State facts (working on project Z):  decay = 0.05/day  → 50% after 14 days
Relationship facts (X works at Y):   decay = 0.003/day → 50% after 231 days
Decision facts (chose option A):     decay = 0.002/day → 50% after 347 days
```

### The Math

```
confidence(t) = initial_confidence × e^(-decay_rate × days_since_last_reinforcement)
```

When a fact is reinforced (re-imported, confirmed in conversation, explicitly verified):
```
confidence = min(1.0, current_confidence + 0.3)
last_reinforcement = now
```

### What This Enables

```bash
cortex stale
# Facts below 50% confidence:
#
# ⚠️  0.12  "Meeting with vendor on Tuesday" (temporal, 45 days old, never reinforced)
# ⚠️  0.34  "Working on Eyes Web redesign" (state, 28 days old, not recent)
# ⚠️  0.41  "Prefers VSCode over Vim" (preference, 89 days old)
#
# ✅  0.98  "Q's birthday: December 25" (identity, 200 days old, type-adjusted)
# ✅  0.95  "Broker: TradeStation" (kv, 60 days old, recalled 12 times)
```

### Why Nobody Does This

Memory tools are built by ML researchers who think in vectors and embeddings. Cognitive science concepts like the Ebbinghaus forgetting curve come from psychology. Nobody's bridging these disciplines.

It's not technically hard — it's a float column and an exponential decay formula. The insight is applying it to AI memory at all.

### Phase: v1.0 (implement decay model) → v1.1 (auto-reinforcement from recalls) → v2 (user-tunable decay rates)

---

## 3. Memory Lenses (Context-Dependent Views)

### The Concept

The same memory store, different views for different contexts:

```bash
cortex search "what's the plan?" --lens trading
# Returns: trading strategy, positions, market analysis

cortex search "what's the plan?" --lens wedding  
# Returns: Cabrera villa, budget tiers, vendor contacts

cortex search "what's the plan?" --lens technical
# Returns: Cortex MVP, architecture decisions, repo status
```

### How Lenses Work

A lens is:
1. **A named filter** — include/exclude facts by tag, category, or source
2. **A relevance bias** — boost certain fact types, demote others
3. **A decay override** — trading lens might decay state facts faster than the wedding lens

```json
{
  "name": "trading",
  "include_tags": ["trading", "market", "finance", "options", "stocks"],
  "exclude_tags": ["personal", "wedding"],
  "boost": {"type": "decision", "factor": 1.5},
  "demote": {"type": "preference", "factor": 0.5}
}
```

### Auto-Detection

The killer feature: agents can auto-detect which lens to apply based on the current query context. No explicit flag needed.

```
Query: "What's my QQQ position?"
→ Keywords match trading lens → auto-apply
→ Results filtered and boosted for trading context
```

### Phase: v1.1 (manual lens selection) → v2 (auto-detection)

---

## 4. Differential Memory (Git for Memories)

### The Concept

Track memory over time like git tracks code:

```bash
cortex diff --since "2 weeks ago"
# + Added: Wedding budget decided at $18K (mid-tier)
# + Added: Wedding month changed to October  
# ~ Updated: Guest count 25 → 40
# - Decayed: September availability confirmed (confidence < 0.1)

cortex log
# 2026-02-16 22:00  imported MEMORY.md (47 facts extracted)
# 2026-02-16 22:01  imported daily-notes/ (123 facts, 12 conflicts flagged)
# 2026-02-16 22:05  LLM extraction on chat-log.txt (31 facts)
# 2026-02-16 23:00  auto-decay: 3 temporal facts dropped below threshold
# 2026-02-17 09:00  reinforced 5 facts from morning session

cortex snapshot --tag "pre-wedding-planning"
cortex restore --tag "pre-wedding-planning"
```

### Implementation

Every change to the memory store is an event in an append-only log:

```sql
CREATE TABLE memory_events (
    id         INTEGER PRIMARY KEY,
    event_type TEXT NOT NULL,  -- 'add', 'update', 'merge', 'decay', 'delete', 'reinforce'
    fact_id    INTEGER,
    old_value  TEXT,           -- for updates
    new_value  TEXT,           -- for updates
    source     TEXT,           -- what triggered this event
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE snapshots (
    id         INTEGER PRIMARY KEY,
    tag        TEXT UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    event_id   INTEGER REFERENCES memory_events(id)  -- snapshot is "state at this event"
);
```

### Why This Matters

- **Roll back bad imports** — imported a file that corrupted your memory? `cortex restore`
- **Track evolution** — how has your agent's understanding changed over months?
- **Audit trail** — for enterprise/compliance, every change is logged
- **Branching** (future) — agent A's perspective vs agent B's perspective, merge when they sync

### Phase: v1.0 (event log) → v1.1 (diff/log commands) → v2 (snapshots/restore) → v3 (branching)

---

## 5. Cortex Memory Protocol (CMP) — The Long Game

### The Concept

What if Cortex isn't primarily a tool — it's a **PROTOCOL**?

Like LSP (Language Server Protocol) standardized how editors talk to language intelligence, CMP standardizes how agents talk to memory.

### Protocol Operations

```
STORE(fact, source, confidence) → id
    Store a new fact with provenance

RECALL(query, lens?, limit?, min_confidence?) → []fact
    Retrieve relevant facts

FORGET(id) → void
    Mark a fact as forgotten (soft delete)

MERGE(id1, id2) → id
    Combine duplicate facts, preserve both sources

CONFLICT(id1, id2) → {fact1, fact2, suggestion}
    Surface contradictory facts for resolution

OBSERVE(filter?) → stats
    Get memory health metrics

REINFORCE(id) → void
    Boost confidence of a fact (it was just confirmed)

DIFF(since) → []event
    Get changes since a point in time

SNAPSHOT(tag) → void
    Create a named point-in-time snapshot

EXPORT(format) → data
    Export memory in a given format
```

### Transport

```
stdio   — for CLI and MCP integration
HTTP    — for remote/API integration  
embedded — Go library import (in-process)
```

### Why Protocol > Product

- **Tools come and go. Protocols persist.** HTTP, SMTP, LSP — they outlive every implementation.
- **Network effects.** Every tool that speaks CMP makes every other CMP tool more valuable.
- **Defensible position.** Mem0 can't copy a protocol — it contradicts their SaaS model.
- **Adoption path.** Start as a tool → gain users → users demand other tools support CMP → protocol emerges.

### The LSP Parallel

LSP didn't start as a standard. Microsoft built it for VS Code. It was good enough that other editors adopted it. Now every editor speaks LSP.

We build Cortex. It's good enough that people use it. Other tools want to import/export Cortex data. They implement CMP. Now there's a standard.

### Phase: v1.0 (internal interfaces match CMP operations) → v2.0 (MCP server implementing CMP) → v3.0 (formal CMP spec published)

---

## Summary: What's Novel vs What Exists

| Idea | Exists Elsewhere? | Our Innovation |
|------|-------------------|----------------|
| Provenance chains | ❌ Nobody tracks what facts DO | Citation graph for every memory |
| Confidence decay | ❌ All facts equal weight forever | Ebbinghaus curve by fact type |
| Memory lenses | ❌ (tag filtering exists but no context-aware views) | Auto-detecting contextual views |
| Differential memory | ❌ (Mem0 has versioning but no diff/snapshot) | Full git-style history |
| Memory protocol | ❌ (MCP handles tools, not memory specifically) | LSP-equivalent for memory |
| Import-first | ❌ Every tool says "start fresh" | Migration IS the product |
| Zero-LLM default | ❌ Mem0/Zep/Letta all require LLM | Local extraction + optional LLM |
| Observability | ❌ Black box everywhere | Stats, stale, conflicts, audit |
