package reason

import "testing"

func TestParseProviderModel(t *testing.T) {
	tests := []struct {
		input    string
		provider string
		model    string
	}{
		{"phi4-mini", "ollama", "phi4-mini"},
		{"gemma2:9b", "ollama", "gemma2:9b"},
		{"ollama/phi4-mini", "ollama", "phi4-mini"},
		{"openrouter/deepseek/deepseek-chat", "openrouter", "deepseek/deepseek-chat"},
		{"google/gemini-2.5-flash", "openrouter", "google/gemini-2.5-flash"},
		{"minimax/minimax-m2.5", "openrouter", "minimax/minimax-m2.5"},
		{"deepseek/deepseek-chat", "openrouter", "deepseek/deepseek-chat"},
		{"x-ai/grok-4.1-fast", "openrouter", "x-ai/grok-4.1-fast"},
	}

	for _, tt := range tests {
		provider, model := ParseProviderModel(tt.input)
		if provider != tt.provider || model != tt.model {
			t.Errorf("ParseProviderModel(%q) = (%q, %q), want (%q, %q)",
				tt.input, provider, model, tt.provider, tt.model)
		}
	}
}

func TestStripThinkingTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello world"},
		{"<think>internal reasoning</think>actual response", "actual response"},
		{"before<think>stuff</think>after", "beforeafter"},
		{"<think>unclosed thinking", ""},
		{"no thinking here", "no thinking here"},
		{"<think>first</think>middle<think>second</think>end", "middleend"},
	}

	for _, tt := range tests {
		result := stripThinkingTags(tt.input)
		if result != tt.expected {
			t.Errorf("stripThinkingTags(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestExpandTemplate(t *testing.T) {
	tests := []struct {
		tmpl     string
		context  string
		query    string
		expected string
	}{
		{
			"Context: {{context}}\nQuery: {{.Query}}",
			"some memories", "what happened",
			"Context: some memories\nQuery: what happened",
		},
		{
			"{{if .Query}}Focus: {{.Query}}{{end}}",
			"", "trading",
			"Focus: trading",
		},
		{
			"base {{if .Query}}Focus: {{.Query}}{{end}} end",
			"", "",
			"base  end",
		},
	}

	for _, tt := range tests {
		result := expandTemplate(tt.tmpl, tt.context, tt.query)
		if result != tt.expected {
			t.Errorf("expandTemplate(%q, %q, %q) = %q, want %q",
				tt.tmpl, tt.context, tt.query, result, tt.expected)
		}
	}
}

func TestGetPreset_Builtin(t *testing.T) {
	for name := range BuiltinPresets {
		p, err := GetPreset(name, "/nonexistent")
		if err != nil {
			t.Errorf("GetPreset(%q) error: %v", name, err)
		}
		if p.Name != name {
			t.Errorf("GetPreset(%q).Name = %q", name, p.Name)
		}
	}
}

func TestGetPreset_Unknown(t *testing.T) {
	_, err := GetPreset("nonexistent-preset", "/nonexistent")
	if err == nil {
		t.Error("expected error for unknown preset")
	}
}

func TestEstimateCost(t *testing.T) {
	// gemini-2.5-flash: $0.15/M in, $0.60/M out
	cost := estimateCost("google/gemini-2.5-flash", 2000, 500)
	// Expected: (2000 * 0.15 / 1M) + (500 * 0.60 / 1M) = 0.0003 + 0.0003 = 0.0006
	if cost < 0.0005 || cost > 0.0007 {
		t.Errorf("estimateCost = %f, want ~0.0006", cost)
	}

	// Unknown model returns 0
	cost = estimateCost("unknown/model", 1000, 1000)
	if cost != 0 {
		t.Errorf("unknown model cost = %f, want 0", cost)
	}
}

func TestListPresets(t *testing.T) {
	presets := ListPresets("/nonexistent")
	if len(presets) != len(BuiltinPresets) {
		t.Errorf("ListPresets returned %d, want %d", len(presets), len(BuiltinPresets))
	}
}
