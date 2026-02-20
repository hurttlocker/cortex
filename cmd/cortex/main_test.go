package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	if !strings.Contains(out, "Sample conflicts (showing 10 of 14)") {
		t.Fatalf("expected sample header, got: %q", out)
	}
	if !strings.Contains(out, "additional conflicts hidden") {
		t.Fatalf("expected hidden-details hint, got: %q", out)
	}
	if strings.Contains(out, "[11/14]") {
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
