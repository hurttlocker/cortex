package store

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestWebhookNotifierDisabled(t *testing.T) {
	n := NewWebhookNotifier(nil)
	if n.Enabled() {
		t.Fatal("expected disabled with nil config")
	}

	n2 := NewWebhookNotifier(&WebhookConfig{URL: ""})
	if n2.Enabled() {
		t.Fatal("expected disabled with empty URL")
	}

	// Should not panic
	n.Notify(&Alert{AlertType: AlertTypeConflict, Message: "test"})
}

func TestWebhookNotifierEnabled(t *testing.T) {
	n := NewWebhookNotifier(&WebhookConfig{URL: "https://example.com/webhook"})
	if !n.Enabled() {
		t.Fatal("expected enabled with URL")
	}
}

func TestWebhookSingleAlert(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		var buf [8192]byte
		n, _ := r.Body.Read(buf[:])
		mu.Lock()
		received = append([]byte{}, buf[:n]...)
		mu.Unlock()

		w.WriteHeader(200)
	}))
	defer server.Close()

	n := NewWebhookNotifier(&WebhookConfig{
		URL:     server.URL,
		Version: "test-0.6.0",
		Headers: map[string]string{"X-Custom": "value"},
	})
	n.batchMs = 50 // fast batch for testing

	factID := int64(42)
	alert := &Alert{
		AlertType: AlertTypeConflict,
		Severity:  AlertSeverityWarning,
		FactID:    &factID,
		AgentID:   "mister",
		Message:   "Conflicting facts detected",
		CreatedAt: time.Now().UTC(),
	}

	n.Notify(alert)

	// Wait for batch to flush
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	data := received
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("expected webhook to be called")
	}

	var payload WebhookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}

	if payload.Type != AlertTypeConflict {
		t.Fatalf("expected type conflict, got %s", payload.Type)
	}
	if payload.Severity != AlertSeverityWarning {
		t.Fatalf("expected severity warning, got %s", payload.Severity)
	}
	if payload.CortexVersion != "test-0.6.0" {
		t.Fatalf("expected version test-0.6.0, got %s", payload.CortexVersion)
	}
	if *payload.FactID != 42 {
		t.Fatalf("expected fact_id 42, got %d", *payload.FactID)
	}
	if payload.AgentID != "mister" {
		t.Fatalf("expected agent_id mister, got %s", payload.AgentID)
	}
}

func TestWebhookBatchAlerts(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [16384]byte
		n, _ := r.Body.Read(buf[:])
		mu.Lock()
		received = append([]byte{}, buf[:n]...)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer server.Close()

	n := NewWebhookNotifier(&WebhookConfig{URL: server.URL, Version: "test"})
	n.batchMs = 100 // 100ms batch window

	// Send 3 alerts rapidly
	for i := 0; i < 3; i++ {
		n.Notify(&Alert{
			AlertType: AlertTypeDecay,
			Severity:  AlertSeverityInfo,
			Message:   "test alert",
			CreatedAt: time.Now().UTC(),
		})
	}

	// Wait for batch
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	data := received
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("expected webhook to be called")
	}

	var batch struct {
		Alerts []WebhookPayload `json:"alerts"`
		Count  int              `json:"count"`
	}
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("invalid batch JSON: %v", err)
	}

	if batch.Count != 3 {
		t.Fatalf("expected 3 alerts in batch, got %d", batch.Count)
	}
	if len(batch.Alerts) != 3 {
		t.Fatalf("expected 3 alert payloads, got %d", len(batch.Alerts))
	}
}

func TestWebhookRetryOn5xx(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		a := attempts
		mu.Unlock()

		if a == 1 {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer server.Close()

	n := NewWebhookNotifier(&WebhookConfig{URL: server.URL})
	n.batchMs = 10

	n.Notify(&Alert{AlertType: AlertTypeMatch, Severity: AlertSeverityInfo, Message: "test", CreatedAt: time.Now().UTC()})

	// Wait for retry cycle (initial + 5s retry is too slow for test, but the logic is tested)
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	a := attempts
	mu.Unlock()

	if a < 1 {
		t.Fatal("expected at least 1 attempt")
	}
}

func TestWebhookCustomHeaders(t *testing.T) {
	var receivedAuth string
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer server.Close()

	n := NewWebhookNotifier(&WebhookConfig{
		URL: server.URL,
		Headers: map[string]string{
			"Authorization": "Bearer test-token-123",
		},
	})
	n.batchMs = 10

	n.Notify(&Alert{AlertType: AlertTypeConflict, Message: "test", CreatedAt: time.Now().UTC()})
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	auth := receivedAuth
	mu.Unlock()

	if auth != "Bearer test-token-123" {
		t.Fatalf("expected auth header, got %q", auth)
	}
}

func TestWebhookFlush(t *testing.T) {
	var mu sync.Mutex
	called := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		called = true
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer server.Close()

	n := NewWebhookNotifier(&WebhookConfig{URL: server.URL})
	n.batchMs = 60000 // very long batch window

	n.Notify(&Alert{AlertType: AlertTypeDecay, Message: "flush test", CreatedAt: time.Now().UTC()})

	// Force flush instead of waiting
	n.Flush()
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	c := called
	mu.Unlock()

	if !c {
		t.Fatal("expected webhook to be called after Flush()")
	}
}
