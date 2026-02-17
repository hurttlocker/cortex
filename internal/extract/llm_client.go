package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// System prompt for LLM extraction (v1)
const systemPromptV1 = `You are a fact extraction system. Extract structured facts from the provided text.

RULES:
1. Extract ONLY explicitly stated facts - never infer or assume
2. Each fact must have a source quote that is EXACT text from the input
3. Use confidence 0.0-1.0 based on how clearly stated the fact is
4. Return ONLY the JSON array, no additional text

FACT TYPES:
- kv: Key-value pairs (name: John, age: 25, price: $100)
- relationship: Connections between entities (Alice works at Acme, Bob is Alice's manager)  
- preference: Likes/dislikes (prefers dark mode, dislikes meetings)
- temporal: Time-related facts (meeting on Tuesday, deadline March 15, created in 2023)
- identity: Personal identifiers (email, phone, address, username)
- location: Geographic references (lives in NYC, office in SF, meeting at Starbucks)
- decision: Choices made (chose option A, decided to postpone, approved the proposal)
- state: Current conditions (status: active, running, temperature: 72°F)

JSON SCHEMA:
{
  "facts": [
    {
      "subject": "entity this fact is about (empty string for key-value facts)",
      "predicate": "relationship/attribute/key name", 
      "object": "value/related entity",
      "type": "one of the valid fact types above",
      "confidence": 0.85,
      "source_quote": "exact text from input this was extracted from"
    }
  ]
}

EXAMPLES:
Input: "Alice (alice@company.com) is the project manager for the Q1 launch. She prefers morning meetings and decided to move the deadline to March 15th."
Output:
{
  "facts": [
    {
      "subject": "Alice",
      "predicate": "email", 
      "object": "alice@company.com",
      "type": "identity",
      "confidence": 1.0,
      "source_quote": "Alice (alice@company.com)"
    },
    {
      "subject": "Alice",
      "predicate": "role",
      "object": "project manager", 
      "type": "relationship",
      "confidence": 0.9,
      "source_quote": "Alice (alice@company.com) is the project manager for the Q1 launch"
    },
    {
      "subject": "Alice", 
      "predicate": "prefers",
      "object": "morning meetings",
      "type": "preference",
      "confidence": 0.9,
      "source_quote": "She prefers morning meetings"
    },
    {
      "subject": "Alice",
      "predicate": "decided",
      "object": "move deadline to March 15th", 
      "type": "decision",
      "confidence": 0.9,
      "source_quote": "decided to move the deadline to March 15th"
    }
  ]
}`

// LLMClient handles communication with OpenAI-compatible APIs.
type LLMClient struct {
	config LLMConfig
	http   *http.Client
}

// ChatRequest represents an OpenAI-compatible chat completion request.
type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ChatMessage represents a message in the conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResponseFormat specifies the expected response format.
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatResponse represents an OpenAI-compatible chat completion response.
type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// LLMExtractionResponse represents the structured response from the LLM.
type LLMExtractionResponse struct {
	Facts []ExtractedFact `json:"facts"`
}

// NewLLMClient creates a new LLM client with the given configuration.
func NewLLMClient(config *LLMConfig) *LLMClient {
	return &LLMClient{
		config: *config,
		http: &http.Client{
			Timeout: time.Duration(config.TimeoutSecs) * time.Second,
		},
	}
}

// Extract extracts facts from text using the configured LLM.
func (c *LLMClient) Extract(ctx context.Context, text string) ([]ExtractedFact, error) {
	// Build the chat messages
	messages := []ChatMessage{
		{
			Role:    "system",
			Content: systemPromptV1,
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Extract facts from this text:\n\n---\n%s\n---\n\nReturn JSON matching the schema.", text),
		},
	}

	// Create request
	req := ChatRequest{
		Model:       c.config.Model,
		Messages:    messages,
		Temperature: 0.1,
		ResponseFormat: &ResponseFormat{
			Type: "json_object",
		},
	}

	// Retry logic with exponential backoff
	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		facts, err := c.attemptExtraction(ctx, req)
		if err == nil {
			return facts, nil
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

	return nil, fmt.Errorf("LLM extraction failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
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

// attemptExtraction makes a single extraction attempt.
func (c *LLMClient) attemptExtraction(ctx context.Context, req ChatRequest) ([]ExtractedFact, error) {
	// Send request to LLM
	resp, err := c.sendChatRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	// Extract message content
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in LLM response")
	}

	content := resp.Choices[0].Message.Content
	if content == "" {
		return nil, fmt.Errorf("empty response from LLM")
	}

	// Parse JSON response
	facts, err := c.parseExtractionResponse(content)
	if err != nil {
		return nil, fmt.Errorf("parsing LLM response: %w", err)
	}

	// Validate and enrich facts
	validFacts := make([]ExtractedFact, 0, len(facts))
	for _, fact := range facts {
		if err := c.validateFact(fact); err != nil {
			continue // Skip invalid facts
		}

		// Set extraction method and decay rate
		fact.ExtractionMethod = "llm"
		if rate, ok := DecayRates[fact.FactType]; ok {
			fact.DecayRate = rate
		} else {
			fact.DecayRate = DecayRates["kv"] // default
		}

		validFacts = append(validFacts, fact)
	}

	return validFacts, nil
}

// sendChatRequest sends a chat completion request to the LLM API.
func (c *LLMClient) sendChatRequest(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
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
	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("parsing response JSON: %w", err)
	}

	return &chatResp, nil
}

// parseExtractionResponse parses the LLM's JSON response into facts.
func (c *LLMClient) parseExtractionResponse(content string) ([]ExtractedFact, error) {
	content = strings.TrimSpace(content)
	
	var response LLMExtractionResponse
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return response.Facts, nil
}

// validateFact validates a fact extracted by the LLM.
func (c *LLMClient) validateFact(fact ExtractedFact) error {
	// Required fields
	if fact.Predicate == "" {
		return fmt.Errorf("predicate is required")
	}
	if fact.Object == "" {
		return fmt.Errorf("object is required")
	}
	if fact.SourceQuote == "" {
		return fmt.Errorf("source_quote is required")
	}

	// Validate fact type
	validTypes := map[string]bool{
		"kv": true, "relationship": true, "preference": true, "temporal": true,
		"identity": true, "location": true, "decision": true, "state": true,
	}
	if !validTypes[fact.FactType] {
		return fmt.Errorf("invalid fact type: %s", fact.FactType)
	}

	// Validate confidence range
	if fact.Confidence < 0.0 || fact.Confidence > 1.0 {
		return fmt.Errorf("confidence must be between 0.0 and 1.0, got %.2f", fact.Confidence)
	}

	return nil
}