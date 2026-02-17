package extract

import (
	"fmt"
	"os"
	"strings"
)

// LLMConfig holds LLM provider configuration.
type LLMConfig struct {
	Provider      string // "ollama", "openai", "deepseek", "openrouter", "custom"
	Model         string // model name
	Endpoint      string // full API URL
	APIKey        string
	ContextWindow int // max tokens (0 = use provider default)
	MaxRetries    int // default: 3
	TimeoutSecs   int // per-request timeout (default: 60)
}

// ParseLLMFlag parses "--llm provider/model" format.
// Handles complex model names with slashes and colons like "openrouter/google/gemini-2.0-flash-exp:free"
func ParseLLMFlag(flag string) (*LLMConfig, error) {
	if flag == "" {
		return nil, fmt.Errorf("empty LLM flag")
	}

	// Find the first slash to split provider from model
	slashIdx := strings.Index(flag, "/")
	if slashIdx == -1 {
		return nil, fmt.Errorf("invalid --llm format: expected 'provider/model', got %q", flag)
	}

	provider := flag[:slashIdx]
	model := flag[slashIdx+1:]

	if provider == "" {
		return nil, fmt.Errorf("empty provider in --llm flag: %q", flag)
	}
	if model == "" {
		return nil, fmt.Errorf("empty model in --llm flag: %q", flag)
	}

	config := &LLMConfig{
		Provider:    provider,
		Model:       model,
		MaxRetries:  3,
		TimeoutSecs: 60,
	}

	// Set provider-specific defaults
	switch provider {
	case "ollama":
		config.Endpoint = "http://localhost:11434/v1/chat/completions"
		config.ContextWindow = 4096
		// No API key needed for Ollama
	case "openai":
		config.Endpoint = "https://api.openai.com/v1/chat/completions"
		config.APIKey = os.Getenv("OPENAI_API_KEY")
		config.ContextWindow = 128000
	case "deepseek":
		config.Endpoint = "https://api.deepseek.com/v1/chat/completions"
		config.APIKey = os.Getenv("DEEPSEEK_API_KEY")
		config.ContextWindow = 64000
	case "openrouter":
		config.Endpoint = "https://openrouter.ai/api/v1/chat/completions"
		config.APIKey = os.Getenv("OPENROUTER_API_KEY")
		config.ContextWindow = 128000
	case "custom":
		// Custom provider - user must set endpoint and key via env vars
		config.Endpoint = os.Getenv("CORTEX_LLM_ENDPOINT")
		config.APIKey = os.Getenv("CORTEX_LLM_API_KEY")
		config.ContextWindow = 4096 // Conservative default
	default:
		return nil, fmt.Errorf("unknown provider %q. Supported: ollama, openai, deepseek, openrouter, custom", provider)
	}

	// Allow environment variable overrides
	if endpoint := os.Getenv("CORTEX_LLM_ENDPOINT"); endpoint != "" {
		config.Endpoint = endpoint
	}
	if apiKey := os.Getenv("CORTEX_LLM_API_KEY"); apiKey != "" {
		config.APIKey = apiKey
	}

	return config, nil
}

// ResolveLLMConfig resolves configuration from all sources.
// Priority: CLI flag > CORTEX_LLM env var > config file (not implemented yet)
func ResolveLLMConfig(cliFlag string) (*LLMConfig, error) {
	// 1. CLI flag takes priority
	if cliFlag != "" {
		return ParseLLMFlag(cliFlag)
	}

	// 2. Environment variable
	if envLLM := os.Getenv("CORTEX_LLM"); envLLM != "" {
		config, err := ParseLLMFlag(envLLM)
		if err != nil {
			return nil, fmt.Errorf("parsing CORTEX_LLM env var: %w", err)
		}
		return config, nil
	}

	// 3. Config file support not implemented in v1
	// Future: load from ~/.cortex/config.yaml

	return nil, nil // No LLM configuration found
}

// Validate checks if the LLM configuration is valid and complete.
func (c *LLMConfig) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	if c.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}

	// API key validation (except for Ollama which doesn't need one)
	if c.Provider != "ollama" && c.APIKey == "" {
		return fmt.Errorf("API key is required for provider %q (set via environment variable)", c.Provider)
	}

	if c.ContextWindow <= 0 {
		return fmt.Errorf("context window must be positive")
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative")
	}
	if c.TimeoutSecs <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	return nil
}