package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type ValueSource string

const (
	SourceUnknown ValueSource = "unknown"
	SourceConfig  ValueSource = "config"
	SourceEnv     ValueSource = "env"
	SourceCLI     ValueSource = "cli"
	SourceDefault ValueSource = "default"
)

type ResolvedValue struct {
	Value  string      `json:"value"`
	Source ValueSource `json:"source"`
	From   string      `json:"from,omitempty"`
}

type DenylistEntry struct {
	Pattern  string         `yaml:"pattern" json:"pattern"`
	Reason   string         `yaml:"reason" json:"reason"`
	Compiled *regexp.Regexp `yaml:"-" json:"-"`
}

func (d *DenylistEntry) Compile() error {
	pattern := strings.TrimSpace(d.Pattern)
	if pattern == "" {
		d.Compiled = nil
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	d.Compiled = re
	return nil
}

func (d DenylistEntry) Matches(text string) bool {
	if d.Compiled == nil {
		return false
	}
	return d.Compiled.MatchString(text)
}

type ImportConfig struct {
	Denylist []DenylistEntry `yaml:"denylist" json:"denylist"`
}

type ExtractConfig struct {
	SuppressPatterns []DenylistEntry `yaml:"suppress_patterns" json:"suppress_patterns"`
}

type QualityProfile string

const (
	QualityProfilePersonal QualityProfile = "personal"
	QualityProfileAgentOps QualityProfile = "agent-ops"
	QualityProfileCodebase QualityProfile = "codebase"
	QualityProfileTrading  QualityProfile = "trading"
)

type SearchSourceBoostConfig struct {
	Prefix string  `yaml:"prefix" json:"prefix"`
	Weight float64 `yaml:"weight" json:"weight"`
}

type SearchConfig struct {
	SourceBoosts []SearchSourceBoostConfig `yaml:"source_boosts" json:"source_boosts"`
}

type IntegrationMode string

const (
	IntegrationModeAuto     IntegrationMode = "auto"
	IntegrationModeEnabled  IntegrationMode = "enabled"
	IntegrationModeDisabled IntegrationMode = "disabled"
)

type OpenClawIntegrationConfig struct {
	Mode             ResolvedValue `json:"mode"`
	EffectiveEnabled bool          `json:"effective_enabled"`
	Configured       bool          `json:"configured"`
	ConfigPath       string        `json:"config_path,omitempty"`
}

type IntegrationsConfig struct {
	OpenClaw OpenClawIntegrationConfig `json:"openclaw"`
}

type ResolveOptions struct {
	ConfigPath string
	CLILLM     string
	CLIEmbed   string
	CLIDBPath  string
}

type ReinforcePromotePolicy struct {
	Enabled           bool   `yaml:"enabled" json:"enabled"`
	MinReinforcements int    `yaml:"min_reinforcements" json:"min_reinforcements"`
	MinSources        int    `yaml:"min_sources" json:"min_sources"`
	TargetState       string `yaml:"target_state" json:"target_state"`
}

type DecayRetirePolicy struct {
	Enabled         bool    `yaml:"enabled" json:"enabled"`
	InactiveDays    int     `yaml:"inactive_days" json:"inactive_days"`
	ConfidenceBelow float64 `yaml:"confidence_below" json:"confidence_below"`
	TargetState     string  `yaml:"target_state" json:"target_state"`
}

type ConflictSupersedePolicy struct {
	Enabled              bool    `yaml:"enabled" json:"enabled"`
	RequireStrictlyNewer bool    `yaml:"require_strictly_newer" json:"require_strictly_newer"`
	MinConfidenceDelta   float64 `yaml:"min_confidence_delta" json:"min_confidence_delta"`
}

type PolicyConfig struct {
	ReinforcePromote  ReinforcePromotePolicy  `yaml:"reinforce_promote" json:"reinforce_promote"`
	DecayRetire       DecayRetirePolicy       `yaml:"decay_retire" json:"decay_retire"`
	ConflictSupersede ConflictSupersedePolicy `yaml:"conflict_supersede" json:"conflict_supersede"`
	DecayRates        map[string]float64      `yaml:"decay_rates" json:"decay_rates"`
	PredicatePolicies map[string]string       `yaml:"predicate_policies" json:"predicate_policies"`
}

type AgentTrustRule struct {
	Trust string `yaml:"trust" json:"trust"`
}

type AgentTrustEntry struct {
	AgentID string `json:"agent_id"`
	Trust   string `json:"trust"`
	Scope   string `json:"scope"`
}

type ObsidianHubTypesConfig struct {
	Person   string `yaml:"person" json:"person"`
	Project  string `yaml:"project" json:"project"`
	Strategy string `yaml:"strategy" json:"strategy"`
	System   string `yaml:"system" json:"system"`
}

type ObsidianExportConfig struct {
	HubMinRefs         int                    `yaml:"hub_min_refs" json:"hub_min_refs"`
	ConceptMinRefs     int                    `yaml:"concept_min_refs" json:"concept_min_refs"`
	ConceptMinOutbound int                    `yaml:"concept_min_outbound" json:"concept_min_outbound"`
	MaxEntityNameLen   int                    `yaml:"max_entity_name_len" json:"max_entity_name_len"`
	Stopwords          []string               `yaml:"stopwords" json:"stopwords"`
	Allowlist          []string               `yaml:"allowlist" json:"allowlist"`
	HubTypes           ObsidianHubTypesConfig `yaml:"hub_types" json:"hub_types"`
}

func DefaultObsidianExportConfig() ObsidianExportConfig {
	return ObsidianExportConfig{
		HubMinRefs:         5,
		ConceptMinRefs:     10,
		ConceptMinOutbound: 2,
		MaxEntityNameLen:   45,
		Stopwords:          []string{},
		Allowlist:          []string{},
		HubTypes:           ObsidianHubTypesConfig{},
	}
}

func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		ReinforcePromote: ReinforcePromotePolicy{
			Enabled:           true,
			MinReinforcements: 5,
			MinSources:        3,
			TargetState:       "core",
		},
		DecayRetire: DecayRetirePolicy{
			Enabled:         true,
			InactiveDays:    45,
			ConfidenceBelow: 0.35,
			TargetState:     "retired",
		},
		ConflictSupersede: ConflictSupersedePolicy{
			Enabled:              true,
			RequireStrictlyNewer: true,
			MinConfidenceDelta:   0.02,
		},
		DecayRates: map[string]float64{
			"identity":     0.01,
			"preference":   0.02,
			"config":       0.03,
			"decision":     0.05,
			"state":        0.10,
			"temporal":     0.15,
			"kv":           0.05,
			"relationship": 0.02,
			"location":     0.03,
		},
		PredicatePolicies: map[string]string{
			"references": "append-only",
			"cites":      "append-only",
			"tag":        "multi-valued",
			"tagged":     "multi-valued",
			"related to": "multi-valued",
			"supports":   "multi-valued",
			"uses":       "multi-valued",
		},
	}
}

type ResolvedConfig struct {
	ConfigPath string `json:"config_path"`
	Profile    string `json:"profile,omitempty"`

	DBPath           ResolvedValue `json:"db_path"`
	LLMProvider      ResolvedValue `json:"llm_provider"`
	LLMEnrichModel   ResolvedValue `json:"llm_enrich_model"`
	LLMClassifyModel ResolvedValue `json:"llm_classify_model"`
	LLMExpandModel   ResolvedValue `json:"llm_expand_model"`

	EmbedProvider ResolvedValue `json:"embed_provider"`
	EmbedAPIKey   ResolvedValue `json:"embed_api_key"`
	EmbedEndpoint ResolvedValue `json:"embed_endpoint"`

	Policies       PolicyConfig             `json:"policies"`
	ObsidianExport ObsidianExportConfig     `json:"obsidian_export"`
	Import         ImportConfig             `json:"import"`
	Extract        ExtractConfig            `json:"extract"`
	Search         SearchConfig             `json:"search"`
	Integrations   IntegrationsConfig       `json:"integrations"`
	LLMKeys        map[string]ResolvedValue `json:"llm_keys,omitempty"`
}

type fileConfig struct {
	Profile string `yaml:"profile"`
	DBPath  string `yaml:"db_path"`
	LLM     struct {
		Provider         string `yaml:"provider"`
		APIKey           string `yaml:"api_key"`
		EnrichModel      string `yaml:"enrich_model"`
		EnrichProvider   string `yaml:"enrich_provider"`
		ClassifyModel    string `yaml:"classify_model"`
		ClassifyProvider string `yaml:"classify_provider"`
		ExpandModel      string `yaml:"expand_model"`
		ExpandProvider   string `yaml:"expand_provider"`
	} `yaml:"llm"`
	Embed struct {
		Provider string `yaml:"provider"`
		APIKey   string `yaml:"api_key"`
		Endpoint string `yaml:"endpoint"`
	} `yaml:"embed"`
	Import       ImportConfig  `yaml:"import"`
	Extract      ExtractConfig `yaml:"extract"`
	Search       SearchConfig  `yaml:"search"`
	Integrations struct {
		OpenClaw struct {
			Mode string `yaml:"mode"`
		} `yaml:"openclaw"`
	} `yaml:"integrations"`
	Policies PolicyConfig              `yaml:"policies"`
	Agents   map[string]AgentTrustRule `yaml:"agents"`
	Export   struct {
		Obsidian ObsidianExportConfig `yaml:"obsidian"`
	} `yaml:"export"`
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cortex", "config.yaml")
}

func ResolveConfig(opts ResolveOptions) (ResolvedConfig, error) {
	path := strings.TrimSpace(opts.ConfigPath)
	if path == "" {
		path = DefaultConfigPath()
	}

	out := ResolvedConfig{
		ConfigPath:     path,
		Policies:       DefaultPolicyConfig(),
		ObsidianExport: DefaultObsidianExportConfig(),
		Integrations: IntegrationsConfig{
			OpenClaw: OpenClawIntegrationConfig{
				Mode: ResolvedValue{
					Value:  string(IntegrationModeAuto),
					Source: SourceDefault,
					From:   "built-in default",
				},
			},
		},
		LLMKeys: map[string]ResolvedValue{},
	}

	cfg, err := loadConfig(path)
	if err != nil {
		return out, err
	}

	if cfg != nil {
		out.Profile = strings.TrimSpace(cfg.Profile)
		out.Policies = cfg.Policies
		out.ObsidianExport = cfg.Export.Obsidian
		out.Import = cfg.Import
		out.Extract = cfg.Extract
		out.Search = cfg.Search
		applyIntegrationMode(&out.Integrations.OpenClaw.Mode, cfg.Integrations.OpenClaw.Mode, SourceConfig, path)
		apply(&out.DBPath, cfg.DBPath, SourceConfig, path)
		apply(&out.LLMProvider, cfg.LLM.Provider, SourceConfig, path)
		apply(&out.LLMEnrichModel, firstNonEmpty(cfg.LLM.EnrichModel, cfg.LLM.EnrichProvider), SourceConfig, path)
		apply(&out.LLMClassifyModel, firstNonEmpty(cfg.LLM.ClassifyModel, cfg.LLM.ClassifyProvider), SourceConfig, path)
		apply(&out.LLMExpandModel, firstNonEmpty(cfg.LLM.ExpandModel, cfg.LLM.ExpandProvider), SourceConfig, path)
		apply(&out.EmbedProvider, cfg.Embed.Provider, SourceConfig, path)
		apply(&out.EmbedEndpoint, cfg.Embed.Endpoint, SourceConfig, path)

		if key := strings.TrimSpace(cfg.Embed.APIKey); key != "" {
			out.EmbedAPIKey = ResolvedValue{Value: key, Source: SourceConfig, From: path}
		}

		if key := strings.TrimSpace(cfg.LLM.APIKey); key != "" {
			providers := map[string]struct{}{}
			for _, v := range []string{cfg.LLM.Provider, cfg.LLM.EnrichModel, cfg.LLM.ClassifyModel, cfg.LLM.ExpandModel} {
				p := providerOf(v)
				if p != "" {
					providers[p] = struct{}{}
				}
			}
			if len(providers) == 0 {
				providers["default"] = struct{}{}
			}
			for p := range providers {
				out.LLMKeys[p] = ResolvedValue{Value: key, Source: SourceConfig, From: path}
			}
		}
	}

	applyEnv(&out.DBPath, "CORTEX_DB")
	applyEnv(&out.DBPath, "CORTEX_DB_PATH")

	applyEnv(&out.LLMProvider, "CORTEX_LLM")
	applyEnv(&out.LLMEnrichModel, "CORTEX_LLM_ENRICH")
	applyEnv(&out.LLMClassifyModel, "CORTEX_LLM_CLASSIFY")
	applyEnv(&out.LLMExpandModel, "CORTEX_LLM_EXPAND")

	applyEnv(&out.EmbedProvider, "CORTEX_EMBED")
	applyEnv(&out.EmbedEndpoint, "CORTEX_EMBED_ENDPOINT")
	if v := strings.TrimSpace(os.Getenv("CORTEX_EMBED_API_KEY")); v != "" {
		out.EmbedAPIKey = ResolvedValue{Value: v, Source: SourceEnv, From: "CORTEX_EMBED_API_KEY"}
	}
	applyIntegrationMode(&out.Integrations.OpenClaw.Mode, os.Getenv("CORTEX_OPENCLAW_MODE"), SourceEnv, "CORTEX_OPENCLAW_MODE")
	if enabled, ok := parseEnvBool(os.Getenv("CORTEX_OPENCLAW_ENABLED")); ok {
		mode := string(IntegrationModeDisabled)
		if enabled {
			mode = string(IntegrationModeEnabled)
		}
		out.Integrations.OpenClaw.Mode = ResolvedValue{Value: mode, Source: SourceEnv, From: "CORTEX_OPENCLAW_ENABLED"}
	}

	for env, provider := range map[string]string{
		"OPENROUTER_API_KEY": "openrouter",
		"OPENAI_API_KEY":     "openai",
		"GEMINI_API_KEY":     "google",
		"GOOGLE_API_KEY":     "google",
		"DEEPSEEK_API_KEY":   "deepseek",
	} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			out.LLMKeys[provider] = ResolvedValue{Value: v, Source: SourceEnv, From: env}
		}
	}

	apply(&out.LLMProvider, opts.CLILLM, SourceCLI, "--llm")
	apply(&out.EmbedProvider, opts.CLIEmbed, SourceCLI, "--embed")
	apply(&out.DBPath, opts.CLIDBPath, SourceCLI, "--db")

	if out.DBPath.Value != "" {
		out.DBPath.Value = expandUserPath(out.DBPath.Value)
	}
	resolveOpenClawIntegration(&out.Integrations.OpenClaw)

	return out, nil
}

func ResolvePolicyConfig(configPath string) (PolicyConfig, error) {
	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: configPath})
	if err != nil {
		return PolicyConfig{}, err
	}
	return resolved.Policies, nil
}

func ResolveObsidianExportConfig(configPath string) (ObsidianExportConfig, error) {
	resolved, err := ResolveConfig(ResolveOptions{ConfigPath: configPath})
	if err != nil {
		return ObsidianExportConfig{}, err
	}
	return resolved.ObsidianExport, nil
}

func ResolveAgentTrustConfig(configPath string) (map[string]AgentTrustEntry, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = DefaultConfigPath()
	}

	cfg, err := loadConfig(path)
	if err != nil {
		return nil, err
	}

	entries := map[string]AgentTrustEntry{}
	if cfg == nil || len(cfg.Agents) == 0 {
		return entries, nil
	}

	for rawAgentID, rule := range cfg.Agents {
		agentID := strings.TrimSpace(rawAgentID)
		if agentID == "" {
			return nil, fmt.Errorf("parsing %s: agents contains an empty agent id", path)
		}
		trust := strings.ToLower(strings.TrimSpace(rule.Trust))
		if trust == "" {
			return nil, fmt.Errorf("parsing %s: agents.%s.trust is required", path, agentID)
		}
		scope, ok := AgentTrustScope(trust)
		if !ok {
			return nil, fmt.Errorf("parsing %s: agents.%s.trust=%q is invalid (allowed: owner, collaborator, reader)", path, agentID, trust)
		}
		entries[agentID] = AgentTrustEntry{
			AgentID: agentID,
			Trust:   trust,
			Scope:   scope,
		}
	}

	return entries, nil
}

func AgentTrustScope(trustLevel string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(trustLevel)) {
	case "owner":
		return "read:all write:all", true
	case "collaborator":
		return "read:all write:own", true
	case "reader":
		return "read:all write:none", true
	default:
		return "", false
	}
}

func (r ResolvedConfig) EffectiveLLMModel(purpose, fallback string) ResolvedValue {
	purpose = strings.ToLower(strings.TrimSpace(purpose))

	candidates := []ResolvedValue{}
	switch purpose {
	case "enrich":
		candidates = append(candidates, r.LLMEnrichModel)
	case "classify":
		candidates = append(candidates, r.LLMClassifyModel)
	case "expand":
		candidates = append(candidates, r.LLMExpandModel)
	}
	candidates = append(candidates, r.LLMProvider)

	for _, c := range candidates {
		if strings.TrimSpace(c.Value) == "" {
			continue
		}
		if strings.Contains(c.Value, "/") {
			return c
		}
		if fallback != "" && strings.HasPrefix(strings.ToLower(fallback), strings.ToLower(strings.TrimSpace(c.Value))+"/") {
			return ResolvedValue{Value: fallback, Source: c.Source, From: c.From}
		}
	}

	if strings.TrimSpace(fallback) != "" {
		return ResolvedValue{Value: fallback, Source: SourceDefault, From: "built-in default"}
	}
	return ResolvedValue{}
}

func (r ResolvedConfig) APIKeyForProvider(providerOrModel string) ResolvedValue {
	provider := providerOf(providerOrModel)
	if provider == "" {
		return ResolvedValue{}
	}
	if v, ok := r.LLMKeys[provider]; ok && strings.TrimSpace(v.Value) != "" {
		return v
	}
	if v, ok := r.LLMKeys["default"]; ok && strings.TrimSpace(v.Value) != "" {
		return v
	}
	return ResolvedValue{}
}

func providerOf(providerOrModel string) string {
	v := strings.ToLower(strings.TrimSpace(providerOrModel))
	if v == "" {
		return ""
	}
	if idx := strings.Index(v, "/"); idx > 0 {
		return v[:idx]
	}
	return v
}

func apply(dst *ResolvedValue, raw string, source ValueSource, from string) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return
	}
	*dst = ResolvedValue{Value: v, Source: source, From: from}
}

func applyEnv(dst *ResolvedValue, envKey string) {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		*dst = ResolvedValue{Value: v, Source: SourceEnv, From: envKey}
	}
}

func applyIntegrationMode(dst *ResolvedValue, raw string, source ValueSource, from string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	mode, ok := normalizeIntegrationMode(raw)
	if !ok {
		return
	}
	*dst = ResolvedValue{Value: mode, Source: source, From: from}
}

func loadConfig(path string) (*fileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	cfg := fileConfig{Policies: DefaultPolicyConfig()}
	cfg.Export.Obsidian = DefaultObsidianExportConfig()
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	applyQualityProfileDefaults(&cfg)
	for i := range cfg.Import.Denylist {
		if err := cfg.Import.Denylist[i].Compile(); err != nil {
			return nil, fmt.Errorf("parsing %s import.denylist[%d].pattern: %w", path, i, err)
		}
	}
	for i := range cfg.Extract.SuppressPatterns {
		if err := cfg.Extract.SuppressPatterns[i].Compile(); err != nil {
			return nil, fmt.Errorf("parsing %s extract.suppress_patterns[%d].pattern: %w", path, i, err)
		}
	}
	return &cfg, nil
}

func applyQualityProfileDefaults(cfg *fileConfig) {
	if cfg == nil {
		return
	}
	profile := QualityProfile(strings.ToLower(strings.TrimSpace(cfg.Profile)))
	if profile == "" {
		return
	}

	switch profile {
	case QualityProfilePersonal, QualityProfileAgentOps, QualityProfileCodebase, QualityProfileTrading:
	default:
		return
	}

	if len(cfg.Import.Denylist) == 0 {
		cfg.Import.Denylist = []DenylistEntry{
			{Pattern: "^(Pre-compaction memory flush|System:.*Post-compaction)", Reason: "compaction system messages"},
			{Pattern: "cortex (search|embed|cleanup|optimize|import)", Reason: "cortex CLI commands in transcripts"},
			{Pattern: "^(HEARTBEAT_OK|NO_REPLY)$", Reason: "agent protocol noise"},
			{Pattern: "Run these test queries and verify", Reason: "agent task prompts"},
		}
	}

	if len(cfg.Extract.SuppressPatterns) == 0 {
		cfg.Extract.SuppressPatterns = []DenylistEntry{
			{Pattern: "(?i)^current.*(time|date|timestamp)", Reason: "ephemeral temporal headers"},
			{Pattern: "(?i)^(heartbeat|heartbeat_status)", Reason: "heartbeat protocol noise"},
		}
	}

	if len(cfg.Search.SourceBoosts) == 0 {
		cfg.Search.SourceBoosts = []SearchSourceBoostConfig{
			{Prefix: "MEMORY.md", Weight: 1.5},
			{Prefix: "memory/", Weight: 1.3},
			{Prefix: "shared-context/", Weight: 1.2},
			{Prefix: "/var/folders/", Weight: 0.7},
			{Prefix: "/tmp/", Weight: 0.7},
			{Prefix: "auto-capture", Weight: 0.6},
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func expandUserPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func normalizeIntegrationMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(IntegrationModeAuto):
		return string(IntegrationModeAuto), true
	case string(IntegrationModeEnabled), "true", "1", "on", "yes":
		return string(IntegrationModeEnabled), true
	case string(IntegrationModeDisabled), "false", "0", "off", "no":
		return string(IntegrationModeDisabled), true
	default:
		return "", false
	}
}

func parseEnvBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes", "enabled":
		return true, true
	case "0", "false", "off", "no", "disabled":
		return false, true
	default:
		return false, false
	}
}

func resolveOpenClawIntegration(cfg *OpenClawIntegrationConfig) {
	if cfg == nil {
		return
	}
	mode, ok := normalizeIntegrationMode(cfg.Mode.Value)
	if !ok {
		mode = string(IntegrationModeAuto)
		cfg.Mode = ResolvedValue{Value: mode, Source: SourceDefault, From: "built-in default"}
	} else {
		cfg.Mode.Value = mode
		if cfg.Mode.Source == "" {
			cfg.Mode.Source = SourceDefault
			cfg.Mode.From = "built-in default"
		}
	}

	cfg.ConfigPath = defaultOpenClawConfigPath()
	if cfg.ConfigPath != "" {
		if _, err := os.Stat(cfg.ConfigPath); err == nil {
			cfg.Configured = true
		}
	}

	cfg.EffectiveEnabled = mode == string(IntegrationModeEnabled) || (mode == string(IntegrationModeAuto) && cfg.Configured)
}

func defaultOpenClawConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".openclaw", "openclaw.json")
}
