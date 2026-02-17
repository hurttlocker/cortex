// Package embed provides text-to-vector embedding via OpenAI-compatible APIs.
//
// Supports multiple providers:
// - ollama: http://localhost:11434/v1/embeddings
// - openai: https://api.openai.com/v1/embeddings  
// - openrouter: https://openrouter.ai/api/v1/embeddings
// - deepseek: https://api.deepseek.com/v1/embeddings
// - custom: user-specified endpoint
//
// All providers use the OpenAI-compatible /v1/embeddings format for consistency.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Embedder generates embedding vectors from text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
}

// EmbedConfig holds embedding provider configuration.
type EmbedConfig struct {
	Provider      string // "ollama", "openai", "deepseek", "openrouter", "custom"
	Model         string // model name  
	Endpoint      string // full API URL
	APIKey        string
	MaxRetries    int // default: 3
	TimeoutSecs   int // per-request timeout (default: 60)
	dimensions    int // auto-detected on first call
}

// EmbedRequest represents an OpenAI-compatible embeddings request.
type EmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbedResponse represents an OpenAI-compatible embeddings response.
type EmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// HTTPError represents an HTTP error with additional context.
type HTTPError struct {
	StatusCode int
	Message    string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// Client implements Embedder with HTTP API calls.
type Client struct {
	config EmbedConfig
	http   *http.Client
}

// ParseEmbedFlag parses "--embed provider/model" format.
// Handles complex model names with slashes and colons like "openrouter/sentence-transformers/all-MiniLM-L6-v2"
func ParseEmbedFlag(flag string) (*EmbedConfig, error) {
	if flag == "" {
		return nil, fmt.Errorf("empty embedding flag")
	}

	// Find the first slash to split provider from model
	slashIdx := strings.Index(flag, "/")
	if slashIdx == -1 {
		return nil, fmt.Errorf("invalid --embed format: expected 'provider/model', got %q", flag)
	}

	provider := flag[:slashIdx]
	model := flag[slashIdx+1:]

	if provider == "" {
		return nil, fmt.Errorf("empty provider in --embed flag: %q", flag)
	}
	if model == "" {
		return nil, fmt.Errorf("empty model in --embed flag: %q", flag)
	}

	config := &EmbedConfig{
		Provider:    provider,
		Model:       model,
		MaxRetries:  3,
		TimeoutSecs: 60,
	}

	// Set provider-specific defaults
	switch provider {
	case "ollama":
		config.Endpoint = "http://localhost:11434/v1/embeddings"
		// No API key needed for Ollama
	case "openai":
		config.Endpoint = "https://api.openai.com/v1/embeddings"
		config.APIKey = os.Getenv("OPENAI_API_KEY")
	case "deepseek":
		config.Endpoint = "https://api.deepseek.com/v1/embeddings"
		config.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	case "openrouter":
		config.Endpoint = "https://openrouter.ai/api/v1/embeddings"
		config.APIKey = os.Getenv("OPENROUTER_API_KEY")
	case "custom":
		// Custom provider - user must set endpoint and key via env vars
		config.Endpoint = os.Getenv("CORTEX_EMBED_ENDPOINT")
		config.APIKey = os.Getenv("CORTEX_EMBED_API_KEY")
	default:
		return nil, fmt.Errorf("unknown provider %q. Supported: ollama, openai, deepseek, openrouter, custom", provider)
	}

	// Allow environment variable overrides
	if endpoint := os.Getenv("CORTEX_EMBED_ENDPOINT"); endpoint != "" {
		config.Endpoint = endpoint
	}
	if apiKey := os.Getenv("CORTEX_EMBED_API_KEY"); apiKey != "" {
		config.APIKey = apiKey
	}

	return config, nil
}

// NewEmbedConfig is an alias for ParseEmbedFlag for consistency with LLM config patterns.
func NewEmbedConfig(providerModel string) (*EmbedConfig, error) {
	return ParseEmbedFlag(providerModel)
}

// ResolveEmbedConfig resolves configuration from all sources.
// Priority: CLI flag > CORTEX_EMBED env var > config file (not implemented yet)
func ResolveEmbedConfig(cliFlag string) (*EmbedConfig, error) {
	// 1. CLI flag takes priority
	if cliFlag != "" {
		return ParseEmbedFlag(cliFlag)
	}

	// 2. Environment variable
	if envEmbed := os.Getenv("CORTEX_EMBED"); envEmbed != "" {
		config, err := ParseEmbedFlag(envEmbed)
		if err != nil {
			return nil, fmt.Errorf("parsing CORTEX_EMBED env var: %w", err)
		}
		return config, nil
	}

	// 3. Config file support not implemented in v1
	// Future: load from ~/.cortex/config.yaml

	return nil, nil // No embedding configuration found
}

// Validate checks if the embedding configuration is valid and complete.
func (c *EmbedConfig) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	if c.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}

	// API key validation (except for Ollama and test providers which don't need one)
	if c.Provider != "ollama" && c.Provider != "test" && c.APIKey == "" {
		return fmt.Errorf("API key is required for provider %q (set via environment variable)", c.Provider)
	}

	if c.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative")
	}
	if c.TimeoutSecs <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	return nil
}

// NewClient creates a new embedding client with the given configuration.
func NewClient(config *EmbedConfig) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}
	
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &Client{
		config: *config,
		http: &http.Client{
			Timeout: time.Duration(config.TimeoutSecs) * time.Second,
		},
	}, nil
}

// Embed generates an embedding vector for a single text.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}

	embeddings, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(embeddings) != 1 {
		return nil, fmt.Errorf("expected 1 embedding, got %d", len(embeddings))
	}

	return embeddings[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Filter out empty texts
	nonEmptyTexts := make([]string, 0, len(texts))
	indexMap := make([]int, 0, len(texts)) // Maps result index to original index
	for i, text := range texts {
		if strings.TrimSpace(text) != "" {
			nonEmptyTexts = append(nonEmptyTexts, text)
			indexMap = append(indexMap, i)
		}
	}

	if len(nonEmptyTexts) == 0 {
		// Return zero vectors for all empty texts
		result := make([][]float32, len(texts))
		return result, nil
	}

	// Retry logic with exponential backoff
	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		embeddings, err := c.attemptEmbedBatch(ctx, nonEmptyTexts)
		if err == nil {
			// Map results back to original indices
			result := make([][]float32, len(texts))
			for i, embedding := range embeddings {
				if i < len(indexMap) {
					result[indexMap[i]] = embedding
				}
			}
			
			// Update dimensions from first non-empty embedding
			for _, emb := range embeddings {
				if len(emb) > 0 {
					c.config.dimensions = len(emb)
					break
				}
			}

			return result, nil
		}

		lastErr = err

		// Check if we should retry
		if attempt == c.config.MaxRetries {
			break
		}

		// Exponential backoff: 1s, 2s, 4s
		backoffDuration := time.Duration(1<<attempt) * time.Second

		// For rate limit errors, respect Retry-After if present
		if httpErr, ok := err.(*HTTPError); ok && httpErr.StatusCode == 429 {
			if retryAfter := httpErr.RetryAfter; retryAfter > 0 {
				backoffDuration = retryAfter
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoffDuration):
			// Continue to next attempt
		}
	}

	return nil, fmt.Errorf("embedding failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
}

// Dimensions returns the dimensionality of embeddings from this client.
// Returns 0 if no embeddings have been generated yet.
func (c *Client) Dimensions() int {
	return c.config.dimensions
}

// attemptEmbedBatch makes a single embedding attempt.
func (c *Client) attemptEmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	// Create request
	req := EmbedRequest{
		Model: c.config.Model,
		Input: texts,
	}

	// Marshal request
	requestBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.Endpoint, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	// OpenRouter-specific headers
	if c.config.Provider == "openrouter" {
		httpReq.Header.Set("HTTP-Referer", "https://github.com/hurttlocker/cortex")
		httpReq.Header.Set("X-Title", "Cortex")
	}

	// Send request
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// Handle HTTP errors
	if resp.StatusCode != 200 {
		var retryAfter time.Duration
		if retryAfterHeader := resp.Header.Get("Retry-After"); retryAfterHeader != "" {
			if seconds, err := strconv.Atoi(retryAfterHeader); err == nil {
				retryAfter = time.Duration(seconds) * time.Second
			}
		}

		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
			RetryAfter: retryAfter,
		}
	}

	// Parse response
	var embedResp EmbedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("parsing response JSON: %w", err)
	}

	// Extract embeddings in correct order
	if len(embedResp.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embedResp.Data))
	}

	embeddings := make([][]float32, len(texts))
	for _, data := range embedResp.Data {
		if data.Index < 0 || data.Index >= len(embeddings) {
			return nil, fmt.Errorf("invalid embedding index: %d", data.Index)
		}
		embeddings[data.Index] = data.Embedding
	}

	return embeddings, nil
}