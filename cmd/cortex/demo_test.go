package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDemoMarkdownFiles(t *testing.T) {
	dir := t.TempDir()
	files, err := createDemoMarkdownFiles(dir)
	if err != nil {
		t.Fatalf("createDemoMarkdownFiles: %v", err)
	}
	if len(files) < 3 {
		t.Fatalf("expected at least 3 demo files, got %d", len(files))
	}
	for _, f := range files {
		if filepath.Ext(f) != ".md" {
			t.Fatalf("expected markdown file, got %s", f)
		}
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("expected file to exist: %s (%v)", f, err)
		}
	}
}

func TestCleanupDemoArtifacts(t *testing.T) {
	dir := t.TempDir()
	demoDir := filepath.Join(dir, "demo")
	dbPath := filepath.Join(dir, "cortex-demo.db")
	if err := os.MkdirAll(demoDir, 0o755); err != nil {
		t.Fatalf("mkdir demoDir: %v", err)
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	if err := cleanupDemoArtifacts(demoDir, dbPath); err != nil {
		t.Fatalf("cleanupDemoArtifacts: %v", err)
	}
	if _, err := os.Stat(demoDir); !os.IsNotExist(err) {
		t.Fatalf("expected demoDir removed, stat err=%v", err)
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed, stat err=%v", p, err)
		}
	}
}
