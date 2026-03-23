package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/connect"
	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/observe"
	"github.com/hurttlocker/cortex/internal/store"
)

// ==================== parseGlobalFlags ====================

func TestParseGlobalFlags_DBFlag(t *testing.T) {
	// Reset globals between tests.
	globalDBPath = ""
	globalVerbose = false

	args := parseGlobalFlags([]string{"--db", "/tmp/test.db", "search", "query"})

	if globalDBPath != "/tmp/test.db" {
		t.Errorf("globalDBPath = %q, want %q", globalDBPath, "/tmp/test.db")
	}
	if len(args) != 2 || args[0] != "search" || args[1] != "query" {
		t.Errorf("filtered args = %v, want [search query]", args)
	}
}

func TestParseGlobalFlags_DBFlagEquals(t *testing.T) {
	globalDBPath = ""
	globalVerbose = false

	args := parseGlobalFlags([]string{"--db=/tmp/eq.db", "list"})

	if globalDBPath != "/tmp/eq.db" {
		t.Errorf("globalDBPath = %q, want %q", globalDBPath, "/tmp/eq.db")
	}
	if len(args) != 1 || args[0] != "list" {
		t.Errorf("filtered args = %v, want [list]", args)
	}
}

func TestParseGlobalFlags_VerboseFlag(t *testing.T) {
	globalDBPath = ""
	globalVerbose = false

	args := parseGlobalFlags([]string{"--verbose", "stats"})

	if !globalVerbose {
		t.Error("globalVerbose should be true")
	}
	if len(args) != 1 || args[0] != "stats" {
		t.Errorf("filtered args = %v, want [stats]", args)
	}
}

func TestParseGlobalFlags_NoFlags(t *testing.T) {
	globalDBPath = ""
	globalVerbose = false

	args := parseGlobalFlags([]string{"search", "hello world"})

	if globalDBPath != "" {
		t.Errorf("globalDBPath should be empty, got %q", globalDBPath)
	}
	if globalVerbose {
		t.Error("globalVerbose should be false")
	}
	if len(args) != 2 {
		t.Errorf("filtered args = %v, want [search hello world]", args)
	}
}

func TestParseGlobalFlags_Empty(t *testing.T) {
	globalDBPath = ""
	globalVerbose = false

	args := parseGlobalFlags([]string{})
	if len(args) != 0 {
		t.Errorf("expected empty filtered args, got %v", args)
	}
}

// ==================== getDBPath ====================

func TestGetDBPath_FromFlag(t *testing.T) {
	globalDBPath = "/flag/path.db"
	t.Cleanup(func() { globalDBPath = "" })

	if got := getDBPath(); got != "/flag/path.db" {
		t.Errorf("getDBPath() = %q, want %q", got, "/flag/path.db")
	}
}

func TestGetDBPath_FromEnv(t *testing.T) {
	globalDBPath = ""
	t.Setenv("CORTEX_DB", "/env/path.db")

	if got := getDBPath(); got != "/env/path.db" {
		t.Errorf("getDBPath() = %q, want %q", got, "/env/path.db")
	}
}

func TestGetDBPath_Default(t *testing.T) {
	globalDBPath = ""
	os.Unsetenv("CORTEX_DB")

	if got := getDBPath(); got != "" {
		t.Errorf("getDBPath() = %q, want empty string (use store default)", got)
	}
}

func TestGetDBPath_ExpandsTildeFromEnv(t *testing.T) {
	globalDBPath = ""
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CORTEX_DB", "~/tmp/cortex.db")

	want := filepath.Join(home, "tmp", "cortex.db")
	if got := getDBPath(); got != want {
		t.Errorf("getDBPath() = %q, want %q", got, want)
	}
}

func TestGetDBPath_ExpandsTildeFromFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDBPath = "~/tmp/cortex-flag.db"
	t.Cleanup(func() { globalDBPath = "" })

	want := filepath.Join(home, "tmp", "cortex-flag.db")
	if got := getDBPath(); got != want {
		t.Errorf("getDBPath() = %q, want %q", got, want)
	}
}

// ==================== formatBytes ====================

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		got := formatBytes(c.in)
		if got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEstimateReasonRunCost_KnownModel(t *testing.T) {
	cost, known := estimateReasonRunCost("openrouter", "google/gemini-3-flash-preview", 1000, 500)
	if !known {
		t.Fatal("expected known cost")
	}
	want := 0.00045 // 1000*0.15/M + 500*0.60/M
	if math.Abs(cost-want) > 1e-10 {
		t.Fatalf("cost = %.8f, want %.8f", cost, want)
	}
}

func TestEstimateReasonRunCost_UnknownModel(t *testing.T) {
	cost, known := estimateReasonRunCost("openrouter", "unknown/model", 1000, 500)
	if known {
		t.Fatal("expected unknown cost")
	}
	if cost != 0 {
		t.Fatalf("cost = %.8f, want 0", cost)
	}
}

func TestShouldWriteReasonTelemetry_DefaultOn(t *testing.T) {
	t.Setenv("CORTEX_REASON_TELEMETRY", "")
	if !shouldWriteReasonTelemetry() {
		t.Fatal("expected telemetry enabled by default")
	}
}

func TestShouldWriteReasonTelemetry_Disabled(t *testing.T) {
	t.Setenv("CORTEX_REASON_TELEMETRY", "off")
	if shouldWriteReasonTelemetry() {
		t.Fatal("expected telemetry disabled")
	}
}

func TestWriteReasonTelemetry_WritesJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	entry := reasonRunTelemetry{
		Timestamp: "2026-02-19T20:00:00Z",
		Mode:      "one-shot",
		Query:     "test query",
		Provider:  "openrouter",
		Model:     "google/gemini-3-flash-preview",
	}
	if err := writeReasonTelemetry(entry); err != nil {
		t.Fatalf("writeReasonTelemetry failed: %v", err)
	}

	path := filepath.Join(home, ".cortex", "reason-telemetry.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading telemetry file failed: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"mode":"one-shot"`) || !strings.Contains(s, `"query":"test query"`) {
		t.Fatalf("unexpected telemetry content: %s", s)
	}
}

// ==================== outputStaleTTY ====================

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestOutputStaleTTY_NoFacts(t *testing.T) {
	opts := observe.StaleOpts{MaxConfidence: 0.5, MaxDays: 30, Limit: 50}
	out := captureStdout(func() {
		outputStaleTTY([]observe.StaleFact{}, opts, 0)
	})
	if !strings.Contains(out, "No stale facts") {
		t.Errorf("expected 'No stale facts' in output, got: %q", out)
	}
}

func TestOutputStaleTTY_NoFacts_WithTotal(t *testing.T) {
	opts := observe.StaleOpts{MaxConfidence: 0.5, MaxDays: 30, Limit: 50}
	out := captureStdout(func() {
		outputStaleTTY([]observe.StaleFact{}, opts, 100)
	})
	if !strings.Contains(out, "100") {
		t.Errorf("expected total count in output, got: %q", out)
	}
	if !strings.Contains(out, "30") {
		t.Errorf("expected days count in output, got: %q", out)
	}
}

func TestOutputStaleTTY_WithStaleFacts(t *testing.T) {
	opts := observe.StaleOpts{MaxConfidence: 0.5, MaxDays: 30, Limit: 50}

	sf := observe.StaleFact{
		EffectiveConfidence: 0.12,
		DaysSinceReinforced: 45,
	}
	sf.Fact.Predicate = "language"
	sf.Fact.Object = "Go"
	sf.Fact.FactType = "kv"
	sf.Fact.Confidence = 0.9

	out := captureStdout(func() {
		outputStaleTTY([]observe.StaleFact{sf}, opts, 100)
	})

	if !strings.Contains(out, "stale fact") {
		t.Errorf("expected 'stale fact' count in output, got: %q", out)
	}
}

// ==================== runStale flag parsing ====================

func TestRunStale_PositionalArgument(t *testing.T) {
	// runStale with positional "30" should not return "unexpected argument" error.
	// It will fail at the store open stage, but not at arg parsing.
	// We just check it doesn't return the old "unexpected argument" error.
	err := runStale([]string{"30"})
	if err != nil && strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("positional number arg should be accepted as --days, got: %v", err)
	}
}

func TestRunStale_UnknownFlag(t *testing.T) {
	err := runStale([]string{"--unknown"})
	if err == nil {
		t.Error("expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("expected 'unknown flag' error, got: %v", err)
	}
}

func TestRunStale_InvalidPositionalArg(t *testing.T) {
	err := runStale([]string{"notanumber"})
	if err == nil {
		t.Error("expected error for non-numeric positional arg")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("expected 'unexpected argument' error, got: %v", err)
	}
}

// ==================== version output ====================

func TestVersionOutput(t *testing.T) {
	out := captureStdout(func() {
		fmt.Printf("cortex %s\n", version)
	})
	if !strings.Contains(out, "cortex") {
		t.Errorf("version output missing 'cortex', got: %q", out)
	}
	if !strings.Contains(out, version) {
		t.Errorf("version output missing version string %q, got: %q", version, out)
	}
}

// ==================== import arg parsing ====================

func TestRunImport_NoArgs(t *testing.T) {
	err := runImport([]string{})
	if err == nil {
		t.Error("expected error for no arguments")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected usage message, got: %v", err)
	}
}

func TestRunImport_UnknownFlag(t *testing.T) {
	err := runImport([]string{"--unknown-flag", "/some/path"})
	if err == nil {
		t.Error("expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("expected 'unknown flag' error, got: %v", err)
	}
}

func TestRunImport_InvalidSimilarityThreshold(t *testing.T) {
	err := runImport([]string{"--capture-dedupe", "--similarity-threshold", "2.0", "/tmp/x.md"})
	if err == nil {
		t.Fatal("expected error for invalid similarity threshold")
	}
	if !strings.Contains(err.Error(), "--similarity-threshold") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunImport_InvalidDedupeWindow(t *testing.T) {
	err := runImport([]string{"--capture-dedupe", "--dedupe-window-sec", "0", "/tmp/x.md"})
	if err == nil {
		t.Fatal("expected error for invalid dedupe window")
	}
	if !strings.Contains(err.Error(), "--dedupe-window-sec") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunImport_InvalidCaptureMinChars(t *testing.T) {
	err := runImport([]string{"--capture-low-signal", "--capture-min-chars", "0", "/tmp/x.md"})
	if err == nil {
		t.Fatal("expected error for invalid capture min chars")
	}
	if !strings.Contains(err.Error(), "--capture-min-chars") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunUpdate_NoArgs(t *testing.T) {
	err := runUpdate([]string{})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: cortex update") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunUpdate_RequiresContentOrFile(t *testing.T) {
	err := runUpdate([]string{"123"})
	if err == nil {
		t.Fatal("expected missing content/file error")
	}
	if !strings.Contains(err.Error(), "must provide either --content or --file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunUpdate_ContentPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDB := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDB })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "old content", SourceFile: "note.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	s.Close()

	if err := runUpdate([]string{fmt.Sprintf("%d", memoryID), "--content", "new content"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}

	s2, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore reopen: %v", err)
	}
	defer s2.Close()
	updated, err := s2.GetMemory(ctx, memoryID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if updated == nil || updated.Content != "new content" {
		t.Fatalf("expected updated content, got %+v", updated)
	}
}

// ==================== optimize command ====================

func TestRunOptimize_UnknownFlag(t *testing.T) {
	err := runOptimize([]string{"--nope"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunOptimize_CheckOnlyJSON(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDB := globalDBPath
	oldReadOnly := globalReadOnly
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDB
		globalReadOnly = oldReadOnly
	})

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	_ = s.Close()

	out := captureStdout(func() {
		if err := runOptimize([]string{"--check-only", "--json"}); err != nil {
			t.Fatalf("runOptimize: %v", err)
		}
	})

	if !strings.Contains(out, `"integrity_check"`) {
		t.Fatalf("expected integrity_check in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"vacuum_ran": false`) {
		t.Fatalf("expected vacuum_ran=false in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"analyze_ran": false`) {
		t.Fatalf("expected analyze_ran=false in JSON output, got: %s", out)
	}
}

func TestRunOptimize_ReadOnlyBlocked(t *testing.T) {
	oldReadOnly := globalReadOnly
	globalReadOnly = true
	t.Cleanup(func() { globalReadOnly = oldReadOnly })

	err := runOptimize([]string{"--check-only"})
	if err == nil {
		t.Fatal("expected read-only mode error")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== search arg parsing ====================

func TestRunSearch_NoArgs(t *testing.T) {
	err := runSearch([]string{})
	if err == nil {
		t.Error("expected error for no arguments")
	}
}

func TestRunSearch_ExplainFlagAccepted(t *testing.T) {
	err := runSearch([]string{"--explain"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("--explain should be accepted, got: %v", err)
	}
}

func TestRunSearch_InvalidLimitRejected(t *testing.T) {
	err := runSearch([]string{"hello", "--limit", "-5"})
	if err == nil {
		t.Fatal("expected limit validation error")
	}
	if !strings.Contains(err.Error(), "--limit must be between 1 and 1000") {
		t.Fatalf("unexpected error: %v", err)
	}

	err = runSearch([]string{"hello", "--limit", "1001"})
	if err == nil {
		t.Fatal("expected limit validation error")
	}
	if !strings.Contains(err.Error(), "--limit must be between 1 and 1000") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSourceBoostArg_DefaultWeight(t *testing.T) {
	boost, err := parseSourceBoostArg("github:")
	if err != nil {
		t.Fatalf("parseSourceBoostArg: %v", err)
	}
	if boost.Prefix != "github:" {
		t.Fatalf("unexpected prefix: %q", boost.Prefix)
	}
	if boost.Weight != 1.15 {
		t.Fatalf("expected default weight 1.15, got %f", boost.Weight)
	}
}

func TestParseSourceBoostArg_CustomWeightAndClamp(t *testing.T) {
	boost, err := parseSourceBoostArg("file:1.5")
	if err != nil {
		t.Fatalf("parseSourceBoostArg: %v", err)
	}
	if boost.Weight != 1.5 {
		t.Fatalf("expected weight 1.5, got %f", boost.Weight)
	}
	if _, err := parseSourceBoostArg("github:2.5"); err == nil {
		t.Fatal("expected clamp error for >2.0")
	}
}

func TestParseBenchArgs_Compare(t *testing.T) {
	opts, err := parseBenchArgs([]string{"--compare", "google/gemini-2.5-flash,deepseek/deepseek-v3.2", "--recursive"})
	if err != nil {
		t.Fatalf("parseBenchArgs failed: %v", err)
	}
	if !opts.compareMode {
		t.Fatal("expected compareMode=true")
	}
	if len(opts.customModels) != 2 {
		t.Fatalf("expected 2 compare models, got %d", len(opts.customModels))
	}
	if !opts.recursive {
		t.Fatal("expected recursive=true")
	}
}

func TestParseBenchArgs_CompareAndModelsConflict(t *testing.T) {
	_, err := parseBenchArgs([]string{"--compare", "a,b", "--models", "c,d"})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "cannot be used") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseBenchArgs_InvalidCompareCount(t *testing.T) {
	_, err := parseBenchArgs([]string{"--compare", "onlyone"})
	if err == nil {
		t.Fatal("expected compare arity error")
	}
	if !strings.Contains(err.Error(), "exactly two") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSupersede_MissingBy(t *testing.T) {
	err := runSupersede([]string{"1"})
	if err == nil {
		t.Fatal("expected --by required error")
	}
	if !strings.Contains(err.Error(), "--by") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSupersede_InvalidOldID(t *testing.T) {
	err := runSupersede([]string{"abc", "--by", "2"})
	if err == nil {
		t.Fatal("expected invalid old id error")
	}
	if !strings.Contains(err.Error(), "invalid old fact id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== conflicts arg parsing ====================

func TestRunConflicts_UnknownFlag(t *testing.T) {
	err := runConflicts([]string{"--unknown"})
	if err == nil {
		t.Error("expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("expected 'unknown flag' error, got: %v", err)
	}
}

func TestRunConflicts_UnexpectedArg(t *testing.T) {
	err := runConflicts([]string{"unexpected"})
	if err == nil {
		t.Error("expected error for unexpected argument")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("expected 'unexpected argument' error, got: %v", err)
	}
}

func TestOutputConflictsTTY_CompactsByDefault(t *testing.T) {
	conflicts := make([]observe.Conflict, 0, 14)
	for i := 0; i < 14; i++ {
		c := observe.Conflict{ConflictType: "attribute", Similarity: 0.90}
		c.Fact1.ID = int64(i + 1)
		c.Fact1.Subject = "user"
		c.Fact1.Predicate = "email"
		c.Fact1.Object = fmt.Sprintf("old-%d@example.com", i)
		c.Fact1.Confidence = 0.80
		c.Fact2.ID = int64(100 + i)
		c.Fact2.Subject = "user"
		c.Fact2.Predicate = "email"
		c.Fact2.Object = fmt.Sprintf("new-%d@example.com", i)
		c.Fact2.Confidence = 0.92
		conflicts = append(conflicts, c)
	}

	out := captureStdout(func() {
		if err := outputConflictsTTY(conflicts, false); err != nil {
			t.Fatalf("outputConflictsTTY: %v", err)
		}
	})

	if !strings.Contains(out, "Top conflict groups") {
		t.Fatalf("expected grouped summary, got: %q", out)
	}
	if !strings.Contains(out, "Sample conflicts (showing 8 of 14)") {
		t.Fatalf("expected sample header, got: %q", out)
	}
	if !strings.Contains(out, "additional conflicts hidden") {
		t.Fatalf("expected hidden-details hint, got: %q", out)
	}
	if strings.Contains(out, "[9/14]") {
		t.Fatalf("expected compact output to hide items after preview limit, got: %q", out)
	}
}

func TestOutputConflictsTTY_VerboseShowsAll(t *testing.T) {
	conflicts := make([]observe.Conflict, 0, 11)
	for i := 0; i < 11; i++ {
		c := observe.Conflict{ConflictType: "attribute", Similarity: 0.88}
		c.Fact1.ID = int64(i + 1)
		c.Fact1.Subject = "system"
		c.Fact1.Predicate = "status"
		c.Fact1.Object = fmt.Sprintf("old-%d", i)
		c.Fact2.ID = int64(200 + i)
		c.Fact2.Subject = "system"
		c.Fact2.Predicate = "status"
		c.Fact2.Object = fmt.Sprintf("new-%d", i)
		conflicts = append(conflicts, c)
	}

	out := captureStdout(func() {
		if err := outputConflictsTTY(conflicts, true); err != nil {
			t.Fatalf("outputConflictsTTY: %v", err)
		}
	})

	if !strings.Contains(out, "Detailed conflicts") {
		t.Fatalf("expected detailed header, got: %q", out)
	}
	if !strings.Contains(out, "[11/11]") {
		t.Fatalf("expected full detail in verbose mode, got: %q", out)
	}
	if strings.Contains(out, "additional conflicts hidden") {
		t.Fatalf("did not expect hidden-details hint in verbose mode, got: %q", out)
	}
}

func TestOutputConflictsTTY_PrioritizesHigherSimilarity(t *testing.T) {
	conflicts := []observe.Conflict{
		{
			ConflictType: "attribute",
			Similarity:   0.42,
			Fact1:        store.Fact{ID: 1, Subject: "user", Predicate: "email", Object: "low@example.com"},
			Fact2:        store.Fact{ID: 101, Subject: "user", Predicate: "email", Object: "low2@example.com"},
		},
		{
			ConflictType: "attribute",
			Similarity:   0.97,
			Fact1:        store.Fact{ID: 2, Subject: "user", Predicate: "email", Object: "high@example.com"},
			Fact2:        store.Fact{ID: 102, Subject: "user", Predicate: "email", Object: "high2@example.com"},
		},
	}

	out := captureStdout(func() {
		if err := outputConflictsTTY(conflicts, false); err != nil {
			t.Fatalf("outputConflictsTTY: %v", err)
		}
	})

	if strings.Index(out, "Similarity: 0.97") > strings.Index(out, "Similarity: 0.42") {
		t.Fatalf("expected higher-similarity conflict to be shown first, got: %q", out)
	}
}

func TestOutputResolveBatchTTY_CompactsByDefault(t *testing.T) {
	batch := &observe.ResolveBatch{Total: 15, Resolved: 15, Results: make([]observe.Resolution, 0, 15)}
	for i := 0; i < 15; i++ {
		c := observe.Conflict{ConflictType: "attribute", Similarity: 0.93}
		c.Fact1.Subject = "user"
		c.Fact1.Predicate = "timezone"
		c.Fact2.Subject = "user"
		c.Fact2.Predicate = "timezone"
		batch.Results = append(batch.Results, observe.Resolution{
			Conflict: c,
			Winner:   "fact1",
			WinnerID: int64(10 + i),
			LoserID:  int64(100 + i),
			Reason:   "higher confidence",
			Applied:  true,
		})
	}

	out := captureStdout(func() {
		if err := outputResolveBatchTTY(batch, observe.StrategyHighestConfidence, false, false); err != nil {
			t.Fatalf("outputResolveBatchTTY: %v", err)
		}
	})

	if !strings.Contains(out, "Resolution sample (showing 12 of 15)") {
		t.Fatalf("expected sampled resolution header, got: %q", out)
	}
	if !strings.Contains(out, "additional resolution entries hidden") {
		t.Fatalf("expected hidden-details hint, got: %q", out)
	}
	if strings.Contains(out, "[13/15]") {
		t.Fatalf("expected compact output to hide items after preview limit, got: %q", out)
	}
}

func TestOutputResolveBatchTTY_VerboseShowsAll(t *testing.T) {
	batch := &observe.ResolveBatch{Total: 13, Resolved: 13, Results: make([]observe.Resolution, 0, 13)}
	for i := 0; i < 13; i++ {
		c := observe.Conflict{ConflictType: "attribute", Similarity: 0.91}
		c.Fact1.Subject = "company"
		c.Fact1.Predicate = "stage"
		c.Fact2.Subject = "company"
		c.Fact2.Predicate = "stage"
		batch.Results = append(batch.Results, observe.Resolution{
			Conflict: c,
			Winner:   "fact2",
			WinnerID: int64(200 + i),
			LoserID:  int64(20 + i),
			Reason:   "newest memory",
			Applied:  true,
		})
	}

	out := captureStdout(func() {
		if err := outputResolveBatchTTY(batch, observe.StrategyNewest, false, true); err != nil {
			t.Fatalf("outputResolveBatchTTY: %v", err)
		}
	})

	if !strings.Contains(out, "Resolution details") {
		t.Fatalf("expected detailed header, got: %q", out)
	}
	if !strings.Contains(out, "[13/13]") {
		t.Fatalf("expected full details in verbose mode, got: %q", out)
	}
	if strings.Contains(out, "additional resolution entries hidden") {
		t.Fatalf("did not expect hidden-details hint in verbose mode, got: %q", out)
	}
}

// ==================== embed watch parsing + locking ====================

type mockCommandEmbedder struct {
	dims int
}

func (m *mockCommandEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	batch, err := m.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return batch[0], nil
}

func (m *mockCommandEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, m.dims)
		vec[0] = float32(len(text))
		result[i] = vec
	}
	return result, nil
}

func (m *mockCommandEmbedder) Dimensions() int { return m.dims }

func (m *mockCommandEmbedder) HealthCheck(ctx context.Context) error { return nil }

func TestRunEmbedSource_Help(t *testing.T) {
	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runEmbedSource([]string{"--help"})
	})
	if runErr != nil {
		t.Fatalf("runEmbedSource --help: %v", runErr)
	}
	if !strings.Contains(out, "cortex embed-source") {
		t.Fatalf("expected embed-source help output, got: %q", out)
	}
	if !strings.Contains(out, "Embeds only active memories") {
		t.Fatalf("expected scoped description in help, got: %q", out)
	}
}

func TestParseEmbedSourceArgs(t *testing.T) {
	opts, err := parseEmbedSourceArgs([]string{"notes.md", "ollama/nomic-embed-text", "--batch-size", "7"})
	if err != nil {
		t.Fatalf("parseEmbedSourceArgs: %v", err)
	}
	if opts.sourceFile != "notes.md" {
		t.Fatalf("sourceFile = %q, want notes.md", opts.sourceFile)
	}
	if opts.embedFlag != "ollama/nomic-embed-text" {
		t.Fatalf("embedFlag = %q, want ollama/nomic-embed-text", opts.embedFlag)
	}
	if opts.batchSize != 7 {
		t.Fatalf("batchSize = %d, want 7", opts.batchSize)
	}
}

func TestParseEmbedSourceArgs_EnvFallback(t *testing.T) {
	t.Setenv("CORTEX_EMBED", "ollama/nomic-embed-text")
	opts, err := parseEmbedSourceArgs([]string{"notes.md"})
	if err != nil {
		t.Fatalf("parseEmbedSourceArgs env fallback: %v", err)
	}
	if opts.sourceFile != "notes.md" {
		t.Fatalf("sourceFile = %q, want notes.md", opts.sourceFile)
	}
	if opts.embedFlag != "" {
		t.Fatalf("embedFlag = %q, want empty when using env fallback", opts.embedFlag)
	}
}

func TestRunEmbedSource_EmbedsOnlyRequestedSource(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	sourceA := filepath.Join(tmpDir, "2026-02-21.md")
	sourceB := filepath.Join(tmpDir, "2026-02-22.md")

	if err := os.WriteFile(sourceA, []byte("source a"), 0600); err != nil {
		t.Fatalf("write sourceA: %v", err)
	}
	if err := os.WriteFile(sourceB, []byte("source b"), 0600); err != nil {
		t.Fatalf("write sourceB: %v", err)
	}

	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	oldFactory := newEmbedClient
	globalDBPath = ""
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
		newEmbedClient = oldFactory
	})
	t.Setenv("CORTEX_DB", dbPath)
	t.Setenv("CORTEX_EMBED", "ollama/nomic-embed-text")
	newEmbedClient = func(cfg *embed.EmbedConfig) (embed.Embedder, error) {
		return &mockCommandEmbedder{dims: 8}, nil
	}

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memA1, err := s.AddMemory(ctx, &store.Memory{Content: "alpha memory one", SourceFile: sourceA})
	if err != nil {
		t.Fatalf("AddMemory memA1: %v", err)
	}
	memA2, err := s.AddMemory(ctx, &store.Memory{Content: "alpha memory two", SourceFile: sourceA})
	if err != nil {
		t.Fatalf("AddMemory memA2: %v", err)
	}
	memB, err := s.AddMemory(ctx, &store.Memory{Content: "beta memory one", SourceFile: sourceB})
	if err != nil {
		t.Fatalf("AddMemory memB: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close seed store: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runEmbedSource([]string{sourceA})
	})
	if runErr != nil {
		t.Fatalf("runEmbedSource: %v\nout=%s", runErr, out)
	}
	if !strings.Contains(out, "Source: "+sourceA) {
		t.Fatalf("expected source header, got: %q", out)
	}
	if !strings.Contains(out, "Missing embeddings : 2") {
		t.Fatalf("expected missing embedding count for sourceA, got: %q", out)
	}

	s, err = store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("Reopen store: %v", err)
	}
	defer s.Close()

	for _, id := range []int64{memA1, memA2} {
		vec, err := s.GetEmbedding(ctx, id)
		if err != nil || len(vec) == 0 {
			t.Fatalf("expected embedding for source A memory %d", id)
		}
	}
	if vec, err := s.GetEmbedding(ctx, memB); err == nil && len(vec) > 0 {
		t.Fatalf("did not expect embedding for source B memory %d", memB)
	}
}

func TestParseEmbedArgs_WatchMode(t *testing.T) {
	opts, err := parseEmbedArgs([]string{"ollama/nomic-embed-text", "--watch", "--interval", "45m", "--batch-size", "12"})
	if err != nil {
		t.Fatalf("parseEmbedArgs: %v", err)
	}
	if !opts.watch {
		t.Error("expected watch=true")
	}
	if opts.interval != 45*time.Minute {
		t.Errorf("interval = %s, want 45m", opts.interval)
	}
	if opts.batchSize != 12 {
		t.Errorf("batchSize = %d, want 12", opts.batchSize)
	}
}

func TestParseEmbedArgs_RejectWatchForce(t *testing.T) {
	_, err := parseEmbedArgs([]string{"ollama/nomic-embed-text", "--watch", "--force"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot be used with --force") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseEmbedArgs_EnvFallback(t *testing.T) {
	t.Setenv("CORTEX_EMBED", "ollama/nomic-embed-text")
	_, err := parseEmbedArgs([]string{"--watch"})
	if err != nil {
		t.Fatalf("expected env fallback to allow missing positional provider: %v", err)
	}
}

func TestParseEmbedArgs_StatusDoesNotRequireProvider(t *testing.T) {
	opts, err := parseEmbedArgs([]string{"--status"})
	if err != nil {
		t.Fatalf("parseEmbedArgs --status: %v", err)
	}
	if !opts.status {
		t.Fatal("expected status=true")
	}
	if opts.embedFlag != "" {
		t.Fatalf("expected empty embedFlag for status, got %q", opts.embedFlag)
	}
}

func TestParseEmbedArgs_BatchAliasAndWorkers(t *testing.T) {
	opts, err := parseEmbedArgs([]string{"ollama/nomic-embed-text", "--batch", "60", "--workers", "3"})
	if err != nil {
		t.Fatalf("parseEmbedArgs: %v", err)
	}
	if opts.batchSize != 60 {
		t.Fatalf("batchSize = %d, want 60", opts.batchSize)
	}
	if opts.workers != 3 {
		t.Fatalf("workers = %d, want 3", opts.workers)
	}
}

func TestRunEmbed_StatusReportsCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
	})

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	mem1, err := s.AddMemory(ctx, &store.Memory{Content: "alpha", SourceFile: "a.md"})
	if err != nil {
		t.Fatalf("AddMemory mem1: %v", err)
	}
	if _, err := s.AddMemory(ctx, &store.Memory{Content: "beta", SourceFile: "b.md"}); err != nil {
		t.Fatalf("AddMemory mem2: %v", err)
	}
	if err := s.AddEmbedding(ctx, mem1, []float32{1, 0, 0}); err != nil {
		t.Fatalf("AddEmbedding: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runEmbed([]string{"--status"})
	})
	if runErr != nil {
		t.Fatalf("runEmbed --status: %v", runErr)
	}
	if !strings.Contains(out, "memories=2") {
		t.Fatalf("expected memory count in status output, got %q", out)
	}
	if !strings.Contains(out, "embeddings=1") {
		t.Fatalf("expected embedding count in status output, got %q", out)
	}
	if !strings.Contains(out, "remaining=1") {
		t.Fatalf("expected remaining count in status output, got %q", out)
	}
}

func TestMaybeStartBackgroundEmbedWorker_StartsWatchProcess(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	oldFactory := newEmbedClient
	oldSpawner := spawnDetachedBackgroundEmbed
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
		newEmbedClient = oldFactory
		spawnDetachedBackgroundEmbed = oldSpawner
	})

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	newEmbedClient = func(cfg *embed.EmbedConfig) (embed.Embedder, error) {
		return &mockCommandEmbedder{dims: 8}, nil
	}

	var spawnedArgs []string
	spawnDetachedBackgroundEmbed = func(args []string) error {
		spawnedArgs = append([]string(nil), args...)
		return nil
	}

	msg := maybeStartBackgroundEmbedWorker("ollama/nomic-embed-text")
	if !strings.Contains(msg, "started") {
		t.Fatalf("expected worker start message, got %q", msg)
	}
	if len(spawnedArgs) == 0 {
		t.Fatal("expected background worker to be spawned")
	}
	joined := strings.Join(spawnedArgs, " ")
	for _, want := range []string{"embed", "ollama/nomic-embed-text", "--watch", "--interval", "--batch-size"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("spawned args missing %q: %v", want, spawnedArgs)
		}
	}
}

func TestComputeEmbedWatchBackoff_CapsAtInterval(t *testing.T) {
	interval := 30 * time.Second
	delay := computeEmbedWatchBackoff(interval, 10)
	if delay != interval {
		t.Fatalf("delay = %s, want %s", delay, interval)
	}
}

func TestAcquireEmbedRunLock_PreventsOverlap(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "embed.lock")

	lock, err := acquireEmbedRunLock(lockPath)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Release()

	_, err = acquireEmbedRunLock(lockPath)
	if !errors.Is(err, errEmbedLockHeld) {
		t.Fatalf("expected errEmbedLockHeld, got: %v", err)
	}
}

func TestAcquireEmbedRunLock_ReclaimsMalformedPIDLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "embed.lock")
	if err := os.WriteFile(lockPath, []byte("pid=not-a-number\nstarted_at=2026-01-01T00:00:00Z\n"), 0600); err != nil {
		t.Fatalf("write malformed lock: %v", err)
	}

	lock, err := acquireEmbedRunLock(lockPath)
	if err != nil {
		t.Fatalf("expected malformed lock to be reclaimed, got: %v", err)
	}
	defer lock.Release()
}

func TestAcquireEmbedRunLock_ReclaimsZeroPIDLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "embed.lock")
	if err := os.WriteFile(lockPath, []byte("pid=0\nstarted_at=2026-01-01T00:00:00Z\n"), 0600); err != nil {
		t.Fatalf("write zero-pid lock: %v", err)
	}

	lock, err := acquireEmbedRunLock(lockPath)
	if err != nil {
		t.Fatalf("expected zero-pid lock to be reclaimed, got: %v", err)
	}
	defer lock.Release()
}

func TestRunStats_GrowthReportJSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DB", dbPath)
	globalDBPath = ""
	globalReadOnly = false

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	ctx := context.Background()
	memoryID, err := s.AddMemory(ctx, &store.Memory{Content: "growth test", SourceFile: "notes.md"})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	_, err = s.AddFact(ctx, &store.Fact{MemoryID: memoryID, Subject: "user", Predicate: "city", Object: "Philly", FactType: "kv", Confidence: 0.9, DecayRate: 0.01})
	if err != nil {
		t.Fatalf("add fact: %v", err)
	}
	s.Close()

	var runErr error
	out := captureStdout(func() {
		runErr = runStats([]string{"--growth-report", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runStats growth-report failed: %v", runErr)
	}
	if !strings.Contains(out, `"windows"`) || !strings.Contains(out, `"recommendation"`) {
		t.Fatalf("unexpected growth report JSON output: %s", out)
	}
}

func TestRunStats_UnknownFlag(t *testing.T) {
	err := runStats([]string{"--not-a-real-flag"})
	if err == nil {
		t.Fatal("expected error for unknown stats flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunStats_InvalidTopSourceFiles(t *testing.T) {
	err := runStats([]string{"--growth-report", "--top-source-files", "0"})
	if err == nil {
		t.Fatal("expected error for invalid --top-source-files")
	}
	if !strings.Contains(err.Error(), "invalid --top-source-files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunStats_GrowthReportTopSourceFilesLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DB", dbPath)
	globalDBPath = ""
	globalReadOnly = false

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err = s.AddMemory(ctx, &store.Memory{Content: "growth limit test", SourceFile: fmt.Sprintf("src-%d.md", i)})
		if err != nil {
			t.Fatalf("add memory: %v", err)
		}
	}
	s.Close()

	var runErr error
	out := captureStdout(func() {
		runErr = runStats([]string{"--growth-report", "--top-source-files", "2", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runStats growth-report failed: %v", runErr)
	}

	var payload struct {
		Windows []struct {
			Window           string `json:"window"`
			TopMemorySources []any  `json:"top_memory_sources"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode growth report JSON: %v\nraw: %s", err, out)
	}
	if len(payload.Windows) == 0 {
		t.Fatal("expected growth report windows")
	}
	if len(payload.Windows[0].TopMemorySources) > 2 {
		t.Fatalf("expected top_memory_sources <= 2, got %d", len(payload.Windows[0].TopMemorySources))
	}
}

// ==================== extract arg parsing ====================

func TestRunExtract_NoArgsUsage(t *testing.T) {
	err := runExtract([]string{})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: cortex extract") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExtract_UnknownFlag(t *testing.T) {
	err := runExtract([]string{"--nope"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --nope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExtract_TooManyPaths(t *testing.T) {
	err := runExtract([]string{"a.md", "b.md"})
	if err == nil {
		t.Fatal("expected multiple path error")
	}
	if !strings.Contains(err.Error(), "only one file path allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExtract_MissingFilePath(t *testing.T) {
	err := runExtract([]string{"--json"})
	if err == nil {
		t.Fatal("expected missing file path error")
	}
	if !strings.Contains(err.Error(), "no file path specified") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== classify arg parsing ====================

func TestRunClassify_InvalidBatchSize(t *testing.T) {
	err := runClassify([]string{"--batch-size", "abc"})
	if err == nil {
		t.Fatal("expected invalid batch-size error")
	}
	if !strings.Contains(err.Error(), "invalid --batch-size: abc") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunClassify_InvalidConcurrency(t *testing.T) {
	err := runClassify([]string{"--concurrency", "abc"})
	if err == nil {
		t.Fatal("expected invalid concurrency error")
	}
	if !strings.Contains(err.Error(), "invalid --concurrency: abc") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunClassify_InvalidLimit(t *testing.T) {
	err := runClassify([]string{"--limit", "abc"})
	if err == nil {
		t.Fatal("expected invalid limit error")
	}
	if !strings.Contains(err.Error(), "invalid --limit: abc") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunClassify_UnknownFlag(t *testing.T) {
	err := runClassify([]string{"--nope"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --nope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== summarize arg parsing ====================

func TestRunSummarize_MissingLLMUsage(t *testing.T) {
	err := runSummarize([]string{})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: cortex summarize --llm") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSummarize_InvalidCluster(t *testing.T) {
	err := runSummarize([]string{"--llm", "openrouter/x", "--cluster", "abc"})
	if err == nil {
		t.Fatal("expected invalid cluster error")
	}
	if !strings.Contains(err.Error(), "invalid --cluster: abc") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSummarize_InvalidMinClusterSize(t *testing.T) {
	err := runSummarize([]string{"--llm", "openrouter/x", "--min-cluster-size", "abc"})
	if err == nil {
		t.Fatal("expected invalid min-cluster-size error")
	}
	if !strings.Contains(err.Error(), "invalid --min-cluster-size: abc") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== list arg parsing ====================

func TestRunList_InvalidLimit(t *testing.T) {
	err := runList([]string{"--limit", "abc"})
	if err == nil {
		t.Fatal("expected invalid --limit error")
	}
	if !strings.Contains(err.Error(), "invalid --limit value: abc") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunList_UnknownFlag(t *testing.T) {
	err := runList([]string{"--nope"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --nope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunList_UnexpectedArgument(t *testing.T) {
	err := runList([]string{"extra"})
	if err == nil {
		t.Fatal("expected unexpected argument error")
	}
	if !strings.Contains(err.Error(), "unexpected argument: extra") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== export arg parsing ====================

func TestRunExport_InvalidFormat(t *testing.T) {
	err := runExport([]string{"--format", "xml"})
	if err == nil {
		t.Fatal("expected unsupported format error")
	}
	if !strings.Contains(err.Error(), "unsupported format: xml") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExport_UnknownFlag(t *testing.T) {
	err := runExport([]string{"--nope"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --nope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExport_UnexpectedArgument(t *testing.T) {
	err := runExport([]string{"extra"})
	if err == nil {
		t.Fatal("expected unexpected argument error")
	}
	if !strings.Contains(err.Error(), "unexpected argument: extra") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== remediation hints ====================

func TestRemediationHint_UsageAndFlags(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want string
	}{
		{
			name: "usage",
			err:  "usage: cortex list [--json]",
			want: "Run `cortex help`",
		},
		{
			name: "unknown flag",
			err:  "unknown flag: --bad",
			want: "Run `cortex help`",
		},
		{
			name: "unknown argument",
			err:  "unknown argument: --bad",
			want: "Run `cortex help`",
		},
		{
			name: "unexpected argument",
			err:  "unexpected argument: nope",
			want: "Run `cortex help`",
		},
		{
			name: "unknown connect subcommand",
			err:  "unknown connect subcommand: nope",
			want: "Run `cortex help`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remediationHint(errors.New(tt.err))
			if !strings.Contains(got, tt.want) {
				t.Fatalf("remediationHint(%q) = %q, want substring %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestRemediationHint_APIKeys(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want string
	}{
		{
			name: "openrouter",
			err:  "openrouter provider requires OPENROUTER_API_KEY env var",
			want: "OPENROUTER_API_KEY",
		},
		{
			name: "google",
			err:  "google provider requires GEMINI_API_KEY or GOOGLE_API_KEY env var",
			want: "GOOGLE_API_KEY",
		},
		{
			name: "openai",
			err:  "openai provider requires OPENAI_API_KEY env var",
			want: "OPENAI_API_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remediationHint(errors.New(tt.err))
			if !strings.Contains(got, tt.want) {
				t.Fatalf("remediationHint(%q) = %q, want substring %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestRemediationHint_DatabaseCases(t *testing.T) {
	t.Run("locked", func(t *testing.T) {
		got := remediationHint(errors.New("database is locked"))
		if !strings.Contains(got, "Another process is using this DB") {
			t.Fatalf("unexpected hint: %q", got)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		got := remediationHint(errors.New("file is not a database"))
		if !strings.Contains(got, "Database appears corrupted or stale") {
			t.Fatalf("unexpected hint: %q", got)
		}
		if !strings.Contains(got, "cortex reimport") {
			t.Fatalf("expected reimport recommendation, got: %q", got)
		}
	})

	t.Run("open-store-with-path", func(t *testing.T) {
		oldDBPath := globalDBPath
		globalDBPath = "/tmp/test.db"
		t.Cleanup(func() { globalDBPath = oldDBPath })

		got := remediationHint(errors.New("opening store: unable to open database file"))
		if !strings.Contains(got, "Verify the DB path is valid and writable") {
			t.Fatalf("unexpected hint: %q", got)
		}
		if !strings.Contains(got, "/tmp/test.db") {
			t.Fatalf("expected db path in hint, got: %q", got)
		}
	})

	t.Run("open-store-no-path", func(t *testing.T) {
		oldDBPath := globalDBPath
		globalDBPath = ""
		t.Cleanup(func() { globalDBPath = oldDBPath })

		got := remediationHint(errors.New("opening store: permission denied"))
		if !strings.Contains(got, "Set --db <path>") && !strings.Contains(got, "Check file permissions") {
			t.Fatalf("unexpected hint: %q", got)
		}
	})
}

func TestRemediationHint_UnknownErrorReturnsEmpty(t *testing.T) {
	got := remediationHint(errors.New("some unrelated failure"))
	if got != "" {
		t.Fatalf("expected empty hint, got %q", got)
	}
}

func TestRemediationHint_NoSuchFileOrDirectory(t *testing.T) {
	got := remediationHint(errors.New("opening store: no such file or directory"))
	if !strings.Contains(got, "does not exist") {
		t.Fatalf("expected 'does not exist' hint, got: %q", got)
	}
	if !strings.Contains(got, "cortex import") {
		t.Fatalf("expected import suggestion, got: %q", got)
	}
}

func TestRemediationHint_ReadOnly(t *testing.T) {
	got := remediationHint(errors.New("database is read-only"))
	if !strings.Contains(got, "read-only") {
		t.Fatalf("expected read-only hint, got: %q", got)
	}
}

func TestRemediationHint_Nil(t *testing.T) {
	got := remediationHint(nil)
	if got != "" {
		t.Fatalf("expected empty hint for nil error, got: %q", got)
	}
}

// ==================== main exit codes ====================

func TestMain_ExitCodeUnknownCommand(t *testing.T) {
	exitCode, out := runMainSubprocess(t, "not-a-command")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "Unknown command: not-a-command") {
		t.Fatalf("expected unknown command output, got: %q", out)
	}
	if !strings.Contains(out, "cortex help") {
		t.Fatalf("expected help remediation hint, got: %q", out)
	}
}

func TestMain_ExitCodeNoCommand(t *testing.T) {
	exitCode, out := runMainSubprocess(t)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("expected usage output, got: %q", out)
	}
}

func TestMain_UnknownFlagIncludesHint(t *testing.T) {
	exitCode, out := runMainSubprocess(t, "list", "--nope")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "unknown flag") {
		t.Fatalf("expected unknown flag output, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Run `cortex help`") {
		t.Fatalf("expected usage remediation hint, got: %q", out)
	}
}

func TestMain_DBOpenFailureIncludesHint(t *testing.T) {
	tmpDir := t.TempDir()
	blockingPath := filepath.Join(tmpDir, "not-a-dir")
	if err := os.WriteFile(blockingPath, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	badDBPath := filepath.Join(blockingPath, "cortex.db")

	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"CORTEX_DB": badDBPath,
	}, "list")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "opening store") {
		t.Fatalf("expected store-open error, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Verify the DB path is valid and writable") {
		t.Fatalf("expected DB path hint, got: %q", out)
	}
	if !strings.Contains(out, badDBPath) {
		t.Fatalf("expected hinted DB path %q, got: %q", badDBPath, out)
	}
}

func TestMain_CorruptDBIncludesRecoveryHint(t *testing.T) {
	tmpDir := t.TempDir()
	corruptDBPath := filepath.Join(tmpDir, "corrupt.db")
	if err := os.WriteFile(corruptDBPath, []byte("this is not sqlite"), 0600); err != nil {
		t.Fatalf("write corrupt db file: %v", err)
	}

	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"CORTEX_DB": corruptDBPath,
	}, "list")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(strings.ToLower(out), "not a database") {
		t.Fatalf("expected corruption message, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Database appears corrupted or stale") {
		t.Fatalf("expected corruption remediation hint, got: %q", out)
	}
	if !strings.Contains(out, "cortex reimport") {
		t.Fatalf("expected reimport remediation, got: %q", out)
	}
}

func TestMain_OpenRouterMissingKeyIncludesHint(t *testing.T) {
	homeDir := t.TempDir()
	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"OPENROUTER_API_KEY": "",
		"HOME":               homeDir,
	}, "classify", "--llm", "openrouter/deepseek/deepseek-v3.2", "--limit", "1")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "OPENROUTER_API_KEY") {
		t.Fatalf("expected missing-key message, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Set OPENROUTER_API_KEY") {
		t.Fatalf("expected API-key remediation hint, got: %q", out)
	}
}

func TestMain_SearchDBOpenFailureIncludesHint(t *testing.T) {
	tmpDir := t.TempDir()
	blockingPath := filepath.Join(tmpDir, "db-blocker")
	if err := os.WriteFile(blockingPath, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	badDBPath := filepath.Join(blockingPath, "cortex.db")

	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"CORTEX_DB": badDBPath,
	}, "search", "memory")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "opening store") {
		t.Fatalf("expected store-open error, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Verify the DB path is valid and writable") {
		t.Fatalf("expected DB path remediation hint, got: %q", out)
	}
}

func TestMain_ReasonDBOpenFailureIncludesHint(t *testing.T) {
	tmpDir := t.TempDir()
	blockingPath := filepath.Join(tmpDir, "db-blocker")
	if err := os.WriteFile(blockingPath, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	badDBPath := filepath.Join(blockingPath, "cortex.db")

	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"CORTEX_DB":          badDBPath,
		"OPENROUTER_API_KEY": "",
	}, "reason", "what changed")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "opening store") {
		t.Fatalf("expected store-open error, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Verify the DB path is valid and writable") {
		t.Fatalf("expected DB path remediation hint, got: %q", out)
	}
}

func TestMain_ConnectDBOpenFailureIncludesHint(t *testing.T) {
	tmpDir := t.TempDir()
	blockingPath := filepath.Join(tmpDir, "db-blocker")
	if err := os.WriteFile(blockingPath, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	badDBPath := filepath.Join(blockingPath, "cortex.db")

	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"CORTEX_DB": badDBPath,
	}, "connect", "status")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "opening store") {
		t.Fatalf("expected store-open error, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Verify the DB path is valid and writable") {
		t.Fatalf("expected DB path remediation hint, got: %q", out)
	}
}

func TestMain_MCPDBOpenFailureIncludesHint(t *testing.T) {
	tmpDir := t.TempDir()
	blockingPath := filepath.Join(tmpDir, "db-blocker")
	if err := os.WriteFile(blockingPath, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	badDBPath := filepath.Join(blockingPath, "cortex.db")

	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"CORTEX_DB": badDBPath,
	}, "mcp")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "opening store") {
		t.Fatalf("expected store-open error, got: %q", out)
	}
	if !strings.Contains(out, "Hint: Verify the DB path is valid and writable") {
		t.Fatalf("expected DB path remediation hint, got: %q", out)
	}
}

func TestMainProcessHelper(t *testing.T) {
	if os.Getenv("CORTEX_TEST_MAIN_HELPER") != "1" {
		return
	}

	args := []string{"cortex"}
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--" {
			args = append(args, os.Args[i+1:]...)
			break
		}
	}
	os.Args = args
	main()
}

func runMainSubprocess(t *testing.T, args ...string) (int, string) {
	t.Helper()
	return runMainSubprocessWithEnv(t, nil, args...)
}

func runMainSubprocessWithEnv(t *testing.T, env map[string]string, args ...string) (int, string) {
	t.Helper()

	cmdArgs := []string{"-test.run=^TestMainProcessHelper$", "--"}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = mergeEnv(os.Environ(), env)
	cmd.Env = append(cmd.Env, "CORTEX_TEST_MAIN_HELPER=1")

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err == nil {
		return 0, out.String()
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), out.String()
	}

	t.Fatalf("running subprocess main helper: %v", err)
	return -1, out.String()
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return append([]string{}, base...)
	}

	skip := make(map[string]struct{}, len(overrides))
	for k := range overrides {
		skip[k] = struct{}{}
	}

	merged := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if _, shouldSkip := skip[key]; shouldSkip {
			continue
		}
		merged = append(merged, kv)
	}
	for k, v := range overrides {
		merged = append(merged, fmt.Sprintf("%s=%s", k, v))
	}
	return merged
}

// ==================== doctor command ====================

func TestRunDoctor_UnexpectedArgument(t *testing.T) {
	err := runDoctor([]string{"extra"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: cortex doctor") && !strings.Contains(err.Error(), "Usage: cortex doctor") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunDoctor_JSONMissingDBFails(t *testing.T) {
	tmpDir := t.TempDir()
	missingDB := filepath.Join(tmpDir, "missing.db")

	oldDBPath := globalDBPath
	globalDBPath = missingDB
	t.Cleanup(func() { globalDBPath = oldDBPath })

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runDoctor([]string{"--json"})
	})
	if runErr == nil {
		t.Fatal("expected doctor failure when DB is missing")
	}

	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode doctor report: %v\nout=%q", err, out)
	}
	if report.Summary.Fail == 0 {
		t.Fatalf("expected failing checks, got summary=%+v", report.Summary)
	}
	check, ok := findDoctorCheck(report, "database_path")
	if !ok {
		t.Fatalf("expected database_path check in report: %+v", report)
	}
	if check.Status != "fail" {
		t.Fatalf("expected database_path status=fail, got %q", check.Status)
	}
}

func TestRunDoctor_JSONHealthyDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	hnswPath := filepath.Join(tmpDir, "hnsw.idx")
	if err := os.WriteFile(hnswPath, []byte("idx"), 0600); err != nil {
		t.Fatalf("write hnsw index: %v", err)
	}

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })
	t.Setenv("OPENROUTER_API_KEY", "test-key")

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:       "doctor command seed memory",
		SourceFile:    "doctor.md",
		SourceLine:    1,
		SourceSection: "doctor",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "cortex",
		Predicate:  "health",
		Object:     "ok",
		Confidence: 0.9,
		FactType:   "state",
	}); err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	if err := s.AddEmbedding(ctx, memID, []float32{1, 0, 0}); err != nil {
		t.Fatalf("AddEmbedding: %v", err)
	}
	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		t.Fatal("expected SQLiteStore")
	}
	cs := connect.NewConnectorStore(sqlStore.GetDB())
	if _, err := cs.Add(ctx, "github", json.RawMessage(`{"token":"x"}`)); err != nil {
		t.Fatalf("add connector: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runDoctor([]string{"--json"})
	})
	if runErr != nil {
		t.Fatalf("runDoctor --json failed: %v\nout=%s", runErr, out)
	}

	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode doctor report: %v\nout=%q", err, out)
	}
	if report.Summary.Fail != 0 {
		t.Fatalf("expected zero failing checks, got %+v", report.Summary)
	}

	embeddings, ok := findDoctorCheck(report, "embeddings")
	if !ok || embeddings.Status != "pass" {
		t.Fatalf("expected embeddings check to pass, got %+v", embeddings)
	}
	connectorsCheck, ok := findDoctorCheck(report, "connectors")
	if !ok || connectorsCheck.Status != "pass" {
		t.Fatalf("expected connectors check to pass, got %+v", connectorsCheck)
	}
	llm, ok := findDoctorCheck(report, "llm_keys")
	if !ok || llm.Status != "pass" {
		t.Fatalf("expected llm_keys check to pass, got %+v", llm)
	}
}

func TestRunDoctor_ResolvedConfigProviderOnlyPasses(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	home := filepath.Join(tmpDir, "home")
	cfgDir := filepath.Join(home, ".cortex")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfg := []byte("llm:\n  provider: openrouter\n")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), cfg, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("CORTEX_LLM", "")

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runDoctor([]string{"--json"})
	})
	if runErr != nil {
		t.Fatalf("runDoctor --json failed: %v\nout=%s", runErr, out)
	}

	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode doctor report: %v\nout=%q", err, out)
	}

	check, ok := findDoctorCheck(report, "resolved_config")
	if !ok {
		t.Fatalf("expected resolved_config check in report: %+v", report)
	}
	if check.Status != "pass" {
		t.Fatalf("expected resolved_config status=pass for provider-only config, got %+v", check)
	}
	if !strings.Contains(strings.ToLower(check.Details), "openrouter") {
		t.Fatalf("expected resolved_config details to include provider, got %+v", check)
	}
}

func TestRunDoctor_QuietSuppressesPassingChecks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	out := captureStdout(func() {
		if err := runDoctor([]string{"--quiet"}); err != nil {
			t.Fatalf("runDoctor --quiet: %v", err)
		}
	})

	if strings.Contains(out, "database_path") {
		t.Fatalf("expected --quiet to hide passing checks, got output: %s", out)
	}
	if !strings.Contains(out, "Summary:") {
		t.Fatalf("expected summary line in output, got: %s", out)
	}
}

// ==================== beliefs command ====================

func TestRunBeliefs_JSONStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{Content: "beliefs stats seed", SourceFile: "beliefs.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	fActive, _ := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "A", Predicate: "is", Object: "active", FactType: "state", Confidence: 0.9})
	fCore, _ := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "B", Predicate: "is", Object: "core", FactType: "state", Confidence: 0.9})
	if err := s.UpdateFactState(ctx, fCore, store.FactStateCore); err != nil {
		t.Fatalf("UpdateFactState core: %v", err)
	}
	fRet, _ := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "C", Predicate: "is", Object: "retired", FactType: "state", Confidence: 0.9})
	if err := s.UpdateFactState(ctx, fRet, store.FactStateRetired); err != nil {
		t.Fatalf("UpdateFactState retired: %v", err)
	}
	fOld, _ := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "D", Predicate: "is", Object: "old", FactType: "state", Confidence: 0.7})
	fNew, _ := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "D", Predicate: "is", Object: "new", FactType: "state", Confidence: 0.95})
	if err := s.SupersedeFact(ctx, fOld, fNew, "test"); err != nil {
		t.Fatalf("SupersedeFact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	_ = fActive

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runBeliefs([]string{"--json"})
	})
	if runErr != nil {
		t.Fatalf("runBeliefs --json: %v\nout=%s", runErr, out)
	}

	var report beliefsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode beliefs report: %v\nout=%q", err, out)
	}
	if report.States[store.FactStateActive] != 2 {
		t.Fatalf("active count = %d, want 2", report.States[store.FactStateActive])
	}
	if report.States[store.FactStateCore] != 1 {
		t.Fatalf("core count = %d, want 1", report.States[store.FactStateCore])
	}
	if report.States[store.FactStateRetired] != 1 {
		t.Fatalf("retired count = %d, want 1", report.States[store.FactStateRetired])
	}
	if report.States[store.FactStateSuperseded] != 1 {
		t.Fatalf("superseded count = %d, want 1", report.States[store.FactStateSuperseded])
	}
	if report.Total != 5 {
		t.Fatalf("total = %d, want 5", report.Total)
	}
}

func TestRunBeliefs_StateOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{Content: "beliefs override seed", SourceFile: "beliefs.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	factID, err := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "Q", Predicate: "focus", Object: "cortex", FactType: "state", Confidence: 0.9})
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if err := runBeliefs([]string{"promote", fmt.Sprintf("%d", factID)}); err != nil {
		t.Fatalf("beliefs promote: %v", err)
	}

	s2, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore reopen: %v", err)
	}
	f, err := s2.GetFact(ctx, factID)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	if f.State != store.FactStateCore {
		t.Fatalf("state after promote = %q, want %q", f.State, store.FactStateCore)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("close s2: %v", err)
	}

	if err := runBeliefs([]string{"archive", fmt.Sprintf("%d", factID)}); err != nil {
		t.Fatalf("beliefs archive: %v", err)
	}

	s3, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore reopen #2: %v", err)
	}
	f, err = s3.GetFact(ctx, factID)
	if err != nil {
		t.Fatalf("GetFact #2: %v", err)
	}
	if f.State != store.FactStateRetired {
		t.Fatalf("state after archive = %q, want %q", f.State, store.FactStateRetired)
	}
	if err := s3.Close(); err != nil {
		t.Fatalf("close s3: %v", err)
	}
}

func TestRunBeliefs_InspectJSON(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("expected SQLiteStore")
	}
	ctx := context.Background()
	memID1, err := s.AddMemory(ctx, &store.Memory{Content: "belief inspect one", SourceFile: "beliefs.md"})
	if err != nil {
		t.Fatalf("AddMemory #1: %v", err)
	}
	memID2, err := s.AddMemory(ctx, &store.Memory{Content: "belief inspect two", SourceFile: "beliefs.md"})
	if err != nil {
		t.Fatalf("AddMemory #2: %v", err)
	}
	factID, err := s.AddFact(ctx, &store.Fact{MemoryID: memID1, Subject: "Q", Predicate: "focus", Object: "cortex", FactType: "state", Confidence: 0.9})
	if err != nil {
		t.Fatalf("AddFact #1: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{MemoryID: memID2, Subject: "Q", Predicate: "focus", Object: "cortex", FactType: "state", Confidence: 0.8}); err != nil {
		t.Fatalf("AddFact #2: %v", err)
	}
	if err := sqlStore.RecordFactAccess(ctx, factID, "mister", store.AccessTypeReinforce); err != nil {
		t.Fatalf("RecordFactAccess reinforce: %v", err)
	}
	if err := sqlStore.RecordFactAccess(ctx, factID, "hawk", store.AccessTypeReference); err != nil {
		t.Fatalf("RecordFactAccess reference: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runBeliefs([]string{"inspect", "--json", "--limit", "5"})
	})
	if runErr != nil {
		t.Fatalf("runBeliefs inspect --json: %v\nout=%s", runErr, out)
	}

	var report beliefsInspectReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode beliefs inspect report: %v\nout=%q", err, out)
	}
	if len(report.Facts) == 0 {
		t.Fatalf("expected at least one belief inspect row")
	}
	if report.Facts[0].ConvictionScore < 0 || report.Facts[0].ConvictionScore > 1 {
		t.Fatalf("conviction score out of range: %.3f", report.Facts[0].ConvictionScore)
	}
	if report.Thresholds.MinReinforcements <= 0 {
		t.Fatalf("expected positive reinforcement threshold, got %d", report.Thresholds.MinReinforcements)
	}
}

func TestRunBeliefs_InspectStateFilter(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{Content: "belief inspect filter", SourceFile: "beliefs.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	activeID, err := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "A", Predicate: "state", Object: "active", FactType: "state", Confidence: 0.7})
	if err != nil {
		t.Fatalf("AddFact active: %v", err)
	}
	retiredID, err := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "B", Predicate: "state", Object: "retired", FactType: "state", Confidence: 0.6})
	if err != nil {
		t.Fatalf("AddFact retired: %v", err)
	}
	if err := s.UpdateFactState(ctx, retiredID, store.FactStateRetired); err != nil {
		t.Fatalf("UpdateFactState retired: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	_ = activeID

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runBeliefs([]string{"inspect", "--state", "retired", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runBeliefs inspect retired --json: %v\nout=%s", runErr, out)
	}

	var report beliefsInspectReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode beliefs inspect report: %v\nout=%q", err, out)
	}
	if len(report.Facts) == 0 {
		t.Fatalf("expected retired facts in report")
	}
	for _, row := range report.Facts {
		if row.State != store.FactStateRetired {
			t.Fatalf("expected only retired state rows, got %q", row.State)
		}
	}
}

// ==================== agents command ====================

func TestRunAgents_UnknownArgument(t *testing.T) {
	err := runAgents([]string{"--bad"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "usage: cortex agents") && !strings.Contains(err.Error(), "Usage: cortex agents") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAgents_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cfgDir := filepath.Join(homeDir, ".cortex")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	trustCfg := `agents:
  mister:
    trust: owner
  hawk:
    trust: reader
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(trustCfg), 0o600); err != nil {
		t.Fatalf("write trust config: %v", err)
	}

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()

	misterMemID, err := s.AddMemory(ctx, &store.Memory{
		Content:       "mister memory",
		SourceFile:    "mister.md",
		SourceLine:    1,
		SourceSection: "agent",
		Metadata:      &store.Metadata{AgentID: "mister"},
	})
	if err != nil {
		t.Fatalf("add mister memory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   misterMemID,
		Subject:    "deploy",
		Predicate:  "owner",
		Object:     "mister",
		Confidence: 0.95,
		FactType:   "identity",
		AgentID:    "mister",
	}); err != nil {
		t.Fatalf("add mister fact: %v", err)
	}

	hawkMemID, err := s.AddMemory(ctx, &store.Memory{
		Content:       "hawk memory",
		SourceFile:    "hawk.md",
		SourceLine:    1,
		SourceSection: "agent",
		Metadata:      &store.Metadata{AgentID: "hawk"},
	})
	if err != nil {
		t.Fatalf("add hawk memory: %v", err)
	}
	oldFactID, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   hawkMemID,
		Subject:    "build",
		Predicate:  "status",
		Object:     "red",
		Confidence: 0.7,
		FactType:   "state",
		AgentID:    "hawk",
	})
	if err != nil {
		t.Fatalf("add hawk old fact: %v", err)
	}
	newFactID, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   hawkMemID,
		Subject:    "build",
		Predicate:  "status",
		Object:     "green",
		Confidence: 0.9,
		FactType:   "state",
		AgentID:    "hawk",
	})
	if err != nil {
		t.Fatalf("add hawk new fact: %v", err)
	}
	if err := s.SupersedeFact(ctx, oldFactID, newFactID, "state updated"); err != nil {
		t.Fatalf("supersede hawk fact: %v", err)
	}

	// Global fact should not appear in `cortex agents`.
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   hawkMemID,
		Subject:    "global",
		Predicate:  "scope",
		Object:     "shared",
		Confidence: 0.8,
		FactType:   "state",
	}); err != nil {
		t.Fatalf("add global fact: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runAgents([]string{"--json"})
	})
	if runErr != nil {
		t.Fatalf("runAgents --json failed: %v\nout=%s", runErr, out)
	}

	var payload agentsReport
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode agents payload: %v\nout=%q", err, out)
	}
	if payload.TotalAgents != 2 {
		t.Fatalf("expected 2 agents, got %d (%+v)", payload.TotalAgents, payload)
	}

	mister, ok := findAgentSummary(payload.Agents, "mister")
	if !ok {
		t.Fatalf("expected mister in payload: %+v", payload)
	}
	if mister.MemoryCount != 1 || mister.FactCount != 1 || mister.ActiveFactCount != 1 {
		t.Fatalf("unexpected mister stats: %+v", mister)
	}
	if !mister.TrustConfigured || mister.TrustLevel != "owner" || mister.TrustScope != "read:all write:all" {
		t.Fatalf("unexpected mister trust visibility: %+v", mister)
	}

	hawk, ok := findAgentSummary(payload.Agents, "hawk")
	if !ok {
		t.Fatalf("expected hawk in payload: %+v", payload)
	}
	if hawk.MemoryCount != 1 || hawk.FactCount != 2 || hawk.ActiveFactCount != 1 {
		t.Fatalf("unexpected hawk stats: %+v", hawk)
	}
	if !hawk.TrustConfigured || hawk.TrustLevel != "reader" || hawk.TrustScope != "read:all write:none" {
		t.Fatalf("unexpected hawk trust visibility: %+v", hawk)
	}
}

func TestRunAgents_TextIncludesTrustVisibility(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cfgDir := filepath.Join(homeDir, ".cortex")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	trustCfg := `agents:
  mister:
    trust: owner
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(trustCfg), 0o600); err != nil {
		t.Fatalf("write trust config: %v", err)
	}

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{Content: "mister memory", SourceFile: "mister.md", Metadata: &store.Metadata{AgentID: "mister"}})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "deploy", Predicate: "owner", Object: "mister", FactType: "identity", AgentID: "mister"}); err != nil {
		t.Fatalf("add fact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	out := captureStdout(func() {
		if err := runAgents(nil); err != nil {
			t.Fatalf("runAgents: %v", err)
		}
	})
	if !strings.Contains(out, "TRUST") || !strings.Contains(out, "SCOPE") || !strings.Contains(out, "read:all write:all") {
		t.Fatalf("expected trust columns in output, got: %s", out)
	}
	if !strings.Contains(out, "read-only visibility only") {
		t.Fatalf("expected read-only note in output, got: %s", out)
	}
}

func TestRunAgents_NoAgents(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	t.Setenv("HOME", t.TempDir())

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	out := captureStdout(func() {
		if err := runAgents(nil); err != nil {
			t.Fatalf("runAgents: %v", err)
		}
	})
	if !strings.Contains(out, "No agents found") {
		t.Fatalf("expected empty-state message, got: %s", out)
	}
}

func TestRunAgents_InvalidTrustConfig(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cfgDir := filepath.Join(homeDir, ".cortex")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	badCfg := `agents:
  hawk:
    trust: admin
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(badCfg), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	err = runAgents([]string{"--json"})
	if err == nil {
		t.Fatal("expected trust config validation error")
	}
	if !strings.Contains(err.Error(), "loading trust config") || !strings.Contains(err.Error(), "agents.hawk.trust") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func findAgentSummary(agents []agentSummary, agentID string) (agentSummary, bool) {
	for _, a := range agents {
		if a.AgentID == agentID {
			return a, true
		}
	}
	return agentSummary{}, false
}

func findDoctorCheck(report doctorReport, name string) (doctorCheck, bool) {
	for _, c := range report.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctorCheck{}, false
}

// ==================== reinforce error paths ====================

func TestRunReinforce_NonexistentFactReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s.Close()

	err = runReinforce([]string{"999999"})
	if err == nil {
		t.Fatal("expected error when reinforcing nonexistent fact")
	}
	if !strings.Contains(err.Error(), "no facts were reinforced") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ==================== list fact type validation ====================

func TestRunList_InvalidFactType(t *testing.T) {
	err := runList([]string{"--facts", "--type", "badtype"})
	if err == nil {
		t.Fatal("expected error for invalid fact type")
	}
	if !strings.Contains(err.Error(), "unknown fact type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunList_ValidFactType(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s.Close()

	// Should not error with a valid type (even if no results)
	err = runList([]string{"--facts", "--type", "decision", "--json"})
	if err != nil {
		t.Fatalf("unexpected error with valid fact type: %v", err)
	}
}

// ==================== edge case: empty search ====================

func TestRunSearch_NoQuery(t *testing.T) {
	err := runSearch([]string{})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSearch_UnknownMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	globalDBPath = dbPath
	t.Cleanup(func() { globalDBPath = oldDBPath })

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s.Close()

	err = runSearch([]string{"test query", "--mode", "badmode"})
	if err == nil {
		t.Fatal("expected error for unknown search mode")
	}
	if !strings.Contains(err.Error(), "badmode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSearch_FactsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
	})

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Q prefers green for additions and blue for deletions in diffs",
		SourceFile: "memory/2026-03-18.md",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memID,
		Subject:    "Q",
		Predicate:  "prefers",
		Object:     "green for additions and blue for deletions in code diffs",
		FactType:   "preference",
		Confidence: 0.95,
	}); err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runSearch([]string{"green blue code diffs", "--facts", "--json"})
	})
	if runErr != nil {
		t.Fatalf("runSearch --facts --json: %v", runErr)
	}
	if !strings.Contains(out, "\"fact_id\"") {
		t.Fatalf("expected fact_id in JSON output, got %q", out)
	}
	if !strings.Contains(out, "\"predicate\": \"prefers\"") {
		t.Fatalf("expected fact payload in JSON output, got %q", out)
	}
}

func TestRunAnswer_AutoResolvesEmbedderForHybrid(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	oldFactory := newEmbedClient
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
		newEmbedClient = oldFactory
	})

	t.Setenv("HOME", t.TempDir())
	t.Setenv("CORTEX_EMBED", "ollama/nomic-embed-text")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	newEmbedClient = func(cfg *embed.EmbedConfig) (embed.Embedder, error) {
		return &mockCommandEmbedder{dims: 8}, nil
	}

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	if _, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Caroline went to the support group on 7 May 2023.",
		SourceFile: "locomo.md",
	}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	var (
		runErr error
		stdout string
		stderr string
	)
	stderr = captureStderr(func() {
		stdout = captureStdout(func() {
			runErr = runAnswer([]string{"support group", "--mode", "hybrid", "--json"})
		})
	})
	if runErr != nil {
		t.Fatalf("runAnswer: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if strings.Contains(stderr, "hybrid mode requires an embedder") {
		t.Fatalf("expected auto-resolved embedder, got fallback stderr: %q", stderr)
	}
	if !strings.Contains(stdout, "\"answer\"") {
		t.Fatalf("expected JSON answer payload, got %q", stdout)
	}
}

func TestRunAnswer_EmbedFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")

	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	oldFactory := newEmbedClient
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
		newEmbedClient = oldFactory
	})

	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	newEmbedClient = func(cfg *embed.EmbedConfig) (embed.Embedder, error) {
		return &mockCommandEmbedder{dims: 8}, nil
	}

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	if _, err := s.AddMemory(ctx, &store.Memory{
		Content:    "Validator yield data is in this note.",
		SourceFile: "staking.md",
	}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	var (
		runErr error
		stdout string
		stderr string
	)
	stderr = captureStderr(func() {
		stdout = captureStdout(func() {
			runErr = runAnswer([]string{"validator yield", "--mode", "hybrid", "--embed", "ollama/nomic-embed-text", "--json"})
		})
	})
	if runErr != nil {
		t.Fatalf("runAnswer with --embed: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if strings.Contains(stderr, "hybrid mode requires an embedder") {
		t.Fatalf("expected explicit embedder, got fallback stderr: %q", stderr)
	}
	if !strings.Contains(stdout, "\"answer\"") {
		t.Fatalf("expected JSON answer payload, got %q", stdout)
	}
}

func TestRunHealth_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
	})

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, err := s.AddMemory(ctx, &store.Memory{Content: "health seed", SourceFile: "health.md"})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "Q", Predicate: "status", Object: "active", FactType: "state", Confidence: 0.9}); err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runHealth([]string{"--json"})
	})
	if runErr != nil {
		t.Fatalf("runHealth --json: %v", runErr)
	}
	if !strings.Contains(out, "\"memories\"") || !strings.Contains(out, "\"recommendations\"") {
		t.Fatalf("unexpected health output: %q", out)
	}
	if !strings.Contains(out, "\"source_tiers\"") || !strings.Contains(out, "\"predicate_modes\"") {
		t.Fatalf("expected source tier + predicate mode output, got %q", out)
	}
}

func TestRunEvalSearch_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	corpusDir := filepath.Join(tmpDir, "corpus")
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		t.Fatalf("mkdir corpus: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corpusDir, "profile.md"), []byte("Q prefers Sonnet for coding tasks.\n"), 0o644); err != nil {
		t.Fatalf("write corpus file: %v", err)
	}
	fixturePath := filepath.Join(tmpDir, "fixture.json")
	fixture := `{
	  "name": "test-fixture",
	  "noise_markers": ["HEARTBEAT_OK"],
	  "queries": [
	    {
	      "query": "Q prefers Sonnet for coding tasks",
	      "limit": 5,
	      "max_noisy_top3": 0,
	      "k": 1,
	      "expected_contains_any": ["sonnet", "coding"],
	      "min_precision_at_k": 1.0,
	      "min_hits": 1
	    }
	  ]
	}`
	if err := os.WriteFile(fixturePath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var (
		runErr error
		out    string
	)
	out = captureStdout(func() {
		runErr = runEval([]string{"search", "--fixture", fixturePath, "--corpus", corpusDir, "--json"})
	})
	if runErr != nil {
		t.Fatalf("runEval search: %v\nout=%s", runErr, out)
	}
	if !strings.Contains(out, "\"pass_rate\"") || !strings.Contains(out, "\"test-fixture\"") {
		t.Fatalf("unexpected eval JSON output: %q", out)
	}
}

func TestRunSuppress_AddListRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".cortex")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	if err := runSuppress([]string{"add", "^NO_REPLY$", "--reason", "protocol noise"}); err != nil {
		t.Fatalf("runSuppress add: %v", err)
	}

	out := captureStdout(func() {
		if err := runSuppress([]string{"list"}); err != nil {
			t.Fatalf("runSuppress list: %v", err)
		}
	})
	if !strings.Contains(out, "^NO_REPLY$") {
		t.Fatalf("expected suppression pattern in list output, got %q", out)
	}

	if err := runSuppress([]string{"remove", "^NO_REPLY$"}); err != nil {
		t.Fatalf("runSuppress remove: %v", err)
	}
}

func TestRunSourceWeight_AddListRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".cortex")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	if err := runSourceWeight([]string{"add", "memory/", "1.4"}); err != nil {
		t.Fatalf("runSourceWeight add: %v", err)
	}

	out := captureStdout(func() {
		if err := runSourceWeight([]string{"list"}); err != nil {
			t.Fatalf("runSourceWeight list: %v", err)
		}
	})
	if !strings.Contains(out, "memory/") || !strings.Contains(out, "1.4") {
		t.Fatalf("expected source weight in list output, got %q", out)
	}

	if err := runSourceWeight([]string{"remove", "memory/"}); err != nil {
		t.Fatalf("runSourceWeight remove: %v", err)
	}
}

func TestRunFactCommand_KeepAndDrop(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
	})

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memID, _ := s.AddMemory(ctx, &store.Memory{Content: "fact command seed", SourceFile: "fact.md"})
	factID, _ := s.AddFact(ctx, &store.Fact{MemoryID: memID, Subject: "Q", Predicate: "status", Object: "active", FactType: "state", Confidence: 0.9})
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	if err := runFactCommand([]string{"keep", strconv.FormatInt(factID, 10)}); err != nil {
		t.Fatalf("runFactCommand keep: %v", err)
	}
	s, _ = store.NewStore(store.StoreConfig{DBPath: dbPath})
	fact, _ := s.GetFact(ctx, factID)
	if fact.State != store.FactStateCore {
		t.Fatalf("expected core state after keep, got %q", fact.State)
	}
	s.Close()

	if err := runFactCommand([]string{"drop", strconv.FormatInt(factID, 10)}); err != nil {
		t.Fatalf("runFactCommand drop: %v", err)
	}
	s, _ = store.NewStore(store.StoreConfig{DBPath: dbPath})
	defer s.Close()
	fact, _ = s.GetFact(ctx, factID)
	if fact.State != store.FactStateRetired {
		t.Fatalf("expected retired state after drop, got %q", fact.State)
	}
}

func TestRunCleanup_PrunesTemporalNoiseFacts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cortex.db")
	oldDBPath := globalDBPath
	oldReadOnly := globalReadOnly
	globalDBPath = dbPath
	globalReadOnly = false
	t.Cleanup(func() {
		globalDBPath = oldDBPath
		globalReadOnly = oldReadOnly
	})

	s, err := store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	memNoise, err := s.AddMemory(ctx, &store.Memory{Content: "current time value with enough context to survive base cleanup", SourceFile: "noise.md"})
	if err != nil {
		t.Fatalf("AddMemory noise: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memNoise,
		Subject:    "Current time",
		Predicate:  "value",
		Object:     "Friday, March 20th, 2026 — 8:48 PM",
		FactType:   "temporal",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("AddFact noise: %v", err)
	}
	memKeep, err := s.AddMemory(ctx, &store.Memory{Content: "service is running with enough context to survive base cleanup", SourceFile: "keep.md"})
	if err != nil {
		t.Fatalf("AddMemory keep: %v", err)
	}
	if _, err := s.AddFact(ctx, &store.Fact{
		MemoryID:   memKeep,
		Subject:    "service",
		Predicate:  "status",
		Object:     "running",
		FactType:   "state",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("AddFact keep: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	if err := runCleanup([]string{"--prune-temporal-noise"}); err != nil {
		t.Fatalf("runCleanup: %v", err)
	}

	s, err = store.NewStore(store.StoreConfig{DBPath: dbPath})
	if err != nil {
		t.Fatalf("Reopen store: %v", err)
	}
	defer s.Close()

	facts, err := s.ListFacts(ctx, store.ListOpts{Limit: 20, IncludeSuperseded: true})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact after pruning, got %d", len(facts))
	}
	if facts[0].Subject != "service" {
		t.Fatalf("expected non-noise fact to remain, got %+v", facts[0])
	}
}

func TestCompletion_Bash(t *testing.T) {
	exitCode, out := runMainSubprocess(t, "completion", "bash")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "complete -F") {
		t.Fatalf("expected bash completion function, got: %q", out)
	}
	// Verify all top-level commands are in the completion
	for _, cmd := range []string{"import", "search", "stats", "mcp", "doctor"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("completion missing command %q", cmd)
		}
	}
}

func TestCompletion_Zsh(t *testing.T) {
	exitCode, out := runMainSubprocess(t, "completion", "zsh")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "#compdef cortex") {
		t.Fatalf("expected zsh compdef header, got: %q", out)
	}
}

func TestCompletion_Fish(t *testing.T) {
	exitCode, out := runMainSubprocess(t, "completion", "fish")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "complete -c cortex") {
		t.Fatalf("expected fish completions, got: %q", out)
	}
}

func TestCompletion_InvalidShell(t *testing.T) {
	exitCode, out := runMainSubprocess(t, "completion", "powershell")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "unsupported shell") {
		t.Fatalf("expected unsupported shell error, got: %q", out)
	}
}

func TestCompletion_NoArgs(t *testing.T) {
	exitCode, out := runMainSubprocess(t, "completion")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; output=%q", exitCode, out)
	}
	if !strings.Contains(out, "usage:") {
		t.Fatalf("expected usage text, got: %q", out)
	}
}
