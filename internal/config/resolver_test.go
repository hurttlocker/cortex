package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfig_Precedence_ConfigEnvCLI(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `db_path: ~/.cortex/from-config.db
llm:
  provider: openrouter/x-ai/grok-4.1-fast
  classify_model: openrouter/deepseek/deepseek-v3.2
embed:
  provider: ollama/nomic-embed-text
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("CORTEX_DB", "~/from-env.db")
	t.Setenv("CORTEX_LLM", "google/gemini-2.5-flash")

	resolved, err := ResolveConfig(ResolveOptions{
		ConfigPath: cfgPath,
		CLILLM:     "openrouter/google/gemini-2.0-flash-001",
		CLIDBPath:  "~/from-cli.db",
	})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if resolved.DBPath.Source != SourceCLI {
		t.Fatalf("expected DB path source cli, got %s", resolved.DBPath.Source)
	}
	if resolved.LLMProvider.Source != SourceCLI {
		t.Fatalf("expected llm provider source cli, got %s", resolved.LLMProvider.Source)
	}
	if resolved.LLMClassifyModel.Source != SourceConfig {
		t.Fatalf("expected classify model from config, got %s", resolved.LLMClassifyModel.Source)
	}
}

func TestEffectiveLLMModel_PurposeFallback(t *testing.T) {
	resolved := ResolvedConfig{
		LLMProvider:      ResolvedValue{Value: "openrouter", Source: SourceConfig},
		LLMClassifyModel: ResolvedValue{Value: "", Source: SourceUnknown},
	}

	m := resolved.EffectiveLLMModel("classify", "openrouter/deepseek/deepseek-v3.2")
	if m.Value != "openrouter/deepseek/deepseek-v3.2" {
		t.Fatalf("unexpected effective model: %q", m.Value)
	}
	if m.Source != SourceConfig {
		t.Fatalf("expected source=config from provider fallback, got %s", m.Source)
	}
}

func TestAPIKeyForProvider_EnvOverridesConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `llm:
  provider: openrouter/x-ai/grok-4.1-fast
  api_key: config-key
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("OPENROUTER_API_KEY", "env-key")

	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	k := resolved.APIKeyForProvider("openrouter/some-model")
	if k.Value != "env-key" {
		t.Fatalf("expected env key, got %q", k.Value)
	}
	if k.Source != SourceEnv {
		t.Fatalf("expected source env, got %s", k.Source)
	}
}
