// Package reason provides LLM-powered reasoning over Cortex memories.
// Supports local (ollama) and cloud (openrouter) providers via OpenAI-compatible chat API.
package reason

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// LLM is a chat completion client for reasoning tasks.
type LLM struct {
	provider string // "ollama", "openrouter"
	model    string
	endpoint string
	apiKey   string
	client   *http.Client
}

// Default models for different use cases.
const (
	DefaultInteractiveModel = "google/gemini-2.5-flash" // Sub-3s, cheapest reliable
	DefaultCronModel        = "deepseek/deepseek-v3.2"  // Deep analysis, cron/background
	DefaultLocalModel       = "phi4-mini"               // Zero data leaves machine
)

// LLMConfig configures an LLM provider.
type LLMConfig struct {
	Provider string // "ollama" or "openrouter"
	Model    string // e.g., "phi4-mini", "minimax/minimax-m2.5"
	APIKey   string // required for openrouter
}

// ChatMessage represents a single message in a chat completion request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the OpenAI-compatible chat completion request.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

// ChatResponse is the OpenAI-compatible chat completion response.
type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// LLMResult holds the output of a chat completion call.
type LLMResult struct {
	Content          string        `json:"content"`
	Model            string        `json:"model"`
	Provider         string        `json:"provider"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	Duration         time.Duration `json:"duration"`
}

// NewLLM creates a new LLM client.
func NewLLM(cfg LLMConfig) (*LLM, error) {
	l := &LLM{
		provider: cfg.Provider,
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		client: &http.Client{
			Timeout: 5 * time.Minute, // reasoning can be slow on CPU
		},
	}

	switch cfg.Provider {
	case "ollama":
		host := os.Getenv("OLLAMA_HOST")
		if host == "" {
			host = "http://localhost:11434"
		}
		l.endpoint = host + "/v1/chat/completions"
	case "openrouter":
		l.endpoint = "https://openrouter.ai/api/v1/chat/completions"
		if l.apiKey == "" {
			l.apiKey = os.Getenv("OPENROUTER_API_KEY")
		}
		if l.apiKey == "" {
			return nil, fmt.Errorf("openrouter requires OPENROUTER_API_KEY env var or --api-key flag")
		}
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q (use 'ollama' or 'openrouter')", cfg.Provider)
	}

	return l, nil
}

// Chat sends a chat completion request and returns the result.
func (l *LLM) Chat(ctx context.Context, messages []ChatMessage, maxTokens int) (*LLMResult, error) {
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	req := ChatRequest{
		Model:       l.model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: 0.3, // Low temperature for analytical tasks
		Stream:      false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if l.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+l.apiKey)
	}
	if l.provider == "openrouter" {
		httpReq.Header.Set("HTTP-Referer", "https://github.com/hurttlocker/cortex")
		httpReq.Header.Set("X-Title", "Cortex Reason")
	}

	start := time.Now()
	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}
	defer resp.Body.Close()
	duration := time.Since(start)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w (body: %s)", err, truncate(string(respBody), 200))
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned no choices")
	}

	content := chatResp.Choices[0].Message.Content

	// Strip qwen3's <think>...</think> blocks if present
	content = stripThinkingTags(content)

	return &LLMResult{
		Content:          strings.TrimSpace(content),
		Model:            l.model,
		Provider:         l.provider,
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
		Duration:         duration,
	}, nil
}

// ParseProviderModel splits "provider/model" into provider and model.
// If no "/" is found, assumes ollama as the provider.
func ParseProviderModel(s string) (provider, model string) {
	if i := strings.Index(s, "/"); i >= 0 {
		provider = s[:i]
		model = s[i+1:]
		// Handle openrouter's double-slash models like "minimax/minimax-m2.5"
		if provider != "ollama" && provider != "openrouter" {
			// Likely an openrouter model like "minimax/minimax-m2.5"
			provider = "openrouter"
			model = s
		}
		return
	}
	// No slash — local ollama model
	return "ollama", s
}

// stripThinkingTags removes <think>...</think> blocks from model output.
// Common in qwen3 and deepseek-r1 models.
func stripThinkingTags(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s, "</think>")
		if end == -1 {
			// Unclosed think tag — strip from start to end
			s = s[:start]
			break
		}
		s = s[:start] + s[end+len("</think>"):]
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
