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
	"strconv"

	"github.com/hurttlocker/cortex/internal/store"
)

//go:embed visualizer.html
var visualizerFS embed.FS

// ServerConfig holds settings for the graph visualization server.
type ServerConfig struct {
	Store *store.SQLiteStore
	Port  int
}

// ExportNode is the visualization-friendly format for a fact.
type ExportNode struct {
	ID         int64   `json:"id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	AgentID    string  `json:"agent_id,omitempty"`
	FactType   string  `json:"type"`
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
	Nodes         []ExportNode         `json:"nodes"`
	Edges         []ExportEdge         `json:"edges"`
	Cooccurrences []ExportCooccurrence `json:"cooccurrences"`
	Meta          map[string]interface{} `json:"meta"`
}

// Serve starts the graph visualization web server.
func Serve(cfg ServerConfig) error {
	mux := http.NewServeMux()

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

	// Graph API endpoint
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		handleGraphAPI(w, r, cfg.Store)
	})

	// Search API endpoint — search facts by text
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		handleSearchAPI(w, r, cfg.Store)
	})

	// Sample cluster endpoint — returns a cluster of related facts for demo/exploration
	mux.HandleFunc("/api/cluster", func(w http.ResponseWriter, r *http.Request) {
		handleClusterAPI(w, r, cfg.Store)
	})

	// Stats endpoint — DB health numbers for the banner
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		handleStatsAPI(w, r, cfg.Store)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("🧠 Cortex graph visualizer: http://localhost%s\n", addr)
	fmt.Printf("   Open in browser to explore your knowledge graph in 3D.\n")
	return http.ListenAndServe(addr, mux)
}

func handleGraphAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Parse parameters
	factIDStr := r.URL.Query().Get("fact_id")
	if factIDStr == "" {
		writeJSON(w, 400, map[string]string{"error": "fact_id parameter required"})
		return
	}

	factID, err := strconv.ParseInt(factIDStr, 10, 64)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid fact_id"})
		return
	}

	depth := 2
	if d := r.URL.Query().Get("depth"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 {
			depth = v
			if depth > 5 {
				depth = 5
			}
		}
	}

	minConf := 0.0
	if c := r.URL.Query().Get("min_confidence"); c != "" {
		if v, err := strconv.ParseFloat(c, 64); err == nil {
			minConf = v
		}
	}

	agentFilter := r.URL.Query().Get("agent")

	ctx := context.Background()

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
			result.Edges = append(result.Edges, ExportEdge{
				Source:     e.SourceFactID,
				Target:     e.TargetFactID,
				EdgeType:   string(e.EdgeType),
				Confidence: e.Confidence,
				SourceType: string(e.Source),
			})
		}
	}

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
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	FactType   string  `json:"type"`
	AgentID    string  `json:"agent_id,omitempty"`
}

// SearchResult is the search API response.
type SearchResult struct {
	Facts []SearchFact `json:"facts"`
	Total int          `json:"total"`
}

func handleSearchAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, 400, map[string]string{"error": "q parameter required"})
		return
	}

	limit := 15
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}

	// Direct SQL search on facts table — fast LIKE matching on subject/predicate/object
	db := st.GetDB()
	q := "%" + query + "%"
	rows, err := db.QueryContext(context.Background(),
		`SELECT id, subject, predicate, object, confidence, fact_type, COALESCE(agent_id, '')
		 FROM facts
		 WHERE (subject LIKE ? OR predicate LIKE ? OR object LIKE ?)
		   AND (superseded_by IS NULL OR superseded_by = 0)
		 ORDER BY confidence DESC
		 LIMIT ?`, q, q, q, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var facts []SearchFact
	for rows.Next() {
		var f SearchFact
		if err := rows.Scan(&f.ID, &f.Subject, &f.Predicate, &f.Object, &f.Confidence, &f.FactType, &f.AgentID); err != nil {
			continue
		}
		facts = append(facts, f)
	}

	writeJSON(w, 200, SearchResult{Facts: facts, Total: len(facts)})
}

func handleClusterAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}

	query := r.URL.Query().Get("q")

	db := st.GetDB()
	ctx := context.Background()

	// Find subjects that have multiple facts (natural clusters)
	var rows *sql.Rows
	var err error

	if query != "" {
		q := "%" + query + "%"
		rows, err = db.QueryContext(ctx,
			`SELECT id, subject, predicate, object, confidence, fact_type, COALESCE(agent_id, '')
			 FROM facts
			 WHERE (subject LIKE ? OR predicate LIKE ? OR object LIKE ?)
			   AND (superseded_by IS NULL OR superseded_by = 0)
			 ORDER BY confidence DESC
			 LIMIT ?`, q, q, q, limit)
	} else {
		// Sample facts across diverse subjects using window functions
		// Take top 5 facts per subject from subjects with 3-50 facts (skip mega-subjects)
		rows, err = db.QueryContext(ctx,
			`WITH ranked AS (
			   SELECT f.id, f.subject, f.predicate, f.object, f.confidence, f.fact_type,
			          COALESCE(f.agent_id, '') as agent_id,
			          ROW_NUMBER() OVER (PARTITION BY f.subject ORDER BY f.confidence DESC) as rn
			   FROM facts f
			   INNER JOIN (
			     SELECT subject, COUNT(*) as cnt
			     FROM facts
			     WHERE superseded_by IS NULL OR superseded_by = 0
			     GROUP BY subject
			     HAVING cnt BETWEEN 3 AND 50
			     ORDER BY cnt DESC
			     LIMIT 15
			   ) top ON f.subject = top.subject
			   WHERE (f.superseded_by IS NULL OR f.superseded_by = 0)
			 )
			 SELECT id, subject, predicate, object, confidence, fact_type, agent_id
			 FROM ranked
			 WHERE rn <= 5
			 LIMIT ?`, limit)
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var nodes []ExportNode
	subjectGroups := make(map[string][]int64) // subject -> fact IDs

	for rows.Next() {
		var n ExportNode
		if err := rows.Scan(&n.ID, &n.Subject, &n.Predicate, &n.Object, &n.Confidence, &n.FactType, &n.AgentID); err != nil {
			continue
		}
		nodes = append(nodes, n)
		subjectGroups[n.Subject] = append(subjectGroups[n.Subject], n.ID)
	}

	// Create edges between facts that share the same subject (natural clustering)
	var edges []ExportEdge
	for _, ids := range subjectGroups {
		if len(ids) < 2 {
			continue
		}
		// Connect facts in a chain within the same subject group
		for i := 0; i < len(ids)-1; i++ {
			edges = append(edges, ExportEdge{
				Source:     ids[i],
				Target:     ids[i+1],
				EdgeType:   "relates_to",
				Confidence: 0.6,
				SourceType: "subject_cluster",
			})
		}
		// Also connect last to first for a loop if 3+ facts
		if len(ids) >= 3 {
			edges = append(edges, ExportEdge{
				Source:     ids[len(ids)-1],
				Target:     ids[0],
				EdgeType:   "relates_to",
				Confidence: 0.4,
				SourceType: "subject_cluster",
			})
		}
	}

	result := ExportResult{
		Nodes: nodes,
		Edges: edges,
		Meta: map[string]interface{}{
			"mode":        "cluster",
			"total_nodes": len(nodes),
			"total_edges": len(edges),
			"total_cooccurrences": 0,
			"subjects":    len(subjectGroups),
		},
	}

	writeJSON(w, 200, result)
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
