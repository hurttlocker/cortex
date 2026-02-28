package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMD(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestObsValidateOutput_Pass(t *testing.T) {
	root := t.TempDir()
	writeMD(t, root, "Cortex Dashboard.md", "# Dashboard\n- [[topics/Trading]]\n- [[entities/Q]]\n")
	writeMD(t, root, "topics/Trading.md", "# Trading\n- [[Cortex Dashboard]]\n- [[entities/Q]]\n- [[topics/General]]\n")
	writeMD(t, root, "topics/General.md", "# General\n- [[Cortex Dashboard]]\n- [[entities/Q]]\n")
	writeMD(t, root, "entities/Q.md", "# Q\n- [[Cortex Dashboard]]\n- [[topics/Trading]]\n- [[topics/General]]\n")

	report, err := obsValidateOutput(root)
	if err != nil {
		t.Fatalf("obsValidateOutput err: %v", err)
	}
	if report.BrokenLinks != 0 || report.MissingDashboardLinks != 0 || report.Orphans != 0 {
		t.Fatalf("expected clean report, got %+v", report)
	}
	if report.Files != 4 {
		t.Fatalf("expected files=4, got %d", report.Files)
	}
}

func TestObsValidateOutput_FindsBrokenAndMissingDashboard(t *testing.T) {
	root := t.TempDir()
	writeMD(t, root, "Cortex Dashboard.md", "# Dashboard\n- [[topics/Trading]]\n")
	writeMD(t, root, "topics/Trading.md", "# Trading\n- [[entities/MissingHub]]\n")
	writeMD(t, root, "entities/Q.md", "# Q\n- [[topics/Trading]]\n")

	report, err := obsValidateOutput(root)
	if err != nil {
		t.Fatalf("obsValidateOutput err: %v", err)
	}
	if report.BrokenLinks == 0 {
		t.Fatalf("expected broken links > 0, got %+v", report)
	}
	if report.MissingDashboardLinks == 0 {
		t.Fatalf("expected missing dashboard links > 0, got %+v", report)
	}
}
