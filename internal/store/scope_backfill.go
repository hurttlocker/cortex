package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type ScopeBackfillChange struct {
	FactID         int64  `json:"fact_id"`
	MemoryID       int64  `json:"memory_id"`
	ObserverAgent  string `json:"observer_agent,omitempty"`
	ObservedEntity string `json:"observed_entity,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
}

type ScopeBackfillReport struct {
	DryRun        bool                  `json:"dry_run"`
	TotalFacts    int                   `json:"total_facts"`
	AlreadyScoped int                   `json:"already_scoped"`
	Inferred      int                   `json:"inferred"`
	UnableToInfer int                   `json:"unable_to_infer"`
	Applied       int                   `json:"applied"`
	DurationMs    int64                 `json:"duration_ms"`
	Changes       []ScopeBackfillChange `json:"changes,omitempty"`
}

func (s *SQLiteStore) BackfillFactScope(ctx context.Context, apply bool) (*ScopeBackfillReport, error) {
	started := time.Now()
	report := &ScopeBackfillReport{DryRun: !apply}
	type rowData struct {
		FactID         int64
		MemoryID       int64
		AgentID        string
		ObserverAgent  string
		ObservedEntity string
		SessionID      string
		ProjectID      string
		MemoryProject  string
		MetadataRaw    sql.NullString
	}
	rowsData := make([]rowData, 0, 256)

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			f.id,
			f.memory_id,
			COALESCE(f.agent_id, ''),
			COALESCE(f.observer_agent, ''),
			COALESCE(f.observed_entity, ''),
			COALESCE(f.session_id, ''),
			COALESCE(f.project_id, ''),
			COALESCE(m.project, ''),
			m.metadata
		FROM facts f
		LEFT JOIN memories m ON m.id = f.memory_id
		ORDER BY f.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("querying facts for scope backfill: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var row rowData
		if err := rows.Scan(&row.FactID, &row.MemoryID, &row.AgentID, &row.ObserverAgent, &row.ObservedEntity, &row.SessionID, &row.ProjectID, &row.MemoryProject, &row.MetadataRaw); err != nil {
			return nil, fmt.Errorf("scanning scope backfill row: %w", err)
		}
		rowsData = append(rowsData, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating scope backfill rows: %w", err)
	}

	tx := (*sql.Tx)(nil)
	if apply {
		tx, err = s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("beginning scope backfill tx: %w", err)
		}
		defer tx.Rollback()
	}

	for _, row := range rowsData {
		report.TotalFacts++
		observerAgent := row.ObserverAgent
		observedEntity := row.ObservedEntity
		sessionID := row.SessionID
		projectID := row.ProjectID
		memoryProject := row.MemoryProject
		meta := unmarshalMetadata(row.MetadataRaw)

		if strings.TrimSpace(observerAgent) != "" && strings.TrimSpace(observedEntity) != "" {
			report.AlreadyScoped++
			continue
		}

		inferred := ScopeBackfillChange{
			FactID:   row.FactID,
			MemoryID: row.MemoryID,
		}
		changed := false

		if strings.TrimSpace(observerAgent) == "" {
			observerAgent = strings.TrimSpace(row.AgentID)
			if observerAgent == "" && meta != nil {
				observerAgent = strings.TrimSpace(meta.AgentID)
			}
			if observerAgent != "" {
				inferred.ObserverAgent = observerAgent
				changed = true
			}
		}

		if strings.TrimSpace(observedEntity) == "" && meta != nil {
			observedEntity = strings.TrimSpace(meta.ObservedEntity)
			if observedEntity != "" {
				inferred.ObservedEntity = observedEntity
				changed = true
			}
		}

		if strings.TrimSpace(sessionID) == "" && meta != nil {
			sessionID = strings.TrimSpace(meta.SessionID)
			if sessionID == "" {
				sessionID = strings.TrimSpace(meta.SessionKey)
			}
			if sessionID != "" {
				inferred.SessionID = sessionID
				changed = true
			}
		}

		if strings.TrimSpace(projectID) == "" {
			projectID = strings.TrimSpace(memoryProject)
			if projectID != "" {
				inferred.ProjectID = projectID
				changed = true
			}
		}

		if !changed {
			report.UnableToInfer++
			continue
		}

		report.Inferred++
		report.Changes = append(report.Changes, inferred)
		if !apply {
			continue
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE facts
			   SET observer_agent = CASE WHEN COALESCE(observer_agent, '') = '' THEN ? ELSE observer_agent END,
			       observed_entity = CASE WHEN COALESCE(observed_entity, '') = '' THEN ? ELSE observed_entity END,
			       session_id = CASE WHEN COALESCE(session_id, '') = '' THEN ? ELSE session_id END,
			       project_id = CASE WHEN COALESCE(project_id, '') = '' THEN ? ELSE project_id END
			 WHERE id = ?`,
			observerAgent, observedEntity, sessionID, projectID, row.FactID,
		); err != nil {
			return nil, fmt.Errorf("updating fact %d scope: %w", row.FactID, err)
		}
		report.Applied++
	}

	if apply {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("committing scope backfill tx: %w", err)
		}
	}
	report.DurationMs = time.Since(started).Milliseconds()
	return report, nil
}
