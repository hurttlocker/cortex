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
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hurttlocker/cortex/internal/answer"
	"github.com/hurttlocker/cortex/internal/connect"
	"github.com/hurttlocker/cortex/internal/embed"
	"github.com/hurttlocker/cortex/internal/extract"
	"github.com/hurttlocker/cortex/internal/observe"
	"github.com/hurttlocker/cortex/internal/reason"
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
	AgentID  string         // if set, all operations are scoped to this agent
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

	// Server-level agent scope (if set, all tools default to this agent)
	defaultAgent := cfg.AgentID

	// Register tools
	registerSearchTool(s, searchEngine, defaultAgent)
	registerAnswerTool(s, searchEngine, defaultAgent)
	registerImportTool(s, cfg.Store, defaultAgent)
	registerStatsTool(s, observeEngine)
	registerFactsTool(s, cfg.Store, defaultAgent)
	registerStaleTool(s, observeEngine)
	registerReinforceTool(s, cfg.Store)
	registerReasonTool(s, searchEngine, cfg.Store)
	registerEdgeAddTool(s, cfg.Store)
	registerGraphTool(s, cfg.Store)
	registerGraphExportTool(s, cfg.Store)
	registerGraphExploreTool(s, cfg.Store)
	registerGraphImpactTool(s, cfg.Store)
	registerListClustersTool(s, cfg.Store)

	// Register connector management tools
	if sqlStore, ok := cfg.Store.(*store.SQLiteStore); ok {
		connStore := connect.NewConnectorStore(sqlStore.GetDB())
		registerConnectListTool(s, connStore)
		registerConnectAddTool(s, connStore)
		registerConnectSyncTool(s, connStore, cfg.Store)
		registerConnectStatusTool(s, connStore)
	}

	// Register resources
	registerStatsResource(s, observeEngine)
	registerRecentResource(s, cfg.Store)
	registerGraphSubjectsResource(s, cfg.Store)
	registerGraphClustersResource(s, cfg.Store)

	return s
}

// --- Tools ---

func registerSearchTool(s *server.MCPServer, engine *search.Engine, defaultAgent string) {
	tool := mcp.NewTool("cortex_search",
		mcp.WithDescription("Search your memory for information. Use when you need to recall past decisions, facts, preferences, or context. Returns ranked results with confidence scores and source provenance. Default mode is hybrid (keyword + semantic); use 'bm25' for exact keyword matching. NOT for exploring relationships between topics (use cortex_graph_explore) or synthesizing answers (use cortex_reason)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query string"),
		),
		mcp.WithString("mode",
			mcp.Description("Search mode: bm25, semantic, hybrid, or rrf (default: keyword)"),
			mcp.Enum("keyword", "bm25", "semantic", "hybrid", "rrf"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results (default: 10, max: 50)"),
		),
		mcp.WithString("project",
			mcp.Description("Scope search to a specific project (e.g., 'trading', 'eyes-web'). Empty = search all."),
		),
		mcp.WithString("agent_id",
			mcp.Description("Filter and boost results for a specific agent (e.g., 'mister', 'hawk'). Agent's facts rank higher; global facts still visible."),
		),
		mcp.WithString("source",
			mcp.Description("Filter results by source prefix (e.g., 'github', 'gmail'). Matches connector-imported records by provider name."),
		),
		mcp.WithString("intent",
			mcp.Description("Intent bucket filter before scoring: memory, import, connector, all (default all)."),
			mcp.Enum("memory", "import", "connector", "all"),
		),
		mcp.WithArray("source_boosts",
			mcp.Description("Optional source-specific score boosts. Array of {prefix, weight}. Weight defaults to 1.15 and is clamped to 1.0-2.0."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prefix": map[string]any{"type": "string"},
					"weight": map[string]any{"type": "number", "minimum": 1.0, "maximum": 2.0},
				},
				"required":             []string{"prefix"},
				"additionalProperties": false,
			}),
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

		if agentID, err := req.RequireString("agent_id"); err == nil && agentID != "" {
			opts.Agent = agentID
			opts.BoostAgent = agentID
		} else if defaultAgent != "" {
			opts.Agent = defaultAgent
			opts.BoostAgent = defaultAgent
		}

		if source, err := req.RequireString("source"); err == nil && source != "" {
			opts.Source = source
		}
		if intent, err := req.RequireString("intent"); err == nil && intent != "" {
			opts.Intent = intent
		}
		if boosts, err := parseMCPSourceBoosts(req); err == nil {
			opts.SourceBoosts = boosts
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("invalid source_boosts: %v", err)), nil
		}

		results, err := engine.Search(ctx, query, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search error: %v", err)), nil
		}

		data, _ := json.MarshalIndent(results, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

type mcpSourceBoost struct {
	Prefix string  `json:"prefix"`
	Weight float64 `json:"weight,omitempty"`
}

func parseMCPSourceBoosts(req mcp.CallToolRequest) ([]search.SourceBoost, error) {
	raw := req.GetArguments()
	if raw == nil {
		return nil, nil
	}
	v, ok := raw["source_boosts"]
	if !ok || v == nil {
		return nil, nil
	}
	blob, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var parsed []mcpSourceBoost
	if err := json.Unmarshal(blob, &parsed); err != nil {
		return nil, err
	}
	out := make([]search.SourceBoost, 0, len(parsed))
	for _, b := range parsed {
		prefix := strings.TrimSpace(b.Prefix)
		if prefix == "" {
			return nil, fmt.Errorf("prefix is required")
		}
		weight := b.Weight
		if weight == 0 {
			weight = 1.15
		}
		if weight < 1.0 || weight > 2.0 {
			return nil, fmt.Errorf("weight for prefix %q must be between 1.0 and 2.0", prefix)
		}
		out = append(out, search.SourceBoost{Prefix: prefix, Weight: weight})
	}
	return out, nil
}

func registerAnswerTool(s *server.MCPServer, searchEngine *search.Engine, defaultAgent string) {
	tool := mcp.NewTool("cortex_answer",
		mcp.WithDescription("Search memory, synthesize a short answer with citations, and return a single-pass result. No memory writes."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("query", mcp.Required(), mcp.Description("Question to answer")),
		mcp.WithString("mode", mcp.Description("Search mode: bm25, semantic, hybrid, or rrf (default: hybrid)"), mcp.Enum("keyword", "bm25", "semantic", "hybrid", "rrf")),
		mcp.WithNumber("limit", mcp.Description("Max search results to use (default 5, max 20)")),
		mcp.WithString("project", mcp.Description("Project scope (optional)")),
		mcp.WithString("agent_id", mcp.Description("Agent scope/filter (optional)")),
		mcp.WithString("source", mcp.Description("Filter by source prefix (optional)")),
		mcp.WithArray("source_boosts",
			mcp.Description("Optional source-specific score boosts. Array of {prefix, weight}."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prefix": map[string]any{"type": "string"},
					"weight": map[string]any{"type": "number", "minimum": 1.0, "maximum": 2.0},
				},
				"required":             []string{"prefix"},
				"additionalProperties": false,
			}),
		),
		mcp.WithString("model", mcp.Description("LLM model override provider/model (optional)")),
		mcp.WithNumber("maxSentences", mcp.Description("Max sentences in answer (default 6, max 12)")),
		mcp.WithNumber("maxContextChars", mcp.Description("Max context chars sent to LLM (default 5500)")),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		query, err := req.RequireString("query")
		if err != nil || strings.TrimSpace(query) == "" {
			return mcp.NewToolResultError("query is required"), nil
		}

		sopts := search.Options{Mode: search.ModeHybrid, Limit: 5}
		if modeStr, err := req.RequireString("mode"); err == nil && modeStr != "" {
			mode, err := search.ParseMode(modeStr)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid mode: %v", err)), nil
			}
			sopts.Mode = mode
		}
		if limitVal, err := req.RequireFloat("limit"); err == nil {
			limit := int(limitVal)
			if limit > 20 {
				limit = 20
			}
			if limit > 0 {
				sopts.Limit = limit
			}
		}
		if project, err := req.RequireString("project"); err == nil && project != "" {
			sopts.Project = project
		}
		if agentID, err := req.RequireString("agent_id"); err == nil && agentID != "" {
			sopts.Agent = agentID
			sopts.BoostAgent = agentID
		} else if defaultAgent != "" {
			sopts.Agent = defaultAgent
			sopts.BoostAgent = defaultAgent
		}
		if source, err := req.RequireString("source"); err == nil && source != "" {
			sopts.Source = source
		}
		if boosts, err := parseMCPSourceBoosts(req); err == nil {
			sopts.SourceBoosts = boosts
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("invalid source_boosts: %v", err)), nil
		}

		model := ""
		if v, err := req.RequireString("model"); err == nil {
			model = v
		}
		maxSentences := 6
		if v, err := req.RequireFloat("maxSentences"); err == nil && v > 0 {
			if int(v) > 12 {
				maxSentences = 12
			} else {
				maxSentences = int(v)
			}
		}
		maxContextChars := 5500
		if v, err := req.RequireFloat("maxContextChars"); err == nil && v >= 500 {
			maxContextChars = int(v)
		}

		provider, resolvedModel, _, err := answer.ResolveProvider(model)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("model resolution error: %v", err)), nil
		}

		eng := answer.NewEngine(searchEngine, provider, resolvedModel)
		result, err := eng.Answer(ctx, answer.Options{
			Query:           query,
			Search:          sopts,
			MaxSentences:    maxSentences,
			MaxContextChars: maxContextChars,
			PerResultChars:  1000,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("answer error: %v", err)), nil
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerImportTool(s *server.MCPServer, st store.Store, defaultAgent string) {
	tool := mcp.NewTool("cortex_import",
		mcp.WithDescription("Save new information to memory. Use when the user shares important facts, decisions, preferences, or context worth remembering. Content is chunked automatically. Set extract=true to pull out structured facts (people, dates, configs, decisions). Returns the IDs of created memories."),
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
		mcp.WithString("agent_id",
			mcp.Description("Agent identity for imported content (e.g., 'mister', 'hawk'). Tags both memories and extracted facts."),
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

		agentID := defaultAgent
		if a, err := req.RequireString("agent_id"); err == nil && a != "" {
			agentID = a
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
			if agentID != "" {
				mem.Metadata = &store.Metadata{AgentID: agentID}
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
		mcp.WithDescription("Get memory health overview: total memories, facts, sources, storage size, confidence distribution, and freshness breakdown. Use to understand what the agent knows and how fresh the knowledge is. Returns JSON with counts and breakdowns."),
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

func registerFactsTool(s *server.MCPServer, st store.Store, defaultAgent string) {
	tool := mcp.NewTool("cortex_facts",
		mcp.WithDescription("List structured facts extracted from memories. Facts are subject-predicate-object triples (e.g., 'Q → lives in → Philadelphia'). Filter by subject name or fact type. Use when you need specific factual data rather than full-text search results. NOT for free-text queries (use cortex_search)."),
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
		mcp.WithString("agent_id",
			mcp.Description("Filter facts by agent (returns agent's facts + global facts). Empty = all facts."),
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

		if agentID, err := req.RequireString("agent_id"); err == nil && agentID != "" {
			opts.Agent = agentID
		} else if defaultAgent != "" {
			opts.Agent = defaultAgent
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
		mcp.WithDescription("Find facts that are fading from memory due to time-based confidence decay. Facts not reinforced within the threshold period are returned. Use to identify knowledge that may be outdated or needs verification. Pair with cortex_reinforce to keep important facts fresh."),
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
		mcp.WithDescription("Reset the decay timer on important facts to keep them fresh. Pass fact IDs (from cortex_stale or cortex_facts) as comma-separated values. Use when a fact has been confirmed as still accurate and relevant. Returns count of reinforced facts."),
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

func registerReasonTool(s *server.MCPServer, searchEngine *search.Engine, st store.Store) {
	tool := mcp.NewTool("cortex_reason",
		mcp.WithDescription("Synthesize an answer by reasoning over multiple memories. Searches for relevant context, weighs by confidence, and produces a narrative answer. Use for complex questions that need multiple facts combined, not simple lookups (use cortex_search for those). Requires an LLM API key. Returns a reasoned analysis with citations."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The question or topic to reason about"),
		),
		mcp.WithString("preset",
			mcp.Description("Reasoning preset: daily-digest, fact-audit, weekly-dive, conflict-check, agent-review (default: daily-digest)"),
		),
		mcp.WithString("model",
			mcp.Description("LLM model to use (e.g., 'google/gemini-2.5-flash', 'deepseek/deepseek-v3.2', 'phi4-mini'). Default: auto-selects based on preset."),
		),
		mcp.WithString("project",
			mcp.Description("Scope reasoning to a specific project (e.g., 'trading', 'wedding'). Empty = all."),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query is required"), nil
		}

		presetName := "daily-digest"
		if p, err := req.RequireString("preset"); err == nil && p != "" {
			presetName = p
		}

		project := ""
		if p, err := req.RequireString("project"); err == nil && p != "" {
			project = p
		}

		// Determine model
		modelStr := ""
		if m, err := req.RequireString("model"); err == nil && m != "" {
			modelStr = m
		} else {
			// Smart defaults: deepseek for deep analysis, gemini for interactive
			switch presetName {
			case "weekly-dive", "fact-audit":
				modelStr = reason.DefaultCronModel
			default:
				modelStr = reason.DefaultInteractiveModel
			}
		}

		provider, model := reason.ParseProviderModel(modelStr)
		llm, err := reason.NewLLM(reason.LLMConfig{
			Provider: provider,
			Model:    model,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("LLM init error: %v", err)), nil
		}

		homeDir := ""
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h + "/.cortex"
		}

		engine := reason.NewEngine(reason.EngineConfig{
			SearchEngine: searchEngine,
			Store:        st,
			LLM:          llm,
			ConfigDir:    homeDir,
		})

		result, err := engine.Reason(ctx, reason.ReasonOptions{
			Query:   query,
			Preset:  presetName,
			Project: project,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("reason error: %v", err)), nil
		}

		// Format response with metadata
		output := map[string]interface{}{
			"content":       result.Content,
			"model":         result.Model,
			"provider":      result.Provider,
			"preset":        result.Preset,
			"memories_used": result.MemoriesUsed,
			"facts_used":    result.FactsUsed,
			"duration_ms":   result.Duration.Milliseconds(),
			"tokens_in":     result.TokensIn,
			"tokens_out":    result.TokensOut,
		}

		data, _ := json.MarshalIndent(output, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerEdgeAddTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_edge_add",
		mcp.WithDescription("Create an explicit relationship edge between two facts in the knowledge graph. Edge types: supports, contradicts, relates_to, supersedes, derived_from. Use when you discover a connection between existing facts. Returns the created edge ID."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithNumber("source_fact_id", mcp.Required(),
			mcp.Description("Source fact ID"),
		),
		mcp.WithNumber("target_fact_id", mcp.Required(),
			mcp.Description("Target fact ID"),
		),
		mcp.WithString("edge_type", mcp.Required(),
			mcp.Description("Relationship type: supports, contradicts, relates_to, supersedes, derived_from"),
		),
		mcp.WithString("agent_id",
			mcp.Description("Agent creating this edge"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("edges require SQLiteStore"), nil
		}

		sourceID, err := req.RequireFloat("source_fact_id")
		if err != nil {
			return mcp.NewToolResultError("source_fact_id required"), nil
		}
		targetID, err := req.RequireFloat("target_fact_id")
		if err != nil {
			return mcp.NewToolResultError("target_fact_id required"), nil
		}
		edgeTypeStr, err := req.RequireString("edge_type")
		if err != nil {
			return mcp.NewToolResultError("edge_type required"), nil
		}

		edgeType, err := store.ParseEdgeType(edgeTypeStr)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		agentID := ""
		if a, err := req.RequireString("agent_id"); err == nil && a != "" {
			agentID = a
		}

		edge := &store.FactEdge{
			SourceFactID: int64(sourceID),
			TargetFactID: int64(targetID),
			EdgeType:     edgeType,
			Source:       store.EdgeSourceExplicit,
			AgentID:      agentID,
		}

		if err := sqlStore.AddEdge(ctx, edge); err != nil {
			if errors.Is(err, store.ErrEdgeExists) {
				return mcp.NewToolResultText(fmt.Sprintf("Edge already exists: fact %d -[%s]→ fact %d", int64(sourceID), edgeType, int64(targetID))), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("add edge error: %v", err)), nil
		}

		result := map[string]interface{}{
			"id":             edge.ID,
			"source_fact_id": edge.SourceFactID,
			"target_fact_id": edge.TargetFactID,
			"edge_type":      edge.EdgeType,
			"message":        fmt.Sprintf("🔗 Edge created: fact %d -[%s]→ fact %d", int64(sourceID), edgeType, int64(targetID)),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerGraphTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_graph",
		mcp.WithDescription("Traverse the knowledge graph from a starting fact ID, following typed edges (supports, contradicts, relates_to, etc.) up to N hops deep. Returns connected facts with edge metadata. Use for understanding how a specific fact connects to other knowledge. NOT for exploring by topic name (use cortex_graph_explore)."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithNumber("fact_id", mcp.Required(),
			mcp.Description("Starting fact ID for graph traversal"),
		),
		mcp.WithNumber("depth",
			mcp.Description("Maximum traversal depth (default: 2, max: 5)"),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Minimum edge confidence to follow (default: 0, range: 0-1)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("graph requires SQLiteStore"), nil
		}

		factID, err := req.RequireFloat("fact_id")
		if err != nil {
			return mcp.NewToolResultError("fact_id required"), nil
		}

		depth := 2
		if d, err := req.RequireFloat("depth"); err == nil && d > 0 {
			depth = int(d)
			if depth > 5 {
				depth = 5
			}
		}

		minConf := 0.0
		if c, err := req.RequireFloat("min_confidence"); err == nil {
			minConf = c
		}

		nodes, err := sqlStore.TraverseGraph(ctx, int64(factID), depth, minConf)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph error: %v", err)), nil
		}

		if len(nodes) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No graph found from fact #%d", int64(factID))), nil
		}

		data, _ := json.MarshalIndent(nodes, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

// GraphExportNode is a fact for the visualization-friendly export format.
type GraphExportNode struct {
	ID         int64   `json:"id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	AgentID    string  `json:"agent_id,omitempty"`
	FactType   string  `json:"type"`
}

// GraphExportEdge is an edge for the visualization-friendly export format.
type GraphExportEdge struct {
	Source     int64   `json:"source"`
	Target     int64   `json:"target"`
	EdgeType   string  `json:"type"`
	Confidence float64 `json:"confidence"`
	SourceType string  `json:"source_type"`
}

// GraphExportCooccurrence is a co-occurrence pair for export.
type GraphExportCooccurrence struct {
	A     int64 `json:"a"`
	B     int64 `json:"b"`
	Count int   `json:"count"`
}

// GraphExportResult is the full visualization-ready graph export.
type GraphExportResult struct {
	Nodes         []GraphExportNode         `json:"nodes"`
	Edges         []GraphExportEdge         `json:"edges"`
	Cooccurrences []GraphExportCooccurrence `json:"cooccurrences"`
	Meta          map[string]interface{}    `json:"meta"`
}

func registerGraphExportTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("cortex_graph_export",
		mcp.WithDescription("Export a subgraph as structured JSON with nodes, edges, and co-occurrence data. Use for building visualizations or analyzing graph structure programmatically. Start from a fact_id and expand to the requested depth. NOT for browsing — use cortex_graph_explore for interactive exploration."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithNumber("fact_id", mcp.Required(),
			mcp.Description("Starting fact ID for graph traversal"),
		),
		mcp.WithNumber("depth",
			mcp.Description("Maximum traversal depth (default: 2, max: 5)"),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Minimum edge confidence to follow (default: 0, range: 0-1)"),
		),
		mcp.WithString("agent_id",
			mcp.Description("Filter to facts from a specific agent (empty = all)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("graph export requires SQLiteStore"), nil
		}

		factID, err := req.RequireFloat("fact_id")
		if err != nil {
			return mcp.NewToolResultError("fact_id required"), nil
		}

		depth := 2
		if d, err := req.RequireFloat("depth"); err == nil && d > 0 {
			depth = int(d)
			if depth > 5 {
				depth = 5
			}
		}

		minConf := 0.0
		if c, err := req.RequireFloat("min_confidence"); err == nil {
			minConf = c
		}

		agentFilter := ""
		if a, err := req.RequireString("agent_id"); err == nil {
			agentFilter = a
		}

		// Traverse graph
		graphNodes, err := sqlStore.TraverseGraph(ctx, int64(factID), depth, minConf)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph error: %v", err)), nil
		}

		// Build export format
		result := GraphExportResult{
			Meta: map[string]interface{}{
				"root_fact_id": int64(factID),
				"depth":        depth,
			},
		}

		seenNodes := make(map[int64]bool)
		var allFactIDs []int64

		for _, gn := range graphNodes {
			if gn.Fact == nil {
				continue
			}
			// Agent filter
			if agentFilter != "" && gn.Fact.AgentID != agentFilter && gn.Fact.AgentID != "" {
				continue
			}

			if !seenNodes[gn.Fact.ID] {
				seenNodes[gn.Fact.ID] = true
				allFactIDs = append(allFactIDs, gn.Fact.ID)

				result.Nodes = append(result.Nodes, GraphExportNode{
					ID:         gn.Fact.ID,
					Subject:    gn.Fact.Subject,
					Predicate:  gn.Fact.Predicate,
					Object:     gn.Fact.Object,
					Confidence: gn.Fact.Confidence,
					AgentID:    gn.Fact.AgentID,
					FactType:   gn.Fact.FactType,
				})
			}

			for _, edge := range gn.Edges {
				result.Edges = append(result.Edges, GraphExportEdge{
					Source:     edge.SourceFactID,
					Target:     edge.TargetFactID,
					EdgeType:   string(edge.EdgeType),
					Confidence: edge.Confidence,
					SourceType: string(edge.Source),
				})
			}
		}

		// Get co-occurrences for all involved facts
		for _, fid := range allFactIDs {
			coocs, err := sqlStore.GetCooccurrencesForFact(ctx, fid, 10)
			if err != nil {
				continue
			}
			for _, c := range coocs {
				// Only include co-occurrences where both facts are in the graph
				if seenNodes[c.FactIDA] && seenNodes[c.FactIDB] {
					result.Cooccurrences = append(result.Cooccurrences, GraphExportCooccurrence{
						A:     c.FactIDA,
						B:     c.FactIDB,
						Count: c.Count,
					})
				}
			}
		}

		// Deduplicate co-occurrences
		result.Cooccurrences = dedupeCooccurrences(result.Cooccurrences)

		result.Meta["total_nodes"] = len(result.Nodes)
		result.Meta["total_edges"] = len(result.Edges)
		result.Meta["total_cooccurrences"] = len(result.Cooccurrences)

		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func dedupeCooccurrences(coocs []GraphExportCooccurrence) []GraphExportCooccurrence {
	seen := make(map[[2]int64]bool)
	var result []GraphExportCooccurrence
	for _, c := range coocs {
		key := [2]int64{c.A, c.B}
		if c.A > c.B {
			key = [2]int64{c.B, c.A}
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, c)
		}
	}
	return result
}

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

// --- Connect tools ---

func registerConnectListTool(s *server.MCPServer, connStore *connect.ConnectorStore) {
	tool := mcp.NewTool("cortex_connect_list",
		mcp.WithDescription("List all configured external source connectors (GitHub, Gmail, Slack, etc.) with their sync status, last sync time, and record counts. Use to check what data sources are connected and whether syncs are healthy."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithBoolean("enabled_only",
			mcp.Description("Only show enabled connectors (default: false, show all)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		enabledOnly := false
		if e, err := req.RequireString("enabled_only"); err == nil && e == "true" {
			enabledOnly = true
		}

		connectors, err := connStore.List(ctx, enabledOnly)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list connectors error: %v", err)), nil
		}

		if len(connectors) == 0 {
			return mcp.NewToolResultText("No connectors configured. Use cortex_connect_add to set one up."), nil
		}

		type connectorInfo struct {
			ID              int64      `json:"id"`
			Provider        string     `json:"provider"`
			Enabled         bool       `json:"enabled"`
			LastSyncAt      *time.Time `json:"last_sync_at,omitempty"`
			LastError       string     `json:"last_error,omitempty"`
			RecordsImported int64      `json:"records_imported"`
		}

		var infos []connectorInfo
		for _, c := range connectors {
			infos = append(infos, connectorInfo{
				ID:              c.ID,
				Provider:        c.Provider,
				Enabled:         c.Enabled,
				LastSyncAt:      c.LastSyncAt,
				LastError:       c.LastError,
				RecordsImported: c.RecordsImported,
			})
		}

		data, _ := json.MarshalIndent(infos, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerConnectAddTool(s *server.MCPServer, connStore *connect.ConnectorStore) {
	// Build provider list
	providerNames := connect.DefaultRegistry.List()

	tool := mcp.NewTool("cortex_connect_add",
		mcp.WithDescription(fmt.Sprintf("Add a new external data source connector. Available providers: %s. Pass provider-specific config as JSON (tokens, repos, channels, etc.). After adding, use cortex_connect_sync to fetch data.", strings.Join(providerNames, ", "))),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("provider", mcp.Required(),
			mcp.Description("Provider name (e.g., github, gmail, slack, calendar, drive)"),
		),
		mcp.WithString("config", mcp.Required(),
			mcp.Description("Provider configuration as JSON string"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		providerName, err := req.RequireString("provider")
		if err != nil {
			return mcp.NewToolResultError("provider is required"), nil
		}

		configStr, err := req.RequireString("config")
		if err != nil {
			return mcp.NewToolResultError("config is required"), nil
		}

		// Validate provider exists
		provider := connect.DefaultRegistry.Get(providerName)
		if provider == nil {
			return mcp.NewToolResultError(fmt.Sprintf("unknown provider %q. Available: %s", providerName, strings.Join(providerNames, ", "))), nil
		}

		// Validate config
		configJSON := json.RawMessage(configStr)
		if err := provider.ValidateConfig(configJSON); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid config: %v", err)), nil
		}

		// Add connector
		id, err := connStore.Add(ctx, providerName, configJSON)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("add connector error: %v", err)), nil
		}

		result := map[string]interface{}{
			"id":       id,
			"provider": providerName,
			"message":  fmt.Sprintf("✅ Connector %q added (id: %d). Run cortex_connect_sync to sync.", providerName, id),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerConnectSyncTool(s *server.MCPServer, connStore *connect.ConnectorStore, memStore store.Store) {
	tool := mcp.NewTool("cortex_connect_sync",
		mcp.WithDescription("Fetch new data from an external source and import into memory. Specify a provider name to sync one connector, or omit to sync all. Set extract=true to automatically extract facts from imported records. Returns count of new records imported."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("provider",
			mcp.Description("Provider name to sync (e.g., github). Leave empty to sync all enabled connectors."),
		),
		mcp.WithBoolean("extract",
			mcp.Description("Run fact extraction on imported records (default: false)"),
		),
		mcp.WithBoolean("no_infer",
			mcp.Description("Skip edge inference after extraction (default: false)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		engine := connect.NewSyncEngine(connect.DefaultRegistry, connStore, memStore, false)

		providerName, _ := req.RequireString("provider")
		extractEnabled := false
		if ext, err := req.RequireString("extract"); err == nil {
			extractEnabled = ext == "true"
		}
		noInfer := false
		if ni, err := req.RequireString("no_infer"); err == nil {
			noInfer = ni == "true"
		}

		opts := connect.SyncOptions{
			Extract: extractEnabled,
			NoInfer: noInfer,
		}

		if providerName != "" {
			result, err := engine.SyncProvider(ctx, providerName, opts)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("sync error: %v", err)), nil
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		// Sync all
		results, err := engine.SyncAll(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("sync all error: %v", err)), nil
		}
		if len(results) == 0 {
			return mcp.NewToolResultText("No enabled connectors to sync."), nil
		}

		data, _ := json.MarshalIndent(results, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerConnectStatusTool(s *server.MCPServer, connStore *connect.ConnectorStore) {
	tool := mcp.NewTool("cortex_connect_status",
		mcp.WithDescription("Get detailed health status for a specific connector: config (tokens redacted), last sync time, sync history, error state, and record counts. Use to diagnose sync failures or check connector configuration."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("provider", mcp.Required(),
			mcp.Description("Provider name to get status for"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		providerName, err := req.RequireString("provider")
		if err != nil {
			return mcp.NewToolResultError("provider is required"), nil
		}

		c, err := connStore.Get(ctx, providerName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("connector %q: %v", providerName, err)), nil
		}

		// Redact sensitive fields from config
		redacted := redactConfig(c.Config)

		status := map[string]interface{}{
			"id":               c.ID,
			"provider":         c.Provider,
			"enabled":          c.Enabled,
			"config":           json.RawMessage(redacted),
			"last_sync_at":     c.LastSyncAt,
			"last_error":       c.LastError,
			"records_imported": c.RecordsImported,
			"created_at":       c.CreatedAt,
			"updated_at":       c.UpdatedAt,
		}

		data, _ := json.MarshalIndent(status, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

// redactConfig masks sensitive fields (token, password, secret) in connector config.
func redactConfig(config json.RawMessage) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return "{}"
	}

	sensitiveKeys := []string{"token", "password", "secret", "api_key", "apikey"}
	for k, v := range parsed {
		lk := toLower(k)
		for _, sk := range sensitiveKeys {
			if contains(lk, sk) {
				if s, ok := v.(string); ok && len(s) > 8 {
					parsed[k] = s[:4] + "..." + s[len(s)-4:]
				} else {
					parsed[k] = "***"
				}
			}
		}
	}

	data, _ := json.MarshalIndent(parsed, "", "  ")
	return string(data)
}
