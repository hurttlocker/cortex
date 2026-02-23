package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// EdgeType defines the type of relationship between facts.
type EdgeType string

const (
	EdgeTypeSupports    EdgeType = "supports"
	EdgeTypeContradicts EdgeType = "contradicts"
	EdgeTypeRelatesTo   EdgeType = "relates_to"
	EdgeTypeSupersedes  EdgeType = "supersedes"
	EdgeTypeDerivedFrom EdgeType = "derived_from"
)

// EdgeSource defines how an edge was created.
type EdgeSource string

const (
	EdgeSourceExplicit EdgeSource = "explicit"
	EdgeSourceDetected EdgeSource = "detected"
	EdgeSourceInferred EdgeSource = "inferred"
)

// FactEdge represents a typed relationship between two facts.
type FactEdge struct {
	ID           int64      `json:"id"`
	SourceFactID int64      `json:"source_fact_id"`
	TargetFactID int64      `json:"target_fact_id"`
	EdgeType     EdgeType   `json:"edge_type"`
	Confidence   float64    `json:"confidence"`
	Source       EdgeSource `json:"source"`
	AgentID      string     `json:"agent_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// GraphNode represents a fact in a graph traversal result.
type GraphNode struct {
	Fact  *Fact      `json:"fact"`
	Edges []FactEdge `json:"edges"`
	Depth int        `json:"depth"`
}

// ErrEdgeExists is returned when an edge already exists (duplicate).
var ErrEdgeExists = fmt.Errorf("edge already exists")

// AddEdge creates a relationship between two facts.
// Returns ErrEdgeExists if the edge already exists (same source, target, type).
func (s *SQLiteStore) AddEdge(ctx context.Context, edge *FactEdge) error {
	if edge.SourceFactID == edge.TargetFactID {
		return fmt.Errorf("cannot create edge from a fact to itself")
	}
	if _, err := ParseEdgeType(string(edge.EdgeType)); err != nil {
		return err
	}
	if edge.Confidence <= 0 {
		edge.Confidence = 1.0
	}
	if edge.Source == "" {
		edge.Source = EdgeSourceExplicit
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO fact_edges_v1 (source_fact_id, target_fact_id, edge_type, confidence, source, agent_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		edge.SourceFactID, edge.TargetFactID, string(edge.EdgeType),
		edge.Confidence, string(edge.Source), edge.AgentID,
	)
	if err != nil {
		return fmt.Errorf("adding edge: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrEdgeExists
	}

	id, _ := result.LastInsertId()
	edge.ID = id
	edge.CreatedAt = time.Now().UTC()
	return nil
}

// GetEdgesForFact returns all edges where the given fact is source or target.
func (s *SQLiteStore) GetEdgesForFact(ctx context.Context, factID int64) ([]FactEdge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source_fact_id, target_fact_id, edge_type, confidence, source, agent_id, created_at
		 FROM fact_edges_v1
		 WHERE source_fact_id = ? OR target_fact_id = ?
		 ORDER BY created_at DESC`,
		factID, factID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting edges for fact %d: %w", factID, err)
	}
	defer rows.Close()

	return scanEdges(rows)
}

// GetEdgesByType returns all edges of a specific type.
func (s *SQLiteStore) GetEdgesByType(ctx context.Context, edgeType EdgeType, limit int) ([]FactEdge, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source_fact_id, target_fact_id, edge_type, confidence, source, agent_id, created_at
		 FROM fact_edges_v1 WHERE edge_type = ? ORDER BY created_at DESC LIMIT ?`,
		string(edgeType), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting edges by type: %w", err)
	}
	defer rows.Close()

	return scanEdges(rows)
}

// RemoveEdge deletes an edge by ID.
func (s *SQLiteStore) RemoveEdge(ctx context.Context, edgeID int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM fact_edges_v1 WHERE id = ?`, edgeID)
	if err != nil {
		return fmt.Errorf("removing edge: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("edge %d not found", edgeID)
	}
	return nil
}

// TraverseGraph performs a breadth-first traversal from a starting fact,
// following edges up to maxDepth hops. Returns all reachable facts with their edges.
func (s *SQLiteStore) TraverseGraph(ctx context.Context, startFactID int64, maxDepth int, minConfidence float64) ([]GraphNode, error) {
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if minConfidence <= 0 {
		minConfidence = 0.0
	}

	visited := map[int64]bool{}
	var result []GraphNode

	// BFS queue
	type queueItem struct {
		factID int64
		depth  int
	}
	queue := []queueItem{{startFactID, 0}}
	visited[startFactID] = true

	for len(queue) > 0 {
		// Respect context cancellation
		if err := ctx.Err(); err != nil {
			return result, err
		}

		item := queue[0]
		queue = queue[1:]

		// Get the fact
		fact, err := s.GetFact(ctx, item.factID)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			continue // Fact may have been deleted
		}
		if fact == nil {
			continue
		}

		// Get edges
		edges, err := s.GetEdgesForFact(ctx, item.factID)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			continue // Edge query failed, skip this node
		}

		// Filter by confidence
		var filteredEdges []FactEdge
		for _, e := range edges {
			if e.Confidence >= minConfidence {
				filteredEdges = append(filteredEdges, e)
			}
		}

		result = append(result, GraphNode{
			Fact:  fact,
			Edges: filteredEdges,
			Depth: item.depth,
		})

		// Enqueue neighbors if not at max depth
		if item.depth < maxDepth {
			for _, e := range filteredEdges {
				neighborID := e.TargetFactID
				if neighborID == item.factID {
					neighborID = e.SourceFactID
				}
				if !visited[neighborID] {
					visited[neighborID] = true
					queue = append(queue, queueItem{neighborID, item.depth + 1})
				}
			}

			// Also follow strong co-occurrence links (count >= 5)
			coocs, _ := s.GetCooccurrencesForFact(ctx, item.factID, 10)
			for _, c := range coocs {
				if c.Count < 5 {
					continue
				}
				neighborID := c.FactIDB
				if neighborID == item.factID {
					neighborID = c.FactIDA
				}
				if !visited[neighborID] {
					visited[neighborID] = true
					queue = append(queue, queueItem{neighborID, item.depth + 1})
				}
			}
		}
	}

	return result, nil
}

// CountEdges returns the total number of edges in the graph.
func (s *SQLiteStore) CountEdges(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fact_edges_v1`).Scan(&count)
	return count, err
}

// DecayInferredEdges removes inferred edges that haven't been reinforced
// (no co-occurrence or access) within the given number of days.
func (s *SQLiteStore) DecayInferredEdges(ctx context.Context, maxAgeDays int) (int, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 90
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -maxAgeDays)

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM fact_edges_v1 WHERE source = 'inferred' AND created_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("decaying inferred edges: %w", err)
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}

func scanEdges(rows *sql.Rows) ([]FactEdge, error) {
	var edges []FactEdge
	for rows.Next() {
		var e FactEdge
		var createdStr string
		if err := rows.Scan(&e.ID, &e.SourceFactID, &e.TargetFactID,
			&e.EdgeType, &e.Confidence, &e.Source, &e.AgentID, &createdStr); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		if t, err := time.Parse("2006-01-02 15:04:05", createdStr); err == nil {
			e.CreatedAt = t
		} else if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
			e.CreatedAt = t
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// ValidEdgeTypes returns the valid edge type strings.
func ValidEdgeTypes() []string {
	return []string{
		string(EdgeTypeSupports), string(EdgeTypeContradicts),
		string(EdgeTypeRelatesTo), string(EdgeTypeSupersedes),
		string(EdgeTypeDerivedFrom),
	}
}

// ParseEdgeType validates and returns an EdgeType.
func ParseEdgeType(s string) (EdgeType, error) {
	switch EdgeType(strings.ToLower(s)) {
	case EdgeTypeSupports, EdgeTypeContradicts, EdgeTypeRelatesTo, EdgeTypeSupersedes, EdgeTypeDerivedFrom:
		return EdgeType(strings.ToLower(s)), nil
	default:
		return "", fmt.Errorf("invalid edge type %q (valid: %s)", s, strings.Join(ValidEdgeTypes(), ", "))
	}
}
