package reason

import (
	"testing"
)

func TestParseAction(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantAct string
		wantArg string
	}{
		{
			name:    "simple FINAL",
			input:   "FINAL(This is my answer.)",
			wantAct: ActionFinal,
			wantArg: "This is my answer.",
		},
		{
			name:    "SEARCH action",
			input:   "I need more context about trading.\n\nSEARCH(ORB strategy options performance)",
			wantAct: ActionSearch,
			wantArg: "ORB strategy options performance",
		},
		{
			name:    "FACTS action",
			input:   "Let me look up the specific model.\n\nFACTS(default interactive model)",
			wantAct: ActionFacts,
			wantArg: "default interactive model",
		},
		{
			name:    "PEEK action",
			input:   "PEEK(42)",
			wantAct: ActionPeek,
			wantArg: "42",
		},
		{
			name:    "SUB_QUERY action",
			input:   "This requires a sub-analysis.\n\nSUB_QUERY(What is the current trading strategy?)",
			wantAct: ActionSubQuery,
			wantArg: "What is the current trading strategy?",
		},
		{
			name:    "multi-line FINAL",
			input:   "FINAL(Here is my analysis.\n\n1. Point one\n2. Point two\n3. Point three)",
			wantAct: ActionFinal,
			wantArg: "Here is my analysis.\n\n1. Point one\n2. Point two\n3. Point three",
		},
		{
			name:    "FINAL with nested parens",
			input:   "FINAL(The function foo(bar) was called with baz(qux).)",
			wantAct: ActionFinal,
			wantArg: "The function foo(bar) was called with baz(qux).",
		},
		{
			name:    "no action — raw text",
			input:   "I think the answer is 42.",
			wantAct: "",
			wantArg: "",
		},
		{
			name:    "reasoning then FINAL",
			input:   "Based on the context provided, I can see several patterns.\n\nFINAL(The main pattern is X.)",
			wantAct: ActionFinal,
			wantArg: "The main pattern is X.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAct, gotArg := parseAction(tt.input)
			if gotAct != tt.wantAct {
				t.Errorf("action = %q, want %q", gotAct, tt.wantAct)
			}
			if gotArg != tt.wantArg {
				t.Errorf("arg = %q, want %q", gotArg, tt.wantArg)
			}
		})
	}
}

func TestExtractPreActionText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "text before SEARCH",
			input: "I need to find more about trading strategies.\n\nSEARCH(ORB strategy)",
			want:  "I need to find more about trading strategies.",
		},
		{
			name:  "text before FINAL",
			input: "After analyzing everything:\n\nFINAL(My conclusion)",
			want:  "After analyzing everything:",
		},
		{
			name:  "no action in text",
			input: "Just some regular text.",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPreActionText(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		query string
		text  string
		want  bool
	}{
		{"trading strategy", "our trading strategy uses orb", true},
		{"wedding venue", "found a nice wedding venue in dr", true},
		{"quantum physics", "trading options with orb", false},
		{"cortex reason", "cortex reason engine benchmarks", true},
		{"a b", "short words test", false}, // words too short (<=2 chars)
	}

	for _, tt := range tests {
		t.Run(tt.query+" vs "+tt.text, func(t *testing.T) {
			got := fuzzyMatch(tt.query, tt.text)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.query, tt.text, got, tt.want)
			}
		})
	}
}

func TestTruncateArg(t *testing.T) {
	short := "hello world"
	if got := truncateArg(short, 20); got != short {
		t.Errorf("short string changed: %q", got)
	}

	long := "this is a very long string that should get truncated at some point"
	got := truncateArg(long, 20)
	if len(got) > 23 { // 20 + "..."
		t.Errorf("truncation failed: %q (len %d)", got, len(got))
	}

	withNewlines := "line1\nline2\nline3"
	got = truncateArg(withNewlines, 100)
	if got != "line1 line2 line3" {
		t.Errorf("newline replacement failed: %q", got)
	}
}
