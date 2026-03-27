package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestResolveConfig_ObsidianExportDefaultsWhenMissing(t *testing.T) {
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
	if resolved.ObsidianExport.HubMinRefs != 5 || resolved.ObsidianExport.ConceptMinRefs != 10 {
		t.Fatalf("unexpected obsidian export defaults: %+v", resolved.ObsidianExport)
	}
	if resolved.ObsidianExport.ConceptMinOutbound != 2 || resolved.ObsidianExport.MaxEntityNameLen != 45 {
		t.Fatalf("unexpected obsidian export defaults: %+v", resolved.ObsidianExport)
	}
}

func TestResolveConfig_ObsidianExportOverrides(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `export:
  obsidian:
    hub_min_refs: 7
    concept_min_refs: 12
    concept_min_outbound: 3
    max_entity_name_len: 40
    stopwords: ["(?i)^todo$", "(?i)^session$"]
    allowlist: ["Q", "Mister"]
    hub_types:
      person: '^(Q|SB|Mister)$'
      project: '^(Cortex|Spear)$'
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	obs, err := ResolveObsidianExportConfig(cfgPath)
	if err != nil {
		t.Fatalf("ResolveObsidianExportConfig: %v", err)
	}
	if obs.HubMinRefs != 7 || obs.ConceptMinRefs != 12 || obs.ConceptMinOutbound != 3 || obs.MaxEntityNameLen != 40 {
		t.Fatalf("unexpected numeric overrides: %+v", obs)
	}
	if len(obs.Stopwords) != 2 || len(obs.Allowlist) != 2 {
		t.Fatalf("expected stopwords+allowlist override, got %+v", obs)
	}
	if obs.HubTypes.Person == "" || obs.HubTypes.Project == "" {
		t.Fatalf("expected hub type regex overrides, got %+v", obs.HubTypes)
	}
}

func TestResolveConfig_ImportDenylistAndSearchSourceBoosts(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `import:
  denylist:
    - pattern: "^(HEARTBEAT_OK|NO_REPLY)$"
      reason: "protocol noise"
search:
  source_boosts:
    - prefix: "memory/"
      weight: 1.4
extract:
  suppress_patterns:
    - pattern: "^heartbeat"
      reason: "protocol noise"
policies:
  decay_rates:
    temporal: 0.2
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if len(resolved.Import.Denylist) != 1 {
		t.Fatalf("expected 1 denylist entry, got %d", len(resolved.Import.Denylist))
	}
	if !resolved.Import.Denylist[0].Matches("HEARTBEAT_OK") {
		t.Fatalf("expected compiled denylist regex to match")
	}
	if len(resolved.Search.SourceBoosts) != 1 {
		t.Fatalf("expected 1 search source boost, got %d", len(resolved.Search.SourceBoosts))
	}
	if resolved.Search.SourceBoosts[0].Prefix != "memory/" || resolved.Search.SourceBoosts[0].Weight != 1.4 {
		t.Fatalf("unexpected search source boost: %+v", resolved.Search.SourceBoosts[0])
	}
	if len(resolved.Extract.SuppressPatterns) != 1 || !resolved.Extract.SuppressPatterns[0].Matches("heartbeat_status") {
		t.Fatalf("expected compiled extract suppression pattern, got %+v", resolved.Extract.SuppressPatterns)
	}
	if resolved.Policies.DecayRates["temporal"] != 0.2 {
		t.Fatalf("expected decay rate override, got %+v", resolved.Policies.DecayRates)
	}
}

func TestResolveConfig_QualityProfileSeedsDefaults(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `profile: personal
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if resolved.Profile != "personal" {
		t.Fatalf("expected profile personal, got %q", resolved.Profile)
	}
	if len(resolved.Import.Denylist) == 0 || len(resolved.Extract.SuppressPatterns) == 0 || len(resolved.Search.SourceBoosts) == 0 {
		t.Fatalf("expected profile to seed defaults, got import=%d extract=%d search=%d", len(resolved.Import.Denylist), len(resolved.Extract.SuppressPatterns), len(resolved.Search.SourceBoosts))
	}
}

func TestResolveConfig_OpenClawIntegrationAutoWithoutConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: filepath.Join(home, "missing-config.yaml")})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if got := resolved.Integrations.OpenClaw.Mode.Value; got != string(IntegrationModeAuto) {
		t.Fatalf("expected default mode auto, got %q", got)
	}
	if resolved.Integrations.OpenClaw.Configured {
		t.Fatalf("expected openclaw configured=false, got %+v", resolved.Integrations.OpenClaw)
	}
	if resolved.Integrations.OpenClaw.EffectiveEnabled {
		t.Fatalf("expected effective_enabled=false without OpenClaw config, got %+v", resolved.Integrations.OpenClaw)
	}
}

func TestResolveConfig_OpenClawIntegrationAutoWithConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	openClawDir := filepath.Join(home, ".openclaw")
	if err := os.MkdirAll(openClawDir, 0o755); err != nil {
		t.Fatalf("mkdir openclaw dir: %v", err)
	}
	openClawConfigPath := filepath.Join(openClawDir, "openclaw.json")
	if err := os.WriteFile(openClawConfigPath, []byte(`{"plugins":{"entries":{}}}`), 0o600); err != nil {
		t.Fatalf("write openclaw config: %v", err)
	}

	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: filepath.Join(home, "missing-config.yaml")})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if !resolved.Integrations.OpenClaw.Configured {
		t.Fatalf("expected openclaw configured=true, got %+v", resolved.Integrations.OpenClaw)
	}
	if !resolved.Integrations.OpenClaw.EffectiveEnabled {
		t.Fatalf("expected effective_enabled=true in auto mode when OpenClaw config exists, got %+v", resolved.Integrations.OpenClaw)
	}
	if resolved.Integrations.OpenClaw.ConfigPath != openClawConfigPath {
		t.Fatalf("expected config path %q, got %q", openClawConfigPath, resolved.Integrations.OpenClaw.ConfigPath)
	}
}

func TestResolveConfig_OpenClawIntegrationEnvOverridesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	yaml := `integrations:
  openclaw:
    mode: disabled
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CORTEX_OPENCLAW_ENABLED", "true")

	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}

	if got := resolved.Integrations.OpenClaw.Mode.Value; got != string(IntegrationModeEnabled) {
		t.Fatalf("expected env override to set mode enabled, got %q", got)
	}
	if resolved.Integrations.OpenClaw.Mode.Source != SourceEnv {
		t.Fatalf("expected env source, got %+v", resolved.Integrations.OpenClaw.Mode)
	}
	if !resolved.Integrations.OpenClaw.EffectiveEnabled {
		t.Fatalf("expected effective_enabled=true when explicitly enabled, got %+v", resolved.Integrations.OpenClaw)
	}
}

func TestResolveAgentTrustConfig_Valid(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `agents:
  opus:
    trust: owner
  x7:
    trust: collaborator
  hawk:
    trust: reader
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	trust, err := ResolveAgentTrustConfig(cfgPath)
	if err != nil {
		t.Fatalf("ResolveAgentTrustConfig: %v", err)
	}
	if len(trust) != 3 {
		t.Fatalf("expected 3 trust entries, got %d", len(trust))
	}
	if trust["opus"].Scope != "read:all write:all" {
		t.Fatalf("unexpected opus scope: %+v", trust["opus"])
	}
	if trust["x7"].Scope != "read:all write:own" {
		t.Fatalf("unexpected x7 scope: %+v", trust["x7"])
	}
	if trust["hawk"].Scope != "read:all write:none" {
		t.Fatalf("unexpected hawk scope: %+v", trust["hawk"])
	}
}

func TestResolveAgentTrustConfig_InvalidTrust(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	yaml := `agents:
  hawk:
    trust: admin
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := ResolveAgentTrustConfig(cfgPath)
	if err == nil {
		t.Fatal("expected invalid trust error")
	}
	if got := err.Error(); got == "" || !containsAll(got, "agents.hawk.trust", "owner", "collaborator", "reader") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}
