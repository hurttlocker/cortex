package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hurttlocker/cortex/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerGraphSubjectsResource(s *server.MCPServer, st store.Store) {
	resource := mcp.NewResource(
		"cortex://graph/subjects",
		"Graph Subjects",
		mcp.WithResourceDescription("Unique graph subjects with active fact counts and average confidence."),
		mcp.WithMIMEType("application/json"),
	)

	s.AddResource(resource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return nil, fmt.Errorf("graph subjects resource requires SQLiteStore")
		}

		type subjectInfo struct {
			Subject       string  `json:"subject"`
			FactCount     int     `json:"fact_count"`
			AvgConfidence float64 `json:"avg_confidence"`
		}

		rows, err := sqlStore.GetDB().QueryContext(ctx,
			`SELECT MIN(subject) AS subject, COUNT(*) AS fact_count, COALESCE(AVG(confidence), 0) AS avg_confidence
			 FROM facts
			 WHERE (superseded_by IS NULL OR superseded_by = 0)
			   AND TRIM(COALESCE(subject, '')) != ''
			 GROUP BY LOWER(TRIM(subject))
			 ORDER BY fact_count DESC, subject ASC
			 LIMIT 500`,
		)
		if err != nil {
			return nil, fmt.Errorf("querying subjects resource: %w", err)
		}
		defer rows.Close()

		subjects := make([]subjectInfo, 0, 256)
		for rows.Next() {
			var item subjectInfo
			if err := rows.Scan(&item.Subject, &item.FactCount, &item.AvgConfidence); err != nil {
				return nil, fmt.Errorf("scanning subjects resource row: %w", err)
			}
			subjects = append(subjects, item)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating subjects resource rows: %w", err)
		}

		payload := map[string]interface{}{
			"subjects": subjects,
			"count":    len(subjects),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "application/json", Text: string(data)},
		}, nil
	})
}

func registerGraphClustersResource(s *server.MCPServer, st store.Store) {
	resource := mcp.NewResource(
		"cortex://graph/clusters",
		"Graph Clusters",
		mcp.WithResourceDescription("Topic clusters with cohesion scores when clustering tables are available."),
		mcp.WithMIMEType("application/json"),
	)

	s.AddResource(resource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		dbMu.Lock()
		defer dbMu.Unlock()

		sqlStore, ok := st.(*store.SQLiteStore)
		if !ok {
			return nil, fmt.Errorf("graph clusters resource requires SQLiteStore")
		}
		db := sqlStore.GetDB()

		ready, err := hasClusterTables(ctx, db)
		if err != nil {
			return nil, fmt.Errorf("checking cluster tables: %w", err)
		}
		if !ready {
			payload := map[string]interface{}{
				"available": false,
				"clusters":  []mcpClusterSummary{},
				"message":   "cluster tables not available yet",
			}
			data, _ := json.MarshalIndent(payload, "", "  ")
			return []mcp.ResourceContents{
				mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "application/json", Text: string(data)},
			}, nil
		}

		clusters, err := queryClusterSummaries(ctx, db, defaultClusterListLimit)
		if err != nil {
			return nil, fmt.Errorf("querying cluster resource: %w", err)
		}

		var unclusteredCount int
		_ = db.QueryRowContext(ctx,
			`SELECT COUNT(*)
			 FROM facts f
			 WHERE (f.superseded_by IS NULL OR f.superseded_by = 0)
			   AND NOT EXISTS (SELECT 1 FROM fact_clusters fc WHERE fc.fact_id = f.id)`,
		).Scan(&unclusteredCount)

		var totalFacts int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM facts WHERE superseded_by IS NULL OR superseded_by = 0`).Scan(&totalFacts)

		payload := map[string]interface{}{
			"available":         true,
			"clusters":          clusters,
			"count":             len(clusters),
			"unclustered_count": unclusteredCount,
			"total_facts":       totalFacts,
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{URI: req.Params.URI, MIMEType: "application/json", Text: string(data)},
		}, nil
	})
}
