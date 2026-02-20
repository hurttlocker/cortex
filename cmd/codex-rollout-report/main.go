package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type telemetryEvent struct {
	Mode      string  `json:"mode"`
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	WallMS    int64   `json:"wall_ms"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	CostKnown bool    `json:"cost_known"`
}

type modeReport struct {
	Mode          string
	Runs          int
	P50MS         int64
	P95MS         int64
	EstimatedCost float64
	CostKnownRuns int
	ModelMix      map[string]int
}

type report struct {
	TotalRuns      int
	SkippedLines   int
	ModeReports    []modeReport
	OverallModelMx map[string]int
}

type guardrailConfig struct {
	OneShotP95WarnMS           int64
	RecursiveKnownCostMinShare float64
	WarnOnly                   bool
}

func main() {
	home, _ := os.UserHomeDir()
	defaultPath := filepath.Join(home, ".cortex", "reason-telemetry.jsonl")

	filePath := flag.String("file", defaultPath, "path to reason telemetry jsonl")
	oneShotP95WarnMS := flag.Int64("one-shot-p95-warn-ms", 20_000, "warn when one-shot p95 latency exceeds this threshold (ms)")
	recursiveKnownCostMinShare := flag.Float64("recursive-known-cost-min-share", 0.80, "warn when recursive known-cost share drops below this ratio (0-1)")
	warnOnly := flag.Bool("warn-only", true, "when true, emit warnings but always exit 0; set false for CI/cron non-zero exit on warnings")
	flag.Parse()

	if *recursiveKnownCostMinShare < 0 || *recursiveKnownCostMinShare > 1 {
		fmt.Fprintln(os.Stderr, "Error: --recursive-known-cost-min-share must be between 0 and 1")
		os.Exit(1)
	}
	if *oneShotP95WarnMS < 0 {
		fmt.Fprintln(os.Stderr, "Error: --one-shot-p95-warn-ms must be >= 0")
		os.Exit(1)
	}

	events, skipped, err := loadTelemetry(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cfg := guardrailConfig{
		OneShotP95WarnMS:           *oneShotP95WarnMS,
		RecursiveKnownCostMinShare: *recursiveKnownCostMinShare,
		WarnOnly:                   *warnOnly,
	}

	r := buildReport(events, skipped)
	warnings := evaluateGuardrails(r, cfg)
	fmt.Println(renderReport(*filePath, r, warnings, cfg))
	if len(warnings) > 0 && !cfg.WarnOnly {
		os.Exit(2)
	}
}

func loadTelemetry(path string) ([]telemetryEvent, int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []telemetryEvent{}, 0, nil
		}
		return nil, 0, fmt.Errorf("open telemetry file: %w", err)
	}
	defer f.Close()

	var events []telemetryEvent
	skipped := 0
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}

		var ev telemetryEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			skipped++
			continue
		}
		ev.Mode = normalizeMode(ev.Mode)
		if ev.Provider == "" {
			ev.Provider = "unknown"
		}
		if ev.Model == "" {
			ev.Model = "unknown"
		}
		if ev.WallMS < 0 {
			ev.WallMS = 0
		}
		events = append(events, ev)
	}

	if err := s.Err(); err != nil {
		return nil, skipped, fmt.Errorf("scan telemetry file: %w", err)
	}

	return events, skipped, nil
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "recursive":
		return "recursive"
	default:
		return "one-shot"
	}
}

func buildReport(events []telemetryEvent, skipped int) report {
	byMode := map[string][]telemetryEvent{
		"one-shot":  {},
		"recursive": {},
	}
	overallMix := map[string]int{}

	for _, ev := range events {
		byMode[ev.Mode] = append(byMode[ev.Mode], ev)
		overallMix[fmt.Sprintf("%s/%s", ev.Provider, ev.Model)]++
	}

	modes := []string{"one-shot", "recursive"}
	modeReports := make([]modeReport, 0, len(modes))
	for _, mode := range modes {
		runs := byMode[mode]
		if len(runs) == 0 {
			modeReports = append(modeReports, modeReport{Mode: mode, ModelMix: map[string]int{}})
			continue
		}

		latencies := make([]int64, 0, len(runs))
		cost := 0.0
		costKnownRuns := 0
		mix := map[string]int{}
		for _, ev := range runs {
			latencies = append(latencies, ev.WallMS)
			if ev.CostKnown {
				cost += ev.CostUSD
				costKnownRuns++
			}
			mix[fmt.Sprintf("%s/%s", ev.Provider, ev.Model)]++
		}

		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

		modeReports = append(modeReports, modeReport{
			Mode:          mode,
			Runs:          len(runs),
			P50MS:         percentileInt64(latencies, 0.50),
			P95MS:         percentileInt64(latencies, 0.95),
			EstimatedCost: cost,
			CostKnownRuns: costKnownRuns,
			ModelMix:      mix,
		})
	}

	return report{
		TotalRuns:      len(events),
		SkippedLines:   skipped,
		ModeReports:    modeReports,
		OverallModelMx: overallMix,
	}
}

func percentileInt64(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(float64(len(sorted))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func evaluateGuardrails(r report, cfg guardrailConfig) []string {
	warnings := []string{}

	oneShot, ok := findModeReport(r, "one-shot")
	if ok && oneShot.Runs > 0 && oneShot.P95MS > cfg.OneShotP95WarnMS {
		warnings = append(warnings, fmt.Sprintf("one-shot p95 latency %dms exceeds threshold %dms", oneShot.P95MS, cfg.OneShotP95WarnMS))
	}

	recursive, ok := findModeReport(r, "recursive")
	if ok && recursive.Runs > 0 {
		share := float64(recursive.CostKnownRuns) / float64(recursive.Runs)
		if share < cfg.RecursiveKnownCostMinShare {
			warnings = append(warnings, fmt.Sprintf("recursive known-cost completeness %.1f%% below threshold %.1f%%", share*100, cfg.RecursiveKnownCostMinShare*100))
		}
	}

	return warnings
}

func findModeReport(r report, mode string) (modeReport, bool) {
	for _, mr := range r.ModeReports {
		if mr.Mode == mode {
			return mr, true
		}
	}
	return modeReport{}, false
}

func renderReport(path string, r report, warnings []string, cfg guardrailConfig) string {
	var b strings.Builder
	b.WriteString("Cortex Codex rollout report\n")
	b.WriteString(fmt.Sprintf("Telemetry file: %s\n", path))
	b.WriteString(fmt.Sprintf("Runs parsed: %d", r.TotalRuns))
	if r.SkippedLines > 0 {
		b.WriteString(fmt.Sprintf(" (skipped malformed lines: %d)", r.SkippedLines))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Guardrails: one-shot p95 <= %dms, recursive known-cost share >= %.0f%%\n", cfg.OneShotP95WarnMS, cfg.RecursiveKnownCostMinShare*100))
	if cfg.WarnOnly {
		b.WriteString("Exit mode: warn-only (always 0)\n")
	} else {
		b.WriteString("Exit mode: strict (non-zero on guardrail warnings)\n")
	}
	b.WriteString("\n")

	b.WriteString("By mode (one-shot vs recursive)\n")
	b.WriteString("mode       runs  p50(ms)  p95(ms)  est_cost_usd  cost_runs\n")
	for _, mr := range r.ModeReports {
		b.WriteString(fmt.Sprintf("%-10s %-5d %-8d %-8d $%-12.6f %-9d\n",
			mr.Mode,
			mr.Runs,
			mr.P50MS,
			mr.P95MS,
			mr.EstimatedCost,
			mr.CostKnownRuns,
		))
	}

	b.WriteString("\nGuardrail status\n")
	if len(warnings) == 0 {
		b.WriteString("- OK: all configured guardrails passed\n")
	} else {
		for _, w := range warnings {
			b.WriteString("- WARN: " + w + "\n")
		}
	}

	b.WriteString("\nProvider/model mix (overall)\n")
	for _, line := range formatSortedMix(r.OverallModelMx) {
		b.WriteString(line + "\n")
	}

	for _, mr := range r.ModeReports {
		b.WriteString(fmt.Sprintf("\nProvider/model mix (%s)\n", mr.Mode))
		for _, line := range formatSortedMix(mr.ModelMix) {
			b.WriteString(line + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func formatSortedMix(m map[string]int) []string {
	if len(m) == 0 {
		return []string{"(none)"}
	}
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	total := 0
	for k, v := range m {
		items = append(items, kv{k: k, v: v})
		total += v
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v == items[j].v {
			return items[i].k < items[j].k
		}
		return items[i].v > items[j].v
	})
	out := make([]string, 0, len(items))
	for _, it := range items {
		pct := 0.0
		if total > 0 {
			pct = (float64(it.v) / float64(total)) * 100
		}
		out = append(out, fmt.Sprintf("- %s: %d (%.1f%%)", it.k, it.v, pct))
	}
	return out
}
