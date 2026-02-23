// Package graph provides an interactive knowledge graph visualizer.
// It embeds a self-contained HTML/JS application that renders fact relationships
// using D3.js force-directed layout and fetches data from a local API endpoint.
package graph

import (
	"context"
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

	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("🧠 Cortex graph visualizer: http://localhost%s\n", addr)
	fmt.Printf("   Open in browser and enter a fact ID to explore.\n")
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

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}
