package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/observe"
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

// ==================== search arg parsing ====================

func TestRunSearch_NoArgs(t *testing.T) {
	err := runSearch([]string{})
	if err == nil {
		t.Error("expected error for no arguments")
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
