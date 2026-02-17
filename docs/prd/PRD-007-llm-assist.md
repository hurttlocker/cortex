# PRD-007: LLM-Assist Mode

**Status:** Draft  
**Priority:** P0  
**Phase:** 1  
**Depends On:** PRD-003 (Extraction Pipeline)  
**Package:** `internal/extract/` (shared with Tier 1)

---

## Overview

LLM-Assist mode enables optional AI-powered fact extraction for unstructured text where rule-based extraction falls short. It supports any OpenAI-compatible API (Ollama, OpenAI, DeepSeek, OpenRouter, etc.), uses the Instructor pattern (schema + prompt → validated JSON), and never exposes existing memory to the LLM. This is Tier 2 of the extraction pipeline.

## Problem

Rule-based extraction (Tier 1) works well for structured input like Markdown with headers and JSON, but fails on unstructured text:

```
"Yesterday I talked to SB about moving the wedding to October instead of 
September because her mom can't make it in September. We also decided to 
go with the mid-tier budget, around $18K."
```

A human extracts 4+ facts from this. Tier 1 gets date fragments and a dollar amount. We need LLM-assist to extract relationships, decisions, and temporal changes — but we refuse to make it mandatory. Optional, any provider, privacy-preserving.

---

## Requirements

### Must Have (P0)

- **Configuration** (priority order: CLI flag > env var > config file)

  | Source | Key | Example |
  |--------|-----|---------|
  | CLI flag | `--llm <provider>/<model>` | `--llm ollama/gemma2:2b` |
  | Env var | `CORTEX_LLM` | `ollama/gemma2:2b` |
  | Env var | `CORTEX_LLM_ENDPOINT` | `http://localhost:11434/v1` |
  | Env var | `CORTEX_LLM_API_KEY` | `sk-...` |
  | Config file | `~/.cortex/config.yaml` | See below |

  Config file format:
  ```yaml
  llm:
    default: ollama/gemma2:2b
    endpoint: http://localhost:11434/v1
    api_key: ""
  ```

- **Provider support** — any OpenAI-compatible chat completions API

  | Provider | Format | Endpoint | Auth |
  |----------|--------|----------|------|
  | Ollama | `ollama/<model>` | `http://localhost:11434/v1/chat/completions` | None |
  | OpenAI | `openai/<model>` | `https://api.openai.com/v1/chat/completions` | `OPENAI_API_KEY` or config |
  | DeepSeek | `deepseek/<model>` | `https://api.deepseek.com/v1/chat/completions` | `DEEPSEEK_API_KEY` or config |
  | OpenRouter | `openrouter/<model>` | `https://openrouter.ai/api/v1/chat/completions` | `OPENROUTER_API_KEY` or config |
  | Anthropic | `anthropic/<model>` | Via compatible proxy | Proxy-specific |
  | Custom | Any | User-specified endpoint | User-specified key |

  **Ollama auto-detection:** If provider is `ollama`, check if `localhost:11434` is reachable. If not, return a clear error: *"Ollama not running. Start it with `ollama serve` or use a cloud provider."*

- **Extraction flow**

  ```
  1. Receive document text from import engine
  2. Check: is LLM configured? If not, skip (Tier 1 only)
  3. Estimate token count (approximate: chars / 4)
  4. If exceeds model context window: chunk document
     - Chunk size: 75% of model context window
     - Overlap: 50 tokens between chunks
  5. For each chunk:
     a. Build prompt: system prompt (versioned) + extraction schema + chunk
     b. Send to LLM endpoint (OpenAI chat completions format)
     c. Parse JSON response
     d. Validate against extraction schema
     e. If validation fails: retry (up to 3 attempts)
     f. If all retries fail: log warning, fall back to Tier 1 for this chunk
  6. Merge facts from all chunks (deduplicate by subject+predicate similarity)
  7. Return []ExtractedFact
  ```

- **Request format** (OpenAI chat completions API)

  ```json
  {
    "model": "<model-name>",
    "messages": [
      {
        "role": "system",
        "content": "<system prompt with extraction rules>"
      },
      {
        "role": "user", 
        "content": "Extract facts from this text:\n\n---\n<document chunk>\n---\n\nReturn JSON matching the schema."
      }
    ],
    "temperature": 0.1,
    "response_format": {"type": "json_object"}
  }
  ```

- **Schema validation**
  - Parse LLM response as JSON
  - Validate required fields: `subject`, `predicate`, `object`, `type`, `confidence`, `source_quote`
  - Validate `type` is one of: `kv`, `relationship`, `preference`, `temporal`, `identity`, `location`, `decision`, `state`
  - Validate `confidence` is between 0.0 and 1.0
  - If invalid: retry with the same prompt (LLM output is non-deterministic, retry often succeeds)

- **Privacy guarantee**
  - LLM only receives the document chunk being processed
  - **Never** send existing facts, memory store contents, or user queries to the LLM
  - **Never** send file paths, database paths, or system information
  - Document this prominently in README and CLI help text

- **Extraction prompts versioned** in `internal/extract/prompts/`
  - `system_v1.txt` — current system prompt
  - Each prompt version is immutable once released
  - Facts store which prompt version was used for extraction
  - Use Go `embed` to bundle prompts into binary

- **Error handling**
  - Network error → retry up to 3 times with exponential backoff (1s, 2s, 4s)
  - Rate limit (429) → wait for `Retry-After` header, then retry
  - Invalid JSON response → retry (up to 3 times per chunk)
  - All retries exhausted → fall back to Tier 1 for this chunk, log warning
  - LLM completely unavailable → warn user, proceed with Tier 1 only
  - **Never fail the entire import because LLM is unavailable**

### Should Have (P1)

- **Cost transparency** (`--dry-run`)
  - Count tokens in document (chars / 4 approximation)
  - Estimate cost based on known model pricing
  - Display before proceeding:
    ```
    LLM Extraction Estimate:
      Model:    openai/gpt-4.1-nano
      Tokens:   ~4,200 (input) + ~1,000 (output)
      Est cost: ~$0.0005
      Chunks:   1 (fits in single request)
    
    Proceed? [Y/n]
    ```
  - Skip confirmation in non-interactive mode (piped output)

- **Chunking configuration**
  - Default context windows per provider:
    | Provider | Default Context |
    |----------|----------------|
    | Ollama (most models) | 4,096 tokens |
    | OpenAI gpt-4.1-nano | 128,000 tokens |
    | DeepSeek v3 | 64,000 tokens |
    | Anthropic Haiku | 200,000 tokens |
  - Configurable via config file: `llm.context_window: 8192`
  - Auto-detect from model name when possible

- **Response caching**
  - Cache LLM responses by content hash + prompt version
  - If same content chunk + same prompt version → return cached response
  - Cache stored in SQLite (same DB, separate table)
  - Saves money on re-imports

### Future (P2)

- **Constrained decoding** via Outlines for local models
- **Streaming response** parsing (start processing before full response)
- **Multi-model extraction** — run cheap model first, expensive model for uncertain facts
- **Custom extraction schemas** — user-defined fact types
- **Extraction benchmarking** — compare Tier 1 vs Tier 2 quality on same input

---

## Technical Design

### LLM Configuration

```go
package extract

import (
    "os"
    "strings"
    "gopkg.in/yaml.v3"
)

// LLMConfig holds LLM provider configuration.
type LLMConfig struct {
    Provider      string // "ollama", "openai", "deepseek", "openrouter", "custom"
    Model         string // "gemma2:2b", "gpt-4.1-nano", etc.
    Endpoint      string // Full API URL
    APIKey        string
    ContextWindow int    // Max tokens (0 = auto-detect from provider)
    MaxRetries    int    // Default: 3
    TimeoutSecs   int    // Per-request timeout (default: 60)
}

// ParseLLMFlag parses "--llm provider/model" format.
func ParseLLMFlag(flag string) (*LLMConfig, error) {
    parts := strings.SplitN(flag, "/", 2)
    if len(parts) != 2 {
        return nil, fmt.Errorf("invalid --llm format: expected 'provider/model', got %q", flag)
    }
    
    provider := parts[0]
    model := parts[1]
    
    config := &LLMConfig{
        Provider:   provider,
        Model:      model,
        MaxRetries: 3,
        TimeoutSecs: 60,
    }
    
    // Set endpoint from provider
    switch provider {
    case "ollama":
        config.Endpoint = "http://localhost:11434/v1/chat/completions"
        config.ContextWindow = 4096
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
    default:
        return nil, fmt.Errorf("unknown provider %q. Use ollama, openai, deepseek, openrouter, or set custom endpoint in config", provider)
    }
    
    return config, nil
}

// ResolveLLMConfig resolves configuration from all sources.
// Priority: CLI flag > env var > config file
func ResolveLLMConfig(cliFlag string) (*LLMConfig, error) {
    // 1. CLI flag
    if cliFlag != "" {
        return ParseLLMFlag(cliFlag)
    }
    
    // 2. Environment variables
    if envLLM := os.Getenv("CORTEX_LLM"); envLLM != "" {
        config, err := ParseLLMFlag(envLLM)
        if err != nil {
            return nil, err
        }
        if endpoint := os.Getenv("CORTEX_LLM_ENDPOINT"); endpoint != "" {
            config.Endpoint = endpoint
        }
        if apiKey := os.Getenv("CORTEX_LLM_API_KEY"); apiKey != "" {
            config.APIKey = apiKey
        }
        return config, nil
    }
    
    // 3. Config file (~/.cortex/config.yaml)
    return loadConfigFile()
}
```

### LLM Client

```go
// LLMClient handles communication with OpenAI-compatible APIs.
type LLMClient struct {
    config LLMConfig
    http   *http.Client
}

// ChatRequest is the OpenAI chat completions request format.
type ChatRequest struct {
    Model          string          `json:"model"`
    Messages       []ChatMessage   `json:"messages"`
    Temperature    float64         `json:"temperature"`
    ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type ChatMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type ResponseFormat struct {
    Type string `json:"type"`
}

// ChatResponse is the OpenAI chat completions response format.
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

func (c *LLMClient) Extract(ctx context.Context, text string) ([]ExtractedFact, error) {
    // 1. Build messages
    messages := []ChatMessage{
        {Role: "system", Content: systemPromptV1},
        {Role: "user", Content: fmt.Sprintf("Extract facts from this text:\n\n---\n%s\n---\n\nReturn JSON matching the schema.", text)},
    }
    
    // 2. Send request
    req := ChatRequest{
        Model:       c.config.Model,
        Messages:    messages,
        Temperature: 0.1,
        ResponseFormat: &ResponseFormat{Type: "json_object"},
    }
    
    // 3. Parse and validate response (with retry)
    var facts []ExtractedFact
    var lastErr error
    
    for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
        resp, err := c.send(ctx, req)
        if err != nil {
            lastErr = err
            time.Sleep(time.Duration(1<<attempt) * time.Second) // exponential backoff
            continue
        }
        
        facts, err = parseExtractionResponse(resp.Choices[0].Message.Content)
        if err != nil {
            lastErr = err
            continue
        }
        
        return facts, nil
    }
    
    return nil, fmt.Errorf("LLM extraction failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
}
```

### Document Chunking

```go
// ChunkDocument splits a document into chunks that fit within the context window.
func ChunkDocument(text string, contextWindow int) []string {
    // Approximate tokens: chars / 4
    estimatedTokens := len(text) / 4
    
    if estimatedTokens <= contextWindow*3/4 {
        // Fits in one chunk (using 75% of context to leave room for prompt + response)
        return []string{text}
    }
    
    // Split into chunks
    chunkSize := contextWindow * 3 / 4 * 4 // back to chars
    overlap := 200 // ~50 tokens overlap
    
    var chunks []string
    for i := 0; i < len(text); i += chunkSize - overlap {
        end := i + chunkSize
        if end > len(text) {
            end = len(text)
        }
        
        // Try to split on paragraph boundary
        chunk := text[i:end]
        if end < len(text) {
            if idx := strings.LastIndex(chunk, "\n\n"); idx > len(chunk)/2 {
                chunk = chunk[:idx]
                end = i + idx
            }
        }
        
        chunks = append(chunks, chunk)
        
        if end >= len(text) {
            break
        }
    }
    
    return chunks
}
```

### Provider-Specific Behavior

```go
// checkOllamaAvailable verifies Ollama is running locally.
func checkOllamaAvailable() error {
    resp, err := http.Get("http://localhost:11434/api/tags")
    if err != nil {
        return fmt.Errorf("Ollama not running at localhost:11434. Start it with 'ollama serve' or use a cloud provider (e.g., --llm openai/gpt-4.1-nano)")
    }
    resp.Body.Close()
    return nil
}
```

---

## Test Strategy

### Unit Tests

**Configuration:**
- **TestParseLLMFlag_Ollama** — `"ollama/gemma2:2b"` → correct config
- **TestParseLLMFlag_OpenAI** — `"openai/gpt-4.1-nano"` → correct config with API key from env
- **TestParseLLMFlag_DeepSeek** — `"deepseek/v3"` → correct config
- **TestParseLLMFlag_OpenRouter** — `"openrouter/meta-llama/llama-3"` → handles nested model names
- **TestParseLLMFlag_Invalid** — `"justmodel"` → error
- **TestParseLLMFlag_UnknownProvider** — `"unknown/model"` → error
- **TestResolveLLMConfig_CLIFlag** — CLI flag takes priority
- **TestResolveLLMConfig_EnvVar** — env var used when no CLI flag
- **TestResolveLLMConfig_ConfigFile** — config file used as fallback
- **TestResolveLLMConfig_Priority** — CLI > env > config verified

**LLM Client:**
- **TestLLMExtract_ValidResponse** — mock HTTP server returns valid JSON, facts extracted
- **TestLLMExtract_InvalidJSON** — mock returns garbage, retry fires
- **TestLLMExtract_RetrySuccess** — fails twice, succeeds on third
- **TestLLMExtract_AllRetriesFail** — exhausts retries, returns error
- **TestLLMExtract_NetworkError** — connection refused, returns clear error
- **TestLLMExtract_RateLimit** — 429 response, backs off
- **TestLLMExtract_Timeout** — context deadline exceeded
- **TestLLMExtract_EmptyResponse** — empty response handled gracefully

**Chunking:**
- **TestChunkDocument_FitsInOne** — small document returns single chunk
- **TestChunkDocument_NeedsChunking** — large document split correctly
- **TestChunkDocument_OverlapExists** — chunks overlap by ~50 tokens
- **TestChunkDocument_ParagraphBoundary** — splits on paragraph when possible
- **TestChunkDocument_EmptyDocument** — returns empty slice

**Validation:**
- **TestValidateResponse_Valid** — all required fields present
- **TestValidateResponse_MissingSubject** — returns validation error
- **TestValidateResponse_InvalidType** — unknown fact type rejected
- **TestValidateResponse_ConfidenceOutOfRange** — confidence > 1.0 rejected
- **TestValidateResponse_ExtraFields** — unknown fields ignored (forward compat)

**Provider Detection:**
- **TestCheckOllamaAvailable_Running** — mock server, returns nil
- **TestCheckOllamaAvailable_NotRunning** — no server, returns clear error

### Integration Tests

- **TestLLMExtractFromSampleMemory** — extract from `tests/testdata/sample-memory.md` with mock LLM
- **TestTier1FallbackOnLLMFailure** — LLM unavailable, Tier 1 results returned
- **TestEndToEndWithMockLLM** — import → LLM extract → store → search

### Test with Mock HTTP Server

```go
func newMockLLMServer(t *testing.T, response string) *httptest.Server {
    t.Helper()
    return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Verify request format
        var req ChatRequest
        json.NewDecoder(r.Body).Decode(&req)
        
        // Return mock response
        resp := ChatResponse{
            Choices: []struct{ Message ChatMessage }{
                {Message: ChatMessage{Role: "assistant", Content: response}},
            },
        }
        json.NewEncoder(w).Encode(resp)
    }))
}
```

---

## Open Questions

1. **Model-specific prompt tuning:** Should we have different prompts for different model families? (Some models respond better to different instruction styles)
2. **Token counting accuracy:** chars/4 is rough. Should we use tiktoken or a proper tokenizer? (v1: chars/4 is fine, v2: proper tokenizer)
3. **Streaming vs. blocking:** Should we stream LLM responses for faster perceived performance? (v1: blocking, v2: streaming)
4. **Cost tracking:** Should we store cumulative LLM costs in the DB? (Would be useful for `cortex stats`)
5. **Model context window database:** Should we maintain a table of known model context windows? (Yes, but v2 — hardcode common ones for v1)
