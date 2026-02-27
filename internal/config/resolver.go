package config

import (
	"fmt"
	"os"
	"path/filepath"
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

type ResolveOptions struct {
	ConfigPath string
	CLILLM     string
	CLIEmbed   string
	CLIDBPath  string
}

type ResolvedConfig struct {
	ConfigPath string `json:"config_path"`

	DBPath           ResolvedValue `json:"db_path"`
	LLMProvider      ResolvedValue `json:"llm_provider"`
	LLMEnrichModel   ResolvedValue `json:"llm_enrich_model"`
	LLMClassifyModel ResolvedValue `json:"llm_classify_model"`
	LLMExpandModel   ResolvedValue `json:"llm_expand_model"`

	EmbedProvider ResolvedValue `json:"embed_provider"`
	EmbedAPIKey   ResolvedValue `json:"embed_api_key"`
	EmbedEndpoint ResolvedValue `json:"embed_endpoint"`

	LLMKeys map[string]ResolvedValue `json:"llm_keys,omitempty"`
}

type fileConfig struct {
	DBPath string `yaml:"db_path"`
	LLM    struct {
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
		ConfigPath: path,
		LLMKeys:    map[string]ResolvedValue{},
	}

	cfg, err := loadConfig(path)
	if err != nil {
		return out, err
	}

	if cfg != nil {
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

	return out, nil
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

func loadConfig(path string) (*fileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg fileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
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
