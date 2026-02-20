package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurttlocker/cortex/internal/codexrollout"
)

func TestCLIExecute_Smoke(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "reason-telemetry.jsonl")
	if err := os.WriteFile(path, []byte(`{"mode":"one-shot","provider":"openrouter","model":"openai-codex/gpt-5.2","wall_ms":1200,"cost_known":true,"cost_usd":0.0012}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	res, err := codexrollout.Execute([]string{"--file", path})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", res.ExitCode)
	}
	if !strings.Contains(res.Output, "Cortex Codex rollout report") {
		t.Fatalf("unexpected output: %s", res.Output)
	}
}
