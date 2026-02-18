package extract

import "testing"

func TestAutoClassifyMemoryClass(t *testing.T) {
	tests := []struct {
		name    string
		content string
		section string
		want    string
	}{
		{
			name:    "rule",
			content: "You must never push to main directly. This is non-negotiable.",
			want:    "rule",
		},
		{
			name:    "decision",
			content: "Final decision: we chose Go over Rust for this service.",
			want:    "decision",
		},
		{
			name:    "preference",
			content: "User prefers concise answers and dark mode.",
			want:    "preference",
		},
		{
			name:    "identity",
			content: "Name: Sydney\nRole: PM\nEmail: syd@example.com",
			want:    "identity",
		},
		{
			name:    "status",
			content: "Status: currently working on migration, blocked on CI.",
			want:    "status",
		},
		{
			name:    "scratch",
			content: "Brainstorm: maybe we could try a rough idea for offline mode.",
			want:    "scratch",
		},
		{
			name:    "unclassified",
			content: "This is a neutral sentence without enough signal.",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AutoClassifyMemoryClass(tt.content, tt.section)
			if got != tt.want {
				t.Fatalf("AutoClassifyMemoryClass() = %q, want %q", got, tt.want)
			}
		})
	}
}
