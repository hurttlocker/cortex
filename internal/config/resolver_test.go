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

func TestResolveConfig_PolicyDefaultsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `db_path: ~/.cortex/test.db
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if !resolved.Policies.ReinforcePromote.Enabled || resolved.Policies.ReinforcePromote.TargetState != "core" {
		t.Fatalf("expected default reinforce-promote policy, got %+v", resolved.Policies.ReinforcePromote)
	}
	if !resolved.Policies.DecayRetire.Enabled || resolved.Policies.DecayRetire.TargetState != "retired" {
		t.Fatalf("expected default decay-retire policy, got %+v", resolved.Policies.DecayRetire)
	}
	if !resolved.Policies.ConflictSupersede.Enabled {
		t.Fatalf("expected default conflict-supersede enabled, got %+v", resolved.Policies.ConflictSupersede)
	}
}

func TestResolveConfig_PolicyPartialOverrides(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `policies:
  reinforce_promote:
    min_reinforcements: 7
  decay_retire:
    enabled: false
    inactive_days: 20
  conflict_supersede:
    min_confidence_delta: 0.25
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	policies, err := ResolvePolicyConfig(cfgPath)
	if err != nil {
		t.Fatalf("ResolvePolicyConfig: %v", err)
	}

	if policies.ReinforcePromote.MinReinforcements != 7 {
		t.Fatalf("expected reinforce min_reinforcements=7, got %d", policies.ReinforcePromote.MinReinforcements)
	}
	if policies.ReinforcePromote.MinSources != 3 {
		t.Fatalf("expected reinforce min_sources default=3, got %d", policies.ReinforcePromote.MinSources)
	}
	if policies.DecayRetire.Enabled {
		t.Fatalf("expected decay-retire disabled, got %+v", policies.DecayRetire)
	}
	if policies.DecayRetire.InactiveDays != 20 {
		t.Fatalf("expected decay-retire inactive_days=20, got %d", policies.DecayRetire.InactiveDays)
	}
	if policies.DecayRetire.TargetState != "retired" {
		t.Fatalf("expected decay-retire target_state default=retired, got %q", policies.DecayRetire.TargetState)
	}
	if policies.ConflictSupersede.MinConfidenceDelta != 0.25 {
		t.Fatalf("expected conflict min_confidence_delta=0.25, got %f", policies.ConflictSupersede.MinConfidenceDelta)
	}
	if !policies.ConflictSupersede.RequireStrictlyNewer {
		t.Fatalf("expected conflict require_strictly_newer default=true, got %+v", policies.ConflictSupersede)
	}
}
