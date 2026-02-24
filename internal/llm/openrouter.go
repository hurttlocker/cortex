package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// openrouterProvider implements Provider using the OpenRouter API (OpenAI-compatible).
type openrouterProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  http.Client
}

// OpenRouter request/response types (OpenAI-compatible).
type orRequest struct {
	Model          string          `json:"model"`
	Messages       []orMessage     `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *orResponseFmt  `json:"response_format,omitempty"`
}

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orResponseFmt struct {
	Type string `json:"type"`
}

type orResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *orUsage `json:"usage,omitempty"`
	Error *orError `json:"error,omitempty"`
}

type orUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type orError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func (o *openrouterProvider) Name() string {
	return "openrouter/" + o.model
}

func (o *openrouterProvider) Complete(ctx context.Context, prompt string, opts CompletionOpts) (string, error) {
	model := o.model
	if opts.Model != "" {
		model = opts.Model
	}

	messages := make([]orMessage, 0, 2)
	if opts.System != "" {
		messages = append(messages, orMessage{Role: "system", Content: opts.System})
	}
	messages = append(messages, orMessage{Role: "user", Content: prompt})

	req := orRequest{
		Model:       model,
		Messages:    messages,
		Temperature: opts.Temperature,
	}
	if opts.MaxTokens > 0 {
		req.MaxTokens = opts.MaxTokens
	}
	if strings.ToLower(opts.Format) == "json" {
		req.ResponseFormat = &orResponseFmt{Type: "json_object"}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := o.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/hurttlocker/cortex")
	httpReq.Header.Set("X-Title", "Cortex Memory")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var orResp orResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if orResp.Error != nil {
		return "", fmt.Errorf("openrouter API error: %s", orResp.Error.Message)
	}

	if len(orResp.Choices) == 0 {
		return "", fmt.Errorf("empty response from openrouter API")
	}

	return strings.TrimSpace(orResp.Choices[0].Message.Content), nil
}
