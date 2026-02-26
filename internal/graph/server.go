// Package graph provides an interactive knowledge graph visualizer.
// It embeds a self-contained HTML/JS application that renders fact relationships
// using D3.js force-directed layout and fetches data from a local API endpoint.
package graph

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	searchpkg "github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

//go:embed visualizer.html cortex-icon-192.png
var visualizerFS embed.FS

// ServerConfig holds settings for the graph visualization server.
type ServerConfig struct {
	Store       *store.SQLiteStore
	Port        int
	AgentFilter string // if set, all API responses are scoped to this agent
}

// ExportNode is the visualization-friendly format for a fact.
type ExportNode struct {
	ID           int64    `json:"id"`
	Subject      string   `json:"subject"`
	Predicate    string   `json:"predicate"`
	Object       string   `json:"object"`
	Confidence   float64  `json:"confidence"`
	Relevance    float64  `json:"relevance,omitempty"`
	Rank         int      `json:"rank,omitempty"`
	AgentID      string   `json:"agent_id,omitempty"`
	FactType     string   `json:"type"`
	ClusterID    int64    `json:"cluster_id,omitempty"`
	ClusterColor string   `json:"cluster_color,omitempty"`
	FactCount    int      `json:"fact_count,omitempty"`
	LastUpdated  string   `json:"last_updated,omitempty"`
	SourceTypes  []string `json:"source_types,omitempty"`
	Depth        int      `json:"depth,omitempty"`
}

// ExportEdge is the visualization-friendly format for an edge.
type ExportEdge struct {
	Source     int64   `json:"source"`
	Target     int64   `json:"target"`
	EdgeType   string  `json:"type"`
	Confidence float64 `json:"confidence"`
	SourceType string  `json:"source_type"`
}

// ExportCooccurrence is a co-occurrence pair.
type ExportCooccurrence struct {
	A     int64 `json:"a"`
	B     int64 `json:"b"`
	Count int   `json:"count"`
}

// ExportResult is the full graph export payload.
type ExportResult struct {
	Nodes         []ExportNode           `json:"nodes"`
	Edges         []ExportEdge           `json:"edges"`
	Cooccurrences []ExportCooccurrence   `json:"cooccurrences"`
	Meta          map[string]interface{} `json:"meta"`
}

const (
	defaultSearchLimit  = 15
	maxSearchLimit      = 100
	defaultGraphDepth   = 2
	maxGraphDepth       = 5
	defaultClusterLimit = 150
	maxClusterLimit     = 400

	clusterSubjectMinFacts  = 3
	clusterSubjectMaxFacts  = 200
	clusterFactsPerSubject  = 6
	clusterMaxSubjectGroups = 90

	defaultSubjectGraphLimit = 120
	maxSubjectGraphLimit     = 300
	maxGraphOffset           = 100000
)

// Serve starts the graph visualization web server.
func Serve(cfg ServerConfig) error {
	mux := http.NewServeMux()

	// Middleware: if server-level agent filter is set, inject it as default query param
	wrapAgent := func(next http.HandlerFunc) http.HandlerFunc {
		if cfg.AgentFilter == "" {
			return next
		}
		return func(w http.ResponseWriter, r *http.Request) {
			// Only inject if the request doesn't already have ?agent=
			if r.URL.Query().Get("agent") == "" {
				q := r.URL.Query()
				q.Set("agent", cfg.AgentFilter)
				r.URL.RawQuery = q.Encode()
			}
			next(w, r)
		}
	}

	// Serve the visualizer HTML
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := visualizerFS.ReadFile("visualizer.html")
		if err != nil {
			http.Error(w, "visualizer not found", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	// Serve embedded brand asset used by the visualizer UI.
	mux.HandleFunc("/assets/cortex-icon-192.png", func(w http.ResponseWriter, r *http.Request) {
		data, err := visualizerFS.ReadFile("cortex-icon-192.png")
		if err != nil {
			http.Error(w, "asset not found", 404)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(data)
	})

	// Graph API endpoint
	mux.HandleFunc("/api/graph", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleGraphAPI(w, r, cfg.Store)
	}))

	// Search API endpoint — search facts by text
	mux.HandleFunc("/api/search", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleSearchAPI(w, r, cfg.Store)
	}))

	// Facts API endpoint — facts by subject or memory.
	mux.HandleFunc("/api/facts", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleFactsAPI(w, r, cfg.Store)
	}))

	// Sample cluster endpoint — returns a cluster of related facts for demo/exploration
	mux.HandleFunc("/api/cluster", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleClusterAPI(w, r, cfg.Store)
	}))

	// Topic clusters endpoints.
	mux.HandleFunc("/api/clusters", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleClustersListAPI(w, r, cfg.Store)
	}))
	mux.HandleFunc("/api/clusters/", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleClusterDetailAPI(w, r, cfg.Store)
	}))

	// Stats endpoint — DB health numbers for the banner
	mux.HandleFunc("/api/stats", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleStatsAPI(w, r, cfg.Store)
	}))

	// Impact endpoint — grouped blast-radius view for a subject.
	mux.HandleFunc("/api/impact", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleImpactAPI(w, r, cfg.Store)
	}))

	// Timeline endpoint — temporal evolution for a subject.
	mux.HandleFunc("/api/timeline", wrapAgent(func(w http.ResponseWriter, r *http.Request) {
		handleTimelineAPI(w, r, cfg.Store)
	}))

	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("🧠 Cortex graph visualizer: http://localhost%s\n", addr)
	fmt.Printf("   Open in browser to explore your knowledge graph in 2D/3D.\n")
	return http.ListenAndServe(addr, mux)
}

func handleGraphAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	minConf := 0.0
	if c := r.URL.Query().Get("min_confidence"); c != "" {
		if v, err := strconv.ParseFloat(c, 64); err == nil {
			minConf = v
		}
	}

	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent"))
	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	ctx := context.Background()

	if subject != "" {
		limit := parseBoundedInt(r.URL.Query().Get("limit"), defaultSubjectGraphLimit, 1, maxSubjectGraphLimit)
		offset := parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, maxGraphOffset)
		result, err := buildSubjectGraphResult(ctx, st.GetDB(), subject, limit, offset, minConf, agentFilter)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, result)
		return
	}

	// Parse fact_id parameters
	factIDStr := r.URL.Query().Get("fact_id")
	if factIDStr == "" {
		writeJSON(w, 400, map[string]string{"error": "fact_id or subject parameter required"})
		return
	}

	factID, err := strconv.ParseInt(factIDStr, 10, 64)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid fact_id"})
		return
	}

	depth := defaultGraphDepth
	if d := r.URL.Query().Get("depth"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 {
			depth = v
			if depth > maxGraphDepth {
				depth = maxGraphDepth
			}
		}
	}

	// Traverse graph
	graphNodes, err := st.TraverseGraph(ctx, factID, depth, minConf)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	// Build export
	result := ExportResult{
		Meta: map[string]interface{}{
			"root_fact_id": factID,
			"depth":        depth,
		},
	}

	seenNodes := make(map[int64]bool)
	seenEdges := make(map[string]bool)
	var allFactIDs []int64

	for _, gn := range graphNodes {
		if gn.Fact == nil {
			continue
		}
		if agentFilter != "" && gn.Fact.AgentID != agentFilter && gn.Fact.AgentID != "" {
			continue
		}
		if !seenNodes[gn.Fact.ID] {
			seenNodes[gn.Fact.ID] = true
			allFactIDs = append(allFactIDs, gn.Fact.ID)
			result.Nodes = append(result.Nodes, ExportNode{
				ID:         gn.Fact.ID,
				Subject:    gn.Fact.Subject,
				Predicate:  gn.Fact.Predicate,
				Object:     gn.Fact.Object,
				Confidence: gn.Fact.Confidence,
				AgentID:    gn.Fact.AgentID,
				FactType:   gn.Fact.FactType,
			})
		}
		for _, e := range gn.Edges {
			key := edgeKey(e.SourceFactID, e.TargetFactID, string(e.EdgeType))
			if seenEdges[key] {
				continue
			}
			seenEdges[key] = true
			result.Edges = append(result.Edges, ExportEdge{
				Source:     e.SourceFactID,
				Target:     e.TargetFactID,
				EdgeType:   string(e.EdgeType),
				Confidence: e.Confidence,
				SourceType: string(e.Source),
			})
		}
	}

	// Keep only edges where both endpoints are present.
	filteredEdges := result.Edges[:0]
	for _, e := range result.Edges {
		if seenNodes[e.Source] && seenNodes[e.Target] {
			filteredEdges = append(filteredEdges, e)
		}
	}
	result.Edges = filteredEdges

	// Co-occurrences
	seenCooc := make(map[[2]int64]bool)
	for _, fid := range allFactIDs {
		coocs, err := st.GetCooccurrencesForFact(ctx, fid, 10)
		if err != nil {
			continue
		}
		for _, c := range coocs {
			if seenNodes[c.FactIDA] && seenNodes[c.FactIDB] {
				key := [2]int64{c.FactIDA, c.FactIDB}
				if c.FactIDA > c.FactIDB {
					key = [2]int64{c.FactIDB, c.FactIDA}
				}
				if !seenCooc[key] {
					seenCooc[key] = true
					result.Cooccurrences = append(result.Cooccurrences, ExportCooccurrence{
						A:     c.FactIDA,
						B:     c.FactIDB,
						Count: c.Count,
					})
				}
			}
		}
	}

	result.Meta["total_nodes"] = len(result.Nodes)
	result.Meta["total_edges"] = len(result.Edges)
	result.Meta["total_cooccurrences"] = len(result.Cooccurrences)

	writeJSON(w, 200, result)
}

// SearchFact is a lightweight fact for search results.
type SearchFact struct {
	ID         int64   `json:"id"`
	MemoryID   int64   `json:"memory_id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	Relevance  float64 `json:"relevance,omitempty"`
	Rank       int     `json:"rank,omitempty"`
	FactType   string  `json:"type"`
	AgentID    string  `json:"agent_id,omitempty"`
	Source     string  `json:"source,omitempty"`
}

type SearchMemory struct {
	MemoryID   int64   `json:"memory_id"`
	SourceFile string  `json:"source_file"`
	Snippet    string  `json:"snippet,omitempty"`
	Score      float64 `json:"score"`
}

type FactsResponse struct {
	Facts []SearchFact `json:"facts"`
	Total int          `json:"total"`
}

// SearchResult is the search API response.
type SearchResult struct {
	Facts          []SearchFact   `json:"facts"`
	Memories       []SearchMemory `json:"memories,omitempty"`
	MatchedNodeIDs []int64        `json:"matched_node_ids,omitempty"`
	Total          int            `json:"total"`
}

func handleFactsAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := context.Background()
	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	memoryIDRaw := strings.TrimSpace(r.URL.Query().Get("memory_id"))

	if subject == "" && memoryIDRaw == "" {
		writeJSON(w, 400, map[string]string{"error": "subject or memory_id parameter required"})
		return
	}

	db := st.GetDB()
	facts := make([]SearchFact, 0)

	if memoryIDRaw != "" {
		memoryID, err := strconv.ParseInt(memoryIDRaw, 10, 64)
		if err != nil || memoryID <= 0 {
			writeJSON(w, 400, map[string]string{"error": "invalid memory_id"})
			return
		}

		memFacts, err := st.GetFactsByMemoryIDs(ctx, []int64{memoryID})
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		mem, _ := st.GetMemory(ctx, memoryID)
		sourceFile := ""
		if mem != nil {
			sourceFile = mem.SourceFile
		}
		for _, f := range memFacts {
			facts = append(facts, SearchFact{
				ID:         f.ID,
				MemoryID:   f.MemoryID,
				Subject:    f.Subject,
				Predicate:  f.Predicate,
				Object:     f.Object,
				Confidence: f.Confidence,
				FactType:   f.FactType,
				AgentID:    f.AgentID,
				Source:     sourceFile,
			})
		}
	}

	if subject != "" {
		rows, err := db.QueryContext(ctx,
			`SELECT f.id, f.memory_id, f.subject, f.predicate, f.object, f.confidence, f.fact_type, COALESCE(f.agent_id, ''), COALESCE(m.source_file, '')
			 FROM facts f
			 LEFT JOIN memories m ON m.id = f.memory_id
			 WHERE LOWER(f.subject) = LOWER(?)
			   AND (f.superseded_by IS NULL OR f.superseded_by = 0)
			 ORDER BY f.confidence DESC, f.id DESC`,
			subject,
		)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()

		seen := make(map[int64]bool)
		for rows.Next() {
			var f SearchFact
			if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object, &f.Confidence, &f.FactType, &f.AgentID, &f.Source); err != nil {
				continue
			}
			if seen[f.ID] {
				continue
			}
			seen[f.ID] = true
			facts = append(facts, f)
		}
	}

	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Confidence == facts[j].Confidence {
			return facts[i].ID > facts[j].ID
		}
		return facts[i].Confidence > facts[j].Confidence
	})

	writeJSON(w, 200, FactsResponse{Facts: facts, Total: len(facts)})
}

func handleSearchAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, 400, map[string]string{"error": "q parameter required"})
		return
	}

	limit := defaultSearchLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= maxSearchLimit {
			limit = v
		}
	}

	ctx := context.Background()
	engine := searchpkg.NewEngine(st)
	memResults, err := engine.Search(ctx, query, searchpkg.Options{
		Mode:  searchpkg.ModeKeyword,
		Limit: limit,
	})
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	memoryIDs := make([]int64, 0, len(memResults))
	memories := make([]SearchMemory, 0, len(memResults))
	seenMemory := make(map[int64]bool, len(memResults))
	for _, m := range memResults {
		if seenMemory[m.MemoryID] {
			continue
		}
		seenMemory[m.MemoryID] = true
		memoryIDs = append(memoryIDs, m.MemoryID)
		memories = append(memories, SearchMemory{
			MemoryID:   m.MemoryID,
			SourceFile: m.SourceFile,
			Snippet:    m.Snippet,
			Score:      m.Score,
		})
	}

	factRows, err := st.GetFactsByMemoryIDs(ctx, memoryIDs)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	memSource := make(map[int64]string, len(memResults))
	for _, m := range memResults {
		if _, ok := memSource[m.MemoryID]; !ok {
			memSource[m.MemoryID] = m.SourceFile
		}
	}

	facts := make([]SearchFact, 0, len(factRows))
	matchedNodeIDs := make([]int64, 0, len(factRows))
	queryLower := strings.ToLower(query)
	for _, f := range factRows {
		text := strings.ToLower(strings.TrimSpace(f.Subject + " " + f.Predicate + " " + f.Object))
		if queryLower != "" && !strings.Contains(text, queryLower) {
			continue
		}
		facts = append(facts, SearchFact{
			ID:         f.ID,
			MemoryID:   f.MemoryID,
			Subject:    f.Subject,
			Predicate:  f.Predicate,
			Object:     f.Object,
			Confidence: f.Confidence,
			FactType:   f.FactType,
			AgentID:    f.AgentID,
			Source:     memSource[f.MemoryID],
		})
		matchedNodeIDs = append(matchedNodeIDs, f.ID)
	}

	if len(facts) == 0 {
		// Fallback to direct fact search so graph browse still works when memories match weakly.
		db := st.GetDB()
		q := "%" + query + "%"
		rows, err := db.QueryContext(ctx,
			`SELECT f.id, f.memory_id, f.subject, f.predicate, f.object, f.confidence, f.fact_type, COALESCE(f.agent_id, ''), COALESCE(m.source_file, '')
			 FROM facts f
			 LEFT JOIN memories m ON m.id = f.memory_id
			 WHERE (f.subject LIKE ? OR f.predicate LIKE ? OR f.object LIKE ?)
			   AND (f.superseded_by IS NULL OR f.superseded_by = 0)
			 ORDER BY f.confidence DESC
			 LIMIT ?`, q, q, q, limit)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var f SearchFact
				if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Predicate, &f.Object, &f.Confidence, &f.FactType, &f.AgentID, &f.Source); err != nil {
					continue
				}
				facts = append(facts, f)
				matchedNodeIDs = append(matchedNodeIDs, f.ID)
			}
		}
	}

	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Confidence == facts[j].Confidence {
			return facts[i].ID > facts[j].ID
		}
		return facts[i].Confidence > facts[j].Confidence
	})
	if len(facts) > limit {
		facts = facts[:limit]
	}
	if len(matchedNodeIDs) > limit*6 {
		matchedNodeIDs = matchedNodeIDs[:limit*6]
	}

	writeJSON(w, 200, SearchResult{
		Facts:          facts,
		Memories:       memories,
		MatchedNodeIDs: matchedNodeIDs,
		Total:          len(facts),
	})
}

func handleClusterAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := parseBoundedInt(r.URL.Query().Get("limit"), defaultClusterLimit, 1, maxClusterLimit)
	offset := parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, maxGraphOffset)

	query := strings.TrimSpace(r.URL.Query().Get("q"))

	db := st.GetDB()
	ctx := context.Background()

	// Find subjects that have multiple facts (natural clusters)
	var rows *sql.Rows
	var err error

	total := 0
	if query != "" {
		q := "%" + query + "%"
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*)
			 FROM facts
			 WHERE (subject LIKE ? OR predicate LIKE ? OR object LIKE ?)
			   AND (superseded_by IS NULL OR superseded_by = 0)`,
			q, q, q,
		).Scan(&total); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		rows, err = db.QueryContext(ctx,
			`SELECT id, subject, predicate, object, confidence, fact_type, COALESCE(agent_id, '')
			 FROM facts
			 WHERE (subject LIKE ? OR predicate LIKE ? OR object LIKE ?)
			   AND (superseded_by IS NULL OR superseded_by = 0)
			 ORDER BY confidence DESC, id DESC
			 LIMIT ? OFFSET ?`, q, q, q, limit, offset)
	} else {
		subjectLimit := clusterMaxSubjectGroups

		if err := db.QueryRowContext(ctx,
			`WITH top_subjects AS (
			   SELECT subject, COUNT(*) as cnt
			   FROM facts
			   WHERE (superseded_by IS NULL OR superseded_by = 0)
			     AND subject IS NOT NULL
			     AND TRIM(subject) != ''
			   GROUP BY subject
			   HAVING cnt BETWEEN ? AND ?
			   ORDER BY cnt DESC
			   LIMIT ?
			 ), ranked AS (
			   SELECT f.id,
			          ROW_NUMBER() OVER (PARTITION BY f.subject ORDER BY f.confidence DESC, f.id DESC) as rn
			   FROM facts f
			   INNER JOIN top_subjects s ON s.subject = f.subject
			   WHERE (f.superseded_by IS NULL OR f.superseded_by = 0)
			 )
			 SELECT COUNT(*)
			 FROM ranked
			 WHERE rn <= ?`,
			clusterSubjectMinFacts,
			clusterSubjectMaxFacts,
			subjectLimit,
			clusterFactsPerSubject,
		).Scan(&total); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}

		// Sample facts across diverse subjects using widened quality bounds now that
		// subject normalization is cleaner.
		rows, err = db.QueryContext(ctx,
			`WITH top_subjects AS (
			   SELECT subject, COUNT(*) as cnt
			   FROM facts
			   WHERE (superseded_by IS NULL OR superseded_by = 0)
			     AND subject IS NOT NULL
			     AND TRIM(subject) != ''
			   GROUP BY subject
			   HAVING cnt BETWEEN ? AND ?
			   ORDER BY cnt DESC
			   LIMIT ?
			 ), ranked AS (
			   SELECT f.id, f.subject, f.predicate, f.object, f.confidence, f.fact_type,
			          COALESCE(f.agent_id, '') as agent_id,
			          ROW_NUMBER() OVER (PARTITION BY f.subject ORDER BY f.confidence DESC, f.id DESC) as rn
			   FROM facts f
			   INNER JOIN top_subjects s ON s.subject = f.subject
			   WHERE (f.superseded_by IS NULL OR f.superseded_by = 0)
			 )
			 SELECT id, subject, predicate, object, confidence, fact_type, agent_id
			 FROM ranked
			 WHERE rn <= ?
			 ORDER BY confidence DESC, id DESC
			 LIMIT ? OFFSET ?`,
			clusterSubjectMinFacts,
			clusterSubjectMaxFacts,
			subjectLimit,
			clusterFactsPerSubject,
			limit,
			offset,
		)
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var nodes []ExportNode
	var nodeIDs []int64
	subjectGroups := make(map[string][]int64) // subject -> fact IDs

	for rows.Next() {
		var n ExportNode
		if err := rows.Scan(&n.ID, &n.Subject, &n.Predicate, &n.Object, &n.Confidence, &n.FactType, &n.AgentID); err != nil {
			continue
		}
		n.Relevance = n.Confidence
		n.Rank = offset + len(nodes) + 1
		nodes = append(nodes, n)
		nodeIDs = append(nodeIDs, n.ID)
		subjectGroups[n.Subject] = append(subjectGroups[n.Subject], n.ID)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	enrichNodeMetadata(ctx, db, nodes)

	edges, edgeErr := loadEdgesForNodeIDs(ctx, db, nodeIDs, 0.0)
	edgeMode := "fact_edges_v1"
	if edgeErr != nil {
		if !isMissingTableErr(edgeErr, "fact_edges_v1") {
			writeJSON(w, 500, map[string]string{"error": edgeErr.Error()})
			return
		}
		edges = nil
	}

	fallbackEdges := 0
	if len(edges) == 0 {
		edges = buildSubjectClusterEdges(subjectGroups)
		edgeMode = "subject_cluster_fallback"
		fallbackEdges = len(edges)
	} else {
		extra := buildSparseSubjectFallbackEdges(subjectGroups, edges)
		if len(extra) > 0 {
			edges = append(edges, extra...)
			edgeMode = "fact_edges_v1+subject_cluster"
			fallbackEdges = len(extra)
		}
	}

	cooccurrences, coocErr := loadCooccurrencesForNodeIDs(ctx, db, nodeIDs, 200)
	if coocErr != nil && !isMissingTableErr(coocErr, "fact_cooccurrence_v1") {
		cooccurrences = nil
	}

	meta := map[string]interface{}{
		"mode":                "cluster",
		"query":               query,
		"total_nodes":         len(nodes),
		"total_edges":         len(edges),
		"total_cooccurrences": len(cooccurrences),
		"subjects":            len(subjectGroups),
		"edge_mode":           edgeMode,
		"fallback_edges":      fallbackEdges,
		"subject_min_facts":   clusterSubjectMinFacts,
		"subject_max_facts":   clusterSubjectMaxFacts,
		"ordering":            "relevance_desc(confidence,id)",
	}
	addPaginationMeta(meta, limit, offset, total, len(nodes))

	result := ExportResult{
		Nodes:         nodes,
		Edges:         edges,
		Cooccurrences: cooccurrences,
		Meta:          meta,
	}

	writeJSON(w, 200, result)
}

func buildSubjectGraphResult(ctx context.Context, db *sql.DB, subject string, limit, offset int, minConf float64, agentFilter string) (ExportResult, error) {
	countQuery := `SELECT COUNT(*)
	               FROM facts
	               WHERE LOWER(subject) = LOWER(?)
	                 AND (superseded_by IS NULL OR superseded_by = 0)
	                 AND confidence >= ?`
	countArgs := []interface{}{subject, minConf}
	if agentFilter != "" {
		countQuery += " AND (agent_id = ? OR agent_id = '')"
		countArgs = append(countArgs, agentFilter)
	}
	var total int
	if err := db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return ExportResult{}, err
	}

	query := `SELECT id, subject, predicate, object, confidence, fact_type, COALESCE(agent_id, '')
	          FROM facts
	          WHERE LOWER(subject) = LOWER(?)
	            AND (superseded_by IS NULL OR superseded_by = 0)
	            AND confidence >= ?`
	args := []interface{}{subject, minConf}
	if agentFilter != "" {
		query += " AND (agent_id = ? OR agent_id = '')"
		args = append(args, agentFilter)
	}
	query += " ORDER BY confidence DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ExportResult{}, err
	}
	defer rows.Close()

	var nodes []ExportNode
	var nodeIDs []int64
	subjectGroups := make(map[string][]int64)
	for rows.Next() {
		var n ExportNode
		if err := rows.Scan(&n.ID, &n.Subject, &n.Predicate, &n.Object, &n.Confidence, &n.FactType, &n.AgentID); err != nil {
			continue
		}
		n.Relevance = n.Confidence
		n.Rank = offset + len(nodes) + 1
		nodes = append(nodes, n)
		nodeIDs = append(nodeIDs, n.ID)
		subjectGroups[n.Subject] = append(subjectGroups[n.Subject], n.ID)
	}
	if err := rows.Err(); err != nil {
		return ExportResult{}, err
	}
	enrichNodeMetadata(ctx, db, nodes)

	edges, edgeErr := loadEdgesForNodeIDs(ctx, db, nodeIDs, minConf)
	edgeMode := "fact_edges_v1"
	if edgeErr != nil {
		if !isMissingTableErr(edgeErr, "fact_edges_v1") {
			return ExportResult{}, edgeErr
		}
		edges = nil
	}

	fallbackEdges := 0
	if len(edges) == 0 {
		edges = buildSubjectClusterEdges(subjectGroups)
		edgeMode = "subject_cluster_fallback"
		fallbackEdges = len(edges)
	} else {
		extra := buildSparseSubjectFallbackEdges(subjectGroups, edges)
		if len(extra) > 0 {
			edges = append(edges, extra...)
			edgeMode = "fact_edges_v1+subject_cluster"
			fallbackEdges = len(extra)
		}
	}

	cooccurrences, coocErr := loadCooccurrencesForNodeIDs(ctx, db, nodeIDs, 300)
	if coocErr != nil && !isMissingTableErr(coocErr, "fact_cooccurrence_v1") {
		cooccurrences = nil
	}

	meta := map[string]interface{}{
		"mode":                "subject",
		"subject":             subject,
		"limit":               limit,
		"total_nodes":         len(nodes),
		"total_edges":         len(edges),
		"total_cooccurrences": len(cooccurrences),
		"edge_mode":           edgeMode,
		"fallback_edges":      fallbackEdges,
		"ordering":            "relevance_desc(confidence,id)",
	}
	addPaginationMeta(meta, limit, offset, total, len(nodes))

	return ExportResult{
		Nodes:         nodes,
		Edges:         edges,
		Cooccurrences: cooccurrences,
		Meta:          meta,
	}, nil
}

func enrichNodeMetadata(ctx context.Context, db *sql.DB, nodes []ExportNode) {
	if len(nodes) == 0 {
		return
	}

	subjectSet := make(map[string]bool, len(nodes))
	subjects := make([]string, 0, len(nodes))
	for _, node := range nodes {
		subject := strings.TrimSpace(node.Subject)
		if subject == "" || subjectSet[subject] {
			continue
		}
		subjectSet[subject] = true
		subjects = append(subjects, subject)
	}
	if len(subjects) == 0 {
		return
	}

	placeholders := make([]string, len(subjects))
	args := make([]interface{}, len(subjects))
	for i, s := range subjects {
		placeholders[i] = "?"
		args[i] = s
	}

	query := fmt.Sprintf(
		`SELECT f.subject,
		        COUNT(*) as fact_count,
		        COALESCE(MAX(f.created_at), ''),
		        COALESCE(GROUP_CONCAT(DISTINCT
		          CASE
		            WHEN INSTR(COALESCE(m.source_file, ''), ':') > 0 THEN SUBSTR(m.source_file, 1, INSTR(m.source_file, ':') - 1)
		            WHEN TRIM(COALESCE(m.source_file, '')) = '' THEN 'unknown'
		            ELSE 'manual'
		          END
		        ), '')
		 FROM facts f
		 LEFT JOIN memories m ON m.id = f.memory_id
		 WHERE (f.superseded_by IS NULL OR f.superseded_by = 0)
		   AND f.subject IN (%s)
		 GROUP BY f.subject`,
		strings.Join(placeholders, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return
	}
	defer rows.Close()

	type meta struct {
		count      int
		last       string
		sourceType []string
	}
	metaBySubject := make(map[string]meta, len(subjects))
	for rows.Next() {
		var subject string
		var factCount int
		var lastRaw string
		var sourceRaw string
		if err := rows.Scan(&subject, &factCount, &lastRaw, &sourceRaw); err != nil {
			continue
		}
		types := make([]string, 0)
		for _, src := range strings.Split(sourceRaw, ",") {
			src = strings.TrimSpace(src)
			if src == "" {
				continue
			}
			types = append(types, src)
		}
		sort.Strings(types)
		metaBySubject[subject] = meta{
			count:      factCount,
			last:       normalizeStoreTimestamp(lastRaw),
			sourceType: types,
		}
	}

	for i := range nodes {
		if m, ok := metaBySubject[nodes[i].Subject]; ok {
			nodes[i].FactCount = m.count
			nodes[i].LastUpdated = m.last
			nodes[i].SourceTypes = m.sourceType
		}
	}
}

func normalizeStoreTimestamp(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return raw
}

func parseBoundedInt(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func addPaginationMeta(meta map[string]interface{}, limit, offset, total, returned int) {
	if meta == nil {
		return
	}
	meta["limit"] = limit
	meta["offset"] = offset
	meta["returned"] = returned
	meta["total"] = total
	meta["has_more"] = offset+returned < total
}

func loadEdgesForNodeIDs(ctx context.Context, db *sql.DB, ids []int64, minConf float64) ([]ExportEdge, error) {
	if len(ids) < 2 {
		return nil, nil
	}
	ph := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)*2+2)
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	edgeLimit := len(ids) * 12
	if edgeLimit > 6000 {
		edgeLimit = 6000
	}
	args = append(args, minConf, edgeLimit)

	query := fmt.Sprintf(
		`SELECT source_fact_id, target_fact_id, edge_type, confidence, source
		 FROM fact_edges_v1
		 WHERE source_fact_id IN (%s)
		   AND target_fact_id IN (%s)
		   AND confidence >= ?
		 ORDER BY confidence DESC, id DESC
		 LIMIT ?`,
		strings.Join(ph, ","),
		strings.Join(ph, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]bool)
	edges := make([]ExportEdge, 0, edgeLimit)
	for rows.Next() {
		var e ExportEdge
		if err := rows.Scan(&e.Source, &e.Target, &e.EdgeType, &e.Confidence, &e.SourceType); err != nil {
			continue
		}
		key := edgeKey(e.Source, e.Target, e.EdgeType)
		if seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func loadCooccurrencesForNodeIDs(ctx context.Context, db *sql.DB, ids []int64, limit int) ([]ExportCooccurrence, error) {
	if len(ids) < 2 {
		return nil, nil
	}

	ph := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)*2+1)
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, limit)

	query := fmt.Sprintf(
		`SELECT fact_id_a, fact_id_b, count
		 FROM fact_cooccurrence_v1
		 WHERE fact_id_a IN (%s)
		   AND fact_id_b IN (%s)
		 ORDER BY count DESC
		 LIMIT ?`,
		strings.Join(ph, ","),
		strings.Join(ph, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var coocs []ExportCooccurrence
	for rows.Next() {
		var c ExportCooccurrence
		if err := rows.Scan(&c.A, &c.B, &c.Count); err != nil {
			continue
		}
		coocs = append(coocs, c)
	}
	return coocs, rows.Err()
}

func buildSubjectClusterEdges(subjectGroups map[string][]int64) []ExportEdge {
	var edges []ExportEdge
	for _, ids := range subjectGroups {
		if len(ids) < 2 {
			continue
		}
		for i := 0; i < len(ids)-1; i++ {
			edges = append(edges, ExportEdge{
				Source:     ids[i],
				Target:     ids[i+1],
				EdgeType:   "relates_to",
				Confidence: 0.5,
				SourceType: "subject_cluster",
			})
		}
		if len(ids) >= 3 {
			edges = append(edges, ExportEdge{
				Source:     ids[len(ids)-1],
				Target:     ids[0],
				EdgeType:   "relates_to",
				Confidence: 0.35,
				SourceType: "subject_cluster",
			})
		}
	}
	return edges
}

func buildSparseSubjectFallbackEdges(subjectGroups map[string][]int64, existing []ExportEdge) []ExportEdge {
	if len(subjectGroups) == 0 {
		return nil
	}

	nodeSubject := make(map[int64]string, len(subjectGroups)*clusterFactsPerSubject)
	groupHasIntraEdge := make(map[string]bool, len(subjectGroups))
	for subject, ids := range subjectGroups {
		for _, id := range ids {
			nodeSubject[id] = subject
		}
	}

	for _, e := range existing {
		sA, okA := nodeSubject[e.Source]
		sB, okB := nodeSubject[e.Target]
		if okA && okB && sA == sB && sA != "" {
			groupHasIntraEdge[sA] = true
		}
	}

	var extra []ExportEdge
	for subject, ids := range subjectGroups {
		if len(ids) < 2 || groupHasIntraEdge[subject] {
			continue
		}
		for i := 0; i < len(ids)-1; i++ {
			extra = append(extra, ExportEdge{
				Source:     ids[i],
				Target:     ids[i+1],
				EdgeType:   "relates_to",
				Confidence: 0.45,
				SourceType: "subject_cluster",
			})
		}
	}
	return extra
}

func edgeKey(source, target int64, edgeType string) string {
	return fmt.Sprintf("%d:%d:%s", source, target, edgeType)
}

func isMissingTableErr(err error, table string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: "+strings.ToLower(table))
}

func handleStatsAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	db := st.GetDB()
	ctx := context.Background()

	var facts, memories, edges int
	var avgConf float64

	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM facts WHERE superseded_by IS NULL OR superseded_by = 0").Scan(&facts)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories").Scan(&memories)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fact_edges_v1").Scan(&edges)
	db.QueryRowContext(ctx, "SELECT COALESCE(AVG(confidence), 0) FROM facts WHERE superseded_by IS NULL OR superseded_by = 0").Scan(&avgConf)

	writeJSON(w, 200, map[string]interface{}{
		"facts":          facts,
		"memories":       memories,
		"edges":          edges,
		"avg_confidence": avgConf,
	})
}

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}
