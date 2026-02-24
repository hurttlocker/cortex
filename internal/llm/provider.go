// Package llm provides a provider-agnostic LLM adapter for Cortex.
// Used by query expansion, conflict resolution, and other v0.9.0 features.
// Zero external dependencies — uses net/http directly.
package llm

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Provider is the interface for LLM completions.
type Provider interface {
	// Complete sends a prompt and returns the response text.
	Complete(ctx context.Context, prompt string, opts CompletionOpts) (string, error)
	// Name returns a human-readable provider name (e.g., "google/gemini-3-flash").
	Name() string
}

// CompletionOpts configures a single completion request.
type CompletionOpts struct {
	MaxTokens   int     // Max tokens to generate (0 = provider default)
	Temperature float64 // 0.0-2.0 (0 = deterministic)
	Model       string  // Override model for this request (empty = use provider default)
	Format      string  // "json" for structured output, empty for plain text
	System      string  // System prompt (optional)
}

// Config holds provider configuration.
type Config struct {
	Provider string // "google", "openrouter"
	Model    string // e.g., "gemini-3-flash", "openai/gpt-5.1-codex-mini"
	APIKey   string // API key (empty = read from env)
	BaseURL  string // Optional URL override
}

// NewProvider creates an LLM provider from the given config.
func NewProvider(cfg Config) (Provider, error) {
	switch strings.ToLower(cfg.Provider) {
	case "google":
		key := cfg.APIKey
		if key == "" {
			key = os.Getenv("GEMINI_API_KEY")
		}
		if key == "" {
			key = os.Getenv("GOOGLE_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("google provider requires GEMINI_API_KEY or GOOGLE_API_KEY env var")
		}
		model := cfg.Model
		if model == "" {
			model = "gemini-2.5-flash"
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "https://generativelanguage.googleapis.com/v1beta"
		}
		return &googleProvider{
			apiKey:  key,
			model:   model,
			baseURL: baseURL,
		}, nil

	case "openrouter":
		key := cfg.APIKey
		if key == "" {
			key = os.Getenv("OPENROUTER_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("openrouter provider requires OPENROUTER_API_KEY env var")
		}
		model := cfg.Model
		if model == "" {
			model = "openai/gpt-4o-mini"
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
		return &openrouterProvider{
			apiKey:  key,
			model:   model,
			baseURL: baseURL,
		}, nil

	default:
		return nil, fmt.Errorf("unknown LLM provider: %q (supported: google, openrouter)", cfg.Provider)
	}
}

// ParseLLMFlag parses a --llm flag value into a Config.
// Format: "provider/model" e.g., "google/gemini-3-flash", "openrouter/openai/gpt-5.1-codex-mini"
func ParseLLMFlag(flag string) (Config, error) {
	if flag == "" {
		return Config{Provider: "google", Model: "gemini-2.5-flash"}, nil
	}

	parts := strings.SplitN(flag, "/", 2)
	if len(parts) < 2 {
		return Config{}, fmt.Errorf("invalid --llm format %q: expected provider/model (e.g., google/gemini-3-flash)", flag)
	}

	provider := strings.ToLower(parts[0])
	model := parts[1]

	switch provider {
	case "google", "openrouter":
		return Config{Provider: provider, Model: model}, nil
	default:
		return Config{}, fmt.Errorf("unknown provider %q in --llm flag (supported: google, openrouter)", provider)
	}
}
