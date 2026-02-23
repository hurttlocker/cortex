package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// WebhookConfig controls alert webhook delivery.
type WebhookConfig struct {
	// URL is the webhook endpoint. If empty, webhooks are disabled.
	URL string

	// Headers are additional HTTP headers to include (e.g., Authorization).
	Headers map[string]string

	// Version is included in the webhook payload.
	Version string
}

// WebhookPayload is the JSON body POSTed to the webhook endpoint.
type WebhookPayload struct {
	Type          AlertType     `json:"type"`
	Severity      AlertSeverity `json:"severity"`
	FactID        *int64        `json:"fact_id,omitempty"`
	RelatedFactID *int64        `json:"related_fact_id,omitempty"`
	AgentID       string        `json:"agent_id,omitempty"`
	Message       string        `json:"message"`
	Details       string        `json:"details,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	CortexVersion string        `json:"cortex_version,omitempty"`
}

// WebhookNotifier delivers alert payloads to a configured webhook URL.
// It batches alerts within a configurable window to avoid flooding.
type WebhookNotifier struct {
	config  WebhookConfig
	client  *http.Client
	mu      sync.Mutex
	pending []WebhookPayload
	timer   *time.Timer
	batchMs int // batch window in milliseconds (default: 5000)
}

// NewWebhookNotifier creates a notifier. Pass nil config or empty URL to disable.
func NewWebhookNotifier(cfg *WebhookConfig) *WebhookNotifier {
	if cfg == nil {
		cfg = &WebhookConfig{}
	}
	// Also check environment
	if cfg.URL == "" {
		cfg.URL = os.Getenv("CORTEX_ALERT_WEBHOOK_URL")
	}
	return &WebhookNotifier{
		config:  *cfg,
		client:  &http.Client{Timeout: 10 * time.Second},
		batchMs: 5000,
	}
}

// Enabled returns true if a webhook URL is configured.
func (w *WebhookNotifier) Enabled() bool {
	return w.config.URL != ""
}

// Notify queues an alert for webhook delivery. Non-blocking.
// If batching is enabled, alerts are grouped and sent together.
func (w *WebhookNotifier) Notify(alert *Alert) {
	if !w.Enabled() {
		return
	}

	payload := WebhookPayload{
		Type:          alert.AlertType,
		Severity:      alert.Severity,
		FactID:        alert.FactID,
		RelatedFactID: alert.RelatedFactID,
		AgentID:       alert.AgentID,
		Message:       alert.Message,
		Details:       alert.Details,
		CreatedAt:     alert.CreatedAt,
		CortexVersion: w.config.Version,
	}

	w.mu.Lock()
	w.pending = append(w.pending, payload)

	// Start or reset the batch timer
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(time.Duration(w.batchMs)*time.Millisecond, w.flush)
	w.mu.Unlock()
}

// Flush sends all pending alerts immediately. Safe to call externally.
func (w *WebhookNotifier) Flush() {
	w.flush()
}

func (w *WebhookNotifier) flush() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.pending
	w.pending = nil
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	w.mu.Unlock()

	// Send batch
	go w.sendBatch(batch)
}

func (w *WebhookNotifier) sendBatch(payloads []WebhookPayload) {
	var body interface{}
	if len(payloads) == 1 {
		body = payloads[0]
	} else {
		body = map[string]interface{}{
			"alerts": payloads,
			"count":  len(payloads),
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortex webhook: marshal error: %v\n", err)
		return
	}

	// Try up to 2 times (initial + 1 retry on 5xx)
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(5 * time.Second)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, "POST", w.config.URL, bytes.NewReader(data))
		if err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "cortex webhook: request error: %v\n", err)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Cortex/"+w.config.Version)
		for k, v := range w.config.Headers {
			req.Header.Set(k, v)
		}

		resp, err := w.client.Do(req)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortex webhook: delivery failed: %v\n", err)
			if attempt == 0 {
				continue // retry
			}
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // success
		}
		if resp.StatusCode >= 500 && attempt == 0 {
			fmt.Fprintf(os.Stderr, "cortex webhook: %d, retrying...\n", resp.StatusCode)
			continue
		}
		fmt.Fprintf(os.Stderr, "cortex webhook: delivery returned %d\n", resp.StatusCode)
		return
	}
}
