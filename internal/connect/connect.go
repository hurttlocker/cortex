// Package connect provides the connector framework for Cortex.
//
// Connectors are integrations that pull data from external services
// (Gmail, GitHub, Google Calendar, Slack, etc.) into Cortex's memory store.
// All connector data flows through the standard ingest pipeline, preserving
// provenance, confidence, fact extraction, and search guarantees.
package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Provider defines the interface that all connectors must implement.
// Each provider handles authentication, data fetching, and record conversion
// for a specific external service.
type Provider interface {
	// Name returns the unique provider identifier (e.g., "gmail", "github").
	Name() string

	// DisplayName returns a human-readable name (e.g., "Gmail", "GitHub").
	DisplayName() string

	// ValidateConfig checks whether the provided JSON config is valid.
	// Returns nil if config is valid, error with actionable message otherwise.
	ValidateConfig(config json.RawMessage) error

	// DefaultConfig returns a template config with placeholder values
	// that users can fill in. Used by `cortex connect add`.
	DefaultConfig() json.RawMessage

	// Fetch retrieves records from the external service.
	// If since is non-nil, only records modified after that time are returned (incremental sync).
	// If since is nil, all available records are fetched (full sync).
	Fetch(ctx context.Context, cfg json.RawMessage, since *time.Time) ([]Record, error)
}

// Record represents a single piece of data fetched from an external provider.
// Records are converted to Cortex memories during sync.
type Record struct {
	// Content is the main text content to store as a memory.
	Content string

	// Source identifies the origin (e.g., "gmail:msg/abc123", "github:issue/42").
	Source string

	// Section provides sub-document context (e.g., email subject, issue title).
	Section string

	// Project is an optional project tag for scoped search.
	Project string

	// MemoryClass categorizes the memory (rule, decision, preference, etc.).
	MemoryClass string

	// Timestamp is when the source content was created/modified.
	Timestamp time.Time

	// ExternalID is a provider-specific unique identifier for deduplication.
	ExternalID string

	// ProviderMeta holds provider-specific metadata as JSON.
	ProviderMeta json.RawMessage
}

// Connector represents a configured and registered connector instance.
type Connector struct {
	ID              int64           `json:"id"`
	Provider        string          `json:"provider"`
	Config          json.RawMessage `json:"config"`
	Enabled         bool            `json:"enabled"`
	LastSyncAt      *time.Time      `json:"last_sync_at,omitempty"`
	LastError       string          `json:"last_error,omitempty"`
	RecordsImported int64           `json:"records_imported"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// SyncResult holds the outcome of a connector sync operation.
type SyncResult struct {
	Provider        string        `json:"provider"`
	RecordsFetched  int           `json:"records_fetched"`
	RecordsImported int           `json:"records_imported"`
	RecordsSkipped  int           `json:"records_skipped"`
	FactsExtracted  int           `json:"facts_extracted,omitempty"`
	EdgesInferred   int           `json:"edges_inferred,omitempty"`
	Duration        time.Duration `json:"duration"`
	Error           string        `json:"error,omitempty"`
	SyncedAt        time.Time     `json:"synced_at"`
}

// Registry holds all registered providers. Thread-safe.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry. Panics on duplicate names.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.providers[name]; exists {
		panic(fmt.Sprintf("connect: duplicate provider registration: %s", name))
	}
	r.providers[name] = p
}

// Get returns a provider by name, or nil if not found.
func (r *Registry) Get(name string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[name]
}

// List returns all registered provider names, sorted.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Providers returns all registered providers as a name→provider map.
func (r *Registry) Providers() map[string]Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]Provider, len(r.providers))
	for k, v := range r.providers {
		result[k] = v
	}
	return result
}

// DefaultRegistry is the global provider registry.
// Providers register themselves during init() or explicit setup.
var DefaultRegistry = NewRegistry()
