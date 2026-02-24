package graph

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
)

const (
	defaultClustersListLimit = 200
	maxClustersListLimit     = 500
	defaultClusterFactsLimit = 800
	maxClusterFactsLimit     = 2000
)

// ClustersResponse is the list payload for /api/clusters.
type ClustersResponse struct {
	Clusters         []store.Cluster `json:"clusters"`
	UnclusteredCount int             `json:"unclustered_count"`
	TotalFacts       int             `json:"total_facts"`
}

// ClusterDetailResponse is the detail payload for /api/clusters/:id.
type ClusterDetailResponse struct {
	Cluster       store.Cluster          `json:"cluster"`
	Facts         []SearchFact           `json:"facts"`
	Nodes         []ExportNode           `json:"nodes"`
	Edges         []ExportEdge           `json:"edges"`
	Cooccurrences []ExportCooccurrence   `json:"cooccurrences"`
	Meta          map[string]interface{} `json:"meta"`
}

func handleClustersListAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := context.Background()
	limit := parseBoundedInt(r.URL.Query().Get("limit"), defaultClustersListLimit, 1, maxClustersListLimit)
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	clusters, err := st.ListClusters(ctx)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	if query != "" {
		q := strings.ToLower(query)
		filtered := make([]store.Cluster, 0, len(clusters))
		for _, c := range clusters {
			if clusterMatchesQuery(c, q) {
				filtered = append(filtered, c)
			}
		}
		clusters = filtered
	}
	if len(clusters) > limit {
		clusters = clusters[:limit]
	}

	unclusteredCount, err := st.CountUnclusteredFacts(ctx)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	totalFacts, err := st.CountActiveFacts(ctx)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, 200, ClustersResponse{
		Clusters:         clusters,
		UnclusteredCount: unclusteredCount,
		TotalFacts:       totalFacts,
	})
}

func handleClusterDetailAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clusterID, ok := parseClusterIDFromPath(r.URL.Path)
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "invalid cluster id"})
		return
	}

	ctx := context.Background()
	limit := parseBoundedInt(r.URL.Query().Get("limit"), defaultClusterFactsLimit, 1, maxClusterFactsLimit)

	detail, err := st.GetClusterDetail(ctx, clusterID, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if detail == nil {
		writeJSON(w, 404, map[string]string{"error": "cluster not found"})
		return
	}

	db := st.GetDB()
	nodes := make([]ExportNode, 0, len(detail.Facts))
	facts := make([]SearchFact, 0, len(detail.Facts))
	nodeIDs := make([]int64, 0, len(detail.Facts))
	subjectGroups := make(map[string][]int64)

	sourceByMemory, _ := loadMemorySourcesForFacts(ctx, db, detail.Facts)

	for _, fact := range detail.Facts {
		nodes = append(nodes, ExportNode{
			ID:           fact.ID,
			Subject:      fact.Subject,
			Predicate:    fact.Predicate,
			Object:       fact.Object,
			Confidence:   fact.Confidence,
			AgentID:      fact.AgentID,
			FactType:     fact.FactType,
			ClusterID:    detail.Cluster.ID,
			ClusterColor: detail.Cluster.Color,
		})
		facts = append(facts, SearchFact{
			ID:         fact.ID,
			MemoryID:   fact.MemoryID,
			Subject:    fact.Subject,
			Predicate:  fact.Predicate,
			Object:     fact.Object,
			Confidence: fact.Confidence,
			FactType:   fact.FactType,
			AgentID:    fact.AgentID,
			Source:     sourceByMemory[fact.MemoryID],
		})
		nodeIDs = append(nodeIDs, fact.ID)
		subjectGroups[fact.Subject] = append(subjectGroups[fact.Subject], fact.ID)
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

	cooccurrences, coocErr := loadCooccurrencesForNodeIDs(ctx, db, nodeIDs, 300)
	if coocErr != nil && !isMissingTableErr(coocErr, "fact_cooccurrence_v1") {
		cooccurrences = nil
	}

	resp := ClusterDetailResponse{
		Cluster:       detail.Cluster,
		Facts:         facts,
		Nodes:         nodes,
		Edges:         edges,
		Cooccurrences: cooccurrences,
		Meta: map[string]interface{}{
			"mode":                "cluster_detail",
			"cluster_id":          detail.Cluster.ID,
			"cluster_name":        detail.Cluster.Name,
			"cohesion":            detail.Cluster.Cohesion,
			"total_nodes":         len(nodes),
			"total_edges":         len(edges),
			"total_cooccurrences": len(cooccurrences),
			"edge_mode":           edgeMode,
			"fallback_edges":      fallbackEdges,
		},
	}

	writeJSON(w, 200, resp)
}

func parseClusterIDFromPath(path string) (int64, bool) {
	raw := strings.TrimPrefix(path, "/api/clusters/")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if idx := strings.IndexByte(raw, '/'); idx >= 0 {
		raw = raw[:idx]
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func clusterMatchesQuery(c store.Cluster, q string) bool {
	if strings.Contains(strings.ToLower(c.Name), q) {
		return true
	}
	for _, alias := range c.Aliases {
		if strings.Contains(strings.ToLower(alias), q) {
			return true
		}
	}
	for _, subject := range c.TopSubjects {
		if strings.Contains(strings.ToLower(subject), q) {
			return true
		}
	}
	return false
}

func loadMemorySourcesForFacts(ctx context.Context, db *sql.DB, facts []*store.Fact) (map[int64]string, error) {
	memoryIDs := make([]int64, 0, len(facts))
	seen := make(map[int64]struct{}, len(facts))
	for _, f := range facts {
		if f.MemoryID <= 0 {
			continue
		}
		if _, ok := seen[f.MemoryID]; ok {
			continue
		}
		seen[f.MemoryID] = struct{}{}
		memoryIDs = append(memoryIDs, f.MemoryID)
	}
	if len(memoryIDs) == 0 {
		return map[int64]string{}, nil
	}

	placeholders := make([]string, len(memoryIDs))
	args := make([]interface{}, len(memoryIDs))
	for i, id := range memoryIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`SELECT id, COALESCE(source_file, '')
		 FROM memories
		 WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]string, len(memoryIDs))
	for rows.Next() {
		var id int64
		var source string
		if err := rows.Scan(&id, &source); err != nil {
			return nil, err
		}
		result[id] = source
	}
	return result, rows.Err()
}
