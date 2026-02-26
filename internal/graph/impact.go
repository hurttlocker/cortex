package graph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
)

const defaultImpactDepth = 3
const (
	defaultImpactLimit = 160
	maxImpactLimit     = 500
)

var errImpactNotFound = errors.New("impact subject not found")

type impactFactMeta struct {
	source      string
	lastUpdated string
}

// ImpactFact is one fact in the impact response.
type ImpactFact struct {
	ID             int64   `json:"id"`
	Subject        string  `json:"subject"`
	Predicate      string  `json:"predicate"`
	Object         string  `json:"object"`
	Confidence     float64 `json:"confidence"`
	Relevance      float64 `json:"relevance,omitempty"`
	Rank           int     `json:"rank,omitempty"`
	Source         string  `json:"source,omitempty"`
	LastUpdated    string  `json:"last_updated,omitempty"`
	ConnectedCount int     `json:"connected_count"`
	Depth          int     `json:"depth"`
}

// ImpactGroup is a semantic relationship bucket.
type ImpactGroup struct {
	Relationship  string       `json:"relationship"`
	Facts         []ImpactFact `json:"facts"`
	AvgConfidence float64      `json:"avg_confidence"`
	FactCount     int          `json:"fact_count"`
}

// ImpactConfidenceDistribution summarizes confidence bands.
type ImpactConfidenceDistribution struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

// ImpactResult is the /api/impact response.
type ImpactResult struct {
	Subject                string                       `json:"subject"`
	TotalFacts             int                          `json:"total_facts"`
	Depth                  int                          `json:"depth"`
	Groups                 []ImpactGroup                `json:"groups"`
	ConfidenceDistribution ImpactConfidenceDistribution `json:"confidence_distribution"`
	ConnectedSubjects      []string                     `json:"connected_subjects"`
	Nodes                  []ExportNode                 `json:"nodes,omitempty"`
	Edges                  []ExportEdge                 `json:"edges,omitempty"`
	Meta                   map[string]interface{}       `json:"meta,omitempty"`
}

func handleImpactAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	if subject == "" {
		writeJSON(w, 400, map[string]string{"error": "subject parameter required"})
		return
	}

	depth := parseBoundedInt(r.URL.Query().Get("depth"), defaultImpactDepth, 1, maxGraphDepth)
	limit := parseBoundedInt(r.URL.Query().Get("limit"), defaultImpactLimit, 1, maxImpactLimit)
	offset := parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, maxGraphOffset)
	minConf := 0.0
	if raw := strings.TrimSpace(r.URL.Query().Get("min_confidence")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid min_confidence"})
			return
		}
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		minConf = v
	}

	result, err := buildImpactResult(context.Background(), st, subject, depth, minConf)
	if err != nil {
		if errors.Is(err, errImpactNotFound) {
			writeJSON(w, 404, map[string]string{"error": fmt.Sprintf("no facts found for subject %q", subject)})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	result = paginateImpactResult(result, limit, offset)
	writeJSON(w, 200, result)
}

func buildImpactResult(ctx context.Context, st *store.SQLiteStore, subject string, depth int, minConf float64) (ImpactResult, error) {
	db := st.GetDB()
	rootIDs, err := loadImpactRootFactIDs(ctx, db, subject, minConf)
	if err != nil {
		return ImpactResult{}, err
	}
	if len(rootIDs) == 0 {
		return ImpactResult{}, errImpactNotFound
	}

	rootLower := strings.ToLower(strings.TrimSpace(subject))
	factsByID := make(map[int64]*store.Fact)
	depthByID := make(map[int64]int)
	edgeByKey := make(map[string]ExportEdge)

	for _, rootID := range rootIDs {
		graphNodes, err := st.TraverseGraph(ctx, rootID, depth, minConf)
		if err != nil {
			return ImpactResult{}, err
		}

		for _, gn := range graphNodes {
			if gn.Fact == nil {
				continue
			}
			if existingDepth, ok := depthByID[gn.Fact.ID]; !ok || gn.Depth < existingDepth {
				depthByID[gn.Fact.ID] = gn.Depth
			}
			if _, ok := factsByID[gn.Fact.ID]; !ok {
				factsByID[gn.Fact.ID] = gn.Fact
			}
			for _, e := range gn.Edges {
				if e.Confidence < minConf {
					continue
				}
				key := edgeKey(e.SourceFactID, e.TargetFactID, string(e.EdgeType))
				edgeByKey[key] = ExportEdge{
					Source:     e.SourceFactID,
					Target:     e.TargetFactID,
					EdgeType:   string(e.EdgeType),
					Confidence: e.Confidence,
					SourceType: string(e.Source),
				}
			}
		}
	}

	if len(factsByID) == 0 {
		return ImpactResult{}, errImpactNotFound
	}

	factIDs := make([]int64, 0, len(factsByID))
	for id := range factsByID {
		factIDs = append(factIDs, id)
	}
	sort.Slice(factIDs, func(i, j int) bool { return factIDs[i] < factIDs[j] })

	metaByID, err := loadImpactFactMeta(ctx, db, factIDs)
	if err != nil {
		return ImpactResult{}, err
	}

	connectedCount := make(map[int64]int)
	adjacency := make(map[int64]map[int64]struct{})
	edges := make([]ExportEdge, 0, len(edgeByKey))
	for _, e := range edgeByKey {
		if _, ok := factsByID[e.Source]; !ok {
			continue
		}
		if _, ok := factsByID[e.Target]; !ok {
			continue
		}

		if adjacency[e.Source] == nil {
			adjacency[e.Source] = make(map[int64]struct{})
		}
		if adjacency[e.Target] == nil {
			adjacency[e.Target] = make(map[int64]struct{})
		}
		adjacency[e.Source][e.Target] = struct{}{}
		adjacency[e.Target][e.Source] = struct{}{}

		// For impact rendering, color edges by normalized relationship group.
		target := factsByID[e.Target]
		if target == nil {
			target = factsByID[e.Source]
		}
		if target != nil {
			e.EdgeType = normalizePredicateGroup(target.Predicate)
		}
		edges = append(edges, e)
	}

	for id, neighbors := range adjacency {
		connectedCount[id] = len(neighbors)
	}

	connectedSubjectsSet := make(map[string]struct{})
	groupMap := make(map[string]*ImpactGroup)
	confDist := ImpactConfidenceDistribution{}
	nodes := make([]ExportNode, 0, len(factIDs))

	for _, id := range factIDs {
		f := factsByID[id]
		if f == nil {
			continue
		}
		factDepth := depthByID[id]
		meta := metaByID[id]

		if subj := strings.TrimSpace(f.Subject); subj != "" && strings.ToLower(subj) != rootLower {
			connectedSubjectsSet[subj] = struct{}{}
		}

		switch {
		case f.Confidence >= 0.8:
			confDist.High++
		case f.Confidence >= 0.5:
			confDist.Medium++
		default:
			confDist.Low++
		}

		groupName := normalizePredicateGroup(f.Predicate)
		group, ok := groupMap[groupName]
		if !ok {
			group = &ImpactGroup{Relationship: groupName}
			groupMap[groupName] = group
		}
		group.Facts = append(group.Facts, ImpactFact{
			ID:             f.ID,
			Subject:        f.Subject,
			Predicate:      f.Predicate,
			Object:         f.Object,
			Confidence:     f.Confidence,
			Source:         meta.source,
			LastUpdated:    meta.lastUpdated,
			ConnectedCount: connectedCount[f.ID],
			Depth:          factDepth,
		})
		group.FactCount++
		group.AvgConfidence += f.Confidence

		nodes = append(nodes, ExportNode{
			ID:          f.ID,
			Subject:     f.Subject,
			Predicate:   f.Predicate,
			Object:      f.Object,
			Confidence:  f.Confidence,
			AgentID:     f.AgentID,
			FactType:    f.FactType,
			LastUpdated: meta.lastUpdated,
			Depth:       factDepth,
		})
	}

	groups := make([]ImpactGroup, 0, len(groupMap))
	for _, g := range groupMap {
		if g.FactCount > 0 {
			g.AvgConfidence = g.AvgConfidence / float64(g.FactCount)
		}
		sort.Slice(g.Facts, func(i, j int) bool {
			if g.Facts[i].Confidence == g.Facts[j].Confidence {
				return g.Facts[i].ID > g.Facts[j].ID
			}
			return g.Facts[i].Confidence > g.Facts[j].Confidence
		})
		groups = append(groups, *g)
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].FactCount == groups[j].FactCount {
			if groups[i].AvgConfidence == groups[j].AvgConfidence {
				return groups[i].Relationship < groups[j].Relationship
			}
			return groups[i].AvgConfidence > groups[j].AvgConfidence
		}
		return groups[i].FactCount > groups[j].FactCount
	})

	connectedSubjects := make([]string, 0, len(connectedSubjectsSet))
	for s := range connectedSubjectsSet {
		connectedSubjects = append(connectedSubjects, s)
	}
	sort.Strings(connectedSubjects)

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Depth == nodes[j].Depth {
			if nodes[i].Confidence == nodes[j].Confidence {
				return nodes[i].ID < nodes[j].ID
			}
			return nodes[i].Confidence > nodes[j].Confidence
		}
		return nodes[i].Depth < nodes[j].Depth
	})

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source == edges[j].Source {
			if edges[i].Target == edges[j].Target {
				return edges[i].EdgeType < edges[j].EdgeType
			}
			return edges[i].Target < edges[j].Target
		}
		return edges[i].Source < edges[j].Source
	})

	return ImpactResult{
		Subject:                subject,
		TotalFacts:             len(nodes),
		Depth:                  depth,
		Groups:                 groups,
		ConfidenceDistribution: confDist,
		ConnectedSubjects:      connectedSubjects,
		Nodes:                  nodes,
		Edges:                  edges,
		Meta: map[string]interface{}{
			"mode":        "impact",
			"subject":     subject,
			"depth":       depth,
			"root_facts":  len(rootIDs),
			"total_nodes": len(nodes),
			"total_edges": len(edges),
		},
	}, nil
}

func paginateImpactResult(result ImpactResult, limit, offset int) ImpactResult {
	type groupedFact struct {
		relationship string
		fact         ImpactFact
	}

	allFacts := make([]groupedFact, 0, result.TotalFacts)
	maxConnected := 0
	for _, g := range result.Groups {
		for _, f := range g.Facts {
			if f.ConnectedCount > maxConnected {
				maxConnected = f.ConnectedCount
			}
			allFacts = append(allFacts, groupedFact{
				relationship: g.Relationship,
				fact:         f,
			})
		}
	}

	for i := range allFacts {
		allFacts[i].fact.Relevance = impactRelevanceScore(
			allFacts[i].fact.Confidence,
			allFacts[i].fact.ConnectedCount,
			allFacts[i].fact.Depth,
			maxConnected,
		)
	}

	sort.Slice(allFacts, func(i, j int) bool {
		if allFacts[i].fact.Relevance == allFacts[j].fact.Relevance {
			if allFacts[i].fact.Confidence == allFacts[j].fact.Confidence {
				if allFacts[i].fact.ConnectedCount == allFacts[j].fact.ConnectedCount {
					return allFacts[i].fact.ID < allFacts[j].fact.ID
				}
				return allFacts[i].fact.ConnectedCount > allFacts[j].fact.ConnectedCount
			}
			return allFacts[i].fact.Confidence > allFacts[j].fact.Confidence
		}
		return allFacts[i].fact.Relevance > allFacts[j].fact.Relevance
	})
	for i := range allFacts {
		allFacts[i].fact.Rank = i + 1
	}

	total := len(allFacts)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := allFacts[start:end]

	groupMap := make(map[string]*ImpactGroup)
	connectedSubjectsSet := make(map[string]struct{})
	confDist := ImpactConfidenceDistribution{}
	allowedFactIDs := make(map[int64]struct{}, len(page))
	for _, item := range page {
		f := item.fact
		allowedFactIDs[f.ID] = struct{}{}
		group, ok := groupMap[item.relationship]
		if !ok {
			group = &ImpactGroup{Relationship: item.relationship}
			groupMap[item.relationship] = group
		}
		group.Facts = append(group.Facts, f)
		group.FactCount++
		group.AvgConfidence += f.Confidence

		if subj := strings.TrimSpace(f.Subject); subj != "" && !strings.EqualFold(subj, result.Subject) {
			connectedSubjectsSet[subj] = struct{}{}
		}

		switch {
		case f.Confidence >= 0.8:
			confDist.High++
		case f.Confidence >= 0.5:
			confDist.Medium++
		default:
			confDist.Low++
		}
	}

	groups := make([]ImpactGroup, 0, len(groupMap))
	for _, g := range groupMap {
		if g.FactCount > 0 {
			g.AvgConfidence /= float64(g.FactCount)
		}
		sort.Slice(g.Facts, func(i, j int) bool {
			if g.Facts[i].Rank == g.Facts[j].Rank {
				return g.Facts[i].ID < g.Facts[j].ID
			}
			return g.Facts[i].Rank < g.Facts[j].Rank
		})
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].FactCount == groups[j].FactCount {
			if groups[i].AvgConfidence == groups[j].AvgConfidence {
				return groups[i].Relationship < groups[j].Relationship
			}
			return groups[i].AvgConfidence > groups[j].AvgConfidence
		}
		return groups[i].FactCount > groups[j].FactCount
	})

	nodeByID := make(map[int64]ExportNode, len(result.Nodes))
	for _, node := range result.Nodes {
		nodeByID[node.ID] = node
	}
	nodes := make([]ExportNode, 0, len(page))
	for _, item := range page {
		if node, ok := nodeByID[item.fact.ID]; ok {
			node.Relevance = item.fact.Relevance
			node.Rank = item.fact.Rank
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Rank == nodes[j].Rank {
			return nodes[i].ID < nodes[j].ID
		}
		return nodes[i].Rank < nodes[j].Rank
	})

	edges := make([]ExportEdge, 0, len(result.Edges))
	for _, edge := range result.Edges {
		_, okSource := allowedFactIDs[edge.Source]
		_, okTarget := allowedFactIDs[edge.Target]
		if okSource && okTarget {
			edges = append(edges, edge)
		}
	}

	connectedSubjects := make([]string, 0, len(connectedSubjectsSet))
	for s := range connectedSubjectsSet {
		connectedSubjects = append(connectedSubjects, s)
	}
	sort.Strings(connectedSubjects)

	if result.Meta == nil {
		result.Meta = map[string]interface{}{}
	}
	result.Meta["ordering"] = "relevance_desc(confidence,connectivity,depth,id)"
	addPaginationMeta(result.Meta, limit, offset, total, len(page))
	result.Meta["returned_nodes"] = len(nodes)
	result.Meta["returned_edges"] = len(edges)
	result.Meta["total_facts"] = total

	result.Groups = groups
	result.Nodes = nodes
	result.Edges = edges
	result.ConnectedSubjects = connectedSubjects
	result.ConfidenceDistribution = confDist
	result.TotalFacts = total
	return result
}

func impactRelevanceScore(confidence float64, connectedCount, depth, maxConnected int) float64 {
	conf := confidence
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	connectedNorm := 0.0
	if maxConnected > 0 {
		connectedNorm = float64(connectedCount) / float64(maxConnected)
	}
	depthPenalty := float64(depth)
	score := (0.78 * conf) + (0.28 * connectedNorm) - (0.06 * depthPenalty)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func loadImpactRootFactIDs(ctx context.Context, db *sql.DB, subject string, minConf float64) ([]int64, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id
		 FROM facts
		 WHERE LOWER(subject) = LOWER(?)
		   AND (superseded_by IS NULL OR superseded_by = 0)
		   AND confidence >= ?
		 ORDER BY confidence DESC, id DESC
		 LIMIT ?`,
		subject, minConf, defaultSubjectGraphLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadImpactFactMeta(ctx context.Context, db *sql.DB, factIDs []int64) (map[int64]impactFactMeta, error) {
	metaByID := make(map[int64]impactFactMeta, len(factIDs))
	if len(factIDs) == 0 {
		return metaByID, nil
	}

	ph := make([]string, len(factIDs))
	args := make([]interface{}, len(factIDs))
	for i, id := range factIDs {
		ph[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT f.id, COALESCE(m.source_file, ''), COALESCE(f.created_at, '')
		 FROM facts f
		 LEFT JOIN memories m ON m.id = f.memory_id
		 WHERE f.id IN (%s)`,
		strings.Join(ph, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var source string
		var createdRaw string
		if err := rows.Scan(&id, &source, &createdRaw); err != nil {
			continue
		}
		metaByID[id] = impactFactMeta{
			source:      source,
			lastUpdated: normalizeStoreTimestamp(createdRaw),
		}
	}
	return metaByID, rows.Err()
}
