package store

import "testing"

func TestFilenameStem(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/q/memory/2026-02-18.md", "2026-02-18"},
		{"docs/setup-guide.md", "setup-guide"},
		{"MEMORY.md", "MEMORY"},
		{"/tmp/cortex-capture-abc123/auto-capture.md", "auto-capture"},
		{"README.md", "README"},
		{"file.tar.gz", "file.tar"},
		{"noext", "noext"},
		{"", ""},
		{"/", ""},
		{".", ""},
	}

	for _, tt := range tests {
		got := FilenameStem(tt.path)
		if got != tt.want {
			t.Errorf("FilenameStem(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestContextPrefix(t *testing.T) {
	tests := []struct {
		file    string
		section string
		want    string
	}{
		{"/Users/q/memory/2026-02-18.md", "Cortex v0.1.3 — Audit Fixes", "[2026-02-18 > Cortex v0.1.3 — Audit Fixes] "},
		{"docs/setup-guide.md", "Installation", "[setup-guide > Installation] "},
		{"MEMORY.md", "", "[MEMORY] "},
		{"", "Some Section", "[Some Section] "},
		{"", "", ""},
		{"/path/to/file.md", "Section", "[file > Section] "},
	}

	for _, tt := range tests {
		got := ContextPrefix(tt.file, tt.section)
		if got != tt.want {
			t.Errorf("ContextPrefix(%q, %q) = %q, want %q", tt.file, tt.section, got, tt.want)
		}
	}
}

func TestEnrichedContent(t *testing.T) {
	tests := []struct {
		content string
		file    string
		section string
		want    string
	}{
		{
			"Conflicts query hanging — O(N²) self-join",
			"/Users/q/memory/2026-02-18.md",
			"Cortex v0.1.3",
			"[2026-02-18 > Cortex v0.1.3] Conflicts query hanging — O(N²) self-join",
		},
		{
			"Some plain content",
			"",
			"",
			"Some plain content",
		},
		{
			"Content with section only",
			"",
			"Trading Notes",
			"[Trading Notes] Content with section only",
		},
	}

	for _, tt := range tests {
		got := EnrichedContent(tt.content, tt.file, tt.section)
		if got != tt.want {
			t.Errorf("EnrichedContent(%q, %q, %q) = %q, want %q",
				tt.content, tt.file, tt.section, got, tt.want)
		}
	}
}
