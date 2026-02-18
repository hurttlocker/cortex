package store

import (
	"context"
	"fmt"
)

// ListProjects returns all project tags with their memory and fact counts.
// Projects with empty name (untagged) are included as name="".
func (s *SQLiteStore) ListProjects(ctx context.Context) ([]ProjectInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.project,
			COUNT(DISTINCT m.id) as memory_count,
			COUNT(f.id) as fact_count
		FROM memories m
		LEFT JOIN facts f ON f.memory_id = m.id
		WHERE m.deleted_at IS NULL
		GROUP BY m.project
		ORDER BY memory_count DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()

	var projects []ProjectInfo
	for rows.Next() {
		var p ProjectInfo
		if err := rows.Scan(&p.Name, &p.MemoryCount, &p.FactCount); err != nil {
			return nil, fmt.Errorf("scanning project row: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// TagMemories sets the project tag on specific memory IDs.
func (s *SQLiteStore) TagMemories(ctx context.Context, project string, memoryIDs []int64) (int64, error) {
	if len(memoryIDs) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning tag transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE memories SET project = ? WHERE id = ? AND deleted_at IS NULL`,
	)
	if err != nil {
		return 0, fmt.Errorf("preparing tag statement: %w", err)
	}
	defer stmt.Close()

	var total int64
	for _, id := range memoryIDs {
		result, err := stmt.ExecContext(ctx, project, id)
		if err != nil {
			return total, fmt.Errorf("tagging memory %d: %w", id, err)
		}
		n, _ := result.RowsAffected()
		total += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing tag transaction: %w", err)
	}
	return total, nil
}

// TagMemoriesBySource sets the project tag on all memories matching a source file pattern.
// Pattern uses SQL LIKE syntax (% for wildcard).
func (s *SQLiteStore) TagMemoriesBySource(ctx context.Context, project string, sourcePattern string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE memories SET project = ? WHERE source_file LIKE ? AND deleted_at IS NULL`,
		project, sourcePattern,
	)
	if err != nil {
		return 0, fmt.Errorf("tagging memories by source %q: %w", sourcePattern, err)
	}
	return result.RowsAffected()
}
