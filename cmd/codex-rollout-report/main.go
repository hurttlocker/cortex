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

func main() {
	home, _ := os.UserHomeDir()
	defaultPath := filepath.Join(home, ".cortex", "reason-telemetry.jsonl")

	filePath := flag.String("file", defaultPath, "path to reason telemetry jsonl")
	flag.Parse()

	events, skipped, err := loadTelemetry(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	r := buildReport(events, skipped)
	fmt.Println(renderReport(*filePath, r))
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

func renderReport(path string, r report) string {
	var b strings.Builder
	b.WriteString("Cortex Codex rollout report\n")
	b.WriteString(fmt.Sprintf("Telemetry file: %s\n", path))
	b.WriteString(fmt.Sprintf("Runs parsed: %d", r.TotalRuns))
	if r.SkippedLines > 0 {
		b.WriteString(fmt.Sprintf(" (skipped malformed lines: %d)", r.SkippedLines))
	}
	b.WriteString("\n\n")

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
