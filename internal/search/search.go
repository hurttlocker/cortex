// Package search provides search capabilities for Cortex.
//
// Three search modes:
// - BM25 keyword search via SQLite FTS5 (instant, zero dependencies)
// - Semantic search via embedding similarity (any provider: Ollama, OpenAI, etc.)
// - Hybrid mode combines both using Weighted Score Fusion (α=0.3 BM25, 0.7 semantic)
//
// Each mode applies minimum score filtering to prevent garbage-in/results-out.
package search

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/ann"
	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/store"
)

// ConfidenceWeight controls how much effective confidence affects search ranking.
// 0.0 = no effect, 1.0 = fully weighted by confidence.
// Default 0.2 gives a gentle boost to high-confidence results without
// completely suppressing low-confidence ones that are otherwise relevant.
const ConfidenceWeight = 0.2

// Class-aware weighting (Issue #34).
// Conservative multipliers to prioritize operator-critical context while
// preserving baseline behavior for unclassified memories.
var classBoostMultipliers = map[string]float64{
	store.MemoryClassRule:       1.30,
	store.MemoryClassDecision:   1.20,
	store.MemoryClassPreference: 1.10,
	store.MemoryClassIdentity:   1.08,
	store.MemoryClassStatus:     1.00,
	store.MemoryClassScratch:    0.90,
}

// Mode specifies the search strategy.
type Mode string

const (
	ModeKeyword  Mode = "keyword"
	ModeSemantic Mode = "semantic"
	ModeHybrid   Mode = "hybrid"
)

// ParseMode converts a string to a Mode, returning an error for invalid values.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(s) {
	case "keyword", "bm25":
		return ModeKeyword, nil
	case "semantic":
		return ModeSemantic, nil
	case "hybrid":
		return ModeHybrid, nil
	default:
		return "", fmt.Errorf("invalid search mode %q (valid: keyword, semantic, hybrid)", s)
	}
}

// Options configures a search query.
type Options struct {
	Mode              Mode     // Search mode (default: keyword)
	Limit             int      // Max results (default: 10)
	MinScore          float64  // Minimum search score threshold (default: mode-dependent, -1 = use default)
	Project           string   // Scope search to a specific project (empty = all)
	Classes           []string // Filter by memory class (rule, decision, preference, identity, status, scratch)
	DisableClassBoost bool     // Disable class-aware weighting (default: false)
	Agent             string   // Filter by metadata agent_id (Issue #30)
	Channel           string   // Filter by metadata channel (Issue #30)
	After             string   // Filter memories imported after date YYYY-MM-DD (Issue #30)
	Before            string   // Filter memories imported before date YYYY-MM-DD (Issue #30)
	IncludeSuperseded bool     // Include memories backed only by superseded facts
	Explain           bool     // Attach explainability/provenance payloads to results
}

// Default minimum score thresholds by mode.
// These filter noise — garbage queries returning low-relevance results.
const (
	defaultMinBM25     = 0.05 // tanh(0.5/10)=0.05 → filters ranks weaker than ~0.5
	defaultMinSemantic = 0.25 // Cosine similarity below 0.25 is essentially random
	defaultMinHybrid   = 0.05 // Fused scores, lower threshold since it's a blend
)

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		Mode:     ModeKeyword,
		Limit:    10,
		MinScore: -1, // -1 = use mode-dependent default
	}
}

// effectiveMinScore returns the minimum score threshold for a given mode.
// If the user set an explicit threshold (>= 0), use that. Otherwise use defaults.
func effectiveMinScore(mode Mode, configured float64) float64 {
	if configured >= 0 {
		return configured
	}
	switch mode {
	case ModeSemantic:
		return defaultMinSemantic
	case ModeHybrid:
		return defaultMinHybrid
	default:
		return defaultMinBM25
	}
}

// Result represents a single search result.
type Result struct {
	Content       string          `json:"content"`
	SourceFile    string          `json:"source_file"`
	SourceLine    int             `json:"source_line"`
	SourceSection string          `json:"source_section,omitempty"`
	Project       string          `json:"project,omitempty"`
	MemoryClass   string          `json:"class,omitempty"`
	Metadata      *store.Metadata `json:"metadata,omitempty"` // Structured metadata (Issue #30)
	Score         float64         `json:"score"`
	Snippet       string          `json:"snippet,omitempty"`
	MatchType     string          `json:"match_type"` // "bm25", "semantic", "hybrid"
	MemoryID      int64           `json:"memory_id"`
	ImportedAt    time.Time       `json:"imported_at,omitempty"` // For metadata date filtering
	Explain       *ExplainDetails `json:"explain,omitempty"`
}

// ExplainDetails surfaces provenance and ranking factors for operator trust/debugging.
type ExplainDetails struct {
	Provenance     ExplainProvenance `json:"provenance"`
	Confidence     ExplainConfidence `json:"confidence"`
	RankComponents RankComponents    `json:"rank_components"`
	Why            string            `json:"why,omitempty"`
}

type ExplainProvenance struct {
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	AgeDays   float64   `json:"age_days,omitempty"`
}

type ExplainConfidence struct {
	Confidence          float64 `json:"confidence"`
	EffectiveConfidence float64 `json:"effective_confidence"`
}

type RankComponents struct {
	BaseScore                  float64  `json:"base_score"`
	ClassBoostMultiplier       float64  `json:"class_boost_multiplier"`
	PreConfidenceScore         float64  `json:"pre_confidence_score"`
	ConfidenceWeight           float64  `json:"confidence_weight"`
	FinalScore                 float64  `json:"final_score"`
	BM25Raw                    *float64 `json:"bm25_raw,omitempty"`
	BM25Score                  *float64 `json:"bm25_score,omitempty"`
	SemanticScore              *float64 `json:"semantic_score,omitempty"`
	HybridBM25Normalized       *float64 `json:"hybrid_bm25_normalized,omitempty"`
	HybridSemanticNormalized   *float64 `json:"hybrid_semantic_normalized,omitempty"`
	HybridBM25Contribution     *float64 `json:"hybrid_bm25_contribution,omitempty"`
	HybridSemanticContribution *float64 `json:"hybrid_semantic_contribution,omitempty"`
}

type confidenceDetail struct {
	confidence          float64
	effectiveConfidence float64
}

// Searcher performs searches across the memory store.
type Searcher interface {
	Search(ctx context.Context, query string, opts Options) ([]Result, error)
}

// Engine implements Searcher with BM25 search and optional semantic search.
type Engine struct {
	store    store.Store
	embedder embed.Embedder // nil = BM25 only
	hnsw     *ann.Index     // nil = brute-force semantic search
}

// NewEngine creates a search engine backed by the given store.
func NewEngine(s store.Store) *Engine {
	return &Engine{store: s}
}

// NewEngineWithEmbedder creates a search engine with semantic search capability.
func NewEngineWithEmbedder(s store.Store, e embed.Embedder) *Engine {
	return &Engine{store: s, embedder: e}
}

// SetHNSW attaches an HNSW index for fast approximate nearest neighbor search.
// When set, semantic search uses HNSW instead of brute-force O(N) scan.
func (e *Engine) SetHNSW(idx *ann.Index) {
	e.hnsw = idx
}

// BuildHNSW constructs an HNSW index from all stored embeddings.
// Returns the number of vectors indexed.
func (e *Engine) BuildHNSW(ctx context.Context) (int, error) {
	// Get all embeddings from store
	ids, err := e.store.ListMemoryIDsWithEmbeddings(ctx, 0) // 0 = no limit
	if err != nil {
		return 0, fmt.Errorf("listing embedded memories: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	// Detect dimensions from first embedding
	firstVec, err := e.store.GetEmbedding(ctx, ids[0])
	if err != nil {
		return 0, fmt.Errorf("getting first embedding: %w", err)
	}

	idx := ann.New(len(firstVec))
	idx.Insert(ids[0], firstVec)

	for i := 1; i < len(ids); i++ {
		vec, err := e.store.GetEmbedding(ctx, ids[i])
		if err != nil {
			continue // skip errors, don't abort entire build
		}
		idx.Insert(ids[i], vec)
	}

	e.hnsw = idx
	return idx.Len(), nil
}

// LoadOrBuildHNSW tries to load a persisted HNSW index from path.
// If the file doesn't exist or is stale, builds a fresh index and saves it.
// staleThreshold: rebuild if file is older than this many seconds (0 = always rebuild).
func (e *Engine) LoadOrBuildHNSW(ctx context.Context, path string, staleThresholdSec int64) (int, error) {
	// Try loading existing index
	if info, err := os.Stat(path); err == nil {
		age := time.Now().Unix() - info.ModTime().Unix()
		if staleThresholdSec == 0 || age < staleThresholdSec {
			loaded, err := ann.Load(path)
			if err == nil {
				e.hnsw = loaded
				return loaded.Len(), nil
			}
			// Fall through to rebuild on load error
		}
	}

	// Build fresh
	count, err := e.BuildHNSW(ctx)
	if err != nil {
		return 0, err
	}
	if e.hnsw == nil {
		return 0, nil // no embeddings
	}

	// Save for next time
	if err := e.hnsw.Save(path); err != nil {
		// Non-fatal: index works in memory even if save fails
		fmt.Fprintf(os.Stderr, "warning: could not save HNSW index: %v\n", err)
	}

	return count, nil
}

// Search performs a search using the specified mode.
// After retrieving results, it applies confidence decay weighting and
// reinforces facts linked to the returned memories (Ebbinghaus reinforcement-on-recall).
func (e *Engine) Search(ctx context.Context, query string, opts Options) ([]Result, error) {
	if query == "" {
		return nil, nil
	}

	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	var results []Result
	var err error

	switch opts.Mode {
	case ModeKeyword, "":
		results, err = e.searchBM25(ctx, query, opts)
	case ModeSemantic:
		results, err = e.searchSemantic(ctx, query, opts)
	case ModeHybrid:
		results, err = e.searchHybrid(ctx, query, opts)
	default:
		return nil, fmt.Errorf("unknown search mode: %q", opts.Mode)
	}

	if err != nil || len(results) == 0 {
		return results, err
	}

	// Apply metadata filters (Issue #30)
	if opts.Agent != "" || opts.Channel != "" || opts.After != "" || opts.Before != "" {
		results = filterByMetadata(results, opts)
	}

	// Apply class filters / weighting (Issue #34)
	if len(opts.Classes) > 0 {
		results = filterByClass(results, opts.Classes)
	}
	if !opts.DisableClassBoost {
		results = applyClassBoost(results, opts.Explain)
	}

	if !opts.IncludeSuperseded {
		results = e.filterSupersededMemories(ctx, results)
	}

	// Apply confidence decay weighting and reinforce-on-recall
	var confidenceDetails map[int64]confidenceDetail
	results, confidenceDetails = e.applyConfidenceDecay(ctx, results, opts.IncludeSuperseded, opts.Explain)

	if opts.Explain {
		e.addExplainability(results, confidenceDetails)
	}

	return results, nil
}

// filterByMetadata applies metadata-based filters to search results.
func filterByMetadata(results []Result, opts Options) []Result {
	var filtered []Result
	for _, r := range results {
		if opts.Agent != "" && !matchAgent(r, opts.Agent) {
			continue
		}
		if opts.Channel != "" && !matchChannel(r, opts.Channel) {
			continue
		}
		if opts.After != "" && r.ImportedAt.Format("2006-01-02") < opts.After {
			continue
		}
		if opts.Before != "" && r.ImportedAt.Format("2006-01-02") > opts.Before {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}

func filterByClass(results []Result, allowed []string) []Result {
	if len(allowed) == 0 {
		return results
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, c := range allowed {
		normalized := store.NormalizeMemoryClass(c)
		if normalized == "" {
			continue
		}
		allowedSet[normalized] = struct{}{}
	}
	if len(allowedSet) == 0 {
		return results
	}

	filtered := make([]Result, 0, len(results))
	for _, r := range results {
		if _, ok := allowedSet[store.NormalizeMemoryClass(r.MemoryClass)]; ok {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func applyClassBoost(results []Result, explain bool) []Result {
	if len(results) == 0 {
		return results
	}

	for i := range results {
		class := store.NormalizeMemoryClass(results[i].MemoryClass)
		multiplier, ok := classBoostMultipliers[class]
		if !ok {
			multiplier = 1.0
		}
		baseScore := results[i].Score
		results[i].Score *= multiplier

		if explain {
			ensureExplain(&results[i])
			results[i].Explain.RankComponents.BaseScore = baseScore
			results[i].Explain.RankComponents.ClassBoostMultiplier = multiplier
			results[i].Explain.RankComponents.PreConfidenceScore = results[i].Score
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// filterSupersededMemories excludes memories where all linked facts are superseded.
// Memories with no facts remain visible.
func (e *Engine) filterSupersededMemories(ctx context.Context, results []Result) []Result {
	if len(results) == 0 {
		return results
	}

	memoryIDs := make([]int64, 0, len(results))
	for _, r := range results {
		if r.MemoryID > 0 {
			memoryIDs = append(memoryIDs, r.MemoryID)
		}
	}
	if len(memoryIDs) == 0 {
		return results
	}

	facts, err := e.store.GetFactsByMemoryIDsIncludingSuperseded(ctx, memoryIDs)
	if err != nil {
		return results // best effort: never fail retrieval due to supersede filter
	}

	hasAny := make(map[int64]bool)
	hasActive := make(map[int64]bool)
	for _, f := range facts {
		hasAny[f.MemoryID] = true
		if f.SupersededBy == nil {
			hasActive[f.MemoryID] = true
		}
	}

	filtered := make([]Result, 0, len(results))
	for _, r := range results {
		if !hasAny[r.MemoryID] || hasActive[r.MemoryID] {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func matchAgent(r Result, agent string) bool {
	if r.Metadata == nil {
		return false
	}
	return r.Metadata.AgentID == agent || r.Metadata.AgentName == agent
}

func matchChannel(r Result, channel string) bool {
	if r.Metadata == nil {
		return false
	}
	return r.Metadata.Channel == channel || r.Metadata.ChannelName == channel
}

// applyConfidenceDecay adjusts search result scores based on the effective confidence
// of facts linked to each memory, and reinforces those facts (Ebbinghaus recall).
func (e *Engine) applyConfidenceDecay(ctx context.Context, results []Result, includeSuperseded bool, explain bool) ([]Result, map[int64]confidenceDetail) {
	// Collect memory IDs from results
	memoryIDs := make([]int64, 0, len(results))
	for _, r := range results {
		if r.MemoryID > 0 {
			memoryIDs = append(memoryIDs, r.MemoryID)
		}
	}

	if len(memoryIDs) == 0 {
		return results, map[int64]confidenceDetail{}
	}

	// Get average confidence/effective-confidence per memory from linked facts.
	confidenceMap := e.getMemoryConfidenceMap(ctx, memoryIDs, includeSuperseded)

	// Apply confidence weighting to scores
	for i := range results {
		detail, ok := confidenceMap[results[i].MemoryID]
		if !ok {
			detail = confidenceDetail{confidence: 1.0, effectiveConfidence: 1.0}
		}

		// Blend: score = (1 - weight) * original_score + weight * (original_score * effective_confidence)
		// This gently penalizes stale memories without completely suppressing them.
		preConfidenceScore := results[i].Score
		results[i].Score = (1-ConfidenceWeight)*results[i].Score + ConfidenceWeight*(results[i].Score*detail.effectiveConfidence)

		if explain {
			ensureExplain(&results[i])
			if results[i].Explain.RankComponents.BaseScore == 0 {
				results[i].Explain.RankComponents.BaseScore = preConfidenceScore
			}
			if results[i].Explain.RankComponents.ClassBoostMultiplier == 0 {
				results[i].Explain.RankComponents.ClassBoostMultiplier = 1.0
			}
			if results[i].Explain.RankComponents.PreConfidenceScore == 0 {
				results[i].Explain.RankComponents.PreConfidenceScore = preConfidenceScore
			}
			results[i].Explain.RankComponents.ConfidenceWeight = ConfidenceWeight
			results[i].Explain.RankComponents.FinalScore = results[i].Score
			results[i].Explain.Confidence.Confidence = detail.confidence
			results[i].Explain.Confidence.EffectiveConfidence = detail.effectiveConfidence
		}
	}

	// Re-sort by adjusted score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Reinforce-on-recall: update last_reinforced for facts linked to returned memories
	// This is fire-and-forget — don't fail the search if reinforcement fails
	go func() {
		_, _ = e.store.ReinforceFactsByMemoryIDs(context.Background(), memoryIDs)
	}()

	return results, confidenceMap
}

// getMemoryConfidenceMap returns average base confidence + average effective confidence
// for facts linked to each memory ID.
func (e *Engine) getMemoryConfidenceMap(ctx context.Context, memoryIDs []int64, includeSuperseded bool) map[int64]confidenceDetail {
	confidenceMap := make(map[int64]confidenceDetail)

	var (
		facts []*store.Fact
		err   error
	)
	if includeSuperseded {
		facts, err = e.store.GetFactsByMemoryIDsIncludingSuperseded(ctx, memoryIDs)
	} else {
		facts, err = e.store.GetFactsByMemoryIDs(ctx, memoryIDs)
	}
	if err != nil || len(facts) == 0 {
		// No facts found — assume full confidence for all memories
		for _, id := range memoryIDs {
			confidenceMap[id] = confidenceDetail{confidence: 1.0, effectiveConfidence: 1.0}
		}
		return confidenceMap
	}

	// Group facts by memory ID and compute confidence/effective confidence.
	type accumulator struct {
		totalConfidence          float64
		totalEffectiveConfidence float64
		count                    int
	}
	accum := make(map[int64]*accumulator)

	now := timeNow()
	for _, f := range facts {
		days := math.Max(0, now.Sub(f.LastReinforced).Hours()/24)
		effective := f.Confidence * math.Exp(-f.DecayRate*days)

		if a, ok := accum[f.MemoryID]; ok {
			a.totalConfidence += f.Confidence
			a.totalEffectiveConfidence += effective
			a.count++
		} else {
			accum[f.MemoryID] = &accumulator{totalConfidence: f.Confidence, totalEffectiveConfidence: effective, count: 1}
		}
	}

	for _, id := range memoryIDs {
		if a, ok := accum[id]; ok && a.count > 0 {
			confidenceMap[id] = confidenceDetail{
				confidence:          a.totalConfidence / float64(a.count),
				effectiveConfidence: a.totalEffectiveConfidence / float64(a.count),
			}
		} else {
			confidenceMap[id] = confidenceDetail{confidence: 1.0, effectiveConfidence: 1.0} // No facts = assume full confidence
		}
	}

	return confidenceMap
}

func ensureExplain(result *Result) {
	if result.Explain != nil {
		return
	}
	result.Explain = &ExplainDetails{}
	result.Explain.RankComponents.ClassBoostMultiplier = 1.0
}

func (e *Engine) addExplainability(results []Result, confidenceMap map[int64]confidenceDetail) {
	now := timeNow()
	for i := range results {
		ensureExplain(&results[i])

		detail, ok := confidenceMap[results[i].MemoryID]
		if !ok {
			detail = confidenceDetail{confidence: 1.0, effectiveConfidence: 1.0}
		}
		results[i].Explain.Confidence.Confidence = detail.confidence
		results[i].Explain.Confidence.EffectiveConfidence = detail.effectiveConfidence

		if results[i].Explain.RankComponents.PreConfidenceScore == 0 {
			results[i].Explain.RankComponents.PreConfidenceScore = results[i].Score
		}
		if results[i].Explain.RankComponents.FinalScore == 0 {
			results[i].Explain.RankComponents.FinalScore = results[i].Score
		}
		if results[i].Explain.RankComponents.ConfidenceWeight == 0 {
			results[i].Explain.RankComponents.ConfidenceWeight = ConfidenceWeight
		}
		if results[i].Explain.RankComponents.BaseScore == 0 {
			results[i].Explain.RankComponents.BaseScore = results[i].Explain.RankComponents.PreConfidenceScore
		}
		if results[i].Explain.RankComponents.ClassBoostMultiplier == 0 {
			results[i].Explain.RankComponents.ClassBoostMultiplier = 1.0
		}

		results[i].Explain.Provenance = ExplainProvenance{
			Source:    buildSourceLabel(results[i]),
			Timestamp: results[i].ImportedAt,
		}
		if !results[i].ImportedAt.IsZero() {
			results[i].Explain.Provenance.AgeDays = math.Max(0, now.Sub(results[i].ImportedAt).Hours()/24)
		}

		results[i].Explain.Why = fmt.Sprintf(
			"%s match with base %.3f × class %.2f, then confidence-adjusted to %.3f (effective confidence %.3f)",
			results[i].MatchType,
			results[i].Explain.RankComponents.BaseScore,
			results[i].Explain.RankComponents.ClassBoostMultiplier,
			results[i].Explain.RankComponents.FinalScore,
			detail.effectiveConfidence,
		)
	}
}

func buildSourceLabel(result Result) string {
	source := result.SourceFile
	if source == "" {
		source = "(unknown source)"
	}
	if result.SourceLine > 0 {
		source = fmt.Sprintf("%s:%d", source, result.SourceLine)
	}
	if result.SourceSection != "" {
		source = fmt.Sprintf("%s#%s", source, result.SourceSection)
	}
	return source
}

func floatPtr(v float64) *float64 {
	val := v
	return &val
}

// timeNow returns the current time. Extracted for testing.
var timeNow = func() time.Time { return time.Now().UTC() }

// searchBM25 performs keyword search using the store's FTS5 capability.
// Uses AND-first-then-OR strategy: tries implicit AND for precision,
// falls back to OR for recall when AND returns zero results.
func (e *Engine) searchBM25(ctx context.Context, query string, opts Options) ([]Result, error) {
	// Sanitize query to prevent FTS5 syntax errors from crashing
	sanitized := sanitizeFTSQuery(query)
	if sanitized == "" {
		return nil, nil
	}

	storeResults, err := e.store.SearchFTSWithProject(ctx, sanitized, opts.Limit, opts.Project)
	if err != nil {
		// If the query has bad FTS5 syntax, try a simpler fallback
		if isFTSSyntaxError(err) {
			escaped := escapeFTSQuery(query)
			storeResults, err = e.store.SearchFTSWithProject(ctx, escaped, opts.Limit, opts.Project)
			if err != nil {
				return nil, fmt.Errorf("search failed: %w", err)
			}
		} else {
			return nil, fmt.Errorf("search failed: %w", err)
		}
	}

	// AND→OR fallback: if AND returned nothing and query has multiple words, retry with OR.
	// This gives precision when all terms co-occur, recall when they don't.
	if len(storeResults) == 0 && hasMultipleSearchTerms(sanitized) {
		orQuery := buildORQuery(sanitized)
		if orQuery != "" {
			storeResults, err = e.store.SearchFTSWithProject(ctx, orQuery, opts.Limit, opts.Project)
			if err != nil {
				// OR fallback failed — not fatal, just return empty
				storeResults = nil
			}
		}
	}

	minScore := effectiveMinScore(ModeKeyword, opts.MinScore)
	results := make([]Result, 0, len(storeResults))
	allFiltered := make([]Result, 0, len(storeResults))

	for _, sr := range storeResults {
		// FTS5 rank is negative (more negative = better match).
		// Convert to positive score where higher = better.
		score := normalizeBM25Score(sr.Score)

		r := Result{
			Content:       sr.Memory.Content,
			SourceFile:    sr.Memory.SourceFile,
			SourceLine:    sr.Memory.SourceLine,
			SourceSection: sr.Memory.SourceSection,
			Project:       sr.Memory.Project,
			MemoryClass:   sr.Memory.MemoryClass,
			Metadata:      sr.Memory.Metadata,
			Score:         score,
			Snippet:       sr.Snippet,
			MatchType:     "bm25",
			MemoryID:      sr.Memory.ID,
			ImportedAt:    sr.Memory.ImportedAt,
		}
		if opts.Explain {
			r.Explain = &ExplainDetails{
				RankComponents: RankComponents{
					BaseScore:            score,
					PreConfidenceScore:   score,
					FinalScore:           score,
					ClassBoostMultiplier: 1.0,
					ConfidenceWeight:     ConfidenceWeight,
					BM25Raw:              floatPtr(sr.Score),
					BM25Score:            floatPtr(score),
				},
			}
		}
		allFiltered = append(allFiltered, r)

		if score >= minScore {
			results = append(results, r)
		}
	}

	// Small-DB rescue: if FTS5 returned matches but all scores fell below
	// the DEFAULT threshold (common with <50 docs where IDF is very low),
	// return the matches anyway. A low-confidence result beats no result.
	// Only applies when user hasn't set an explicit MinScore.
	if len(results) == 0 && len(allFiltered) > 0 && opts.MinScore < 0 {
		results = allFiltered
	}

	// Sort by score descending (should already be sorted from FTS5, but ensure it)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// normalizeBM25Score converts FTS5's negative rank to a 0-1 score.
// FTS5 rank values are negative, with more negative being more relevant.
// We use log normalization: score = log(1 + |rank|) / log(1 + maxRank)
// where maxRank anchors the scale. This preserves relative differences
// better than 1/(1+|rank|) which compresses everything to 0.04-0.16.
// For standalone BM25 results, we use a simpler sigmoid-like mapping.
func normalizeBM25Score(rank float64) float64 {
	absRank := math.Abs(rank)
	// Sigmoid-like mapping: tanh(absRank / scale)
	// scale=10 gives: rank -1 → 0.10, rank -5 → 0.46, rank -10 → 0.76, rank -25 → 0.99
	// This spreads scores across 0-1 range more evenly than 1/(1+x)
	return math.Tanh(absRank / 10.0)
}

// sanitizeFTSQuery performs basic sanitization of an FTS5 query.
// It trims whitespace and returns empty string for empty/whitespace-only queries.
func sanitizeFTSQuery(query string) string {
	return strings.TrimSpace(query)
}

// hasMultipleSearchTerms checks if a query has more than one searchable word.
func hasMultipleSearchTerms(query string) bool {
	words := strings.Fields(query)
	count := 0
	for _, w := range words {
		w = strings.Trim(w, `"`)
		if w == "" || strings.EqualFold(w, "AND") || strings.EqualFold(w, "OR") || strings.EqualFold(w, "NOT") {
			continue
		}
		count++
		if count >= 2 {
			return true
		}
	}
	return false
}

// buildORQuery converts a multi-word query to use OR between terms.
// "SB co-founder Spear" → `"SB" OR "co-founder" OR "Spear"`
func buildORQuery(query string) string {
	words := strings.Fields(query)
	if len(words) <= 1 {
		return query
	}

	terms := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, `"`)
		w = strings.TrimSpace(w)
		if w == "" || strings.EqualFold(w, "AND") || strings.EqualFold(w, "OR") || strings.EqualFold(w, "NOT") {
			continue
		}
		terms = append(terms, `"`+w+`"`)
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

// escapeFTSQuery wraps each word in double quotes to treat them as literal terms,
// used as a fallback when the original query has invalid FTS5 syntax.
func escapeFTSQuery(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return ""
	}

	escaped := make([]string, 0, len(words))
	for _, w := range words {
		// Strip any existing quotes and FTS5 operators
		w = strings.Trim(w, `"`)
		w = strings.TrimSpace(w)
		if w == "" || strings.EqualFold(w, "AND") || strings.EqualFold(w, "OR") || strings.EqualFold(w, "NOT") {
			continue
		}
		escaped = append(escaped, `"`+w+`"`)
	}
	return strings.Join(escaped, " ")
}

// isFTSSyntaxError checks if an error is likely an FTS5 syntax error.
func isFTSSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "fts5: syntax error") ||
		strings.Contains(msg, "FTS") ||
		strings.Contains(msg, "fts5")
}

// TruncateContent truncates content to approximately maxLen characters,
// breaking at a word boundary and appending "..." if truncated.
func TruncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// Find the last space before maxLen
	truncated := content[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// searchSemantic performs semantic search using embedding similarity.
func (e *Engine) searchSemantic(ctx context.Context, query string, opts Options) ([]Result, error) {
	if e.embedder == nil {
		return nil, fmt.Errorf("semantic search requires an embedder. Use --embed <provider/model> flag")
	}

	// Generate embedding for query
	queryEmbedding, err := e.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	// Check dimension compatibility with stored embeddings
	if len(queryEmbedding) > 0 {
		storedDims, err := e.store.GetEmbeddingDimensions(ctx)
		if err == nil { // Only check if there are stored embeddings
			if len(queryEmbedding) != storedDims {
				return nil, fmt.Errorf("dimension mismatch: query embedding has %d dimensions but stored embeddings have %d. Did you change embedding models? Re-embed with: cortex embed <provider/model>", len(queryEmbedding), storedDims)
			}
		}
	}

	minScore := effectiveMinScore(ModeSemantic, opts.MinScore)

	// Use HNSW index if available (O(log N)), otherwise fall back to brute-force (O(N))
	if e.hnsw != nil && opts.Project == "" {
		return e.searchSemanticHNSW(ctx, queryEmbedding, opts, minScore)
	}

	// Brute-force fallback (also used when project filter is active,
	// since HNSW doesn't support filtered search natively)
	storeResults, err := e.store.SearchEmbeddingWithProject(ctx, queryEmbedding, opts.Limit, minScore, opts.Project)
	if err != nil {
		return nil, fmt.Errorf("semantic search failed: %w", err)
	}

	results := make([]Result, 0, len(storeResults))
	for _, sr := range storeResults {
		r := Result{
			Content:       sr.Memory.Content,
			SourceFile:    sr.Memory.SourceFile,
			SourceLine:    sr.Memory.SourceLine,
			SourceSection: sr.Memory.SourceSection,
			Project:       sr.Memory.Project,
			MemoryClass:   sr.Memory.MemoryClass,
			Metadata:      sr.Memory.Metadata,
			Score:         sr.Score,
			Snippet:       sr.Snippet,
			MatchType:     "semantic",
			MemoryID:      sr.Memory.ID,
			ImportedAt:    sr.Memory.ImportedAt,
		}
		if opts.Explain {
			r.Explain = &ExplainDetails{
				RankComponents: RankComponents{
					BaseScore:            sr.Score,
					PreConfidenceScore:   sr.Score,
					FinalScore:           sr.Score,
					ClassBoostMultiplier: 1.0,
					ConfidenceWeight:     ConfidenceWeight,
					SemanticScore:        floatPtr(sr.Score),
				},
			}
		}
		results = append(results, r)
	}

	return results, nil
}

// SaveHNSW persists the current HNSW index to disk.
func (e *Engine) SaveHNSW(path string) error {
	if e.hnsw == nil {
		return fmt.Errorf("no HNSW index loaded")
	}
	return e.hnsw.Save(path)
}

// searchSemanticHNSW performs semantic search using the HNSW index.
// Converts cosine distance to similarity, fetches memory details from store.
func (e *Engine) searchSemanticHNSW(ctx context.Context, queryVec []float32, opts Options, minScore float64) ([]Result, error) {
	// HNSW returns cosine distance; we need extra candidates since we filter by minScore after
	ef := opts.Limit * 3
	if ef < 50 {
		ef = 50
	}

	annResults := e.hnsw.SearchEf(queryVec, opts.Limit*2, ef)

	var results []Result
	for _, ar := range annResults {
		similarity := 1.0 - float64(ar.Distance) // cosine_distance = 1 - cosine_similarity
		if similarity < minScore {
			continue
		}

		// Fetch full memory from store
		mem, err := e.store.GetMemory(ctx, ar.ID)
		if err != nil || mem == nil {
			continue // memory may have been deleted since index was built
		}

		r := Result{
			Content:       mem.Content,
			SourceFile:    mem.SourceFile,
			SourceLine:    mem.SourceLine,
			SourceSection: mem.SourceSection,
			Project:       mem.Project,
			MemoryClass:   mem.MemoryClass,
			Metadata:      mem.Metadata,
			Score:         similarity,
			MatchType:     "semantic",
			MemoryID:      mem.ID,
			ImportedAt:    mem.ImportedAt,
		}
		if opts.Explain {
			r.Explain = &ExplainDetails{
				RankComponents: RankComponents{
					BaseScore:            similarity,
					PreConfidenceScore:   similarity,
					FinalScore:           similarity,
					ClassBoostMultiplier: 1.0,
					ConfidenceWeight:     ConfidenceWeight,
					SemanticScore:        floatPtr(similarity),
				},
			}
		}
		results = append(results, r)

		if len(results) >= opts.Limit {
			break
		}
	}

	return results, nil
}

// searchHybrid performs both BM25 and semantic search, merging results with
// Weighted Score Fusion. Fetches extra candidates from each engine (3x limit)
// to give the fusion algorithm more signal to work with.
func (e *Engine) searchHybrid(ctx context.Context, query string, opts Options) ([]Result, error) {
	if e.embedder == nil {
		return nil, fmt.Errorf("semantic search requires an embedder. Use --embed <provider/model> flag")
	}

	// Fetch more candidates than requested so fusion has a wider pool.
	// With only opts.Limit from each, overlap is sparse and ranking is noisy.
	candidateOpts := opts
	candidateOpts.Limit = opts.Limit * 3
	if candidateOpts.Limit < 15 {
		candidateOpts.Limit = 15
	}

	// Run both searches concurrently
	type searchResult struct {
		results []Result
		err     error
	}

	bm25Chan := make(chan searchResult, 1)
	semanticChan := make(chan searchResult, 1)

	go func() {
		results, err := e.searchBM25(ctx, query, candidateOpts)
		bm25Chan <- searchResult{results, err}
	}()

	go func() {
		results, err := e.searchSemantic(ctx, query, candidateOpts)
		semanticChan <- searchResult{results, err}
	}()

	bm25Result := <-bm25Chan
	semanticResult := <-semanticChan

	// Handle errors - if one fails, return the other
	if bm25Result.err != nil && semanticResult.err != nil {
		return nil, fmt.Errorf("both searches failed: BM25: %w, Semantic: %v", bm25Result.err, semanticResult.err)
	} else if bm25Result.err != nil {
		for i := range semanticResult.results {
			semanticResult.results[i].MatchType = "hybrid"
		}
		if len(semanticResult.results) > opts.Limit {
			semanticResult.results = semanticResult.results[:opts.Limit]
		}
		return semanticResult.results, nil
	} else if semanticResult.err != nil {
		for i := range bm25Result.results {
			bm25Result.results[i].MatchType = "hybrid"
		}
		if len(bm25Result.results) > opts.Limit {
			bm25Result.results = bm25Result.results[:opts.Limit]
		}
		return bm25Result.results, nil
	}

	return mergeWeightedScores(bm25Result.results, semanticResult.results, opts.Limit, opts.Explain), nil
}

// mergeWeightedScores combines BM25 and semantic results using normalized score fusion.
//
// Why not RRF? RRF with k=60 was designed for large candidate sets (hundreds).
// With 5-15 candidates, all scores compress to 0.016-0.033 — indistinguishable.
//
// Weighted Score Fusion:
//  1. Normalize each result set's scores to 0-1 (min-max within set)
//  2. Combine: hybrid_score = α × bm25_norm + (1-α) × semantic_norm
//  3. Results appearing in both sets get boosted by both signals
//
// α=0.3 (BM25 weight) — semantic gets more influence because:
//   - Semantic captures meaning/intent that keywords miss
//   - BM25 already gets a natural boost: keyword matches that ALSO have
//     high semantic similarity will rank highest
const hybridAlpha = 0.3 // BM25 weight. Semantic weight = 1 - hybridAlpha

func mergeWeightedScores(bm25Results, semanticResults []Result, limit int, explain bool) []Result {
	// Normalize scores within each result set to 0-1 range
	bm25Norm := normalizeResultScores(bm25Results)
	semNorm := normalizeResultScores(semanticResults)

	// Build a map of memory_id → normalized scores from each source
	type fusedEntry struct {
		result   Result
		bm25     float64
		semantic float64
	}
	fusedMap := make(map[int64]*fusedEntry)

	for i, r := range bm25Results {
		fusedMap[r.MemoryID] = &fusedEntry{
			result: r,
			bm25:   bm25Norm[i],
		}
	}

	for i, r := range semanticResults {
		if entry, exists := fusedMap[r.MemoryID]; exists {
			// Result found by both engines — use semantic's content (usually richer)
			entry.semantic = semNorm[i]
			if len(strings.TrimSpace(r.Content)) > len(strings.TrimSpace(entry.result.Content)) {
				entry.result.Content = r.Content
			}
			if entry.result.Snippet == "" {
				entry.result.Snippet = r.Snippet
			}
		} else {
			fusedMap[r.MemoryID] = &fusedEntry{
				result:   r,
				semantic: semNorm[i],
			}
		}
	}

	// Calculate fused scores
	var merged []Result
	for _, entry := range fusedMap {
		bm25Contribution := hybridAlpha * entry.bm25
		semanticContribution := (1 - hybridAlpha) * entry.semantic
		fusedScore := bm25Contribution + semanticContribution

		entry.result.Score = fusedScore
		entry.result.MatchType = "hybrid"

		if explain {
			ensureExplain(&entry.result)
			entry.result.Explain.RankComponents.BaseScore = fusedScore
			entry.result.Explain.RankComponents.PreConfidenceScore = fusedScore
			entry.result.Explain.RankComponents.FinalScore = fusedScore
			entry.result.Explain.RankComponents.ClassBoostMultiplier = 1.0
			entry.result.Explain.RankComponents.ConfidenceWeight = ConfidenceWeight
			entry.result.Explain.RankComponents.HybridBM25Normalized = floatPtr(entry.bm25)
			entry.result.Explain.RankComponents.HybridSemanticNormalized = floatPtr(entry.semantic)
			entry.result.Explain.RankComponents.HybridBM25Contribution = floatPtr(bm25Contribution)
			entry.result.Explain.RankComponents.HybridSemanticContribution = floatPtr(semanticContribution)
		}

		merged = append(merged, entry.result)
	}

	// Sort by fused score descending
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	if len(merged) > limit {
		merged = merged[:limit]
	}

	return merged
}

// normalizeResultScores returns min-max normalized scores (0-1) for a result set.
// If all scores are equal, returns 1.0 for all (single-score degenerate case).
func normalizeResultScores(results []Result) []float64 {
	if len(results) == 0 {
		return nil
	}

	scores := make([]float64, len(results))
	minScore := results[0].Score
	maxScore := results[0].Score
	for _, r := range results {
		if r.Score < minScore {
			minScore = r.Score
		}
		if r.Score > maxScore {
			maxScore = r.Score
		}
	}

	spread := maxScore - minScore
	for i, r := range results {
		if spread == 0 {
			scores[i] = 1.0 // All same score
		} else {
			scores[i] = (r.Score - minScore) / spread
		}
	}

	return scores
}
