package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseEmbedFlag(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		want    *EmbedConfig
		wantErr bool
	}{
		{
			name: "ollama simple",
			flag: "ollama/all-minilm",
			want: &EmbedConfig{
				Provider:    "ollama",
				Model:       "all-minilm",
				Endpoint:    "http://localhost:11434/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
		},
		{
			name: "openai simple",
			flag: "openai/text-embedding-3-small",
			want: &EmbedConfig{
				Provider:    "openai",
				Model:       "text-embedding-3-small",
				Endpoint:    "https://api.openai.com/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
		},
		{
			name: "openrouter complex model",
			flag: "openrouter/sentence-transformers/all-MiniLM-L6-v2",
			want: &EmbedConfig{
				Provider:    "openrouter",
				Model:       "sentence-transformers/all-MiniLM-L6-v2",
				Endpoint:    "https://openrouter.ai/api/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
		},
		{
			name:    "empty flag",
			flag:    "",
			wantErr: true,
		},
		{
			name:    "no slash",
			flag:    "ollama",
			wantErr: true,
		},
		{
			name:    "empty provider",
			flag:    "/model",
			wantErr: true,
		},
		{
			name:    "empty model",
			flag:    "provider/",
			wantErr: true,
		},
		{
			name:    "unknown provider",
			flag:    "unknown/model",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEmbedFlag(tt.flag)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEmbedFlag() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if got.Provider != tt.want.Provider {
				t.Errorf("Provider = %v, want %v", got.Provider, tt.want.Provider)
			}
			if got.Model != tt.want.Model {
				t.Errorf("Model = %v, want %v", got.Model, tt.want.Model)
			}
			if got.Endpoint != tt.want.Endpoint {
				t.Errorf("Endpoint = %v, want %v", got.Endpoint, tt.want.Endpoint)
			}
			if got.MaxRetries != tt.want.MaxRetries {
				t.Errorf("MaxRetries = %v, want %v", got.MaxRetries, tt.want.MaxRetries)
			}
			if got.TimeoutSecs != tt.want.TimeoutSecs {
				t.Errorf("TimeoutSecs = %v, want %v", got.TimeoutSecs, tt.want.TimeoutSecs)
			}
		})
	}
}

func TestEmbedConfig_Validate(t *testing.T) {
	tests := []struct {
		name   string
		config EmbedConfig
		want   bool
	}{
		{
			name: "valid ollama",
			config: EmbedConfig{
				Provider:    "ollama",
				Model:       "all-minilm",
				Endpoint:    "http://localhost:11434/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
			want: true,
		},
		{
			name: "valid openai",
			config: EmbedConfig{
				Provider:    "openai",
				Model:       "text-embedding-3-small",
				Endpoint:    "https://api.openai.com/v1/embeddings",
				APIKey:      "sk-test",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
			want: true,
		},
		{
			name: "missing provider",
			config: EmbedConfig{
				Model:       "all-minilm",
				Endpoint:    "http://localhost:11434/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
			want: false,
		},
		{
			name: "missing model",
			config: EmbedConfig{
				Provider:    "ollama",
				Endpoint:    "http://localhost:11434/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
			want: false,
		},
		{
			name: "missing endpoint",
			config: EmbedConfig{
				Provider:    "ollama",
				Model:       "all-minilm",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
			want: false,
		},
		{
			name: "missing api key for openai",
			config: EmbedConfig{
				Provider:    "openai",
				Model:       "text-embedding-3-small",
				Endpoint:    "https://api.openai.com/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 60,
			},
			want: false,
		},
		{
			name: "negative retries",
			config: EmbedConfig{
				Provider:    "ollama",
				Model:       "all-minilm",
				Endpoint:    "http://localhost:11434/v1/embeddings",
				MaxRetries:  -1,
				TimeoutSecs: 60,
			},
			want: false,
		},
		{
			name: "zero timeout",
			config: EmbedConfig{
				Provider:    "ollama",
				Model:       "all-minilm",
				Endpoint:    "http://localhost:11434/v1/embeddings",
				MaxRetries:  3,
				TimeoutSecs: 0,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			got := err == nil
			if got != tt.want {
				t.Errorf("Validate() = %v, want %v, error: %v", got, tt.want, err)
			}
		})
	}
}

// Mock embedding server
func mockEmbeddingServer(t *testing.T, responses map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
		}

		var req EmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
		}

		// Check if we have a mock response for this model
		if mockResp, ok := responses[req.Model]; ok {
			if err, ok := mockResp.(error); ok {
				// Return error response
				w.WriteHeader(500)
				w.Write([]byte(err.Error()))
				return
			}
			if resp, ok := mockResp.(EmbedResponse); ok {
				// Return success response
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
				return
			}
		}

		// Default response: create embeddings based on input length
		resp := EmbedResponse{
			Data: make([]struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}, len(req.Input)),
		}

		for i, text := range req.Input {
			// Create a simple embedding based on text length and content
			embedding := make([]float32, 384) // Common embedding dimension
			for j := range embedding {
				embedding[j] = float32(len(text)+j) * 0.001
			}

			resp.Data[i] = struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				Embedding: embedding,
				Index:     i,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestEmbed_SingleText(t *testing.T) {
	server := mockEmbeddingServer(t, map[string]interface{}{})
	defer server.Close()

	config := &EmbedConfig{
		Provider:    "test",
		Model:       "test-model",
		Endpoint:    server.URL,
		MaxRetries:  1,
		TimeoutSecs: 5,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	embedding, err := client.Embed(ctx, "test text")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(embedding) != 384 {
		t.Errorf("Expected embedding length 384, got %d", len(embedding))
	}

	// Check dimensions are updated
	if client.Dimensions() != 384 {
		t.Errorf("Expected dimensions 384, got %d", client.Dimensions())
	}
}

func TestEmbed_Batch(t *testing.T) {
	server := mockEmbeddingServer(t, map[string]interface{}{})
	defer server.Close()

	config := &EmbedConfig{
		Provider:    "test",
		Model:       "test-model",
		Endpoint:    server.URL,
		MaxRetries:  1,
		TimeoutSecs: 5,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	texts := []string{"text one", "text two", "text three"}
	embeddings, err := client.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(embeddings) != len(texts) {
		t.Errorf("Expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	for i, embedding := range embeddings {
		if len(embedding) != 384 {
			t.Errorf("Embedding %d: expected length 384, got %d", i, len(embedding))
		}
	}
}

func TestEmbed_EmptyTexts(t *testing.T) {
	server := mockEmbeddingServer(t, map[string]interface{}{})
	defer server.Close()

	config := &EmbedConfig{
		Provider:    "test",
		Model:       "test-model",
		Endpoint:    server.URL,
		MaxRetries:  1,
		TimeoutSecs: 5,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	
	// Test empty string
	_, err = client.Embed(ctx, "")
	if err == nil {
		t.Error("Expected error for empty text")
	}

	// Test empty batch
	embeddings, err := client.EmbedBatch(ctx, []string{})
	if err != nil {
		t.Fatalf("EmbedBatch failed with empty slice: %v", err)
	}
	if embeddings != nil {
		t.Error("Expected nil result for empty batch")
	}

	// Test batch with empty texts
	texts := []string{"", "  ", "valid text", ""}
	embeddings, err = client.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(embeddings) != len(texts) {
		t.Errorf("Expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	// Only "valid text" should have a non-nil embedding
	for i, embedding := range embeddings {
		if texts[i] == "valid text" {
			if len(embedding) == 0 {
				t.Errorf("Expected non-empty embedding for valid text at index %d", i)
			}
		} else {
			if len(embedding) != 0 {
				t.Errorf("Expected empty embedding for empty text at index %d", i)
			}
		}
	}
}

func TestEmbed_RetryOnError(t *testing.T) {
	retryCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retryCount++
		if retryCount <= 2 {
			// First two attempts fail
			w.WriteHeader(500)
			w.Write([]byte("internal server error"))
			return
		}

		// Third attempt succeeds
		resp := EmbedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{
					Embedding: []float32{0.1, 0.2, 0.3},
					Index:     0,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &EmbedConfig{
		Provider:    "test",
		Model:       "test-model",
		Endpoint:    server.URL,
		MaxRetries:  3,
		TimeoutSecs: 5,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	embedding, err := client.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Embed failed after retries: %v", err)
	}

	if !reflect.DeepEqual(embedding, []float32{0.1, 0.2, 0.3}) {
		t.Errorf("Unexpected embedding: got %v, want [0.1, 0.2, 0.3]", embedding)
	}

	if retryCount != 3 {
		t.Errorf("Expected 3 attempts, got %d", retryCount)
	}
}

func TestEmbed_RateLimitBackoff(t *testing.T) {
	retryCount := 0
	start := time.Now()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retryCount++
		if retryCount == 1 {
			// First attempt: rate limited with Retry-After
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			w.Write([]byte("rate limited"))
			return
		}

		// Second attempt succeeds
		resp := EmbedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{
					Embedding: []float32{0.1, 0.2, 0.3},
					Index:     0,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &EmbedConfig{
		Provider:    "test",
		Model:       "test-model",
		Endpoint:    server.URL,
		MaxRetries:  3,
		TimeoutSecs: 10,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed < 2*time.Second {
		t.Errorf("Expected at least 2 second delay for rate limit, got %v", elapsed)
	}

	if retryCount != 2 {
		t.Errorf("Expected 2 attempts, got %d", retryCount)
	}
}

func TestEmbed_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"invalid": "json structure"}`))
	}))
	defer server.Close()

	config := &EmbedConfig{
		Provider:    "test",
		Model:       "test-model",
		Endpoint:    server.URL,
		MaxRetries:  1,
		TimeoutSecs: 5,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.Embed(ctx, "test")
	if err == nil {
		t.Error("Expected error for invalid response")
	}
	if !strings.Contains(err.Error(), "expected 1 embeddings") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestEmbed_OllamaProvider(t *testing.T) {
	server := mockEmbeddingServer(t, map[string]interface{}{})
	defer server.Close()

	config, err := ParseEmbedFlag("ollama/all-minilm")
	if err != nil {
		t.Fatalf("ParseEmbedFlag failed: %v", err)
	}

	// Override endpoint for test
	config.Endpoint = server.URL

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.Embed(ctx, "test text")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
}

func TestEmbed_OpenAIProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check Authorization header
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("Expected Bearer authorization, got %s", auth)
		}

		// Mock response
		resp := EmbedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{
					Embedding: []float32{0.1, 0.2, 0.3},
					Index:     0,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config, err := ParseEmbedFlag("openai/text-embedding-3-small")
	if err != nil {
		t.Fatalf("ParseEmbedFlag failed: %v", err)
	}

	// Override endpoint and set API key for test
	config.Endpoint = server.URL
	config.APIKey = "test-key"

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.Embed(ctx, "test text")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
}