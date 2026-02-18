package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AddMemory inserts a new memory. Computes content_hash automatically.
// Returns the new memory ID.
func (s *SQLiteStore) AddMemory(ctx context.Context, m *Memory) (int64, error) {
	if m.Content == "" {
		return 0, fmt.Errorf("memory content cannot be empty")
	}

	if m.ContentHash == "" {
		m.ContentHash = HashMemoryContent(m.Content, m.SourceFile)
	}

	now := time.Now().UTC()
	metadataJSON := marshalMetadata(m.Metadata)
	var metadataArg interface{}
	if metadataJSON != "" {
		metadataArg = metadataJSON
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (content, source_file, source_line, source_section, content_hash, project, metadata, imported_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Content, m.SourceFile, m.SourceLine, m.SourceSection, m.ContentHash, m.Project, metadataArg, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting memory: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}

	m.ID = id
	m.ImportedAt = now
	m.UpdatedAt = now
	return id, nil
}

// GetMemory retrieves a memory by ID. Returns nil if not found or soft-deleted.
func (s *SQLiteStore) GetMemory(ctx context.Context, id int64) (*Memory, error) {
	m := &Memory{}
	var deletedAt sql.NullTime
	var metadataStr sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, content, source_file, source_line, source_section, content_hash, project, metadata, imported_at, updated_at, deleted_at
		 FROM memories WHERE id = ?`, id,
	).Scan(&m.ID, &m.Content, &m.SourceFile, &m.SourceLine, &m.SourceSection,
		&m.ContentHash, &m.Project, &metadataStr, &m.ImportedAt, &m.UpdatedAt, &deletedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting memory %d: %w", id, err)
	}

	m.Metadata = unmarshalMetadata(metadataStr)

	if deletedAt.Valid {
		m.DeletedAt = &deletedAt.Time
	}

	return m, nil
}

// ListMemories returns memories with pagination. Excludes soft-deleted by default.
func (s *SQLiteStore) ListMemories(ctx context.Context, opts ListOpts) ([]*Memory, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	query := `SELECT id, content, source_file, source_line, source_section, content_hash, project, metadata, imported_at, updated_at
			  FROM memories WHERE deleted_at IS NULL`
	args := []interface{}{}

	if opts.SourceFile != "" {
		query += " AND source_file = ?"
		args = append(args, opts.SourceFile)
	}

	if opts.Project != "" {
		query += " AND project = ?"
		args = append(args, opts.Project)
	}

	// Metadata filters (Issue #30)
	if opts.Agent != "" {
		query += ` AND json_extract(metadata, '$.agent_id') = ?`
		args = append(args, opts.Agent)
	}
	if opts.Channel != "" {
		query += ` AND json_extract(metadata, '$.channel') = ?`
		args = append(args, opts.Channel)
	}
	if opts.After != "" {
		query += " AND imported_at >= ?"
		args = append(args, opts.After)
	}
	if opts.Before != "" {
		query += " AND imported_at < ?"
		args = append(args, opts.Before+" 23:59:59")
	}

	orderBy := "imported_at DESC"
	if opts.SortBy == "date" {
		orderBy = "imported_at DESC"
	}
	query += fmt.Sprintf(" ORDER BY %s LIMIT ? OFFSET ?", orderBy)
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing memories: %w", err)
	}
	defer rows.Close()

	var memories []*Memory
	for rows.Next() {
		m := &Memory{}
		var metadataStr sql.NullString
		if err := rows.Scan(&m.ID, &m.Content, &m.SourceFile, &m.SourceLine,
			&m.SourceSection, &m.ContentHash, &m.Project, &metadataStr, &m.ImportedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory row: %w", err)
		}
		m.Metadata = unmarshalMetadata(metadataStr)
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// DeleteMemory soft-deletes a memory by setting deleted_at.
func (s *SQLiteStore) DeleteMemory(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		"UPDATE memories SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL", now, id,
	)
	if err != nil {
		return fmt.Errorf("deleting memory %d: %w", id, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("memory %d not found or already deleted", id)
	}
	return nil
}

// UpdateMemory updates a memory's content and recomputes its hash.
// The memories_au trigger will keep FTS in sync.
// Returns an error if the memory does not exist or is soft-deleted.
func (s *SQLiteStore) UpdateMemory(ctx context.Context, id int64, content string) error {
	if content == "" {
		return fmt.Errorf("memory content cannot be empty")
	}
	newHash := HashContentOnly(content)
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE memories SET content = ?, content_hash = ?, updated_at = ?
		 WHERE id = ? AND deleted_at IS NULL`,
		content, newHash, now, id,
	)
	if err != nil {
		return fmt.Errorf("updating memory %d: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("memory %d not found or already deleted", id)
	}
	return nil
}

// AddMemoryBatch inserts multiple memories in a transaction.
// Uses the configured batch size for chunking.
func (s *SQLiteStore) AddMemoryBatch(ctx context.Context, memories []*Memory) ([]int64, error) {
	ids := make([]int64, 0, len(memories))

	for i := 0; i < len(memories); i += s.batchSize {
		end := i + s.batchSize
		if end > len(memories) {
			end = len(memories)
		}
		chunk := memories[i:end]

		chunkIDs, err := s.insertBatch(ctx, chunk)
		if err != nil {
			return ids, fmt.Errorf("batch insert chunk %d-%d: %w", i, end, err)
		}
		ids = append(ids, chunkIDs...)
	}
	return ids, nil
}

func (s *SQLiteStore) insertBatch(ctx context.Context, memories []*Memory) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO memories (content, source_file, source_line, source_section, content_hash, project, imported_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return nil, fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	ids := make([]int64, 0, len(memories))

	for _, m := range memories {
		if m.ContentHash == "" {
			m.ContentHash = HashMemoryContent(m.Content, m.SourceFile)
		}
		result, err := stmt.ExecContext(ctx,
			m.Content, m.SourceFile, m.SourceLine, m.SourceSection, m.ContentHash, m.Project, now, now,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting memory in batch: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("getting last insert id: %w", err)
		}
		m.ID = id
		m.ImportedAt = now
		m.UpdatedAt = now
		ids = append(ids, id)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing batch: %w", err)
	}
	return ids, nil
}

// FindByHash looks up a memory by its content hash for deduplication.
func (s *SQLiteStore) FindByHash(ctx context.Context, hash string) (*Memory, error) {
	m := &Memory{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, content, source_file, source_line, source_section, content_hash, project, imported_at, updated_at
		 FROM memories WHERE content_hash = ? AND deleted_at IS NULL`, hash,
	).Scan(&m.ID, &m.Content, &m.SourceFile, &m.SourceLine, &m.SourceSection,
		&m.ContentHash, &m.Project, &m.ImportedAt, &m.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding memory by hash: %w", err)
	}
	return m, nil
}

// hashContent is deprecated. Use HashMemoryContent or HashContentOnly from hash.go.
// This is kept for backwards compatibility with existing tests.
func hashContent(content string) string {
	return HashContentOnly(content)
}
