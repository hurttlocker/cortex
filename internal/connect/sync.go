package connect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

// SyncEngine orchestrates connector syncs through the standard Cortex ingest pipeline.
type SyncEngine struct {
	registry *Registry
	connStore *ConnectorStore
	memStore  store.Store
	verbose   bool
}

// NewSyncEngine creates a sync engine backed by the given stores.
func NewSyncEngine(registry *Registry, connStore *ConnectorStore, memStore store.Store, verbose bool) *SyncEngine {
	return &SyncEngine{
		registry:  registry,
		connStore: connStore,
		memStore:  memStore,
		verbose:   verbose,
	}
}

// SyncAll runs sync for all enabled connectors.
func (se *SyncEngine) SyncAll(ctx context.Context) ([]SyncResult, error) {
	connectors, err := se.connStore.List(ctx, true) // enabled only
	if err != nil {
		return nil, fmt.Errorf("listing connectors: %w", err)
	}
	if len(connectors) == 0 {
		return nil, nil
	}

	var results []SyncResult
	for _, c := range connectors {
		result := se.SyncOne(ctx, c)
		results = append(results, result)
	}
	return results, nil
}

// SyncProvider runs sync for a specific provider by name.
func (se *SyncEngine) SyncProvider(ctx context.Context, providerName string) (SyncResult, error) {
	c, err := se.connStore.Get(ctx, providerName)
	if err != nil {
		return SyncResult{Provider: providerName, Error: err.Error()}, err
	}
	if !c.Enabled {
		return SyncResult{Provider: providerName, Error: "connector is disabled"}, fmt.Errorf("connector %q is disabled", providerName)
	}
	return se.SyncOne(ctx, c), nil
}

// SyncOne runs sync for a single connector.
func (se *SyncEngine) SyncOne(ctx context.Context, c *Connector) SyncResult {
	start := time.Now()
	result := SyncResult{
		Provider: c.Provider,
		SyncedAt: start,
	}

	// Look up the provider implementation
	provider := se.registry.Get(c.Provider)
	if provider == nil {
		result.Error = fmt.Sprintf("provider %q not registered", c.Provider)
		_ = se.connStore.RecordSyncError(ctx, c.Provider, result.Error)
		result.Duration = time.Since(start)
		return result
	}

	// Determine sync window (incremental if we have a last sync time)
	var since *time.Time
	if c.LastSyncAt != nil {
		since = c.LastSyncAt
	}

	// Fetch records from provider
	records, err := provider.Fetch(ctx, c.Config, since)
	if err != nil {
		result.Error = fmt.Sprintf("fetch failed: %v", err)
		_ = se.connStore.RecordSyncError(ctx, c.Provider, result.Error)
		result.Duration = time.Since(start)
		return result
	}

	result.RecordsFetched = len(records)

	// Import each record through the standard pipeline
	for _, rec := range records {
		imported, err := se.importRecord(ctx, c.Provider, rec)
		if err != nil {
			if se.verbose {
				fmt.Printf("  warning: failed to import record %s: %v\n", rec.ExternalID, err)
			}
			result.RecordsSkipped++
			continue
		}
		if imported {
			result.RecordsImported++
		} else {
			result.RecordsSkipped++ // deduplicated
		}
	}

	// Update connector state
	if err := se.connStore.RecordSyncSuccess(ctx, c.Provider, int64(result.RecordsImported)); err != nil {
		result.Error = fmt.Sprintf("sync succeeded but state update failed: %v", err)
	}

	result.Duration = time.Since(start)
	return result
}

// importRecord converts a provider Record to a Cortex Memory and stores it.
// Returns (true, nil) if imported, (false, nil) if deduplicated, (false, err) on error.
func (se *SyncEngine) importRecord(ctx context.Context, provider string, rec Record) (bool, error) {
	// Build content hash for deduplication
	hash := contentHash(rec.Content, rec.Source)

	// Check for existing memory with same hash
	existing, err := se.memStore.FindByHash(ctx, hash)
	if err != nil && existing != nil {
		// Already imported — skip
		return false, nil
	}

	// Build source identifier with provider prefix
	source := fmt.Sprintf("%s:%s", provider, rec.Source)
	if rec.Source == "" {
		source = provider
	}

	mem := &store.Memory{
		Content:       rec.Content,
		SourceFile:    source,
		SourceSection: rec.Section,
		ContentHash:   hash,
		Project:       rec.Project,
		MemoryClass:   rec.MemoryClass,
	}

	_, err = se.memStore.AddMemory(ctx, mem)
	if err != nil {
		// Duplicate hash constraint — treat as dedup
		if isDuplicateError(err) {
			return false, nil
		}
		return false, fmt.Errorf("storing memory: %w", err)
	}

	return true, nil
}

// contentHash generates a SHA-256 hash for deduplication.
func contentHash(content, source string) string {
	h := sha256.New()
	h.Write([]byte(content))
	h.Write([]byte("\x00"))
	h.Write([]byte(source))
	return hex.EncodeToString(h.Sum(nil))
}

// isDuplicateError checks if an error is a SQLite unique constraint violation.
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "UNIQUE constraint failed") || contains(msg, "duplicate")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsLower(s, substr)
}

func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
