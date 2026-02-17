# PRD-004: Search

**Status:** Draft  
**Priority:** P0  
**Phase:** 1  
**Depends On:** PRD-001 (Storage)  
**Package:** `internal/search/`

---

## Overview

Cortex provides dual search: BM25 keyword search via SQLite FTS5 and semantic search via local ONNX embeddings (all-MiniLM-L6-v2). The default hybrid mode combines both using Reciprocal Rank Fusion (RRF). All search is local — zero API keys, zero network calls.

## Problem

Users need to find memories by keyword ("deployment process") and by concept ("how we ship code"). Keyword search misses semantic relationships. Semantic search misses exact terms. Hybrid search gives the best of both. Existing tools require cloud APIs for semantic search — Cortex does it locally with a bundled ONNX model.

---

## Requirements

### Must Have (P0)

- **BM25 search via FTS5**
  - Query the `memories_fts` virtual table
  - Support standard FTS5 query syntax:
    - AND (implicit): `deployment process` → both terms
    - OR: `deployment OR shipping`
    - NOT: `deployment NOT staging`
    - Quoted phrases: `"deployment process"`
    - Prefix: `deploy*`
  - Return results ranked by BM25 score (via FTS5 `rank` function)
  - Generate snippets with highlighted matches (via FTS5 `snippet()` function)
  - Configurable result limit (default: 20)
  - Filter out soft-deleted memories (`deleted_at IS NOT NULL`)

- **Semantic search via ONNX embeddings**
  - Load all-MiniLM-L6-v2 ONNX model (384 dimensions, ~80MB)
  - Generate embedding for the query string
  - Compute cosine similarity against all stored embeddings
  - Return top-K results ranked by similarity score
  - Configurable result limit (default: 20)
  - Minimum similarity threshold (configurable, default: 0.3)

- **Hybrid search (default mode)**
  - Run both BM25 and semantic search
  - Combine results using Reciprocal Rank Fusion (RRF):
    ```
    RRF_score(d) = Σ 1/(k + rank_i(d))
    ```
    where `k` is a constant (default: 60) and `rank_i(d)` is the rank of document `d` in result set `i`
  - Configurable weight parameter:
    - `alpha = 0.5` (default) — equal weight to both
    - `alpha = 0.0` — BM25 only
    - `alpha = 1.0` — semantic only
  - Deduplicate results that appear in both result sets
  - Return unified ranked list

- **Search modes** selectable by caller
  - `keyword` — BM25 only
  - `semantic` — embeddings only
  - `hybrid` — both (default)

- **Result structure**
  ```go
  type Result struct {
      Memory    store.Memory
      Score     float64   // Combined or individual score
      Snippet   string    // Highlighted text snippet
      MatchType string    // "bm25", "semantic", "hybrid"
      Facts     []store.Fact // Associated extracted facts (if any)
  }
  ```

### Should Have (P1)

- **Recency boost**
  - Multiply score by a recency factor:
    ```
    recency_factor = 1 + (recency_weight / (1 + days_since_import))
    ```
  - `recency_weight` configurable (default: 0.1)
  - Recently imported memories get a small boost

- **Confidence-weighted results** (when decay model is active)
  - Calculate effective confidence:
    ```
    effective_confidence = stored_confidence × exp(-decay_rate × days_since_reinforced)
    ```
  - Multiply search score by effective confidence
  - Filter out results below confidence threshold (configurable, default: 0.1)
  - Show confidence in results

- **Recall logging**
  - When a search returns results, log each result to `recall_log` table
  - Include: fact_id, query context, session_id, active lens
  - Used for provenance chains and auto-reinforcement

### Future (P2)

- **Memory lenses** — filter/boost based on active lens configuration
- **Faceted search** — filter by fact type, source file, date range
- **Search suggestions** — "did you mean?" for typos
- **Related memories** — "similar to this memory" using embeddings

---

## Technical Design

### Search Interface

```go
package search

import "context"

// Mode specifies the search strategy.
type Mode string

const (
    ModeKeyword  Mode = "keyword"
    ModeSemantic Mode = "semantic"
    ModeHybrid   Mode = "hybrid"
)

// Options configures a search query.
type Options struct {
    Mode            Mode    // Search mode (default: hybrid)
    Limit           int     // Max results (default: 20)
    MinConfidence   float64 // Minimum confidence threshold (default: 0.1)
    Alpha           float64 // BM25 vs semantic weight (default: 0.5)
    RecencyWeight   float64 // Recency boost factor (default: 0.1)
    Lens            string  // Active lens name (optional)
    IncludeFacts    bool    // Include associated facts in results (default: true)
    SessionID       string  // For recall logging
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
    return Options{
        Mode:          ModeHybrid,
        Limit:         20,
        MinConfidence: 0.1,
        Alpha:         0.5,
        RecencyWeight: 0.1,
        IncludeFacts:  true,
    }
}

// Result represents a single search result.
type Result struct {
    Memory             store.Memory
    Score              float64
    BM25Score          float64
    SemanticScore      float64
    EffectiveConfidence float64
    Snippet            string
    MatchType          string // "bm25", "semantic", "hybrid"
    Facts              []store.Fact
}

// Searcher performs searches across the memory store.
type Searcher interface {
    Search(ctx context.Context, query string, opts Options) ([]Result, error)
}

// Engine implements Searcher with BM25 + semantic search.
type Engine struct {
    store    store.Store
    embedder Embedder
}

// Embedder generates embedding vectors from text.
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// NewEngine creates a search engine.
func NewEngine(s store.Store, e Embedder) *Engine { ... }
```

### Hybrid Search Implementation

```go
func (e *Engine) Search(ctx context.Context, query string, opts Options) ([]Result, error) {
    switch opts.Mode {
    case ModeKeyword:
        return e.searchBM25(ctx, query, opts)
    case ModeSemantic:
        return e.searchSemantic(ctx, query, opts)
    case ModeHybrid:
        return e.searchHybrid(ctx, query, opts)
    default:
        return e.searchHybrid(ctx, query, opts)
    }
}

func (e *Engine) searchHybrid(ctx context.Context, query string, opts Options) ([]Result, error) {
    // 1. Run BM25 search (fetch 2x limit to ensure coverage after merge)
    bm25Results, err := e.searchBM25(ctx, query, Options{...opts, Limit: opts.Limit * 2})
    if err != nil {
        return nil, fmt.Errorf("BM25 search: %w", err)
    }
    
    // 2. Run semantic search (same 2x limit)
    semResults, err := e.searchSemantic(ctx, query, Options{...opts, Limit: opts.Limit * 2})
    if err != nil {
        return nil, fmt.Errorf("semantic search: %w", err)
    }
    
    // 3. Apply Reciprocal Rank Fusion
    merged := reciprocalRankFusion(bm25Results, semResults, opts.Alpha)
    
    // 4. Apply recency boost
    if opts.RecencyWeight > 0 {
        applyRecencyBoost(merged, opts.RecencyWeight)
    }
    
    // 5. Apply confidence weighting
    applyConfidenceWeighting(merged, opts.MinConfidence)
    
    // 6. Sort by final score, limit results
    sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
    if len(merged) > opts.Limit {
        merged = merged[:opts.Limit]
    }
    
    // 7. Log recalls (async, don't block search)
    if opts.SessionID != "" {
        go e.logRecalls(ctx, merged, query, opts.SessionID, opts.Lens)
    }
    
    return merged, nil
}
```

### Reciprocal Rank Fusion

```go
func reciprocalRankFusion(bm25, semantic []Result, alpha float64) []Result {
    const k = 60 // RRF constant (standard value from literature)
    
    scores := make(map[int64]float64) // memory ID → fused score
    results := make(map[int64]Result) // memory ID → best result
    
    // Score BM25 results
    for rank, r := range bm25 {
        id := r.Memory.ID
        scores[id] += (1 - alpha) * (1.0 / float64(k + rank + 1))
        results[id] = r
        results[id].BM25Score = r.Score
    }
    
    // Score semantic results
    for rank, r := range semantic {
        id := r.Memory.ID
        scores[id] += alpha * (1.0 / float64(k + rank + 1))
        if existing, ok := results[id]; ok {
            existing.SemanticScore = r.Score
            existing.MatchType = "hybrid"
            results[id] = existing
        } else {
            r.SemanticScore = r.Score
            results[id] = r
        }
    }
    
    // Build final result list with fused scores
    var fused []Result
    for id, score := range scores {
        r := results[id]
        r.Score = score
        if r.MatchType == "" {
            if r.BM25Score > 0 {
                r.MatchType = "bm25"
            } else {
                r.MatchType = "semantic"
            }
        }
        fused = append(fused, r)
    }
    
    return fused
}
```

### Confidence Decay Calculation

```go
func effectiveConfidence(fact store.Fact) float64 {
    daysSinceReinforced := time.Since(fact.LastReinforced).Hours() / 24
    return fact.Confidence * math.Exp(-fact.DecayRate * daysSinceReinforced)
}

func applyConfidenceWeighting(results []Result, minConfidence float64) {
    filtered := results[:0]
    for _, r := range results {
        if len(r.Facts) == 0 {
            // No facts extracted yet — include with full score
            r.EffectiveConfidence = 1.0
            filtered = append(filtered, r)
            continue
        }
        
        // Average effective confidence across all facts
        var totalConf float64
        for _, f := range r.Facts {
            totalConf += effectiveConfidence(f)
        }
        avgConf := totalConf / float64(len(r.Facts))
        
        if avgConf >= minConfidence {
            r.EffectiveConfidence = avgConf
            r.Score *= avgConf // Weight score by confidence
            filtered = append(filtered, r)
        }
    }
    // Replace results in-place
    copy(results, filtered)
}
```

### ONNX Embedder

```go
type ONNXEmbedder struct {
    session *onnxruntime.Session
    tokenizer *tokenizer.Tokenizer
}

// NewONNXEmbedder loads the all-MiniLM-L6-v2 model.
// modelPath is the path to the .onnx file.
func NewONNXEmbedder(modelPath string) (*ONNXEmbedder, error) {
    // 1. Initialize ONNX Runtime
    // 2. Load model from modelPath
    // 3. Load tokenizer (bundled with model)
    // 4. Verify output dimensions = 384
}

func (e *ONNXEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    // 1. Tokenize text
    // 2. Run inference
    // 3. Mean pooling over token embeddings
    // 4. L2 normalize
    // 5. Return 384-dim float32 vector
}

func (e *ONNXEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    // Batch inference for import-time embedding generation
}
```

### Cosine Similarity

```go
func cosineSimilarity(a, b []float32) float64 {
    if len(a) != len(b) {
        return 0
    }
    var dot, normA, normB float64
    for i := range a {
        dot += float64(a[i]) * float64(b[i])
        normA += float64(a[i]) * float64(a[i])
        normB += float64(b[i]) * float64(b[i])
    }
    if normA == 0 || normB == 0 {
        return 0
    }
    return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
```

---

## Test Strategy

### Unit Tests

**BM25 Search:**
- **TestSearchBM25_SingleTerm** — search for "deployment", verify results contain term
- **TestSearchBM25_MultipleTerm** — implicit AND, both terms present
- **TestSearchBM25_OR** — OR query returns results with either term
- **TestSearchBM25_NOT** — NOT excludes results
- **TestSearchBM25_Phrase** — quoted phrase matches exact sequence
- **TestSearchBM25_Prefix** — `deploy*` matches "deployment", "deployed"
- **TestSearchBM25_Ranking** — more relevant results scored higher
- **TestSearchBM25_Snippets** — snippets contain highlighted matches
- **TestSearchBM25_NoResults** — empty result set, no error
- **TestSearchBM25_Limit** — respects result limit

**Semantic Search:**
- **TestSearchSemantic_Similar** — conceptually similar query finds related memories
- **TestSearchSemantic_Dissimilar** — unrelated query returns low scores
- **TestSearchSemantic_Ranking** — more similar results ranked higher
- **TestSearchSemantic_Threshold** — results below similarity threshold filtered
- **TestSearchSemantic_Limit** — respects result limit

**Hybrid Search:**
- **TestSearchHybrid_CombinesResults** — results from both engines merged
- **TestSearchHybrid_RRF** — Reciprocal Rank Fusion produces expected ranking
- **TestSearchHybrid_Dedup** — same memory from both engines appears once
- **TestSearchHybrid_AlphaZero** — alpha=0 equivalent to BM25 only
- **TestSearchHybrid_AlphaOne** — alpha=1 equivalent to semantic only
- **TestSearchHybrid_Default** — default options work correctly

**Confidence Weighting:**
- **TestConfidenceDecay_Fresh** — recently reinforced fact has high confidence
- **TestConfidenceDecay_Old** — old unreinforced fact has decayed confidence
- **TestConfidenceDecay_ByType** — different types decay at different rates
- **TestConfidenceFilter** — facts below threshold excluded from results

**RRF:**
- **TestRRF_BasicMerge** — two result sets merged correctly
- **TestRRF_Overlap** — overlapping results get boosted score
- **TestRRF_NoOverlap** — disjoint sets both included

**Cosine Similarity:**
- **TestCosineSimilarity_Identical** — same vector → 1.0
- **TestCosineSimilarity_Orthogonal** — orthogonal vectors → 0.0
- **TestCosineSimilarity_Opposite** — negated vector → -1.0
- **TestCosineSimilarity_DifferentLengths** — returns 0

### Integration Tests

- **TestSearchEndToEnd** — import sample data, search, verify relevant results
- **TestSearchWithConfidenceDecay** — insert facts with old timestamps, verify decay affects ranking
- **TestEmbedAndSearch** — generate embeddings, store, search semantically

---

## Open Questions

1. **RRF constant k:** Standard is 60 (from the RRF paper). Should we make this configurable?
2. **Embedding generation timing:** Generate on import (slower import) or lazily on first search (slower first search)?
3. **Model loading:** Load ONNX model on startup (slower startup, faster search) or on first search?
4. **Fallback for cortex-lite:** What does semantic search do when ONNX model isn't available? (Return error? Fall back to BM25?)
