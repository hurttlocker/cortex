package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultDaemonHost = "127.0.0.1"
	DefaultDaemonPort = 9720
)

type DaemonCandidate struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type DaemonRequest struct {
	Query      string            `json:"query"`
	Candidates []DaemonCandidate `json:"candidates"`
}

type DaemonResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type DaemonResponse struct {
	Model   string         `json:"model,omitempty"`
	Results []DaemonResult `json:"results"`
}

type DaemonHealth struct {
	Status    string `json:"status"`
	Model     string `json:"model,omitempty"`
	Available bool   `json:"available"`
}

type DaemonClientConfig struct {
	BaseURL string
	Timeout time.Duration
}

type daemonScorer struct {
	baseURL string
	client  *http.Client
	mu      sync.RWMutex
	name    string
}

func ResolveDaemonURL() string {
	if raw := strings.TrimSpace(os.Getenv("CORTEX_RERANK_DAEMON_URL")); raw != "" {
		return strings.TrimRight(raw, "/")
	}
	return fmt.Sprintf("http://%s:%d", DefaultDaemonHost, DefaultDaemonPort)
}

func NewDaemonScorer(cfg DaemonClientConfig) Scorer {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = ResolveDaemonURL()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	return &daemonScorer{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: timeout,
		},
		name: "rerank-daemon",
	}
}

func (d *daemonScorer) Name() string {
	if d == nil {
		return ""
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.name
}

func (d *daemonScorer) Available() bool {
	return d != nil && d.client != nil && d.baseURL != ""
}

func (d *daemonScorer) Close() error {
	return nil
}

func (d *daemonScorer) Ping(ctx context.Context) error {
	if !d.Available() {
		return fmt.Errorf("rerank daemon unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build rerank daemon health request: %w", err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rerank daemon health returned HTTP %d", resp.StatusCode)
	}
	var payload DaemonHealth
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode rerank daemon health: %w", err)
	}
	if !payload.Available {
		return fmt.Errorf("rerank daemon not ready")
	}
	if model := strings.TrimSpace(payload.Model); model != "" {
		d.mu.Lock()
		d.name = model
		d.mu.Unlock()
	}
	return nil
}

func (d *daemonScorer) Score(ctx context.Context, query string, docs []string) ([]float64, error) {
	if !d.Available() {
		return nil, fmt.Errorf("rerank daemon unavailable")
	}
	candidates := make([]DaemonCandidate, 0, len(docs))
	for i, doc := range docs {
		candidates = append(candidates, DaemonCandidate{
			ID:   strconv.Itoa(i),
			Text: doc,
		})
	}

	body, err := json.Marshal(DaemonRequest{
		Query:      query,
		Candidates: candidates,
	})
	if err != nil {
		return nil, fmt.Errorf("encode rerank daemon request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build rerank daemon request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank daemon returned HTTP %d", resp.StatusCode)
	}

	var payload DaemonResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode rerank daemon response: %w", err)
	}
	if model := strings.TrimSpace(payload.Model); model != "" {
		d.mu.Lock()
		d.name = model
		d.mu.Unlock()
	}
	scoreMap := make(map[string]float64, len(payload.Results))
	for _, result := range payload.Results {
		scoreMap[result.ID] = result.Score
	}
	scores := make([]float64, 0, len(docs))
	for i := range docs {
		id := strconv.Itoa(i)
		score, ok := scoreMap[id]
		if !ok {
			return nil, fmt.Errorf("rerank daemon response missing score for candidate %s", id)
		}
		scores = append(scores, score)
	}
	return scores, nil
}

func NewDaemonHandler(scorer Scorer) http.Handler {
	modelName := func() string {
		if scorer == nil {
			return ""
		}
		return scorer.Name()
	}
	available := func() bool {
		return scorer != nil && scorer.Available()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeDaemonJSON(w, http.StatusOK, DaemonHealth{
			Status:    "ok",
			Model:     modelName(),
			Available: available(),
		})
	})
	mux.HandleFunc("/rerank", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req DaemonRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Query) == "" {
			http.Error(w, "query is required", http.StatusBadRequest)
			return
		}
		if len(req.Candidates) == 0 {
			writeDaemonJSON(w, http.StatusOK, DaemonResponse{Model: modelName(), Results: nil})
			return
		}
		if scorer == nil || !scorer.Available() {
			http.Error(w, "reranker unavailable", http.StatusServiceUnavailable)
			return
		}
		docs := make([]string, 0, len(req.Candidates))
		for _, candidate := range req.Candidates {
			docs = append(docs, candidate.Text)
		}
		scores, err := scorer.Score(r.Context(), req.Query, docs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if len(scores) != len(req.Candidates) {
			http.Error(w, "score length mismatch", http.StatusBadGateway)
			return
		}
		results := make([]DaemonResult, 0, len(req.Candidates))
		for i, candidate := range req.Candidates {
			results = append(results, DaemonResult{
				ID:    candidate.ID,
				Score: scores[i],
			})
		}
		writeDaemonJSON(w, http.StatusOK, DaemonResponse{
			Model:   modelName(),
			Results: results,
		})
	})
	return mux
}

func writeDaemonJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func NormalizeDaemonURL(host string, port int) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = DefaultDaemonHost
	}
	if port <= 0 {
		port = DefaultDaemonPort
	}
	base := fmt.Sprintf("http://%s:%d", host, port)
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
