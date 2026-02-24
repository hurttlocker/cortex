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

// googleProvider implements Provider using Google AI Studio (Gemini) REST API.
type googleProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  http.Client
}

// Google AI request/response types.
type googleRequest struct {
	Contents         []googleContent   `json:"contents"`
	SystemInstruction *googleContent   `json:"systemInstruction,omitempty"`
	GenerationConfig *googleGenConfig  `json:"generationConfig,omitempty"`
}

type googleContent struct {
	Parts []googlePart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type googlePart struct {
	Text string `json:"text"`
}

type googleGenConfig struct {
	MaxOutputTokens  int     `json:"maxOutputTokens,omitempty"`
	Temperature      float64 `json:"temperature"`
	ResponseMimeType string  `json:"responseMimeType,omitempty"`
}

type googleResponse struct {
	Candidates []struct {
		Content struct {
			Parts []googlePart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *googleUsage `json:"usageMetadata,omitempty"`
	Error         *googleError `json:"error,omitempty"`
}

type googleUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type googleError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func (g *googleProvider) Name() string {
	return "google/" + g.model
}

func (g *googleProvider) Complete(ctx context.Context, prompt string, opts CompletionOpts) (string, error) {
	model := g.model
	if opts.Model != "" {
		model = opts.Model
	}

	req := googleRequest{
		Contents: []googleContent{
			{
				Parts: []googlePart{{Text: prompt}},
				Role:  "user",
			},
		},
	}

	if opts.System != "" {
		req.SystemInstruction = &googleContent{
			Parts: []googlePart{{Text: opts.System}},
		}
	}

	genConfig := &googleGenConfig{
		Temperature: opts.Temperature,
	}
	if opts.MaxTokens > 0 {
		genConfig.MaxOutputTokens = opts.MaxTokens
	}
	if strings.ToLower(opts.Format) == "json" {
		genConfig.ResponseMimeType = "application/json"
	}
	req.GenerationConfig = genConfig

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", g.baseURL, model, g.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var gResp googleResponse
	if err := json.Unmarshal(respBody, &gResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if gResp.Error != nil {
		return "", fmt.Errorf("google API error: %s (code %d)", gResp.Error.Message, gResp.Error.Code)
	}

	if len(gResp.Candidates) == 0 || len(gResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from google API")
	}

	return strings.TrimSpace(gResp.Candidates[0].Content.Parts[0].Text), nil
}
