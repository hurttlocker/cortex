# PRD-006: Observability

**Status:** Draft  
**Priority:** P0 (stats), P1 (stale, conflicts)  
**Phase:** 1 (stats), 2 (stale interactive, conflicts interactive)  
**Depends On:** PRD-001 (Storage), PRD-004 (Search)  
**Package:** `internal/observe/`

---

## Overview

Observability answers the question nobody else lets you ask: *"What does my agent actually know?"* Three commands — `stats`, `stale`, and `conflicts` — provide a complete health picture of the memory store. Stats gives an overview. Stale finds fading memories. Conflicts detects contradictions.

## Problem

AI agent memory is a black box. Users can't answer basic questions: How many facts does my agent have? Are any outdated? Are there contradictions? When a fact is wrong, the agent silently uses it, leading to compounding errors. There's no way to audit, prune, or verify what the agent "knows." Cortex makes memory observable.

---

## Requirements

### Must Have (P0)

- **`cortex stats`** — Memory overview dashboard

  Display the following metrics:

  | Metric | Source |
  |--------|--------|
  | Total memories | `COUNT(*) FROM memories WHERE deleted_at IS NULL` |
  | Total facts | `COUNT(*) FROM facts` |
  | Total sources | `COUNT(DISTINCT source_file) FROM memories` |
  | Facts by type | `GROUP BY fact_type` distribution |
  | Freshness distribution | Bucket by: today, this week, this month, older |
  | Storage size | File size of `cortex.db` |
  | Average confidence | `AVG(confidence) FROM facts` |
  | Top 5 most-recalled facts | `JOIN recall_log, GROUP BY fact_id, ORDER BY COUNT(*) DESC LIMIT 5` |

  **TTY output example:**
  ```
  ╭─────────────────────────────────────────────╮
  │              Cortex Memory Stats             │
  ├─────────────────────────────────────────────┤
  │ Memories:        1,847                       │
  │ Facts:           4,231                       │
  │ Sources:         12 files                    │
  │ Storage:         14.2 MB                     │
  │ Avg confidence:  0.82                        │
  ├─────────────────────────────────────────────┤
  │ Facts by Type                                │
  │   kv:            1,204 (28.5%)  ████████░░   │
  │   relationship:    892 (21.1%)  ██████░░░░   │
  │   preference:      634 (15.0%)  ████░░░░░░   │
  │   temporal:        521 (12.3%)  ███░░░░░░░   │
  │   identity:        412 (9.7%)   ██░░░░░░░░   │
  │   decision:        298 (7.0%)   ██░░░░░░░░   │
  │   location:        156 (3.7%)   █░░░░░░░░░   │
  │   state:           114 (2.7%)   █░░░░░░░░░   │
  ├─────────────────────────────────────────────┤
  │ Freshness                                    │
  │   Today:           47                        │
  │   This week:       234                       │
  │   This month:      891                       │
  │   Older:           675                       │
  ├─────────────────────────────────────────────┤
  │ Top Recalled                                 │
  │   1. "Q lives in Philadelphia" (47 recalls)  │
  │   2. "Broker: TradeStation" (34 recalls)     │
  │   3. "Prefers dark mode" (28 recalls)        │
  │   4. "Team: Platform Infra" (22 recalls)     │
  │   5. "Timezone: EST" (19 recalls)            │
  ╰─────────────────────────────────────────────╯
  ```

  **JSON output** (when `--json` or piped):
  ```json
  {
    "memories": 1847,
    "facts": 4231,
    "sources": 12,
    "storage_bytes": 14890240,
    "avg_confidence": 0.82,
    "facts_by_type": {"kv": 1204, "relationship": 892, ...},
    "freshness": {"today": 47, "this_week": 234, "this_month": 891, "older": 675},
    "top_recalled": [{"fact": "Q lives in Philadelphia", "recalls": 47}, ...]
  }
  ```

### Should Have (P1)

- **`cortex stale`** — Find outdated entries

  - Find facts below confidence threshold (default: 0.5, configurable via `--min-confidence`)
  - Find facts not recalled in N days (default: 30, configurable via `--days`)
  - Calculate effective confidence using decay model:
    ```
    effective_confidence = confidence × exp(-decay_rate × days_since_reinforced)
    ```
  - Sort by staleness (lowest effective confidence first)
  - Display: fact content, type, effective confidence, days since reinforced, source

  **TTY output example:**
  ```
  Stale Facts (confidence < 0.50, not recalled in 30+ days)
  
  ⚠️  0.12  "Meeting with vendor on Tuesday"
           temporal · 45 days old · never recalled
           Source: notes/2026-01-02.md:15
           [r]einforce  [d]elete  [s]kip
  
  ⚠️  0.34  "Working on Eyes Web redesign"
           state · 28 days old · last recalled 25d ago
           Source: MEMORY.md:89
           [r]einforce  [d]elete  [s]kip
  
  ⚠️  0.41  "Prefers VSCode over Vim"
           preference · 89 days old · last recalled 60d ago
           Source: MEMORY.md:12
           [r]einforce  [d]elete  [s]kip
  
  ✅  3 stale facts found. 0 reinforced, 0 deleted, 0 skipped.
  ```

  **Interactive mode** (TTY only):
  - `r` — reinforce: reset `last_reinforced` to now, boost confidence by 0.3
  - `d` — delete: soft delete the fact
  - `s` — skip: move to next fact
  - `q` — quit: stop reviewing

  **Non-TTY mode**: list all stale facts as JSON, no prompts

- **`cortex conflicts`** — Detect contradictions

  Three types of conflicts:

  1. **Attribute conflicts:** Same subject + similar predicate but different values
     - Example: "timezone: EST" vs "timezone: PST"
     - Detection: exact match on subject, fuzzy match on predicate, different object

  2. **Semantic conflicts:** High embedding similarity (>0.85) but different values
     - Example: "Q is in Philadelphia" vs "Q moved to New York"
     - Detection: cosine similarity on embeddings, value mismatch

  3. **Temporal conflicts:** Same attribute, different values, different dates
     - Example: "Manager: Alice (Jan 2024)" vs "Manager: Bob (Mar 2024)"
     - Detection: same subject+predicate, temporal metadata differs

  **TTY output example:**
  ```
  Conflicts Detected: 3
  
  ❌ Attribute Conflict
     "timezone: EST" (MEMORY.md:8, imported 2026-01-15)
     "timezone: PST" (daily/2026-02-10.md:3, imported 2026-02-10)
     Similarity: 0.97
     [m]erge (keep newer)  [k]eep both  [d]elete one
  
  ❌ Semantic Conflict
     "Q lives in Philadelphia" (MEMORY.md:4, confidence: 0.95)
     "Q is moving to New York" (notes/plans.md:22, confidence: 0.72)
     Similarity: 0.89
     [m]erge  [k]eep both  [d]elete one
  
  ⚠️ Temporal Conflict
     "Manager: Alice" (MEMORY.md:15, Jan 2024)
     "Manager: Bob" (daily/2026-03-01.md:5, Mar 2024)
     Likely update — newer value may be correct
     [m]erge (keep newer)  [k]eep both  [d]elete one
  ```

  **Interactive mode** (TTY only):
  - `m` — merge: keep the newer/higher-confidence value, archive the other
  - `k` — keep both: mark as reviewed, don't flag again
  - `d` — delete one: prompt which to delete (1 or 2)
  - `q` — quit

### Future (P2)

- **Health score** — single 0–100 score summarizing memory health
- **Trend tracking** — how stats change over time (stored in events table)
- **Automated cleanup** — `cortex clean` to auto-delete facts below threshold
- **Alerts** — notify when conflict count exceeds threshold

---

## Technical Design

### Observer Interface

```go
package observe

import "context"

// Stats holds aggregate memory statistics.
type Stats struct {
    TotalMemories int            `json:"memories"`
    TotalFacts    int            `json:"facts"`
    TotalSources  int            `json:"sources"`
    StorageBytes  int64          `json:"storage_bytes"`
    AvgConfidence float64        `json:"avg_confidence"`
    FactsByType   map[string]int `json:"facts_by_type"`
    Freshness     Freshness      `json:"freshness"`
    TopRecalled   []RecalledFact `json:"top_recalled"`
}

type Freshness struct {
    Today     int `json:"today"`
    ThisWeek  int `json:"this_week"`
    ThisMonth int `json:"this_month"`
    Older     int `json:"older"`
}

type RecalledFact struct {
    FactID     int64  `json:"fact_id"`
    Content    string `json:"content"`
    RecallCount int   `json:"recalls"`
}

// StaleFact represents a fact that has decayed below threshold.
type StaleFact struct {
    Fact                store.Fact
    EffectiveConfidence float64   `json:"effective_confidence"`
    DaysSinceReinforced int       `json:"days_since_reinforced"`
    RecallCount         int       `json:"recall_count"`
    SourceFile          string    `json:"source_file"`
}

// Conflict represents two facts that may contradict each other.
type Conflict struct {
    Fact1       store.Fact `json:"fact1"`
    Fact2       store.Fact `json:"fact2"`
    ConflictType string   `json:"conflict_type"` // "attribute", "semantic", "temporal"
    Similarity  float64   `json:"similarity"`
    Suggestion  string    `json:"suggestion"`
}

// Observer provides memory observability.
type Observer interface {
    GetStats(ctx context.Context) (*Stats, error)
    GetStaleFacts(ctx context.Context, opts StaleOpts) ([]StaleFact, error)
    GetConflicts(ctx context.Context) ([]Conflict, error)
    ReinforceFact(ctx context.Context, factID int64) error
    DeleteFact(ctx context.Context, factID int64) error
    MergeConflict(ctx context.Context, keepID, removeID int64) error
}

// StaleOpts configures stale fact detection.
type StaleOpts struct {
    MinConfidence float64 // Threshold (default: 0.5)
    MaxDays       int     // Days without recall (default: 30)
    Limit         int     // Max results (default: 50)
}

// Engine implements Observer.
type Engine struct {
    store    store.Store
    searcher search.Searcher
    dbPath   string // for file size calculation
}

func NewEngine(s store.Store, searcher search.Searcher, dbPath string) *Engine { ... }
```

### Stats Implementation

```go
func (e *Engine) GetStats(ctx context.Context) (*Stats, error) {
    stats := &Stats{}
    
    // Total memories (excluding soft-deleted)
    // SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL
    
    // Total facts
    // SELECT COUNT(*) FROM facts
    
    // Total sources
    // SELECT COUNT(DISTINCT source_file) FROM memories WHERE deleted_at IS NULL
    
    // Facts by type
    // SELECT fact_type, COUNT(*) FROM facts GROUP BY fact_type
    
    // Freshness buckets
    // SELECT COUNT(*) FROM memories WHERE imported_at >= date('now')           → today
    // SELECT COUNT(*) FROM memories WHERE imported_at >= date('now', '-7 days') → this week
    // etc.
    
    // Average confidence
    // SELECT AVG(confidence) FROM facts
    
    // Top 5 recalled
    // SELECT f.*, COUNT(r.id) as recall_count
    // FROM facts f JOIN recall_log r ON f.id = r.fact_id
    // GROUP BY f.id ORDER BY recall_count DESC LIMIT 5
    
    // Storage size
    // os.Stat(dbPath).Size()
    
    return stats, nil
}
```

### Stale Detection

```go
func (e *Engine) GetStaleFacts(ctx context.Context, opts StaleOpts) ([]StaleFact, error) {
    // 1. Query all facts with their recall metadata
    // SELECT f.*, 
    //   COALESCE(MAX(r.recalled_at), f.last_reinforced) as last_activity,
    //   COUNT(r.id) as recall_count
    // FROM facts f
    // LEFT JOIN recall_log r ON f.id = r.fact_id
    // GROUP BY f.id
    
    // 2. For each fact, calculate effective confidence
    //    effective = confidence * exp(-decay_rate * days_since_reinforced)
    
    // 3. Filter: effective < opts.MinConfidence OR days > opts.MaxDays
    
    // 4. Sort by effective confidence (ascending — stalest first)
    
    // 5. Limit results
}
```

### Conflict Detection

```go
func (e *Engine) GetConflicts(ctx context.Context) ([]Conflict, error) {
    var conflicts []Conflict
    
    // 1. Attribute conflicts
    // SELECT f1.*, f2.*
    // FROM facts f1 JOIN facts f2 ON f1.subject = f2.subject 
    //   AND f1.predicate = f2.predicate AND f1.object != f2.object
    //   AND f1.id < f2.id  -- avoid duplicates
    
    // 2. Semantic conflicts (embedding similarity > 0.85)
    // For each fact pair with high embedding similarity:
    //   If values differ, flag as semantic conflict
    // NOTE: This is O(n²) — optimize with locality-sensitive hashing if needed
    
    // 3. Temporal conflicts
    // Same subject + predicate, different objects, different temporal values
    // Suggest keeping the newer one
    
    return conflicts, nil
}
```

### Reinforcement

```go
func (e *Engine) ReinforceFact(ctx context.Context, factID int64) error {
    // 1. Get current fact
    // 2. Boost confidence: min(1.0, current + 0.3)
    // 3. Reset last_reinforced to now
    // 4. Log event: {type: "reinforce", fact_id, old_value, new_value}
    // UPDATE facts SET 
    //   confidence = MIN(1.0, confidence + 0.3),
    //   last_reinforced = CURRENT_TIMESTAMP
    // WHERE id = ?
}
```

---

## Test Strategy

### Unit Tests

**Stats:**
- **TestGetStats_Empty** — empty database returns zero counts
- **TestGetStats_WithData** — populated database returns correct counts
- **TestGetStats_FactsByType** — correct distribution per type
- **TestGetStats_Freshness** — correct bucketing by date
- **TestGetStats_TopRecalled** — correct ordering by recall count
- **TestGetStats_StorageSize** — file size returned correctly
- **TestGetStats_ExcludesSoftDeleted** — soft-deleted memories not counted

**Stale:**
- **TestGetStale_NoStale** — all facts fresh, returns empty
- **TestGetStale_DecayedFacts** — old facts with high decay rate returned
- **TestGetStale_UnrecalledFacts** — facts never recalled after N days returned
- **TestGetStale_Threshold** — only facts below threshold returned
- **TestGetStale_Sorting** — stalest facts first
- **TestGetStale_TypeAwareDecay** — temporal facts stale faster than identity facts
- **TestReinforceFact** — confidence boosted, last_reinforced updated

**Conflicts:**
- **TestGetConflicts_AttributeConflict** — same subject+predicate, different value
- **TestGetConflicts_SemanticConflict** — high similarity, different values
- **TestGetConflicts_TemporalConflict** — same attribute, different dates
- **TestGetConflicts_NoConflicts** — clean data returns empty
- **TestGetConflicts_NoDuplicateReports** — conflict pair reported once, not twice
- **TestMergeConflict** — keep one, archive other

### Integration Tests

- **TestFullObservabilityFlow** — import data, extract facts, run stats/stale/conflicts
- **TestDecayOverTime** — insert facts with old timestamps, verify decay calculations

---

## Open Questions

1. **Semantic conflict performance:** O(n²) embedding comparison is expensive. At what scale does this need optimization? (Probably >10K facts — LSH or clustering)
2. **Conflict resolution persistence:** When user picks "keep both," should we store this decision to avoid re-flagging? (Yes — add a `conflicts_reviewed` table)
3. **Stats caching:** Should we cache stats to avoid re-computing on every call? (No for v1 — queries are fast on SQLite)
4. **Stale thresholds:** Are the defaults (0.5 confidence, 30 days) good? Need user feedback.
