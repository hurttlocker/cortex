package store

import "testing"

func TestInferProject(t *testing.T) {
	tests := []struct {
		sourceFile string
		expected   string
	}{
		// Trading
		{"trading/journal/2026-02-18.md", "trading"},
		{"mister-trading-2026/pre-market.md", "trading"},
		{"crypto/journal/ada_ml220/state.json", "trading"},
		{"/Users/q/clawd/trading_journal/trades/today.json", "trading"},

		// Eyes Web
		{"repos/mybeautifulwife/src/app/page.tsx", "eyes-web"},
		{"eyes-web/onboarding-spec.md", "eyes-web"},

		// Spear
		{"spear/customer-ops.md", "spear"},
		{"scripts/rustdesk_fleet.py", "spear"},

		// Cortex
		{"cortex/internal/store/store.go", "cortex"},

		// Wedding
		{"wedding/vendors.md", "wedding"},

		// YouTube
		{"mister_youtube/scripts/build_video.py", "youtube"},
		{"youtube/thumbnails/today.png", "youtube"},

		// No match
		{"random-notes.md", ""},
		{"memory/2026-02-18.md", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := InferProject(tt.sourceFile, DefaultProjectRules)
		if result != tt.expected {
			t.Errorf("InferProject(%q) = %q, want %q", tt.sourceFile, result, tt.expected)
		}
	}
}

func TestInferProject_CustomRules(t *testing.T) {
	rules := []ProjectRule{
		{Pattern: "notes/work", Project: "work"},
		{Pattern: "notes/personal", Project: "personal"},
	}

	tests := []struct {
		sourceFile string
		expected   string
	}{
		{"notes/work/meeting.md", "work"},
		{"notes/personal/journal.md", "personal"},
		{"other.md", ""},
	}

	for _, tt := range tests {
		result := InferProject(tt.sourceFile, rules)
		if result != tt.expected {
			t.Errorf("InferProject(%q) = %q, want %q", tt.sourceFile, result, tt.expected)
		}
	}
}

func TestInferProjectFromContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "trading - multiple keywords",
			content:  "Pre-market analysis shows QQQ is bullish. VWAP holding above EMA, looking for breakout entry point.",
			expected: "trading",
		},
		{
			name:     "trading - single keyword insufficient",
			content:  "I mentioned QQQ in passing but this is really about something else entirely.",
			expected: "",
		},
		{
			name:     "wedding - multiple keywords",
			content:  "Called the villa in Cabrera about the ceremony. Need to confirm vendor availability for catering.",
			expected: "wedding",
		},
		{
			name:     "cortex - technical terms",
			content:  "Cortex search uses BM25 for keyword matching combined with semantic search via embeddings.",
			expected: "cortex",
		},
		{
			name:     "spear - customer ops",
			content:  "Spear customer called about RustDesk connection dropping. Need to check device fleet status.",
			expected: "spear",
		},
		{
			name:     "youtube - video pipeline",
			content:  "Rendered the YouTube video using Remotion. Thumbnail looks good, need to fix the TTS audio timing.",
			expected: "youtube",
		},
		{
			name:     "empty content",
			content:  "",
			expected: "",
		},
		{
			name:     "no match - generic content",
			content:  "Had a great day today. Weather was nice. Went for a walk.",
			expected: "",
		},
		{
			name:     "highest hits wins",
			content:  "QQQ SPY pre-market ORB entry point stop loss take profit VWAP. Also the wedding ceremony at the villa.",
			expected: "trading", // trading has more hits
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InferProjectFromContent(tt.content, DefaultContentRules)
			if result != tt.expected {
				t.Errorf("InferProjectFromContent(%q) = %q, want %q", tt.name, result, tt.expected)
			}
		})
	}
}

func TestInferProjectFull(t *testing.T) {
	tests := []struct {
		name       string
		sourceFile string
		content    string
		expected   string
	}{
		{
			name:       "path match takes priority",
			sourceFile: "trading/plan.md",
			content:    "This is about the wedding ceremony at the villa in Cabrera",
			expected:   "trading", // path wins
		},
		{
			name:       "content fallback when path doesn't match",
			sourceFile: "memory/2026-02-18.md",
			content:    "Pre-market QQQ analysis shows bullish VWAP and strong EMA crossover",
			expected:   "trading", // content fallback
		},
		{
			name:       "neither matches",
			sourceFile: "memory/2026-02-18.md",
			content:    "Had lunch with a friend today.",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InferProjectFull(tt.sourceFile, tt.content, DefaultProjectRules, DefaultContentRules)
			if result != tt.expected {
				t.Errorf("InferProjectFull(%q) = %q, want %q", tt.name, result, tt.expected)
			}
		})
	}
}
