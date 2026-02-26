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
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/connect"
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
	exitCode, out := runMainSubprocessWithEnv(t, map[string]string{
		"OPENROUTER_API_KEY": "",
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

	hawk, ok := findAgentSummary(payload.Agents, "hawk")
	if !ok {
		t.Fatalf("expected hawk in payload: %+v", payload)
	}
	if hawk.MemoryCount != 1 || hawk.FactCount != 2 || hawk.ActiveFactCount != 1 {
		t.Fatalf("unexpected hawk stats: %+v", hawk)
	}
}

func TestRunAgents_NoAgents(t *testing.T) {
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
		if err := runAgents(nil); err != nil {
			t.Fatalf("runAgents: %v", err)
		}
	})
	if !strings.Contains(out, "No agents found") {
		t.Fatalf("expected empty-state message, got: %s", out)
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
