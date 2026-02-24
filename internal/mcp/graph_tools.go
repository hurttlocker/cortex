package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultGraphExploreDepth = 2
	defaultGraphImpactDepth  = 3
	maxGraphDepth            = 5
	defaultGraphExploreLimit = 50
	maxGraphExploreLimit     = 200
	defaultClusterListLimit  = 100
	maxClusterListLimit      = 500
)

type graphExploreFact struct {
	ID         int64   `json:"id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	Hop        int     `json:"hop"`
	Source     string  `json:"source"`
}

type graphExploreEdge struct {
	From       int64   `json:"from"`
	To         int64   `json:"to"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

type graphExploreResult struct {
	Root              string             `json:"root"`
	Depth             int                `json:"depth"`
	Facts             []graphExploreFact `json:"facts"`
	Edges             []graphExploreEdge `json:"edges"`
	ConnectedSubjects []string           `json:"connected_subjects"`
	TotalFacts        int                `json:"total_facts"`
}

type graphImpactGroup struct {
	Name          string             `json:"name"`
	FactCount     int                `json:"fact_count"`
	AvgConfidence float64            `json:"avg_confidence"`
	Facts         []graphExploreFact `json:"facts"`
}

type graphImpactResult struct {
	Subject                string             `json:"subject"`
	Depth                  int                `json:"depth"`
	TotalFacts             int                `json:"total_facts"`
	Groups                 []graphImpactGroup `json:"groups"`
	ConnectedSubjects      []string           `json:"connected_subjects"`
	ConfidenceDistribution map[string]int     `json:"confidence_distribution"`
	Edges                  []graphExploreEdge `json:"edges"`
}

type mcpClusterSummary struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Aliases       []string `json:"aliases"`
	Cohesion      float64  `json:"cohesion"`
	FactCount     int      `json:"fact_count"`
	AvgConfidence float64  `json:"avg_confidence"`
	TopSubjects   []string `json:"top_subjects"`
}

type graphNodeView struct {
	fact *store.Fact
	hop  int
}

func registerGraphExploreTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("graph_explore",
		mcp.WithDescription("Explore the knowledge graph around a starting subject or fact_id. Returns connected facts and edges within the requested depth."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("subject",
			mcp.Description("Subject to explore from (mutually exclusive with fact_id)"),
		),
		mcp.WithNumber("fact_id",
			mcp.Description("Fact ID to explore from (mutually exclusive with subject)"),
		),
		mcp.WithNumber("depth",
			mcp.Description("Traversal depth (default: 2, max: 5)"),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Minimum confidence threshold (default: 0.3)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum facts to return (default: 50, max: 200)"),
		),
		mcp.WithString("source",
			mcp.Description("Optional source prefix filter (e.g., github, memory/)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("graph_explore requires SQLiteStore"), nil
		}

		subject := ""
		if v, err := req.RequireString("subject"); err == nil {
			subject = strings.TrimSpace(v)
		}
		factID := int64(0)
		if v, err := req.RequireFloat("fact_id"); err == nil {
			factID = int64(v)
		}

		if (subject == "" && factID <= 0) || (subject != "" && factID > 0) {
			return mcp.NewToolResultError("provide exactly one of subject or fact_id"), nil
		}

		depth := defaultGraphExploreDepth
		if v, err := req.RequireFloat("depth"); err == nil {
			depth = int(v)
		}
		if depth < 1 {
			depth = 1
		}
		if depth > maxGraphDepth {
			depth = maxGraphDepth
		}

		minConfidence := 0.3
		if v, err := req.RequireFloat("min_confidence"); err == nil {
			minConfidence = v
		}
		if minConfidence < 0 {
			minConfidence = 0
		}
		if minConfidence > 1 {
			minConfidence = 1
		}

		limit := defaultGraphExploreLimit
		if v, err := req.RequireFloat("limit"); err == nil {
			limit = int(v)
		}
		if limit < 1 {
			limit = 1
		}
		if limit > maxGraphExploreLimit {
			limit = maxGraphExploreLimit
		}

		sourcePrefix := ""
		if v, err := req.RequireString("source"); err == nil {
			sourcePrefix = strings.TrimSpace(v)
		}

		result, err := buildGraphExploreResult(ctx, sqlStore, subject, factID, depth, minConfidence, limit, sourcePrefix)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph_explore error: %v", err)), nil
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerGraphImpactTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("graph_impact",
		mcp.WithDescription("Analyze blast radius for a subject. Returns grouped related facts with confidence statistics."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("subject", mcp.Required(),
			mcp.Description("Subject to analyze"),
		),
		mcp.WithNumber("depth",
			mcp.Description("Traversal depth (default: 3, max: 5)"),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Minimum confidence threshold (default: 0.3)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("graph_impact requires SQLiteStore"), nil
		}

		subject, err := req.RequireString("subject")
		if err != nil || strings.TrimSpace(subject) == "" {
			return mcp.NewToolResultError("subject is required"), nil
		}
		subject = strings.TrimSpace(subject)

		depth := defaultGraphImpactDepth
		if v, err := req.RequireFloat("depth"); err == nil {
			depth = int(v)
		}
		if depth < 1 {
			depth = 1
		}
		if depth > maxGraphDepth {
			depth = maxGraphDepth
		}

		minConfidence := 0.3
		if v, err := req.RequireFloat("min_confidence"); err == nil {
			minConfidence = v
		}
		if minConfidence < 0 {
			minConfidence = 0
		}
		if minConfidence > 1 {
			minConfidence = 1
		}

		result, err := buildGraphImpactResult(ctx, sqlStore, subject, depth, minConfidence)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph_impact error: %v", err)), nil
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func registerListClustersTool(s *server.MCPServer, st store.Store) {
	tool := mcp.NewTool("list_clusters",
		mcp.WithDescription("List topic clusters with cohesion scores and fact counts when clustering is available."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithNumber("limit",
			mcp.Description("Maximum clusters to return (default: 100, max: 500)"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return mcp.NewToolResultError("list_clusters requires SQLiteStore"), nil
		}
		db := sqlStore.GetDB()

		ready, err := hasClusterTables(ctx, db)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list_clusters error: %v", err)), nil
		}
		if !ready {
			payload := map[string]interface{}{
				"available": false,
				"message":   "cluster tables not available yet",
				"clusters":  []mcpClusterSummary{},
			}
			data, _ := json.MarshalIndent(payload, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		limit := defaultClusterListLimit
		if v, err := req.RequireFloat("limit"); err == nil {
			limit = int(v)
		}
		if limit < 1 {
			limit = 1
		}
		if limit > maxClusterListLimit {
			limit = maxClusterListLimit
		}

		clusters, err := queryClusterSummaries(ctx, db, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list_clusters error: %v", err)), nil
		}

		payload := map[string]interface{}{
			"available": true,
			"clusters":  clusters,
			"count":     len(clusters),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func buildGraphExploreResult(
	ctx context.Context,
	sqlStore *store.SQLiteStore,
	subject string,
	factID int64,
	depth int,
	minConfidence float64,
	limit int,
	sourcePrefix string,
) (*graphExploreResult, error) {
	db := sqlStore.GetDB()
	seedFactIDs := make([]int64, 0)
	if factID > 0 {
		seedFactIDs = append(seedFactIDs, factID)
	} else {
		ids, err := resolveSeedFactsBySubject(ctx, db, subject, minConfidence, sourcePrefix, limit)
		if err != nil {
			return nil, err
		}
		seedFactIDs = append(seedFactIDs, ids...)
	}
	if len(seedFactIDs) == 0 {
		root := subject
		if root == "" {
			root = fmt.Sprintf("#%d", factID)
		}
		return &graphExploreResult{Root: root, Depth: depth, Facts: []graphExploreFact{}, Edges: []graphExploreEdge{}, ConnectedSubjects: []string{}, TotalFacts: 0}, nil
	}

	nodeByID := make(map[int64]graphNodeView)
	edgeByKey := make(map[string]graphExploreEdge)

	for _, seedID := range seedFactIDs {
		graphNodes, err := sqlStore.TraverseGraph(ctx, seedID, depth, minConfidence)
		if err != nil {
			return nil, err
		}
		for _, gn := range graphNodes {
			if gn.Fact == nil {
				continue
			}
			if cur, ok := nodeByID[gn.Fact.ID]; !ok || gn.Depth < cur.hop {
				nodeByID[gn.Fact.ID] = graphNodeView{fact: gn.Fact, hop: gn.Depth}
			}
			for _, e := range gn.Edges {
				key := fmt.Sprintf("%d:%d:%s", e.SourceFactID, e.TargetFactID, e.EdgeType)
				edgeByKey[key] = graphExploreEdge{
					From:       e.SourceFactID,
					To:         e.TargetFactID,
					Type:       string(e.EdgeType),
					Confidence: e.Confidence,
				}
			}
		}
	}

	memorySources, err := loadMemorySourcesForNodeViews(ctx, db, nodeByID)
	if err != nil {
		return nil, err
	}

	facts := make([]graphExploreFact, 0, len(nodeByID))
	for id, nv := range nodeByID {
		source := memorySources[nv.fact.MemoryID]
		if !matchesSourcePrefix(source, sourcePrefix) {
			continue
		}
		facts = append(facts, graphExploreFact{
			ID:         id,
			Subject:    nv.fact.Subject,
			Predicate:  nv.fact.Predicate,
			Object:     nv.fact.Object,
			Confidence: nv.fact.Confidence,
			Hop:        nv.hop,
			Source:     source,
		})
	}
	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Hop != facts[j].Hop {
			return facts[i].Hop < facts[j].Hop
		}
		if facts[i].Confidence != facts[j].Confidence {
			return facts[i].Confidence > facts[j].Confidence
		}
		return facts[i].ID < facts[j].ID
	})
	if len(facts) > limit {
		facts = facts[:limit]
	}

	kept := make(map[int64]struct{}, len(facts))
	for _, f := range facts {
		kept[f.ID] = struct{}{}
	}

	edges := make([]graphExploreEdge, 0, len(edgeByKey))
	for _, e := range edgeByKey {
		if _, ok := kept[e.From]; !ok {
			continue
		}
		if _, ok := kept[e.To]; !ok {
			continue
		}
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Type < edges[j].Type
	})

	connectedSet := make(map[string]struct{})
	rootLower := strings.ToLower(strings.TrimSpace(subject))
	if factID > 0 {
		if nv, ok := nodeByID[factID]; ok && nv.fact != nil {
			rootLower = strings.ToLower(strings.TrimSpace(nv.fact.Subject))
		}
	}
	for _, f := range facts {
		subjectLower := strings.ToLower(strings.TrimSpace(f.Subject))
		if subjectLower == "" || subjectLower == rootLower {
			continue
		}
		connectedSet[f.Subject] = struct{}{}
	}
	connectedSubjects := make([]string, 0, len(connectedSet))
	for s := range connectedSet {
		connectedSubjects = append(connectedSubjects, s)
	}
	sort.Strings(connectedSubjects)

	root := subject
	if root == "" {
		root = fmt.Sprintf("#%d", factID)
	}

	return &graphExploreResult{
		Root:              root,
		Depth:             depth,
		Facts:             facts,
		Edges:             edges,
		ConnectedSubjects: connectedSubjects,
		TotalFacts:        len(facts),
	}, nil
}

func buildGraphImpactResult(
	ctx context.Context,
	sqlStore *store.SQLiteStore,
	subject string,
	depth int,
	minConfidence float64,
) (*graphImpactResult, error) {
	explore, err := buildGraphExploreResult(ctx, sqlStore, subject, 0, depth, minConfidence, maxGraphExploreLimit, "")
	if err != nil {
		return nil, err
	}

	groups := make(map[string][]graphExploreFact)
	edges := explore.Edges
	for _, f := range explore.Facts {
		group := predicateGroupForImpact(f.Predicate)
		groups[group] = append(groups[group], f)
	}

	resultGroups := make([]graphImpactGroup, 0, len(groups))
	for name, facts := range groups {
		sort.Slice(facts, func(i, j int) bool {
			if facts[i].Confidence != facts[j].Confidence {
				return facts[i].Confidence > facts[j].Confidence
			}
			return facts[i].ID < facts[j].ID
		})
		avg := 0.0
		for _, f := range facts {
			avg += f.Confidence
		}
		if len(facts) > 0 {
			avg /= float64(len(facts))
		}
		resultGroups = append(resultGroups, graphImpactGroup{
			Name:          name,
			FactCount:     len(facts),
			AvgConfidence: avg,
			Facts:         facts,
		})
	}
	sort.Slice(resultGroups, func(i, j int) bool {
		if resultGroups[i].FactCount != resultGroups[j].FactCount {
			return resultGroups[i].FactCount > resultGroups[j].FactCount
		}
		return resultGroups[i].Name < resultGroups[j].Name
	})

	confidence := map[string]int{"high": 0, "medium": 0, "low": 0, "total": len(explore.Facts)}
	for _, f := range explore.Facts {
		switch {
		case f.Confidence >= 0.7:
			confidence["high"]++
		case f.Confidence >= 0.3:
			confidence["medium"]++
		default:
			confidence["low"]++
		}
	}

	return &graphImpactResult{
		Subject:                subject,
		Depth:                  depth,
		TotalFacts:             explore.TotalFacts,
		Groups:                 resultGroups,
		ConnectedSubjects:      explore.ConnectedSubjects,
		ConfidenceDistribution: confidence,
		Edges:                  edges,
	}, nil
}

func resolveSeedFactsBySubject(ctx context.Context, db *sql.DB, subject string, minConfidence float64, sourcePrefix string, limit int) ([]int64, error) {
	query := `SELECT f.id
	          FROM facts f
	          LEFT JOIN memories m ON m.id = f.memory_id
	          WHERE LOWER(f.subject) = LOWER(?)
	            AND (f.superseded_by IS NULL OR f.superseded_by = 0)
	            AND f.confidence >= ?`
	args := []interface{}{subject, minConfidence}
	if sourcePrefix != "" {
		query += " AND LOWER(COALESCE(m.source_file, '')) LIKE ?"
		args = append(args, strings.ToLower(sourcePrefix)+"%")
	}
	query += " ORDER BY f.confidence DESC, f.id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying seed facts for subject %q: %w", subject, err)
	}
	defer rows.Close()

	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning seed fact id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadMemorySourcesForNodeViews(ctx context.Context, db *sql.DB, nodes map[int64]graphNodeView) (map[int64]string, error) {
	memoryIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, nv := range nodes {
		if nv.fact == nil || nv.fact.MemoryID <= 0 {
			continue
		}
		if _, ok := seen[nv.fact.MemoryID]; ok {
			continue
		}
		seen[nv.fact.MemoryID] = struct{}{}
		memoryIDs = append(memoryIDs, nv.fact.MemoryID)
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
		return nil, fmt.Errorf("querying memory sources: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]string, len(memoryIDs))
	for rows.Next() {
		var id int64
		var source string
		if err := rows.Scan(&id, &source); err != nil {
			return nil, fmt.Errorf("scanning memory source: %w", err)
		}
		result[id] = source
	}
	return result, rows.Err()
}

func matchesSourcePrefix(source, prefix string) bool {
	if strings.TrimSpace(prefix) == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(source), strings.ToLower(strings.TrimSpace(prefix)))
}

func predicateGroupForImpact(predicate string) string {
	p := strings.ToLower(strings.TrimSpace(predicate))
	switch {
	case p == "":
		return "other"
	case hasAnyToken(p, "tool", "uses", "platform", "app", "service", "stack"):
		return "has_tool"
	case hasAnyToken(p, "config", "setting", "flag", "parameter", "env"):
		return "has_config"
	case hasAnyToken(p, "strategy", "approach", "method", "plan"):
		return "has_strategy"
	case hasAnyToken(p, "location", "region", "city", "country", "address"):
		return "has_location"
	case hasAnyToken(p, "depend", "requires", "blocked"):
		return "depends_on"
	case hasAnyToken(p, "relates", "linked", "connected"):
		return "related_to"
	default:
		return "other"
	}
}

func hasAnyToken(value string, tokens ...string) bool {
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func hasClusterTables(ctx context.Context, db *sql.DB) (bool, error) {
	for _, table := range []string{"clusters", "fact_clusters"} {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`,
			table,
		).Scan(&count); err != nil {
			return false, err
		}
		if count == 0 {
			return false, nil
		}
	}
	return true, nil
}

func queryClusterSummaries(ctx context.Context, db *sql.DB, limit int) ([]mcpClusterSummary, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, COALESCE(aliases, '[]'), cohesion, fact_count, avg_confidence
		 FROM clusters
		 ORDER BY fact_count DESC, cohesion DESC, name ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	clusters := make([]mcpClusterSummary, 0, limit)
	for rows.Next() {
		var c mcpClusterSummary
		var aliasesRaw string
		if err := rows.Scan(&c.ID, &c.Name, &aliasesRaw, &c.Cohesion, &c.FactCount, &c.AvgConfidence); err != nil {
			return nil, err
		}
		c.Aliases = parseAliasesRaw(aliasesRaw)
		clusters = append(clusters, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range clusters {
		top, err := queryTopSubjectsForCluster(ctx, db, clusters[i].ID, 5)
		if err != nil {
			return nil, err
		}
		clusters[i].TopSubjects = top
	}

	return clusters, nil
}

func queryTopSubjectsForCluster(ctx context.Context, db *sql.DB, clusterID int64, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT MIN(f.subject) AS subject, COUNT(*) AS cnt
		 FROM facts f
		 JOIN fact_clusters fc ON fc.fact_id = f.id
		 WHERE fc.cluster_id = ?
		   AND TRIM(COALESCE(f.subject, '')) != ''
		 GROUP BY LOWER(TRIM(f.subject))
		 ORDER BY cnt DESC, subject ASC
		 LIMIT ?`,
		clusterID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	subjects := make([]string, 0, limit)
	for rows.Next() {
		var subject string
		var count int
		if err := rows.Scan(&subject, &count); err != nil {
			return nil, err
		}
		subjects = append(subjects, subject)
	}
	return subjects, rows.Err()
}

func parseAliasesRaw(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	aliases := make([]string, 0)
	if err := json.Unmarshal([]byte(raw), &aliases); err != nil {
		return nil
	}
	out := aliases[:0]
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		out = append(out, alias)
	}
	return out
}
