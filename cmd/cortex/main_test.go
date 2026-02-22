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
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// ==================== codex rollout report subcommand ====================

func TestRunCodexRolloutReportCLI_StrictModeExitCode(t *testing.T) {
	tmp := t.TempDir()
	telemetryPath := filepath.Join(tmp, "reason-telemetry.jsonl")
	content := strings.Join([]string{
		`{"mode":"one-shot","provider":"openrouter","model":"openai-codex/gpt-5.2","wall_ms":25000,"cost_known":true,"cost_usd":0.001}`,
		`{"mode":"recursive","provider":"openrouter","model":"google/gemini-2.5-flash","wall_ms":35000,"cost_known":false}`,
	}, "\n")
	if err := os.WriteFile(telemetryPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write telemetry fixture: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	exitCode := runCodexRolloutReportCLI([]string{"--file", telemetryPath, "--warn-only=false"}, &out, &errOut)
	if exitCode != 2 {
		t.Fatalf("expected strict mode exit code 2, got %d", exitCode)
	}
	if !strings.Contains(out.String(), "Guardrail status") {
		t.Fatalf("expected guardrail status output, got: %s", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr output: %s", errOut.String())
	}
}

func TestRunCodexRolloutReportCLI_WarnOnlyExitZero(t *testing.T) {
	tmp := t.TempDir()
	telemetryPath := filepath.Join(tmp, "reason-telemetry.jsonl")
	content := strings.Join([]string{
		`{"mode":"one-shot","provider":"openrouter","model":"openai-codex/gpt-5.2","wall_ms":25000,"cost_known":true,"cost_usd":0.001}`,
	}, "\n")
	if err := os.WriteFile(telemetryPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write telemetry fixture: %v", err)
	}

	var out bytes.Buffer
	exitCode := runCodexRolloutReportCLI([]string{"--file", telemetryPath}, &out, io.Discard)
	if exitCode != 0 {
		t.Fatalf("expected warn-only default exit code 0, got %d", exitCode)
	}
	if !strings.Contains(out.String(), "WARN:") {
		t.Fatalf("expected warning in output, got: %s", out.String())
	}
}

func TestRunCodexRolloutReportCLI_HelpExitZero(t *testing.T) {
	var errOut bytes.Buffer
	exitCode := runCodexRolloutReportCLI([]string{"--help"}, io.Discard, &errOut)
	if exitCode != 0 {
		t.Fatalf("expected help exit code 0, got %d", exitCode)
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr output: %s", errOut.String())
	}
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
