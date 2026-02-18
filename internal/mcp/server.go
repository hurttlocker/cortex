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
	"time"

	"github.com/hurttlocker/cortex/internal/embed"
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

	// Register resources
	registerStatsResource(s, observeEngine)
	registerRecentResource(s, cfg.Store)

	return s
}

// --- Tools ---

func registerSearchTool(s *server.MCPServer, engine *search.Engine) {
	tool := mcp.NewTool("cortex_search",
		mcp.WithDescription("Search Cortex memory using BM25 keyword, semantic, or hybrid search. Returns scored results with source provenance."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query string"),
		),
		mcp.WithString("mode",
			mcp.Description("Search mode: bm25, semantic, or hybrid (default: hybrid)"),
			mcp.Enum("bm25", "semantic", "hybrid"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default: 10, max: 50)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		mcp.WithDescription("Import a new memory into Cortex. The memory is stored with content-hash deduplication."),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("The text content to import as a memory"),
		),
		mcp.WithString("source",
			mcp.Description("Source identifier (e.g. filename, URL). Defaults to 'mcp-import'."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError("content is required"), nil
		}

		source := "mcp-import"
		if s, err := req.RequireString("source"); err == nil && s != "" {
			source = s
		}

		mem := &store.Memory{
			Content:    content,
			SourceFile: source,
			ImportedAt: time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}

		id, err := st.AddMemory(ctx, mem)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("import error: %v", err)), nil
		}

		result := map[string]interface{}{
			"id":      id,
			"source":  source,
			"message": "Memory imported successfully",
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerStatsTool(s *server.MCPServer, engine *observe.Engine) {
	tool := mcp.NewTool("cortex_stats",
		mcp.WithDescription("Get comprehensive Cortex memory statistics: total memories, facts, sources, storage size, confidence distribution, and freshness."),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

// --- Resources ---

func registerStatsResource(s *server.MCPServer, engine *observe.Engine) {
	resource := mcp.NewResource(
		"cortex://stats",
		"Memory Statistics",
		mcp.WithResourceDescription("Comprehensive Cortex memory statistics including counts, storage, confidence, and freshness distribution."),
		mcp.WithMIMEType("application/json"),
	)

	s.AddResource(resource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
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
