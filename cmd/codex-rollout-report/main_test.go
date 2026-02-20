package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildReport_ModeStatsAndPercentiles(t *testing.T) {
	events := []telemetryEvent{
		{Mode: "one-shot", Provider: "openrouter", Model: "openai-codex/gpt-5.2", WallMS: 1000, CostKnown: true, CostUSD: 0.001},
		{Mode: "one-shot", Provider: "openrouter", Model: "openai-codex/gpt-5.2", WallMS: 2000, CostKnown: true, CostUSD: 0.002},
		{Mode: "one-shot", Provider: "openrouter", Model: "openai-codex/gpt-5.2", WallMS: 3000, CostKnown: false},
		{Mode: "recursive", Provider: "openrouter", Model: "google/gemini-2.5-flash", WallMS: 10000, CostKnown: true, CostUSD: 0.010},
		{Mode: "recursive", Provider: "openrouter", Model: "google/gemini-2.5-flash", WallMS: 20000, CostKnown: true, CostUSD: 0.020},
	}

	r := buildReport(events, 1)
	if r.TotalRuns != 5 {
		t.Fatalf("expected 5 runs, got %d", r.TotalRuns)
	}
	if r.SkippedLines != 1 {
		t.Fatalf("expected skipped=1, got %d", r.SkippedLines)
	}

	oneShot := r.ModeReports[0]
	if oneShot.Mode != "one-shot" || oneShot.Runs != 3 {
		t.Fatalf("unexpected one-shot report: %+v", oneShot)
	}
	if oneShot.P50MS != 2000 {
		t.Fatalf("expected one-shot p50=2000, got %d", oneShot.P50MS)
	}
	if oneShot.P95MS != 3000 {
		t.Fatalf("expected one-shot p95=3000, got %d", oneShot.P95MS)
	}
	if oneShot.CostKnownRuns != 2 {
		t.Fatalf("expected one-shot known cost runs=2, got %d", oneShot.CostKnownRuns)
	}

	recursive := r.ModeReports[1]
	if recursive.Mode != "recursive" || recursive.Runs != 2 {
		t.Fatalf("unexpected recursive report: %+v", recursive)
	}
	if recursive.P50MS != 10000 {
		t.Fatalf("expected recursive p50=10000, got %d", recursive.P50MS)
	}
	if recursive.P95MS != 20000 {
		t.Fatalf("expected recursive p95=20000, got %d", recursive.P95MS)
	}
}

func TestLoadTelemetry_ParsesJSONLAndSkipsMalformed(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "reason-telemetry.jsonl")
	content := strings.Join([]string{
		`{"mode":"one-shot","provider":"openrouter","model":"openai-codex/gpt-5.2","wall_ms":1200,"cost_known":true,"cost_usd":0.0012}`,
		`{"mode":"recursive","provider":"openrouter","model":"google/gemini-2.5-flash","wall_ms":8800,"cost_known":false}`,
		`not-json`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	events, skipped, err := loadTelemetry(path)
	if err != nil {
		t.Fatalf("loadTelemetry: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if skipped != 1 {
		t.Fatalf("expected skipped=1, got %d", skipped)
	}
	if events[0].Mode != "one-shot" || events[1].Mode != "recursive" {
		t.Fatalf("unexpected mode normalization: %+v", events)
	}

	out := renderReport(path, buildReport(events, skipped))
	if !strings.Contains(out, "By mode (one-shot vs recursive)") {
		t.Fatalf("missing mode section: %s", out)
	}
	if !strings.Contains(out, "openrouter/openai-codex/gpt-5.2") {
		t.Fatalf("missing provider/model mix: %s", out)
	}
}
