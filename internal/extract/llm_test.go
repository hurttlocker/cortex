package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Mock LLM server helpers

func newMockLLMServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		// Return mock response
		resp := ChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Role: "assistant", Content: response}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func newMockLLMServerWithStatusCode(t *testing.T, statusCode int, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(response))
	}))
}

func newMockLLMServerWithRetryAfter(t *testing.T, retryAfter int) *httptest.Server {
	t.Helper()
	callCount := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call returns 429 with Retry-After
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Rate limited"))
			return
		}

		// Second call succeeds
		resp := ChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Role: "assistant", Content: validFactsResponse}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// Test responses
const validFactsResponse = `{
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
      "source_quote": "Alice is the project manager"
    }
  ]
}`

const invalidJSONResponse = `{
  "facts": [
    {
      "subject": "Alice",
      "predicate": "email",
      "object": "alice@company.com",
      "type": "identity",
      "confidence": 1.0,
      "source_quote": "Alice (alice@company.com)"
    }
  // Missing closing bracket`

const invalidFactResponse = `{
  "facts": [
    {
      "subject": "Alice",
      "predicate": "",
      "object": "alice@company.com",
      "type": "identity",
      "confidence": 1.0,
      "source_quote": "Alice (alice@company.com)"
    }
  ]
}`

// Config parsing tests

func TestParseLLMFlag_Ollama(t *testing.T) {
	config, err := ParseLLMFlag("ollama/gemma2:2b")
	if err != nil {
		t.Fatalf("ParseLLMFlag failed: %v", err)
	}

	if config.Provider != "ollama" {
		t.Errorf("Expected provider 'ollama', got %q", config.Provider)
	}
	if config.Model != "gemma2:2b" {
		t.Errorf("Expected model 'gemma2:2b', got %q", config.Model)
	}
	if config.Endpoint != "http://localhost:11434/v1/chat/completions" {
		t.Errorf("Expected Ollama endpoint, got %q", config.Endpoint)
	}
	if config.ContextWindow != 4096 {
		t.Errorf("Expected context window 4096, got %d", config.ContextWindow)
	}
	if config.APIKey != "" {
		t.Errorf("Expected no API key for Ollama, got %q", config.APIKey)
	}
}

func TestParseLLMFlag_OpenAI(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	config, err := ParseLLMFlag("openai/gpt-4o-mini")
	if err != nil {
		t.Fatalf("ParseLLMFlag failed: %v", err)
	}

	if config.Provider != "openai" {
		t.Errorf("Expected provider 'openai', got %q", config.Provider)
	}
	if config.Model != "gpt-4o-mini" {
		t.Errorf("Expected model 'gpt-4o-mini', got %q", config.Model)
	}
	if config.Endpoint != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("Expected OpenAI endpoint, got %q", config.Endpoint)
	}
	if config.APIKey != "test-key" {
		t.Errorf("Expected API key 'test-key', got %q", config.APIKey)
	}
}

func TestParseLLMFlag_DeepSeek(t *testing.T) {
	os.Setenv("DEEPSEEK_API_KEY", "deepseek-key")
	defer os.Unsetenv("DEEPSEEK_API_KEY")

	config, err := ParseLLMFlag("deepseek/v3")
	if err != nil {
		t.Fatalf("ParseLLMFlag failed: %v", err)
	}

	if config.Provider != "deepseek" {
		t.Errorf("Expected provider 'deepseek', got %q", config.Provider)
	}
	if config.Model != "v3" {
		t.Errorf("Expected model 'v3', got %q", config.Model)
	}
	if config.ContextWindow != 64000 {
		t.Errorf("Expected context window 64000, got %d", config.ContextWindow)
	}
}

func TestParseLLMFlag_OpenRouter(t *testing.T) {
	os.Setenv("OPENROUTER_API_KEY", "openrouter-key")
	defer os.Unsetenv("OPENROUTER_API_KEY")

	config, err := ParseLLMFlag("openrouter/google/gemini-2.0-flash-exp:free")
	if err != nil {
		t.Fatalf("ParseLLMFlag failed: %v", err)
	}

	if config.Provider != "openrouter" {
		t.Errorf("Expected provider 'openrouter', got %q", config.Provider)
	}
	if config.Model != "google/gemini-2.0-flash-exp:free" {
		t.Errorf("Expected model 'google/gemini-2.0-flash-exp:free', got %q", config.Model)
	}
	if config.Endpoint != "https://openrouter.ai/api/v1/chat/completions" {
		t.Errorf("Expected OpenRouter endpoint, got %q", config.Endpoint)
	}
}

func TestParseLLMFlag_OpenRouterComplexModel(t *testing.T) {
	config, err := ParseLLMFlag("openrouter/anthropic/claude-3.5-sonnet:beta")
	if err != nil {
		t.Fatalf("ParseLLMFlag failed: %v", err)
	}

	if config.Provider != "openrouter" {
		t.Errorf("Expected provider 'openrouter', got %q", config.Provider)
	}
	if config.Model != "anthropic/claude-3.5-sonnet:beta" {
		t.Errorf("Expected model 'anthropic/claude-3.5-sonnet:beta', got %q", config.Model)
	}
}

func TestParseLLMFlag_InvalidFormat(t *testing.T) {
	_, err := ParseLLMFlag("justmodel")
	if err == nil {
		t.Error("Expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "expected 'provider/model'") {
		t.Errorf("Expected format error, got: %v", err)
	}
}

func TestParseLLMFlag_UnknownProvider(t *testing.T) {
	_, err := ParseLLMFlag("unknown/model")
	if err == nil {
		t.Error("Expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("Expected unknown provider error, got: %v", err)
	}
}

func TestParseLLMFlag_EmptyProvider(t *testing.T) {
	_, err := ParseLLMFlag("/model")
	if err == nil {
		t.Error("Expected error for empty provider")
	}
}

func TestParseLLMFlag_EmptyModel(t *testing.T) {
	_, err := ParseLLMFlag("openai/")
	if err == nil {
		t.Error("Expected error for empty model")
	}
}

func TestParseLLMFlag_Empty(t *testing.T) {
	_, err := ParseLLMFlag("")
	if err == nil {
		t.Error("Expected error for empty flag")
	}
}

func TestParseLLMFlag_EnvironmentOverrides(t *testing.T) {
	os.Setenv("CORTEX_LLM_ENDPOINT", "custom-endpoint")
	os.Setenv("CORTEX_LLM_API_KEY", "custom-key")
	defer os.Unsetenv("CORTEX_LLM_ENDPOINT")
	defer os.Unsetenv("CORTEX_LLM_API_KEY")

	config, err := ParseLLMFlag("openai/gpt-4o-mini")
	if err != nil {
		t.Fatalf("ParseLLMFlag failed: %v", err)
	}

	if config.Endpoint != "custom-endpoint" {
		t.Errorf("Expected custom endpoint, got %q", config.Endpoint)
	}
	if config.APIKey != "custom-key" {
		t.Errorf("Expected custom API key, got %q", config.APIKey)
	}
}

func TestResolveLLMConfig_CLIFlag(t *testing.T) {
	os.Setenv("CORTEX_LLM", "deepseek/v3")
	defer os.Unsetenv("CORTEX_LLM")

	// CLI flag should take priority
	config, err := ResolveLLMConfig("ollama/gemma2:2b")
	if err != nil {
		t.Fatalf("ResolveLLMConfig failed: %v", err)
	}

	if config.Provider != "ollama" {
		t.Errorf("Expected CLI flag to take priority, got provider %q", config.Provider)
	}
}

func TestResolveLLMConfig_EnvVar(t *testing.T) {
	os.Setenv("CORTEX_LLM", "deepseek/v3")
	os.Setenv("DEEPSEEK_API_KEY", "test-key")
	defer os.Unsetenv("CORTEX_LLM")
	defer os.Unsetenv("DEEPSEEK_API_KEY")

	config, err := ResolveLLMConfig("")
	if err != nil {
		t.Fatalf("ResolveLLMConfig failed: %v", err)
	}

	if config.Provider != "deepseek" {
		t.Errorf("Expected provider from env var, got %q", config.Provider)
	}
}

func TestResolveLLMConfig_None(t *testing.T) {
	config, err := ResolveLLMConfig("")
	if err != nil {
		t.Fatalf("ResolveLLMConfig failed: %v", err)
	}

	if config != nil {
		t.Errorf("Expected nil config when no LLM configured, got %+v", config)
	}
}

// LLM client tests

func TestLLMClient_ValidResponse(t *testing.T) {
	server := newMockLLMServer(t, validFactsResponse)
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    3,
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	facts, err := client.Extract(ctx, "Alice (alice@company.com) is the project manager")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(facts) != 2 {
		t.Fatalf("Expected 2 facts, got %d", len(facts))
	}

	// Check first fact
	fact := facts[0]
	if fact.Subject != "Alice" {
		t.Errorf("Expected subject 'Alice', got %q", fact.Subject)
	}
	if fact.Predicate != "email" {
		t.Errorf("Expected predicate 'email', got %q", fact.Predicate)
	}
	if fact.Object != "alice@company.com" {
		t.Errorf("Expected object 'alice@company.com', got %q", fact.Object)
	}
	if fact.FactType != "identity" {
		t.Errorf("Expected type 'identity', got %q", fact.FactType)
	}
	if fact.ExtractionMethod != "llm" {
		t.Errorf("Expected extraction method 'llm', got %q", fact.ExtractionMethod)
	}
}

func TestLLMClient_InvalidJSON(t *testing.T) {
	server := newMockLLMServer(t, invalidJSONResponse)
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    1, // Only one retry for this test
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	_, err := client.Extract(ctx, "test text")
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("Expected JSON error, got: %v", err)
	}
}

func TestLLMClient_RetrySuccess(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 2 {
			// First two calls return invalid JSON
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(invalidJSONResponse))
			return
		}
		// Third call succeeds
		resp := ChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Role: "assistant", Content: validFactsResponse}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    3,
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	facts, err := client.Extract(ctx, "test text")
	if err != nil {
		t.Fatalf("Expected retry to succeed, got error: %v", err)
	}
	if len(facts) != 2 {
		t.Errorf("Expected 2 facts, got %d", len(facts))
	}
	if callCount != 3 {
		t.Errorf("Expected 3 calls (2 failures + 1 success), got %d", callCount)
	}
}

func TestLLMClient_AllRetriesFail(t *testing.T) {
	server := newMockLLMServer(t, invalidJSONResponse)
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    2,
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	_, err := client.Extract(ctx, "test text")
	if err == nil {
		t.Error("Expected error after all retries exhausted")
	}
	if !strings.Contains(err.Error(), "failed after 3 attempts") {
		t.Errorf("Expected retry count in error, got: %v", err)
	}
}

func TestLLMClient_NetworkError(t *testing.T) {
	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      "http://nonexistent:9999",
		ContextWindow: 4096,
		MaxRetries:    1,
		TimeoutSecs:   1, // Short timeout
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	_, err := client.Extract(ctx, "test text")
	if err == nil {
		t.Error("Expected network error")
	}
}

func TestLLMClient_RateLimit(t *testing.T) {
	server := newMockLLMServerWithRetryAfter(t, 1)
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    3,
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	start := time.Now()
	facts, err := client.Extract(ctx, "test text")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Expected rate limit retry to succeed, got: %v", err)
	}
	if len(facts) != 2 {
		t.Errorf("Expected 2 facts, got %d", len(facts))
	}
	if elapsed < time.Second {
		t.Errorf("Expected at least 1 second delay for retry-after, got %v", elapsed)
	}
}

func TestLLMClient_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Role: "assistant", Content: ""}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    1,
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	_, err := client.Extract(ctx, "test text")
	if err == nil {
		t.Error("Expected error for empty response")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("Expected empty response error, got: %v", err)
	}
}

func TestLLMClient_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    1,
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	_, err := client.Extract(ctx, "test text")
	if err == nil {
		t.Error("Expected error for no choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("Expected no choices error, got: %v", err)
	}
}

func TestLLMClient_OpenRouterHeaders(t *testing.T) {
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header
		resp := ChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Role: "assistant", Content: validFactsResponse}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &LLMConfig{
		Provider:      "openrouter",
		Model:         "test",
		Endpoint:      server.URL,
		APIKey:        "test-key",
		ContextWindow: 4096,
		MaxRetries:    3,
		TimeoutSecs:   30,
	}

	client := NewLLMClient(config)
	ctx := context.Background()

	_, err := client.Extract(ctx, "test text")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// Check OpenRouter-specific headers
	if headers.Get("HTTP-Referer") != "https://github.com/hurttlocker/cortex" {
		t.Errorf("Expected HTTP-Referer header, got %q", headers.Get("HTTP-Referer"))
	}
	if headers.Get("X-Title") != "Cortex" {
		t.Errorf("Expected X-Title header, got %q", headers.Get("X-Title"))
	}
}

// Chunking tests

func TestChunkDocument_SmallDoc(t *testing.T) {
	text := "This is a small document."
	chunks := ChunkDocument(text, 1000)

	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk for small document, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("Expected chunk to equal input text")
	}
}

func TestChunkDocument_LargeDoc(t *testing.T) {
	// Create a large document
	paragraph := strings.Repeat("This is a paragraph with some text. ", 20) + "\n\n"
	text := strings.Repeat(paragraph, 50) // Large document

	chunks := ChunkDocument(text, 1000) // Small context window to force chunking

	if len(chunks) <= 1 {
		t.Fatalf("Expected multiple chunks for large document, got %d", len(chunks))
	}

	// Check that chunks have reasonable overlap
	if len(chunks) > 1 {
		chunk1 := chunks[0]
		chunk2 := chunks[1]

		// Should have some overlap (but not test exact amount since it's rough)
		if len(chunk1) < 500 {
			t.Errorf("First chunk seems too small: %d chars", len(chunk1))
		}
		if len(chunk2) < 500 {
			t.Errorf("Second chunk seems too small: %d chars", len(chunk2))
		}
	}
}

func TestChunkDocument_EmptyDoc(t *testing.T) {
	chunks := ChunkDocument("", 1000)

	if len(chunks) != 0 {
		t.Errorf("Expected empty slice for empty document, got %d chunks", len(chunks))
	}
}

func TestChunkDocument_ParagraphBoundary(t *testing.T) {
	text := "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."
	chunks := ChunkDocument(text, 20) // Very small context to force paragraph-aware splitting

	if len(chunks) == 0 {
		t.Fatalf("Expected at least one chunk")
	}

	// Should try to split at paragraph boundaries when possible
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			t.Errorf("Found empty chunk: %q", chunk)
		}
	}
}

// Validation tests

func TestValidateFact_Valid(t *testing.T) {
	client := &LLMClient{}
	fact := ExtractedFact{
		Subject:     "Alice",
		Predicate:   "email",
		Object:      "alice@example.com",
		FactType:    "identity",
		Confidence:  0.9,
		SourceQuote: "Alice's email is alice@example.com",
	}

	err := client.validateFact(fact)
	if err != nil {
		t.Errorf("Expected valid fact to pass validation, got error: %v", err)
	}
}

func TestValidateFact_MissingPredicate(t *testing.T) {
	client := &LLMClient{}
	fact := ExtractedFact{
		Subject:     "Alice",
		Predicate:   "",
		Object:      "alice@example.com",
		FactType:    "identity",
		Confidence:  0.9,
		SourceQuote: "Alice's email is alice@example.com",
	}

	err := client.validateFact(fact)
	if err == nil {
		t.Error("Expected error for missing predicate")
	}
}

func TestValidateFact_MissingObject(t *testing.T) {
	client := &LLMClient{}
	fact := ExtractedFact{
		Subject:     "Alice",
		Predicate:   "email",
		Object:      "",
		FactType:    "identity",
		Confidence:  0.9,
		SourceQuote: "Alice's email is alice@example.com",
	}

	err := client.validateFact(fact)
	if err == nil {
		t.Error("Expected error for missing object")
	}
}

func TestValidateFact_MissingSourceQuote(t *testing.T) {
	client := &LLMClient{}
	fact := ExtractedFact{
		Subject:     "Alice",
		Predicate:   "email",
		Object:      "alice@example.com",
		FactType:    "identity",
		Confidence:  0.9,
		SourceQuote: "",
	}

	err := client.validateFact(fact)
	if err == nil {
		t.Error("Expected error for missing source quote")
	}
}

func TestValidateFact_InvalidType(t *testing.T) {
	client := &LLMClient{}
	fact := ExtractedFact{
		Subject:     "Alice",
		Predicate:   "email",
		Object:      "alice@example.com",
		FactType:    "invalid_type",
		Confidence:  0.9,
		SourceQuote: "Alice's email is alice@example.com",
	}

	err := client.validateFact(fact)
	if err == nil {
		t.Error("Expected error for invalid fact type")
	}
}

func TestValidateFact_ConfidenceOutOfRange(t *testing.T) {
	client := &LLMClient{}

	// Test confidence > 1.0
	fact := ExtractedFact{
		Subject:     "Alice",
		Predicate:   "email",
		Object:      "alice@example.com",
		FactType:    "identity",
		Confidence:  1.5,
		SourceQuote: "Alice's email is alice@example.com",
	}

	err := client.validateFact(fact)
	if err == nil {
		t.Error("Expected error for confidence > 1.0")
	}

	// Test confidence < 0.0
	fact.Confidence = -0.1
	err = client.validateFact(fact)
	if err == nil {
		t.Error("Expected error for confidence < 0.0")
	}
}

// Integration tests

func TestPipeline_WithLLM(t *testing.T) {
	server := newMockLLMServer(t, validFactsResponse)
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    3,
		TimeoutSecs:   30,
	}

	pipeline := NewPipeline(config)
	ctx := context.Background()

	text := "Alice (alice@company.com) is the project manager. **Name:** Bob"
	metadata := map[string]string{"source_file": "test.md"}

	facts, err := pipeline.Extract(ctx, text, metadata)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// Should have facts from both rule-based and LLM extraction
	rulesCount := 0
	llmCount := 0
	for _, fact := range facts {
		if fact.ExtractionMethod == "llm" {
			llmCount++
		} else if fact.ExtractionMethod == "rules" {
			rulesCount++
		}
	}

	if rulesCount == 0 {
		t.Error("Expected some rule-based facts")
	}
	if llmCount == 0 {
		t.Error("Expected some LLM facts")
	}

	t.Logf("Extracted %d total facts: %d rules, %d LLM", len(facts), rulesCount, llmCount)
}

func TestPipeline_LLMFailureFallback(t *testing.T) {
	// Create a server that always fails
	server := newMockLLMServerWithStatusCode(t, 500, "Internal Server Error")
	defer server.Close()

	config := &LLMConfig{
		Provider:      "ollama",
		Model:         "test",
		Endpoint:      server.URL,
		ContextWindow: 4096,
		MaxRetries:    1,
		TimeoutSecs:   30,
	}

	pipeline := NewPipeline(config)
	ctx := context.Background()

	text := "**Name:** Alice\n**Email:** alice@example.com"
	metadata := map[string]string{"source_file": "test.md"}

	facts, err := pipeline.Extract(ctx, text, metadata)
	if err != nil {
		t.Fatalf("Extract should not fail when LLM fails, got error: %v", err)
	}

	// Should still have rule-based facts
	if len(facts) == 0 {
		t.Error("Expected fallback to rule-based extraction")
	}

	// All facts should be from rules
	for _, fact := range facts {
		if fact.ExtractionMethod != "rules" {
			t.Errorf("Expected only rule-based facts, got %s", fact.ExtractionMethod)
		}
	}
}

func TestPipeline_NoLLM(t *testing.T) {
	pipeline := NewPipeline() // No LLM config
	ctx := context.Background()

	text := "**Name:** Alice\n**Email:** alice@example.com"
	metadata := map[string]string{"source_file": "test.md"}

	facts, err := pipeline.Extract(ctx, text, metadata)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// Should have only rule-based facts
	for _, fact := range facts {
		if fact.ExtractionMethod != "rules" {
			t.Errorf("Expected only rule-based facts, got %s", fact.ExtractionMethod)
		}
	}
}
