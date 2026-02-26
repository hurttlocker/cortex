package connect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/extract"
	"github.com/hurttlocker/cortex/internal/store"
)

// SyncOptions controls sync behavior beyond the default fetch-and-store.
type SyncOptions struct {
	// Extract enables fact extraction on newly imported memories.
	Extract bool

	// Enrich enables LLM-powered fact enrichment after rule-based extraction.
	// Requires LLM to be set. Additive only — never removes rule-extracted facts.
	Enrich bool

	// NoInfer disables automatic edge inference after extraction.
	// Only meaningful when Extract is true.
	NoInfer bool

	// LLM optionally specifies an LLM provider/model for extraction (e.g., "ollama/llama3").
	// If empty, only rule-based extraction runs.
	LLM string

	// AgentID tags all imported memories/facts with this agent identity.
	AgentID string
}

// SyncEngine orchestrates connector syncs through the standard Cortex ingest pipeline.
type SyncEngine struct {
	registry  *Registry
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
func (se *SyncEngine) SyncAll(ctx context.Context, opts ...SyncOptions) ([]SyncResult, error) {
	opt := SyncOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}

	connectors, err := se.connStore.List(ctx, true) // enabled only
	if err != nil {
		return nil, fmt.Errorf("listing connectors: %w", err)
	}
	if len(connectors) == 0 {
		return nil, nil
	}

	var results []SyncResult
	for _, c := range connectors {
		result := se.SyncOne(ctx, c, opt)
		results = append(results, result)
	}
	return results, nil
}

// SyncProvider runs sync for a specific provider by name.
func (se *SyncEngine) SyncProvider(ctx context.Context, providerName string, opts ...SyncOptions) (SyncResult, error) {
	opt := SyncOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}

	c, err := se.connStore.Get(ctx, providerName)
	if err != nil {
		return SyncResult{Provider: providerName, Error: err.Error()}, err
	}
	if !c.Enabled {
		return SyncResult{Provider: providerName, Error: "connector is disabled"}, fmt.Errorf("connector %q is disabled", providerName)
	}
	return se.SyncOne(ctx, c, opt), nil
}

// SyncOne runs sync for a single connector.
func (se *SyncEngine) SyncOne(ctx context.Context, c *Connector, opts ...SyncOptions) SyncResult {
	opt := SyncOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}

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
	var importedMemoryIDs []int64
	for i := range records {
		// Tag records with agent identity from sync options
		if opt.AgentID != "" && records[i].AgentID == "" {
			records[i].AgentID = opt.AgentID
		}
		rec := records[i]
		memID, imported, err := se.importRecord(ctx, c.Provider, rec)
		if err != nil {
			if se.verbose {
				fmt.Printf("  warning: failed to import record %s: %v\n", rec.ExternalID, err)
			}
			result.RecordsSkipped++
			continue
		}
		if imported {
			result.RecordsImported++
			if memID > 0 {
				importedMemoryIDs = append(importedMemoryIDs, memID)
			}
		} else {
			result.RecordsSkipped++ // deduplicated
		}
	}

	// Run fact extraction on newly imported memories
	if opt.Extract && len(importedMemoryIDs) > 0 {
		factsExtracted, newFactIDs, err := se.extractFacts(ctx, importedMemoryIDs, opt.LLM)
		if err != nil {
			if se.verbose {
				fmt.Printf("  warning: extraction error: %v\n", err)
			}
		} else {
			result.FactsExtracted = factsExtracted
			if se.verbose {
				fmt.Printf("  Facts extracted: %d\n", factsExtracted)
			}
		}

		if sqlStore, ok := se.memStore.(*store.SQLiteStore); ok && len(newFactIDs) > 0 {
			if _, err := sqlStore.UpdateClusters(ctx, newFactIDs); err != nil && se.verbose {
				fmt.Printf("  warning: cluster update error: %v\n", err)
			}
		}

		// Run edge inference after extraction (unless --no-infer)
		if !opt.NoInfer && factsExtracted > 0 {
			edgesCreated, err := se.inferEdges(ctx)
			if err != nil {
				if se.verbose {
					fmt.Printf("  warning: inference error: %v\n", err)
				}
			} else {
				result.EdgesInferred = edgesCreated
				if se.verbose {
					fmt.Printf("  Edges inferred: %d\n", edgesCreated)
				}
			}
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
// Returns (memoryID, true, nil) if imported, (0, false, nil) if deduplicated, (0, false, err) on error.
func (se *SyncEngine) importRecord(ctx context.Context, provider string, rec Record) (int64, bool, error) {
	// Build content hash for deduplication
	hash := contentHash(rec.Content, rec.Source)

	// Check for existing memory with same hash
	existing, err := se.memStore.FindByHash(ctx, hash)
	if err != nil {
		return 0, false, fmt.Errorf("checking hash: %w", err)
	}
	if existing != nil {
		// Already imported — skip
		return 0, false, nil
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
	if rec.AgentID != "" {
		mem.Metadata = &store.Metadata{AgentID: rec.AgentID}
	}

	id, err := se.memStore.AddMemory(ctx, mem)
	if err != nil {
		// Duplicate hash constraint — treat as dedup
		if isDuplicateError(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("storing memory: %w", err)
	}

	return id, true, nil
}

// extractFacts runs the fact extraction pipeline on a set of memory IDs.
// Returns the total number of facts extracted.
func (se *SyncEngine) extractFacts(ctx context.Context, memoryIDs []int64, llmFlag string) (int, []int64, error) {
	// Configure LLM if requested
	var llmConfig *extract.LLMConfig
	if llmFlag != "" {
		var err error
		llmConfig, err = extract.ResolveLLMConfig(llmFlag)
		if err != nil {
			return 0, nil, fmt.Errorf("configuring LLM: %w", err)
		}
		if llmConfig != nil {
			if err := llmConfig.Validate(); err != nil {
				return 0, nil, fmt.Errorf("invalid LLM configuration: %w", err)
			}
		}
	}

	pipeline := extract.NewPipeline(llmConfig)
	totalFacts := 0
	newFactIDs := make([]int64, 0, len(memoryIDs)*4)

	for _, memID := range memoryIDs {
		// Retrieve the memory content
		mem, err := se.memStore.GetMemory(ctx, memID)
		if err != nil {
			continue // skip individual failures
		}

		// Build metadata for extraction context
		metadata := map[string]string{
			"source_file": mem.SourceFile,
		}
		if strings.HasSuffix(strings.ToLower(mem.SourceFile), ".md") {
			metadata["format"] = "markdown"
		}
		if mem.SourceSection != "" {
			metadata["source_section"] = mem.SourceSection
		}

		// Extract facts
		facts, err := pipeline.Extract(ctx, mem.Content, metadata)
		if err != nil {
			continue // skip extraction errors
		}

		// Store each extracted fact
		for _, ef := range facts {
			fact := &store.Fact{
				MemoryID:    mem.ID,
				Subject:     ef.Subject,
				Predicate:   ef.Predicate,
				Object:      ef.Object,
				FactType:    ef.FactType,
				Confidence:  ef.Confidence,
				DecayRate:   ef.DecayRate,
				SourceQuote: ef.SourceQuote,
			}
			id, err := se.memStore.AddFact(ctx, fact)
			if err != nil {
				continue // skip storage errors
			}
			totalFacts++
			newFactIDs = append(newFactIDs, id)
		}
	}

	return totalFacts, newFactIDs, nil
}

// inferEdges runs the relationship inference engine to create knowledge graph edges.
// Returns the number of new edges created.
func (se *SyncEngine) inferEdges(ctx context.Context) (int, error) {
	sqlStore, ok := se.memStore.(*store.SQLiteStore)
	if !ok {
		return 0, fmt.Errorf("edge inference requires SQLite store")
	}

	opts := store.DefaultInferenceOpts()
	result, err := sqlStore.RunInference(ctx, opts)
	if err != nil {
		return 0, fmt.Errorf("running inference: %w", err)
	}

	return result.EdgesCreated, nil
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
