// Package mcp provides a Model Context Protocol server for Cortex.
//
// It exposes Cortex's memory capabilities (search, import, stats, facts, stale)
// as MCP tools, and memory statistics and recent memories as MCP resources.
// Supports stdio transport (for Claude Desktop, Cursor, OpenClaw) and
// optional HTTP+SSE transport for remote access.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/extract"
	"github.com/hurttlocker/cortex/internal/observe"
	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ServerConfig holds configuration for the MCP server.
type ServerConfig struct {
	Store    store.Store
	DBPath   string
	Version  string         // version string for MCP server info
	Embedder embed.Embedder // optional, for semantic/hybrid search
}

// dbMu serializes all MCP tool calls that touch the database.
// The mcp-go library dispatches handlers concurrently via goroutines.
// SQLite (even with WAL) supports only one writer at a time, and concurrent
// reads during writes can return stale results. A global mutex ensures
// correct ordering: imports complete before searches see their data.
var dbMu sync.Mutex

// NewServer creates a configured MCP server with all Cortex tools and resources.
func NewServer(cfg ServerConfig) *server.MCPServer {
	ver := cfg.Version
	if ver == "" {
		ver = "dev"
	}

	s := server.NewMCPServer(
		"Cortex",
		ver,
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(true, false),
	)

	searchEngine := search.NewEngine(cfg.Store)
	if cfg.Embedder != nil {
		searchEngine = search.NewEngineWithEmbedder(cfg.Store, cfg.Embedder)
	}

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = store.DefaultDBPath
	}
	observeEngine := observe.NewEngine(cfg.Store, dbPath)

	// Register tools
	registerSearchTool(s, searchEngine)
	registerImportTool(s, cfg.Store)
	registerStatsTool(s, observeEngine)
	registerFactsTool(s, cfg.Store)
	registerStaleTool(s, observeEngine)
	registerReinforceTool(s, cfg.Store)

	// Register resources
	registerStatsResource(s, observeEngine)
	registerRecentResource(s, cfg.Store)

	return s
}

// --- Tools ---

func registerSearchTool(s *server.MCPServer, engine *search.Engine) {
	tool := mcp.NewTool("cortex_search",
		mcp.WithDescription("Search Cortex memory using BM25 keyword, semantic, or hybrid search. Returns scored results with source provenance. Optionally scope by project."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query string"),
		),
		mcp.WithString("mode",
			mcp.Description("Search mode: bm25, semantic, or hybrid (default: hybrid)"),
			mcp.Enum("keyword", "bm25", "semantic", "hybrid"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default: 10, max: 50)"),
		),
		mcp.WithString("project",
			mcp.Description("Scope search to a specific project (e.g., 'trading', 'eyes-web'). Empty = search all."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query is required"), nil
		}

		opts := search.DefaultOptions()

		if modeStr, err := req.RequireString("mode"); err == nil && modeStr != "" {
			mode, err := search.ParseMode(modeStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid mode: %v", err)), nil
			}
			opts.Mode = mode
		}

		if limitVal, err := req.RequireFloat("limit"); err == nil {
			limit := int(limitVal)
			if limit > 50 {
				limit = 50
			}
			if limit > 0 {
				opts.Limit = limit
			}
		}

		if project, err := req.RequireString("project"); err == nil && project != "" {
			opts.Project = project
		}

		results, err := engine.Search(ctx, query, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search error: %v", err)), nil
		}

		data, _ := json.MarshalIndent(results, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerImportTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_import",
		mcp.WithDescription("Import a new memory into Cortex. Large content is automatically chunked (max 1500 chars per chunk). Optionally extracts facts using rule-based extraction."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("The text content to import as a memory"),
		),
		mcp.WithString("source",
			mcp.Description("Source identifier (e.g. filename, URL). Defaults to 'mcp-import'."),
		),
		mcp.WithBoolean("extract",
			mcp.Description("Extract facts from imported content using rule-based extraction (default: false)"),
		),
		mcp.WithString("project",
			mcp.Description("Project tag for imported memories (e.g., 'trading', 'eyes-web'). Empty = untagged."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError("content is required"), nil
		}
		if strings.TrimSpace(content) == "" {
			return mcp.NewToolResultError("memory content cannot be empty"), nil
		}

		// Strip null bytes from content
		content = strings.ReplaceAll(content, "\x00", "")

		source := "mcp-import"
		if s, err := req.RequireString("source"); err == nil && s != "" {
			// Sanitize source: strip path traversal
			s = strings.ReplaceAll(s, "..", "")
			s = strings.ReplaceAll(s, "/", "-")
			s = strings.ReplaceAll(s, "\\", "-")
			if s != "" {
				source = s
			}
		}

		enableExtract := false
		if ext, err := req.RequireString("extract"); err == nil {
			enableExtract = ext == "true"
		}

		project := ""
		if p, err := req.RequireString("project"); err == nil && p != "" {
			project = p
		}

		// Chunk large content (same 1500-char max as CLI import)
		chunks := chunkContent(content, 1500)

		var ids []int64
		for i, chunk := range chunks {
			mem := &store.Memory{
				Content:    chunk,
				SourceFile: source,
				SourceLine: i + 1,
				Project:    project,
				ImportedAt: time.Now().UTC(),
				UpdatedAt:  time.Now().UTC(),
			}

			id, err := st.AddMemory(ctx, mem)
			if err != nil {
				// Skip duplicates, report others
				if strings.Contains(err.Error(), "UNIQUE constraint") {
					continue
				}
				return mcp.NewToolResultError(fmt.Sprintf("import error: %v", err)), nil
			}
			ids = append(ids, id)
		}

		// Extract facts if requested
		factsExtracted := 0
		if enableExtract && len(ids) > 0 {
			factsExtracted = extractFactsFromMemories(ctx, st, ids)
		}

		result := map[string]interface{}{
			"ids":     ids,
			"chunks":  len(chunks),
			"stored":  len(ids),
			"source":  source,
			"message": fmt.Sprintf("Imported %d memory chunk(s)", len(ids)),
		}
		if enableExtract {
			result["facts_extracted"] = factsExtracted
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerStatsTool(s *server.MCPServer, engine *observe.Engine) {
	tool := mcp.NewTool("cortex_stats",
		mcp.WithDescription("Get comprehensive Cortex memory statistics: total memories, facts, sources, storage size, confidence distribution, and freshness."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		stats, err := engine.GetStats(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("stats error: %v", err)), nil
		}

		data, _ := json.MarshalIndent(stats, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerFactsTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_facts",
		mcp.WithDescription("Query extracted facts from Cortex memory. Facts are subject-predicate-object triples with confidence scores and provenance."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("subject",
			mcp.Description("Filter facts by subject (case-insensitive partial match)"),
		),
		mcp.WithString("type",
			mcp.Description("Filter facts by type (e.g. 'attribute', 'relationship')"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of facts to return (default: 20, max: 100)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		opts := store.ListOpts{Limit: 20}

		if limitVal, err := req.RequireFloat("limit"); err == nil {
			limit := int(limitVal)
			if limit > 100 {
				limit = 100
			}
			if limit > 0 {
				opts.Limit = limit
			}
		}

		if factType, err := req.RequireString("type"); err == nil && factType != "" {
			opts.FactType = factType
		}

		facts, err := st.ListFacts(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("facts error: %v", err)), nil
		}

		// Filter by subject if provided (store.ListOpts doesn't support subject filter directly)
		subject := ""
		if s, err := req.RequireString("subject"); err == nil && s != "" {
			subject = s
		}

		var filtered []*store.Fact
		if subject != "" {
			for _, f := range facts {
				if containsInsensitive(f.Subject, subject) {
					filtered = append(filtered, f)
				}
			}
		} else {
			filtered = facts
		}

		data, _ := json.MarshalIndent(filtered, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerStaleTool(s *server.MCPServer, engine *observe.Engine) {
	tool := mcp.NewTool("cortex_stale",
		mcp.WithDescription("Find stale facts — facts whose confidence has decayed below threshold due to not being reinforced. Uses Ebbinghaus exponential decay."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithNumber("max_confidence",
			mcp.Description("Effective confidence threshold (default: 0.5). Facts below this are returned."),
		),
		mcp.WithNumber("max_days",
			mcp.Description("Days without reinforcement threshold (default: 30)."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of stale facts to return (default: 20, max: 100)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		opts := observe.StaleOpts{
			MaxConfidence: 0.5,
			MaxDays:       30,
			Limit:         20,
		}

		if mc, err := req.RequireFloat("max_confidence"); err == nil && mc > 0 {
			opts.MaxConfidence = mc
		}
		if md, err := req.RequireFloat("max_days"); err == nil && md > 0 {
			opts.MaxDays = int(md)
		}
		if l, err := req.RequireFloat("limit"); err == nil && l > 0 {
			limit := int(l)
			if limit > 100 {
				limit = 100
			}
			opts.Limit = limit
		}

		stale, err := engine.GetStaleFacts(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("stale facts error: %v", err)), nil
		}

		data, _ := json.MarshalIndent(stale, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerReinforceTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_reinforce",
		mcp.WithDescription("Reinforce one or more facts by ID, resetting their Ebbinghaus decay timer. Use after cortex_stale to keep important facts fresh."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("fact_ids",
			mcp.Required(),
			mcp.Description("Comma-separated fact IDs to reinforce (e.g. '42,108,256')"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		idsStr, err := req.RequireString("fact_ids")
		if err != nil {
			return mcp.NewToolResultError("fact_ids is required"), nil
		}
		idsStr = strings.TrimSpace(idsStr)
		if idsStr == "" {
			return mcp.NewToolResultError("fact_ids cannot be empty"), nil
		}

		parts := strings.Split(idsStr, ",")
		reinforced := 0
		var errors []string

		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			var id int64
			if _, err := fmt.Sscanf(part, "%d", &id); err != nil {
				errors = append(errors, fmt.Sprintf("invalid ID %q", part))
				continue
			}
			if err := st.ReinforceFact(ctx, id); err != nil {
				errors = append(errors, fmt.Sprintf("fact %d: %v", id, err))
				continue
			}
			reinforced++
		}

		result := map[string]interface{}{
			"reinforced": reinforced,
			"message":    fmt.Sprintf("Reinforced %d fact(s)", reinforced),
		}
		if len(errors) > 0 {
			result["errors"] = errors
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Resources ---

func registerStatsResource(s *server.MCPServer, engine *observe.Engine) {
	resource := mcp.NewResource(
		"cortex://stats",
		"Memory Statistics",
		mcp.WithResourceDescription("Comprehensive Cortex memory statistics including counts, storage, confidence, and freshness distribution."),
		mcp.WithMIMEType("application/json"),
	)

	s.AddResource(resource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		stats, err := engine.GetStats(ctx)
		if err != nil {
			return nil, fmt.Errorf("getting stats: %w", err)
		}

		data, _ := json.MarshalIndent(stats, "", "  ")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	})
}

func registerRecentResource(s *server.MCPServer, st store.Store) {
	resource := mcp.NewResource(
		"cortex://recent",
		"Recent Memories",
		mcp.WithResourceDescription("The 20 most recently imported memories."),
		mcp.WithMIMEType("application/json"),
	)

	s.AddResource(resource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		memories, err := st.ListMemories(ctx, store.ListOpts{
			Limit:  20,
			SortBy: "date",
		})
		if err != nil {
			return nil, fmt.Errorf("listing recent memories: %w", err)
		}

		// Build compact representation
		type recentMemory struct {
			ID         int64  `json:"id"`
			Source     string `json:"source"`
			Snippet    string `json:"snippet"`
			ImportedAt string `json:"imported_at"`
		}
		recent := make([]recentMemory, 0, len(memories))
		for _, m := range memories {
			snippet := m.Content
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
			recent = append(recent, recentMemory{
				ID:         m.ID,
				Source:     m.SourceFile,
				Snippet:    snippet,
				ImportedAt: m.ImportedAt.Format(time.RFC3339),
			})
		}

		data, _ := json.MarshalIndent(recent, "", "  ")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	})
}

// --- Helpers ---

// chunkContent splits large text into chunks at paragraph boundaries.
// Chunks are at most maxChars long, split on \n\n > \n > word boundary.
func chunkContent(content string, maxChars int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if len(content) <= maxChars {
		return []string{content}
	}

	var chunks []string
	// Split on double newlines first (paragraph boundaries)
	paragraphs := strings.Split(content, "\n\n")

	var current strings.Builder
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// If adding this paragraph would exceed max, flush current
		if current.Len() > 0 && current.Len()+len(para)+2 > maxChars {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}

		// If a single paragraph exceeds max, split on line boundaries
		if len(para) > maxChars {
			if current.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			lines := strings.Split(para, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if current.Len()+len(line)+1 > maxChars && current.Len() > 0 {
					chunks = append(chunks, strings.TrimSpace(current.String()))
					current.Reset()
				}
				if current.Len() > 0 {
					current.WriteString("\n")
				}
				current.WriteString(line)
			}
		} else {
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(para)
		}
	}

	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	// Filter out tiny chunks (< 20 chars)
	var filtered []string
	for _, c := range chunks {
		if len(c) >= 20 {
			filtered = append(filtered, c)
		} else if len(filtered) > 0 {
			// Merge tiny chunk with previous
			filtered[len(filtered)-1] += "\n" + c
		}
	}

	if len(filtered) == 0 && len(chunks) > 0 {
		return chunks // Don't lose content if all chunks are tiny
	}
	return filtered
}

// extractFactsFromMemories runs rule-based fact extraction on imported memories.
func extractFactsFromMemories(ctx context.Context, st store.Store, memoryIDs []int64) int {
	memories, err := st.GetMemoriesByIDs(ctx, memoryIDs)
	if err != nil || len(memories) == 0 {
		return 0
	}

	pipeline := extract.NewPipeline(nil)
	totalFacts := 0

	for _, mem := range memories {
		metadata := map[string]string{
			"source_file":    mem.SourceFile,
			"source_section": mem.SourceSection,
		}
		facts, err := pipeline.Extract(ctx, mem.Content, metadata)
		if err != nil || len(facts) == 0 {
			continue
		}

		for _, ef := range facts {
			f := &store.Fact{
				MemoryID:    mem.ID,
				Subject:     ef.Subject,
				Predicate:   ef.Predicate,
				Object:      ef.Object,
				FactType:    ef.FactType,
				Confidence:  ef.Confidence,
				DecayRate:   ef.DecayRate,
				SourceQuote: ef.SourceQuote,
			}
			if _, err := st.AddFact(ctx, f); err == nil {
				totalFacts++
			}
		}
	}

	return totalFacts
}

func containsInsensitive(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(substr) == 0 ||
			findInsensitive(s, substr))
}

func findInsensitive(s, substr string) bool {
	sLower := toLower(s)
	subLower := toLower(substr)
	return contains(sLower, subLower)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
