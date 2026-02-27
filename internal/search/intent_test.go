package search

import "testing"

func TestNormalizeIntent(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", IntentAll, false},
		{"all", IntentAll, false},
		{"memory", IntentMemory, false},
		{"import", IntentImport, false},
		{"connector", IntentConnector, false},
		{"weird", "", true},
	}

	for _, tc := range cases {
		got, err := normalizeIntent(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("normalizeIntent(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeIntent(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeIntent(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestFilterByIntent(t *testing.T) {
	results := []Result{
		{SourceFile: "memory/2026-02-27.md", Content: "daily note"},
		{SourceFile: "MEMORY.md", Content: "long-term memory"},
		{SourceFile: "github:issues/123", Content: "connector item"},
		{SourceFile: "knowledge/product/cortex.md", Content: "imported doc"},
	}

	mem := filterByIntent(results, IntentMemory)
	if len(mem) != 2 {
		t.Fatalf("memory intent expected 2, got %d", len(mem))
	}

	conn := filterByIntent(results, IntentConnector)
	if len(conn) != 1 || conn[0].SourceFile != "github:issues/123" {
		t.Fatalf("connector intent mismatch: %+v", conn)
	}

	imp := filterByIntent(results, IntentImport)
	if len(imp) != 1 || imp[0].SourceFile != "knowledge/product/cortex.md" {
		t.Fatalf("import intent mismatch: %+v", imp)
	}
}
