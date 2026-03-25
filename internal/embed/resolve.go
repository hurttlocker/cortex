package embed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	cfgresolver "github.com/hurttlocker/cortex/internal/config"
)

const ollamaDetectTimeout = 750 * time.Millisecond

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// ResolveEmbedConfig resolves configuration from all sources.
//
// Resolution order:
// 1. Explicit CLI/config/env provider
// 2. Ollama running with an embedding model available
// 3. Local ONNX model already downloaded
// 4. Built-in ONNX fallback (auto-downloads on first use)
func ResolveEmbedConfig(cliFlag string) (*EmbedConfig, error) {
	resolved, err := cfgresolver.ResolveConfig(cfgresolver.ResolveOptions{CLIEmbed: cliFlag})
	if err != nil {
		return nil, err
	}

	flag := strings.TrimSpace(cliFlag)
	if flag != "" {
		return parseResolvedEmbedFlag(flag, &resolved, "explicit", "CLI flag")
	}

	if explicit := strings.TrimSpace(resolved.EmbedProvider.Value); explicit != "" {
		from := strings.TrimSpace(resolved.EmbedProvider.From)
		if from == "" {
			from = string(resolved.EmbedProvider.Source)
		}
		return parseResolvedEmbedFlag(explicit, &resolved, "explicit", fmt.Sprintf("from %s", from))
	}

	if model, detail, ok := detectOllamaEmbedModel(); ok {
		cfg, err := ParseEmbedFlag("ollama/" + model)
		if err != nil {
			return nil, err
		}
		cfg.ResolvedFrom = "auto-detected"
		cfg.ResolvedDetail = detail
		return cfg, nil
	}

	spec := DefaultONNXModelSpec()
	cfg, err := ParseEmbedFlag("onnx/" + spec.Key)
	if err != nil {
		return nil, err
	}
	cfg.ResolvedFrom = "auto-detected"
	if cfg.WillDownload {
		cfg.ResolvedDetail = "no Ollama embed model found, downloading built-in ONNX on first use"
	} else {
		cfg.ResolvedDetail = "no Ollama embed model found, using built-in ONNX"
	}
	return cfg, nil
}

func parseResolvedEmbedFlag(flag string, resolved *cfgresolver.ResolvedConfig, source, detail string) (*EmbedConfig, error) {
	flag = normalizeEmbedFlag(flag)
	config, err := ParseEmbedFlag(flag)
	if err != nil {
		return nil, err
	}
	config.ResolvedFrom = source
	config.ResolvedDetail = detail

	if resolved == nil {
		return config, nil
	}
	if strings.TrimSpace(config.APIKey) == "" {
		if strings.TrimSpace(resolved.EmbedAPIKey.Value) != "" {
			config.APIKey = resolved.EmbedAPIKey.Value
		} else if rv := resolved.APIKeyForProvider(config.Provider); strings.TrimSpace(rv.Value) != "" {
			config.APIKey = rv.Value
		}
	}
	if config.Provider != "onnx" && strings.TrimSpace(resolved.EmbedEndpoint.Value) != "" {
		config.Endpoint = resolved.EmbedEndpoint.Value
	}
	return config, nil
}

func normalizeEmbedFlag(flag string) string {
	flag = strings.TrimSpace(flag)
	if strings.Contains(flag, "/") {
		return flag
	}
	switch strings.ToLower(flag) {
	case "ollama":
		return "ollama/all-minilm"
	case "onnx":
		return "onnx/all-minilm-l6-v2"
	case "openrouter":
		return "openrouter/text-embedding-3-small"
	case "openai":
		return "openai/text-embedding-3-small"
	case "deepseek":
		return "deepseek/deepseek-embedding"
	default:
		return flag
	}
}

func detectOllamaEmbedModel() (string, string, bool) {
	models, err := listOllamaModels()
	if err != nil || len(models) == 0 {
		return "", "", false
	}

	preferred := []string{"all-minilm", "nomic-embed-text", "mxbai-embed-large"}
	for _, want := range preferred {
		for _, model := range models {
			if canonicalOllamaModelName(model) == want {
				return model, fmt.Sprintf("Ollama detected with %s", model), true
			}
		}
	}

	sort.Strings(models)
	for _, model := range models {
		if looksLikeOllamaEmbedModel(model) {
			return model, fmt.Sprintf("Ollama detected with %s", model), true
		}
	}

	return "", "", false
}

func listOllamaModels() ([]string, error) {
	if strings.TrimSpace(os.Getenv("CORTEX_DISABLE_OLLAMA_AUTODETECT")) == "1" {
		return nil, fmt.Errorf("ollama autodetect disabled")
	}

	client := &http.Client{Timeout: ollamaDetectTimeout}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags returned HTTP %d", resp.StatusCode)
	}

	var payload ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	unique := make(map[string]struct{}, len(payload.Models))
	for _, model := range payload.Models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = strings.TrimSpace(model.Model)
		}
		if name == "" {
			continue
		}
		unique[name] = struct{}{}
	}

	models := make([]string, 0, len(unique))
	for model := range unique {
		models = append(models, model)
	}
	return models, nil
}

func canonicalOllamaModelName(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	tagIdx := strings.LastIndex(model, ":")
	if tagIdx > strings.LastIndex(model, "/") {
		model = model[:tagIdx]
	}
	return model
}

func looksLikeOllamaEmbedModel(model string) bool {
	base := canonicalOllamaModelName(model)
	for _, token := range []string{"embed", "minilm", "bge", "e5", "gte", "jina"} {
		if strings.Contains(base, token) {
			return true
		}
	}
	return false
}

func ExpectedDimensions(cfg *EmbedConfig) int {
	if cfg == nil {
		return 0
	}
	if cfg.dimensions > 0 {
		return cfg.dimensions
	}
	switch cfg.Provider {
	case "onnx":
		spec, err := ResolveONNXModelSpec(cfg.Model)
		if err == nil {
			return spec.Dimensions
		}
	case "ollama":
		switch canonicalOllamaModelName(cfg.Model) {
		case "all-minilm":
			return 384
		case "nomic-embed-text":
			return 768
		case "mxbai-embed-large":
			return 1024
		}
	}
	return 0
}

func SpeedHint(cfg *EmbedConfig) string {
	if cfg == nil {
		return ""
	}
	if cfg.Provider != "onnx" {
		return ""
	}
	spec, err := ResolveONNXModelSpec(cfg.Model)
	if err != nil {
		return ""
	}
	return spec.SpeedHint
}
