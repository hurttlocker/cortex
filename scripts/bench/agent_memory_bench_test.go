// agent_memory_bench_test.go — Agent-memory quality benchmark suite.
//
// Run: go test -v -run TestAgentMemoryBenchmark ./scripts/bench/
//
// This tests retrieval precision for representative agent workloads:
// - Decision recall
// - Preference lookup
// - Identity resolution
// - Temporal fact retrieval
// - Conflict detection quality
// - Noise suppression
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/extract"
	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

// BenchCase represents a single agent-memory quality test case.
type BenchCase struct {
	Name          string   // Human-readable test name
	Query         string   // Search query
	ExpectInTop3  []string // Substrings that should appear in top 3 results
	ExpectNotTop3 []string // Substrings that should NOT appear in top 3 results
	Mode          string   // Search mode (default: keyword)
}

// BenchScorecard tracks pass/fail across the full benchmark.
type BenchScorecard struct {
	Total       int          `json:"total"`
	Passed      int          `json:"passed"`
	Failed      int          `json:"failed"`
	PassRate    float64      `json:"pass_rate"`
	Cases       []CaseResult `json:"cases"`
	GeneratedAt string       `json:"generated_at"`
}

type CaseResult struct {
	Name   string  `json:"name"`
	Pass   bool    `json:"pass"`
	Reason string  `json:"reason,omitempty"`
	LatMs  float64 `json:"latency_ms"`
}

func TestAgentMemoryBenchmark(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/bench.db"

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	seedAgentMemories(t, ctx, s)

	engine := search.NewEngine(s)

	cases := []BenchCase{
		{
			Name:         "decision_recall_trading",
			Query:        "what trading strategy did we decide on",
			ExpectInTop3: []string{"ORB strategy", "opening range breakout"},
		},
		{
			Name:         "preference_lookup_editor",
			Query:        "preferred editor",
			ExpectInTop3: []string{"vim"},
		},
		{
			Name:         "identity_email",
			Query:        "Q's email address",
			ExpectInTop3: []string{"contact@spear-global.com"},
		},
		{
			Name:         "identity_phone",
			Query:        "phone number",
			ExpectInTop3: []string{"267-995-1461"},
		},
		{
			Name:         "temporal_wedding",
			Query:        "when is the wedding",
			ExpectInTop3: []string{"Sep-Oct 2026"},
		},
		{
			Name:         "project_spear",
			Query:        "what is Spear",
			ExpectInTop3: []string{"HHA Exchange"},
		},
		{
			Name:         "agent_architecture",
			Query:        "agent architecture how many agents",
			ExpectInTop3: []string{"Mister", "Niot"},
		},
		{
			Name:         "crypto_strategy",
			Query:        "crypto ADA strategy",
			ExpectInTop3: []string{"ML220", "Coinbase"},
		},
		{
			Name:          "noise_suppression_heartbeat",
			Query:         "heartbeat status",
			ExpectNotTop3: []string{"untrusted metadata", "conversation_label"},
		},
		{
			Name:          "noise_suppression_scaffold",
			Query:         "system message cron",
			ExpectNotTop3: []string{"sender_id", "message_id"},
		},
	}

	scorecard := BenchScorecard{
		Total:       len(cases),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			mode := search.ModeKeyword
			if tc.Mode == "hybrid" {
				mode = search.ModeHybrid
			}

			start := time.Now()
			results, err := engine.Search(ctx, tc.Query, search.Options{
				Mode:  mode,
				Limit: 5,
			})
			latMs := float64(time.Since(start).Microseconds()) / 1000.0

			cr := CaseResult{
				Name:  tc.Name,
				LatMs: latMs,
			}

			if err != nil {
				cr.Reason = fmt.Sprintf("search error: %v", err)
				scorecard.Cases = append(scorecard.Cases, cr)
				scorecard.Failed++
				t.Errorf("search failed: %v", err)
				return
			}

			top3 := results
			if len(top3) > 3 {
				top3 = top3[:3]
			}

			top3Content := ""
			for _, r := range top3 {
				top3Content += r.Content + " "
			}
			top3Lower := strings.ToLower(top3Content)

			// Check expected matches
			for _, expect := range tc.ExpectInTop3 {
				if !strings.Contains(top3Lower, strings.ToLower(expect)) {
					cr.Reason = fmt.Sprintf("expected %q in top 3, not found", expect)
					scorecard.Cases = append(scorecard.Cases, cr)
					scorecard.Failed++
					t.Errorf("expected %q in top 3 results, got: %s", expect, summarizeResults(top3))
					return
				}
			}

			// Check noise suppression
			for _, reject := range tc.ExpectNotTop3 {
				if strings.Contains(top3Lower, strings.ToLower(reject)) {
					cr.Reason = fmt.Sprintf("unexpected noise %q in top 3", reject)
					scorecard.Cases = append(scorecard.Cases, cr)
					scorecard.Failed++
					t.Errorf("unexpected noise %q in top 3 results", reject)
					return
				}
			}

			cr.Pass = true
			scorecard.Cases = append(scorecard.Cases, cr)
			scorecard.Passed++
		})
	}

	scorecard.PassRate = float64(scorecard.Passed) / float64(scorecard.Total)

	// Output scorecard
	jsonBytes, _ := json.MarshalIndent(scorecard, "", "  ")
	t.Logf("Agent Memory Benchmark Scorecard:\n%s", string(jsonBytes))

	// Write artifact
	outPath := os.Getenv("BENCH_OUTPUT")
	if outPath != "" {
		os.WriteFile(outPath, jsonBytes, 0644)
	}

	// Gate: require >= 80% pass rate
	if scorecard.PassRate < 0.80 {
		t.Errorf("benchmark pass rate %.0f%% below 80%% gate", scorecard.PassRate*100)
	}
}

func seedAgentMemories(t *testing.T, ctx context.Context, s store.Store) {
	t.Helper()

	memories := []struct {
		content string
		section string
		file    string
		class   string
	}{
		{
			content: "Q (Marquise Hurtt) — Philadelphia, PA. Phone: 267-995-1461. Email: contact@spear-global.com. Trading broker: TradeStation.",
			section: "Who",
			file:    "MEMORY.md",
			class:   "identity",
		},
		{
			content: "ORB Strategy (LOCKED Feb 9, 2026): 15-min opening range breakout (09:30-09:45), trade breakouts with bracket orders on Alpaca paper. Position sizing: risk_dollars / actual_stop_distance.",
			section: "ORB Strategy Details",
			file:    "MEMORY.md",
			class:   "decision",
		},
		{
			content: "Spear: HHA Exchange workflow augmentation. PayPal billing. The main business.",
			section: "Active Projects",
			file:    "MEMORY.md",
			class:   "decision",
		},
		{
			content: "4-Agent Architecture: Mister (Opus 4.6, CEO), Niot (Opus 4.6, Lead Engineer), Hawk (Haiku 4.5, QA), Sage (Sonnet 4.5, Research).",
			section: "4-Agent Architecture",
			file:    "MEMORY.md",
			class:   "identity",
		},
		{
			content: "Wedding Planning (Q & SB) — Black tie, 25-50 guests, $25K budget, Sep-Oct 2026, cliffside ocean view.",
			section: "Wedding Planning",
			file:    "MEMORY.md",
			class:   "decision",
		},
		{
			content: "ADA ML220 Module: Strategy MisterLabs220, venue Coinbase Advanced Trade (real money). Bot: crypto/scripts/ada_ml220_scanner.py.",
			section: "ADA ML220 Module",
			file:    "MEMORY.md",
			class:   "decision",
		},
		{
			content: "Q prefers vim as editor, Go as language, dark theme. Show reasoning then conclusion. Root causes only.",
			section: "Communication Rules",
			file:    "USER.md",
			class:   "preference",
		},
		{
			content: "Eyes Web: Personalized anti-inflammatory health companion. Repo: LavonTMCQ/mybeautifulwife. 4-phase roadmap.",
			section: "Active Projects",
			file:    "MEMORY.md",
			class:   "decision",
		},
		// Noise memories that should NOT surface for agent queries
		{
			content: "Read HEARTBEAT.md if it exists. Follow it strictly. Do not infer or repeat old tasks.",
			section: "",
			file:    "cortex-capture-2026-02-22.md",
			class:   "",
		},
		{
			content: "Conversation info (untrusted metadata): {\"conversation_label\": \"Guild #reports\", \"sender_id\": \"913093502911537162\"}",
			section: "",
			file:    "cortex-capture-2026-02-22.md",
			class:   "",
		},
	}

	pipeline := extract.NewPipeline()

	for i, m := range memories {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("seed-%d-%s", i, m.content[:20]))))
		mem := &store.Memory{
			Content:       m.content,
			SourceFile:    m.file,
			SourceSection: m.section,
			ContentHash:   hash,
			MemoryClass:   m.class,
		}
		id, err := s.AddMemory(ctx, mem)
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}

		// Extract and store facts
		metadata := map[string]string{
			"source_file":    m.file,
			"source_section": m.section,
		}
		facts, err := pipeline.Extract(ctx, m.content, metadata)
		if err != nil {
			t.Fatalf("Extract %d: %v", i, err)
		}
		for _, ef := range facts {
			f := &store.Fact{
				MemoryID:   id,
				Subject:    ef.Subject,
				Predicate:  ef.Predicate,
				Object:     ef.Object,
				FactType:   ef.FactType,
				Confidence: ef.Confidence,
				DecayRate:  ef.DecayRate,
			}
			if _, err := s.AddFact(ctx, f); err != nil {
				t.Fatalf("AddFact %d: %v", i, err)
			}
		}
	}
}

func summarizeResults(results []search.Result) string {
	var parts []string
	for i, r := range results {
		content := r.Content
		if len(content) > 80 {
			content = content[:80] + "..."
		}
		parts = append(parts, fmt.Sprintf("[%d] %.2f: %s", i+1, r.Score, content))
	}
	return strings.Join(parts, " | ")
}
