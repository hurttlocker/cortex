// scale_test.go — Scale & performance testing with synthetic data.
// Run: go test ./scripts/bench/ -run TestScale -v -timeout 10m
//
// Generates synthetic corpora at 1K, 10K, 50K, and 100K memories,
// then benchmarks import, search, stats, and graph operations.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

// ScaleTier defines a test tier.
type ScaleTier struct {
	Name     string `json:"name"`
	Memories int    `json:"memories"`
}

// ScaleResult stores benchmark results for a tier.
type ScaleResult struct {
	Tier          string  `json:"tier"`
	Memories      int     `json:"memories"`
	Facts         int     `json:"facts"`
	DBSizeBytes   int64   `json:"db_size_bytes"`
	ImportMs      float64 `json:"import_ms"`
	ImportPerSec  float64 `json:"import_per_sec"`
	SearchBM25P50 float64 `json:"search_bm25_p50_ms"`
	SearchBM25P99 float64 `json:"search_bm25_p99_ms"`
	StatsMs       float64 `json:"stats_ms"`
	StaleMs       float64 `json:"stale_ms"`
	ConflictsMs   float64 `json:"conflicts_ms"`
	ExtractMs     float64 `json:"extract_per_memory_ms"`
}

var tiers = []ScaleTier{
	{"small", 1000},
	{"medium", 10000},
}

// Subjects with realistic distribution (Zipf-like: few appear often, most appear rarely)
var subjects = []string{
	"Q", "SB", "Mister", "Niot", "Hawk", "Spear", "Trading", "eBay",
	"Cortex", "Eyes Web", "YouTube", "Discord", "Telegram", "ADA",
	"Alpaca", "Public", "ORB", "Philadelphia", "MacBook", "iMac",
	"OpenClaw", "GitHub", "Railway", "Vercel", "Coinbase", "PayPal",
	"Gemini", "Grok", "DeepSeek", "Sonnet", "Opus", "Haiku",
}

var predicates = []string{
	"uses", "lives in", "works on", "deployed to", "configured as",
	"decided to", "prefers", "manages", "monitors", "runs on",
	"costs", "expires on", "replaced", "upgraded from", "depends on",
	"connected to", "reports to", "scheduled for", "backed up to",
	"tested with", "verified by", "blocked by", "assigned to",
}

var factTypes = []string{
	"kv", "relationship", "preference", "temporal", "identity",
	"location", "decision", "state", "config",
}

func generateSyntheticMemory(rng *rand.Rand, idx int) (string, string) {
	// Realistic content lengths: 50-2000 chars
	contentLen := 100 + rng.Intn(1900)

	// Pick a primary subject (Zipf: first few subjects appear more)
	subjIdx := int(float64(len(subjects)) * (float64(rng.Intn(100)) / 100.0) * (float64(rng.Intn(100)) / 100.0))
	if subjIdx >= len(subjects) {
		subjIdx = len(subjects) - 1
	}
	subject := subjects[subjIdx]

	// Build realistic content
	pred := predicates[rng.Intn(len(predicates))]
	content := fmt.Sprintf("## %s\n\n%s %s various things. ", subject, subject, pred)

	// Add filler that looks like real notes
	templates := []string{
		"Updated the configuration for %s to use the new settings. ",
		"Deployed %s changes to production at 2:30 PM ET. ",
		"The %s integration is now working correctly after the fix. ",
		"Noticed that %s performance improved after the optimization. ",
		"Meeting notes: discussed %s roadmap for next quarter. ",
		"Bug report: %s throws an error when input exceeds 1000 chars. ",
		"Decision: we will proceed with %s as the primary approach. ",
		"The %s API returns JSON with nested objects. Auth via bearer token. ",
		"Backtest results show %s strategy has 65%% win rate over 90 days. ",
		"Reminder: %s license expires in 30 days. Need to renew. ",
	}

	for len(content) < contentLen {
		tmpl := templates[rng.Intn(len(templates))]
		fillSubj := subjects[rng.Intn(len(subjects))]
		content += fmt.Sprintf(tmpl, fillSubj)
	}

	if len(content) > contentLen {
		content = content[:contentLen]
	}

	source := fmt.Sprintf("synthetic/memory_%05d.md", idx)
	return content, source
}

func benchmarkAtScale(t *testing.T, tier ScaleTier) ScaleResult {
	t.Helper()

	// Create temp DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	cfg := store.StoreConfig{DBPath: dbPath}
	s, err := store.NewStore(cfg)
	if err != nil {
		t.Fatalf("[%s] Failed to create store: %v", tier.Name, err)
	}
	defer s.Close()

	ctx := context.Background()
	rng := rand.New(rand.NewSource(42)) // deterministic for reproducibility

	result := ScaleResult{
		Tier:     tier.Name,
		Memories: tier.Memories,
	}

	// --- IMPORT BENCHMARK ---
	t.Logf("[%s] Importing %d memories...", tier.Name, tier.Memories)
	importStart := time.Now()

	for i := 0; i < tier.Memories; i++ {
		content, source := generateSyntheticMemory(rng, i)
		mem := &store.Memory{
			Content:    content,
			SourceFile: source,
		}
		_, err := s.AddMemory(ctx, mem)
		if err != nil {
			t.Fatalf("[%s] Failed to save memory %d: %v", tier.Name, i, err)
		}
	}

	importDuration := time.Since(importStart)
	result.ImportMs = float64(importDuration.Milliseconds())
	result.ImportPerSec = float64(tier.Memories) / importDuration.Seconds()
	t.Logf("[%s] Import: %d memories in %.1fs (%.0f/sec)",
		tier.Name, tier.Memories, importDuration.Seconds(), result.ImportPerSec)

	// --- EXTRACTION BENCHMARK (sample) ---
	sampleSize := 100
	if tier.Memories < sampleSize {
		sampleSize = tier.Memories
	}

	// Add some facts so search/stats/stale/conflicts have data
	memories, _ := s.ListMemories(ctx, store.ListOpts{Limit: sampleSize})
	totalFacts := 0
	for _, mem := range memories {
		// Generate 2-5 synthetic facts per memory
		nFacts := 2 + rng.Intn(4)
		for j := 0; j < nFacts; j++ {
			subj := subjects[rng.Intn(len(subjects))]
			pred := predicates[rng.Intn(len(predicates))]
			obj := fmt.Sprintf("value_%d_%d", mem.ID, j)
			ft := factTypes[rng.Intn(len(factTypes))]
			f := &store.Fact{
				MemoryID:   mem.ID,
				Subject:    subj,
				Predicate:  pred,
				Object:     obj,
				FactType:   ft,
				Confidence: 0.7 + rng.Float64()*0.3,
			}
			s.AddFact(ctx, f)
			totalFacts++
		}
	}
	t.Logf("[%s] Generated %d facts for %d sample memories", tier.Name, totalFacts, sampleSize)
	result.ExtractMs = 0 // synthetic, not measured

	// Scale up fact count estimate
	result.Facts = totalFacts * (tier.Memories / sampleSize)

	// --- SEARCH BENCHMARK ---
	eng := search.NewEngine(s)
	queries := []string{
		"Q configuration", "trading strategy", "deployment production",
		"API endpoint", "version release", "scanner monitoring",
		"decision approach", "performance optimization",
	}

	var searchTimes []float64
	iterations := 50
	for i := 0; i < iterations; i++ {
		q := queries[i%len(queries)]
		start := time.Now()
		eng.Search(ctx, q, search.Options{Limit: 10, Mode: search.ModeKeyword})
		elapsed := float64(time.Since(start).Microseconds()) / 1000.0
		searchTimes = append(searchTimes, elapsed)
	}

	sortFloat64s(searchTimes)
	result.SearchBM25P50 = searchTimes[len(searchTimes)/2]
	result.SearchBM25P99 = searchTimes[int(float64(len(searchTimes))*0.99)]
	t.Logf("[%s] Search BM25: P50=%.1fms P99=%.1fms",
		tier.Name, result.SearchBM25P50, result.SearchBM25P99)

	// --- STATS BENCHMARK ---
	statsStart := time.Now()
	for i := 0; i < 10; i++ {
		s.Stats(ctx)
	}
	result.StatsMs = float64(time.Since(statsStart).Milliseconds()) / 10.0
	t.Logf("[%s] Stats: %.1fms avg", tier.Name, result.StatsMs)

	// --- STALE BENCHMARK ---
	staleStart := time.Now()
	for i := 0; i < 10; i++ {
		s.StaleFacts(ctx, 0.5, 30)
	}
	result.StaleMs = float64(time.Since(staleStart).Milliseconds()) / 10.0
	t.Logf("[%s] Stale: %.1fms avg", tier.Name, result.StaleMs)

	// --- CONFLICTS BENCHMARK ---
	conflictsStart := time.Now()
	for i := 0; i < 10; i++ {
		s.GetAttributeConflictsLimit(ctx, 20)
	}
	result.ConflictsMs = float64(time.Since(conflictsStart).Milliseconds()) / 10.0
	t.Logf("[%s] Conflicts: %.1fms avg", tier.Name, result.ConflictsMs)

	// --- DB SIZE ---
	if info, err := os.Stat(dbPath); err == nil {
		result.DBSizeBytes = info.Size()
		t.Logf("[%s] DB size: %.1f MB", tier.Name, float64(info.Size())/(1024*1024))
	}

	return result
}

func TestScale(t *testing.T) {
	var results []ScaleResult

	for _, tier := range tiers {
		t.Run(tier.Name, func(t *testing.T) {
			result := benchmarkAtScale(t, tier)
			results = append(results, result)
		})
	}

	// Write report
	report := map[string]interface{}{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"platform":     runtime.GOOS + "/" + runtime.GOARCH,
		"go_version":   runtime.Version(),
		"tiers":        results,
	}

	jsonBytes, _ := json.MarshalIndent(report, "", "  ")
	home, _ := os.UserHomeDir()
	outPath := filepath.Join(home, ".cortex", "scale_results.json")
	os.WriteFile(outPath, jsonBytes, 0644)
	t.Logf("\nScale report written to %s", outPath)

	// Print summary table
	t.Log("\n=== SCALE BENCHMARK SUMMARY ===")
	t.Log("Tier       | Memories | Import/sec | BM25 P50 | BM25 P99 | Stats   | DB Size")
	t.Log("-----------|----------|------------|----------|----------|---------|--------")
	for _, r := range results {
		t.Logf("%-10s | %8d | %10.0f | %7.1fms | %7.1fms | %5.1fms | %.1f MB",
			r.Tier, r.Memories, r.ImportPerSec,
			r.SearchBM25P50, r.SearchBM25P99, r.StatsMs,
			float64(r.DBSizeBytes)/(1024*1024))
	}

	// Performance gates
	for _, r := range results {
		if r.Tier == "medium" {
			if r.SearchBM25P99 > 200 {
				t.Errorf("[%s] BM25 P99 too high: %.1fms (target: <200ms)", r.Tier, r.SearchBM25P99)
			}
			if r.StatsMs > 500 {
				t.Errorf("[%s] Stats too slow: %.1fms (target: <500ms)", r.Tier, r.StatsMs)
			}
			if r.ImportPerSec < 50 {
				t.Errorf("[%s] Import too slow: %.0f/sec (target: >50/sec)", r.Tier, r.ImportPerSec)
			}
		}
	}
}

func sortFloat64s(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
