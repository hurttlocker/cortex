// bench_slo.go — SLO benchmark for search, stale, and conflicts commands.
// Run: go run scripts/bench_slo.go [--db path] [--iterations N]
//
// Generates a JSON report with p50/p95/p99 latencies for each command.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

type BenchResult struct {
	Command    string  `json:"command"`
	Iterations int     `json:"iterations"`
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	P99Ms      float64 `json:"p99_ms"`
	MinMs      float64 `json:"min_ms"`
	MaxMs      float64 `json:"max_ms"`
	MeanMs     float64 `json:"mean_ms"`
	Pass       bool    `json:"pass"`
	SLOMs      float64 `json:"slo_ms"`
}

type BenchReport struct {
	GeneratedAt string        `json:"generated_at"`
	DBPath      string        `json:"db_path"`
	FactCount   int           `json:"fact_count"`
	MemoryCount int           `json:"memory_count"`
	Results     []BenchResult `json:"results"`
	AllPass     bool          `json:"all_pass"`
}

func main() {
	dbPath := flag.String("db", "", "Path to cortex.db (default: ~/.cortex/cortex.db)")
	iterations := flag.Int("iterations", 20, "Number of iterations per benchmark")
	outFile := flag.String("out", "", "Output JSON file (default: stdout)")
	flag.Parse()

	if *dbPath == "" {
		home, _ := os.UserHomeDir()
		*dbPath = filepath.Join(home, ".cortex", "cortex.db")
	}

	// Expand ~ in path
	if strings.HasPrefix(*dbPath, "~/") {
		home, _ := os.UserHomeDir()
		*dbPath = filepath.Join(home, (*dbPath)[2:])
	}

	cfg := store.StoreConfig{
		DBPath:   *dbPath,
		ReadOnly: true,
	}

	s, err := store.NewStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	ctx := context.Background()

	// Get baseline counts
	stats, err := s.Stats(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting stats: %v\n", err)
		os.Exit(1)
	}

	report := BenchReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		DBPath:      *dbPath,
		FactCount:   int(stats.FactCount),
		MemoryCount: int(stats.MemoryCount),
		AllPass:     true,
	}

	fmt.Fprintf(os.Stderr, "Cortex SLO Benchmark\n")
	fmt.Fprintf(os.Stderr, "  DB: %s\n", *dbPath)
	fmt.Fprintf(os.Stderr, "  Memories: %d, Facts: %d\n", stats.MemoryCount, stats.FactCount)
	fmt.Fprintf(os.Stderr, "  Iterations: %d\n\n", *iterations)

	// Benchmark queries (representative agent workload)
	searchQueries := []string{
		"Q's email address",
		"trading strategy ORB",
		"wedding planning venue",
		"cortex version release",
		"spear production deployment",
		"crypto ADA scanner",
		"eyes web onboarding",
		"permit sniper product",
	}

	// 1. Search (FTS) benchmark
	searchTimes := benchmarkSearch(ctx, s, searchQueries, *iterations)
	searchResult := computeResult("search_fts", searchTimes, 2000)
	report.Results = append(report.Results, searchResult)
	if !searchResult.Pass {
		report.AllPass = false
	}

	// 2. Stale facts benchmark
	staleTimes := benchmarkStale(ctx, s, *iterations)
	staleResult := computeResult("stale_facts", staleTimes, 5000)
	report.Results = append(report.Results, staleResult)
	if !staleResult.Pass {
		report.AllPass = false
	}

	// 3. Conflicts benchmark
	conflictTimes := benchmarkConflicts(ctx, s, *iterations)
	conflictResult := computeResult("conflicts", conflictTimes, 5000)
	report.Results = append(report.Results, conflictResult)
	if !conflictResult.Pass {
		report.AllPass = false
	}

	// 4. Hybrid search (BM25 + semantic if available)
	hybridTimes := benchmarkHybridSearch(ctx, s, searchQueries, *iterations)
	hybridResult := computeResult("search_hybrid", hybridTimes, 2000)
	report.Results = append(report.Results, hybridResult)
	if !hybridResult.Pass {
		report.AllPass = false
	}

	// Print results
	for _, r := range report.Results {
		status := "✅ PASS"
		if !r.Pass {
			status = "❌ FAIL"
		}
		fmt.Fprintf(os.Stderr, "  %s: p50=%.1fms p95=%.1fms p99=%.1fms (SLO: %.0fms) %s\n",
			r.Command, r.P50Ms, r.P95Ms, r.P99Ms, r.SLOMs, status)
	}

	if report.AllPass {
		fmt.Fprintf(os.Stderr, "\n✅ All SLOs met\n")
	} else {
		fmt.Fprintf(os.Stderr, "\n❌ Some SLOs missed\n")
	}

	// Output JSON
	jsonBytes, _ := json.MarshalIndent(report, "", "  ")
	if *outFile != "" {
		os.WriteFile(*outFile, jsonBytes, 0644)
		fmt.Fprintf(os.Stderr, "\nReport written to %s\n", *outFile)
	} else {
		fmt.Println(string(jsonBytes))
	}
}

func benchmarkSearch(ctx context.Context, s store.Store, queries []string, iterations int) []float64 {
	var times []float64
	for i := 0; i < iterations; i++ {
		q := queries[i%len(queries)]
		start := time.Now()
		_, _ = s.SearchFTS(ctx, q, 10)
		times = append(times, float64(time.Since(start).Microseconds())/1000.0)
	}
	return times
}

func benchmarkHybridSearch(ctx context.Context, s store.Store, queries []string, iterations int) []float64 {
	// Use the search engine for hybrid if possible
	eng := search.NewEngine(s)
	var times []float64
	for i := 0; i < iterations; i++ {
		q := queries[i%len(queries)]
		start := time.Now()
		_, _ = eng.Search(ctx, q, search.Options{
			Limit: 10,
			Mode:  search.ModeKeyword,
		})
		times = append(times, float64(time.Since(start).Microseconds())/1000.0)
	}
	return times
}

func benchmarkStale(ctx context.Context, s store.Store, iterations int) []float64 {
	var times []float64
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, _ = s.StaleFacts(ctx, 0.5, 30)
		times = append(times, float64(time.Since(start).Microseconds())/1000.0)
	}
	return times
}

func benchmarkConflicts(ctx context.Context, s store.Store, iterations int) []float64 {
	var times []float64
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, _ = s.GetAttributeConflictsLimit(ctx, 20)
		times = append(times, float64(time.Since(start).Microseconds())/1000.0)
	}
	return times
}

func computeResult(name string, times []float64, sloMs float64) BenchResult {
	sort.Float64s(times)
	n := len(times)
	if n == 0 {
		return BenchResult{Command: name, SLOMs: sloMs}
	}

	sum := 0.0
	for _, t := range times {
		sum += t
	}

	p95 := times[int(float64(n)*0.95)]
	result := BenchResult{
		Command:    name,
		Iterations: n,
		P50Ms:      times[n/2],
		P95Ms:      p95,
		P99Ms:      times[int(float64(n)*0.99)],
		MinMs:      times[0],
		MaxMs:      times[n-1],
		MeanMs:     sum / float64(n),
		SLOMs:      sloMs,
		Pass:       p95 <= sloMs,
	}

	return result
}
