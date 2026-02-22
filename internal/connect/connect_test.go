package connect

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// mockProvider implements Provider for testing.
type mockProvider struct {
	name        string
	displayName string
	records     []Record
	fetchErr    error
}

func (m *mockProvider) Name() string                                { return m.name }
func (m *mockProvider) DisplayName() string                         { return m.displayName }
func (m *mockProvider) ValidateConfig(config json.RawMessage) error { return nil }
func (m *mockProvider) DefaultConfig() json.RawMessage              { return json.RawMessage(`{"token": ""}`) }
func (m *mockProvider) Fetch(ctx context.Context, cfg json.RawMessage, since *time.Time) ([]Record, error) {
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}
	return m.records, nil
}

func TestRegistryBasics(t *testing.T) {
	reg := NewRegistry()

	// Empty registry
	if names := reg.List(); len(names) != 0 {
		t.Fatalf("expected empty registry, got %v", names)
	}

	// Register providers
	reg.Register(&mockProvider{name: "gmail", displayName: "Gmail"})
	reg.Register(&mockProvider{name: "github", displayName: "GitHub"})

	// List is sorted
	names := reg.List()
	if len(names) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(names))
	}
	if names[0] != "github" || names[1] != "gmail" {
		t.Fatalf("expected [github gmail], got %v", names)
	}

	// Get existing
	p := reg.Get("gmail")
	if p == nil {
		t.Fatal("expected gmail provider")
	}
	if p.DisplayName() != "Gmail" {
		t.Fatalf("expected display name Gmail, got %s", p.DisplayName())
	}

	// Get non-existing
	if p := reg.Get("slack"); p != nil {
		t.Fatal("expected nil for unregistered provider")
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockProvider{name: "gmail", displayName: "Gmail"})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	reg.Register(&mockProvider{name: "gmail", displayName: "Gmail 2"})
}

func TestRegistryProviders(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockProvider{name: "a", displayName: "A"})
	reg.Register(&mockProvider{name: "b", displayName: "B"})

	providers := reg.Providers()
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
	if providers["a"] == nil || providers["b"] == nil {
		t.Fatal("missing expected providers")
	}
}

func TestRecordDefaults(t *testing.T) {
	r := Record{
		Content:    "test content",
		Source:     "gmail:msg/123",
		ExternalID: "abc123",
	}

	if r.Content != "test content" {
		t.Fatal("content mismatch")
	}
	if r.Project != "" {
		t.Fatal("expected empty project by default")
	}
	if r.MemoryClass != "" {
		t.Fatal("expected empty memory class by default")
	}
}

func TestConnectorDefaults(t *testing.T) {
	c := Connector{
		Provider: "gmail",
		Config:   json.RawMessage("{}"),
		Enabled:  true,
	}

	if c.LastSyncAt != nil {
		t.Fatal("expected nil last sync time")
	}
	if c.LastError != "" {
		t.Fatal("expected empty last error")
	}
	if c.RecordsImported != 0 {
		t.Fatal("expected 0 records imported")
	}
}

func TestSyncResultJSON(t *testing.T) {
	r := SyncResult{
		Provider:        "github",
		RecordsFetched:  10,
		RecordsImported: 8,
		RecordsSkipped:  2,
		Duration:        1500 * time.Millisecond,
		SyncedAt:        time.Now(),
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("failed to marshal SyncResult: %v", err)
	}

	var decoded SyncResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal SyncResult: %v", err)
	}

	if decoded.Provider != "github" {
		t.Fatalf("expected github, got %s", decoded.Provider)
	}
	if decoded.RecordsFetched != 10 {
		t.Fatalf("expected 10 fetched, got %d", decoded.RecordsFetched)
	}
}

func TestContentHash(t *testing.T) {
	h1 := contentHash("hello", "source1")
	h2 := contentHash("hello", "source2")
	h3 := contentHash("hello", "source1")

	if h1 == h2 {
		t.Fatal("different sources should produce different hashes")
	}
	if h1 != h3 {
		t.Fatal("same content+source should produce same hash")
	}
	if len(h1) != 64 { // SHA-256 hex = 64 chars
		t.Fatalf("expected 64-char hash, got %d", len(h1))
	}
}

func TestDefaultRegistry(t *testing.T) {
	// DefaultRegistry should exist and be usable
	if DefaultRegistry == nil {
		t.Fatal("DefaultRegistry is nil")
	}

	// Should be empty at test time (no init() registrations yet)
	names := DefaultRegistry.List()
	// We don't assert length because other test files might register providers
	_ = names
}
