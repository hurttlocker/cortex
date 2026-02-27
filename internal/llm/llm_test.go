package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseLLMFlag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantProv string
		wantMod  string
		wantErr  bool
	}{
		{"empty defaults to google", "", "google", "gemini-2.5-flash", false},
		{"google flash", "google/gemini-3-flash", "google", "gemini-3-flash", false},
		{"google pro", "google/gemini-2.5-pro", "google", "gemini-2.5-pro", false},
		{"openrouter model", "openrouter/openai/gpt-5.1-codex-mini", "openrouter", "openai/gpt-5.1-codex-mini", false},
		{"unknown provider", "anthropic/claude-4", "", "", true},
		{"no slash", "gemini-3-flash", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseLLMFlag(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Provider != tt.wantProv {
				t.Errorf("provider: got %q, want %q", cfg.Provider, tt.wantProv)
			}
			if cfg.Model != tt.wantMod {
				t.Errorf("model: got %q, want %q", cfg.Model, tt.wantMod)
			}
		})
	}
}

func TestNewProviderErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Unknown provider
	_, err := NewProvider(Config{Provider: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}

	// Google without API key (clear env)
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	_, err = NewProvider(Config{Provider: "google"})
	if err == nil {
		t.Fatal("expected error for google without API key")
	}

	// OpenRouter without API key
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err = NewProvider(Config{Provider: "openrouter"})
	if err == nil {
		t.Fatal("expected error for openrouter without API key")
	}
}

func TestGoogleProviderComplete(t *testing.T) {
	// Mock Google API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Verify request body
		var req googleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if len(req.Contents) == 0 || len(req.Contents[0].Parts) == 0 {
			t.Fatal("empty request contents")
		}
		if req.Contents[0].Parts[0].Text != "test prompt" {
			t.Errorf("unexpected prompt: %q", req.Contents[0].Parts[0].Text)
		}

		resp := googleResponse{
			Candidates: []struct {
				Content struct {
					Parts []googlePart `json:"parts"`
				} `json:"content"`
			}{
				{
					Content: struct {
						Parts []googlePart `json:"parts"`
					}{
						Parts: []googlePart{{Text: `["expanded query 1", "expanded query 2"]`}},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &googleProvider{
		apiKey:  "test-key",
		model:   "gemini-3-flash",
		baseURL: server.URL,
	}

	result, err := p.Complete(context.Background(), "test prompt", CompletionOpts{
		MaxTokens:   200,
		Temperature: 0.1,
		Format:      "json",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `["expanded query 1", "expanded query 2"]` {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestGoogleProviderName(t *testing.T) {
	p := &googleProvider{model: "gemini-3-flash"}
	if p.Name() != "google/gemini-3-flash" {
		t.Errorf("unexpected name: %q", p.Name())
	}
}

func TestGoogleProviderSystemPrompt(t *testing.T) {
	var gotSystem bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req googleRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.SystemInstruction != nil && len(req.SystemInstruction.Parts) > 0 {
			gotSystem = req.SystemInstruction.Parts[0].Text == "you are helpful"
		}
		resp := googleResponse{
			Candidates: []struct {
				Content struct {
					Parts []googlePart `json:"parts"`
				} `json:"content"`
			}{
				{Content: struct {
					Parts []googlePart `json:"parts"`
				}{Parts: []googlePart{{Text: "ok"}}}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &googleProvider{apiKey: "test", model: "test", baseURL: server.URL}
	p.Complete(context.Background(), "hello", CompletionOpts{System: "you are helpful"})
	if !gotSystem {
		t.Error("system instruction not sent")
	}
}

func TestGoogleProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`))
	}))
	defer server.Close()

	p := &googleProvider{apiKey: "test", model: "test", baseURL: server.URL}
	_, err := p.Complete(context.Background(), "test", CompletionOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenRouterProviderComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("bad auth header: %q", r.Header.Get("Authorization"))
		}

		var req orRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "openai/gpt-5.1-codex-mini" {
			t.Errorf("unexpected model: %q", req.Model)
		}

		resp := orResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: `["result 1", "result 2"]`},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &openrouterProvider{
		apiKey:  "test-key",
		model:   "openai/gpt-5.1-codex-mini",
		baseURL: server.URL,
	}

	result, err := p.Complete(context.Background(), "test", CompletionOpts{
		MaxTokens: 200,
		Format:    "json",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `["result 1", "result 2"]` {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestOpenRouterProviderName(t *testing.T) {
	p := &openrouterProvider{model: "openai/gpt-5.1-codex-mini"}
	if p.Name() != "openrouter/openai/gpt-5.1-codex-mini" {
		t.Errorf("unexpected name: %q", p.Name())
	}
}

func TestOpenRouterProviderSystemPrompt(t *testing.T) {
	var gotMessages int
	var gotSystemRole bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req orRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotMessages = len(req.Messages)
		for _, m := range req.Messages {
			if m.Role == "system" {
				gotSystemRole = true
			}
		}
		resp := orResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: "ok"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &openrouterProvider{apiKey: "test", model: "test", baseURL: server.URL}
	p.Complete(context.Background(), "hello", CompletionOpts{System: "be helpful"})
	if gotMessages != 2 {
		t.Errorf("expected 2 messages (system+user), got %d", gotMessages)
	}
	if !gotSystemRole {
		t.Error("system message not sent")
	}
}

func TestOpenRouterProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit"}}`))
	}))
	defer server.Close()

	p := &openrouterProvider{apiKey: "test", model: "test", baseURL: server.URL}
	_, err := p.Complete(context.Background(), "test", CompletionOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestContextCancellation(t *testing.T) {
	// Server that delays longer than client timeout
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-serverDone:
		}
	}))
	defer func() {
		close(serverDone)
		server.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	p := &googleProvider{apiKey: "test", model: "test", baseURL: server.URL}
	_, err := p.Complete(ctx, "test", CompletionOpts{})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
