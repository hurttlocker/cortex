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
	Clusters         []ClusterListItem      `json:"clusters"`
	UnclusteredCount int                    `json:"unclustered_count"`
	TotalFacts       int                    `json:"total_facts"`
	Meta             map[string]interface{} `json:"meta,omitempty"`
}

type ClusterListItem struct {
	store.Cluster
	Rank      int     `json:"rank"`
	Relevance float64 `json:"relevance"`
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
	offset := parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, maxGraphOffset)
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
	totalClusters := len(clusters)

	start := offset
	if start > len(clusters) {
		start = len(clusters)
	}
	end := start + limit
	if end > len(clusters) {
		end = len(clusters)
	}
	pagedClusters := clusters[start:end]

	maxCount := 0
	for _, c := range clusters {
		if c.FactCount > maxCount {
			maxCount = c.FactCount
		}
	}
	items := make([]ClusterListItem, 0, len(pagedClusters))
	for i, c := range pagedClusters {
		items = append(items, ClusterListItem{
			Cluster:   c,
			Rank:      offset + i + 1,
			Relevance: clusterRelevanceScore(c, maxCount),
		})
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

	meta := map[string]interface{}{
		"ordering": "fact_count_desc,cohesion_desc,name_asc",
	}
	addPaginationMeta(meta, limit, offset, totalClusters, len(items))

	writeJSON(w, 200, ClustersResponse{
		Clusters:         items,
		UnclusteredCount: unclusteredCount,
		TotalFacts:       totalFacts,
		Meta:             meta,
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
	offset := parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, maxGraphOffset)

	fetchLimit := limit + offset
	if fetchLimit > maxClusterFactsLimit {
		fetchLimit = maxClusterFactsLimit
	}

	detail, err := st.GetClusterDetail(ctx, clusterID, fetchLimit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if detail == nil {
		writeJSON(w, 404, map[string]string{"error": "cluster not found"})
		return
	}

	totalClusterFacts := detail.Cluster.FactCount
	if totalClusterFacts < 0 {
		totalClusterFacts = 0
	}
	pageStart := offset
	if pageStart > len(detail.Facts) {
		pageStart = len(detail.Facts)
	}
	pageEnd := pageStart + limit
	if pageEnd > len(detail.Facts) {
		pageEnd = len(detail.Facts)
	}
	pageFacts := detail.Facts[pageStart:pageEnd]

	db := st.GetDB()
	nodes := make([]ExportNode, 0, len(pageFacts))
	facts := make([]SearchFact, 0, len(pageFacts))
	nodeIDs := make([]int64, 0, len(pageFacts))
	subjectGroups := make(map[string][]int64)

	sourceByMemory, _ := loadMemorySourcesForFacts(ctx, db, pageFacts)

	for i, fact := range pageFacts {
		relevance := fact.Confidence
		rank := offset + i + 1
		nodes = append(nodes, ExportNode{
			ID:           fact.ID,
			Subject:      fact.Subject,
			Predicate:    fact.Predicate,
			Object:       fact.Object,
			Confidence:   fact.Confidence,
			Relevance:    relevance,
			Rank:         rank,
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
			Relevance:  relevance,
			Rank:       rank,
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

	meta := map[string]interface{}{
		"mode":                "cluster_detail",
		"cluster_id":          detail.Cluster.ID,
		"cluster_name":        detail.Cluster.Name,
		"cohesion":            detail.Cluster.Cohesion,
		"total_nodes":         len(nodes),
		"total_edges":         len(edges),
		"total_cooccurrences": len(cooccurrences),
		"edge_mode":           edgeMode,
		"fallback_edges":      fallbackEdges,
		"ordering":            "relevance_desc(confidence,id)",
	}
	addPaginationMeta(meta, limit, offset, totalClusterFacts, len(nodes))

	resp := ClusterDetailResponse{
		Cluster:       detail.Cluster,
		Facts:         facts,
		Nodes:         nodes,
		Edges:         edges,
		Cooccurrences: cooccurrences,
		Meta:          meta,
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

func clusterRelevanceScore(c store.Cluster, maxFactCount int) float64 {
	if maxFactCount <= 0 {
		if c.Cohesion < 0 {
			return 0
		}
		if c.Cohesion > 1 {
			return 1
		}
		return c.Cohesion
	}
	factNorm := float64(c.FactCount) / float64(maxFactCount)
	if factNorm < 0 {
		factNorm = 0
	}
	if factNorm > 1 {
		factNorm = 1
	}
	cohesion := c.Cohesion
	if cohesion < 0 {
		cohesion = 0
	}
	if cohesion > 1 {
		cohesion = 1
	}
	return 0.65*factNorm + 0.35*cohesion
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
