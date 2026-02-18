package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// MetadataSearchFilters holds metadata-based search filters.
type MetadataSearchFilters struct {
	Agent   string // Filter by agent_id in metadata JSON
	Channel string // Filter by channel in metadata JSON
	After   string // Filter memories imported after this date (YYYY-MM-DD)
	Before  string // Filter memories imported before this date (YYYY-MM-DD)
}

// migrateMetadataColumn adds the metadata JSON column to memories.
// Safe, idempotent migration for Issue #30.
func (s *SQLiteStore) migrateMetadataColumn() error {
	// Check if column already exists
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name='metadata'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("checking for metadata column: %w", err)
	}
	if count > 0 {
		return nil // Already migrated
	}

	// Add column
	_, err = s.db.Exec(`ALTER TABLE memories ADD COLUMN metadata TEXT DEFAULT NULL`)
	if err != nil {
		return fmt.Errorf("adding metadata column: %w", err)
	}

	return nil
}

// marshalMetadata converts Metadata to JSON string for storage.
// Returns empty string (not "null") if metadata is nil.
func marshalMetadata(m *Metadata) string {
	if m == nil {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// unmarshalMetadata converts a JSON string (or sql.NullString) back to Metadata.
func unmarshalMetadata(s sql.NullString) *Metadata {
	if !s.Valid || s.String == "" {
		return nil
	}
	var m Metadata
	if err := json.Unmarshal([]byte(s.String), &m); err != nil {
		return nil
	}
	return &m
}

// ParseMetadataJSON parses a JSON string into a Metadata struct.
// Used by CLI --metadata flag.
func ParseMetadataJSON(jsonStr string) (*Metadata, error) {
	if jsonStr == "" {
		return nil, nil
	}
	var m Metadata
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return nil, fmt.Errorf("parsing metadata JSON: %w", err)
	}
	return &m, nil
}

// BuildMetadataPrefix creates a searchable text prefix from metadata.
// This gets prepended to FTS5 content so metadata terms are searchable.
func BuildMetadataPrefix(m *Metadata) string {
	if m == nil {
		return ""
	}

	var parts []string
	if m.AgentID != "" {
		parts = append(parts, "agent:"+m.AgentID)
	}
	if m.AgentName != "" && m.AgentName != m.AgentID {
		parts = append(parts, "agent:"+m.AgentName)
	}
	if m.Channel != "" {
		parts = append(parts, "channel:"+m.Channel)
	}
	if m.ChannelName != "" {
		parts = append(parts, "channel:"+m.ChannelName)
	}
	if m.Surface != "" && m.Surface != m.Channel {
		parts = append(parts, "surface:"+m.Surface)
	}
	if m.Model != "" {
		parts = append(parts, "model:"+m.Model)
	}
	if m.TimestampStart != "" {
		// Extract date portion for date: prefix
		if len(m.TimestampStart) >= 10 {
			parts = append(parts, "date:"+m.TimestampStart[:10])
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ") + "\n"
}

// buildMetadataWhereClause generates SQL WHERE conditions for metadata filters.
// Returns the clause fragment and any args to bind.
func buildMetadataWhereClause(filters MetadataSearchFilters) (string, []interface{}) {
	var conditions []string
	var args []interface{}

	if filters.Agent != "" {
		// Use json_extract for efficient SQLite JSON querying
		conditions = append(conditions, `json_extract(metadata, '$.agent_id') = ?`)
		args = append(args, filters.Agent)
	}

	if filters.Channel != "" {
		conditions = append(conditions, `json_extract(metadata, '$.channel') = ?`)
		args = append(args, filters.Channel)
	}

	if filters.After != "" {
		conditions = append(conditions, `imported_at >= ?`)
		args = append(args, filters.After)
	}

	if filters.Before != "" {
		conditions = append(conditions, `imported_at < ?`)
		args = append(args, filters.Before+" 23:59:59")
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(conditions, " AND "), args
}

// SearchFTSWithMetadata searches FTS5 with additional metadata filters.
func (s *SQLiteStore) SearchFTSWithMetadata(ctx context.Context, query string, limit int, project string, filters MetadataSearchFilters) ([]*SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	metaClause, metaArgs := buildMetadataWhereClause(filters)

	var sqlQuery string
	var args []interface{}

	if project != "" {
		sqlQuery = fmt.Sprintf(`
			SELECT m.id, m.content, m.source_file, m.source_line, m.source_section,
			       m.content_hash, m.project, m.memory_class, m.metadata, m.imported_at, m.updated_at,
			       bm25(memories_fts) AS score
			FROM memories_fts f
			JOIN memories m ON m.id = f.rowid
			WHERE memories_fts MATCH ?
			  AND m.deleted_at IS NULL
			  AND m.project = ?
			  %s
			ORDER BY score
			LIMIT ?`, metaClause)
		args = append([]interface{}{query, project}, metaArgs...)
		args = append(args, limit)
	} else {
		sqlQuery = fmt.Sprintf(`
			SELECT m.id, m.content, m.source_file, m.source_line, m.source_section,
			       m.content_hash, m.project, m.memory_class, m.metadata, m.imported_at, m.updated_at,
			       bm25(memories_fts) AS score
			FROM memories_fts f
			JOIN memories m ON m.id = f.rowid
			WHERE memories_fts MATCH ?
			  AND m.deleted_at IS NULL
			  %s
			ORDER BY score
			LIMIT ?`, metaClause)
		args = append([]interface{}{query}, metaArgs...)
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		// Try OR fallback for multi-word queries
		if strings.Contains(query, " ") {
			orQuery := strings.Join(strings.Fields(query), " OR ")
			if project != "" {
				args = append([]interface{}{orQuery, project}, metaArgs...)
				args = append(args, limit)
			} else {
				args = append([]interface{}{orQuery}, metaArgs...)
				args = append(args, limit)
			}
			rows, err = s.db.QueryContext(ctx, sqlQuery, args...)
			if err != nil {
				return nil, fmt.Errorf("FTS query failed: %w", err)
			}
		} else {
			return nil, fmt.Errorf("FTS query failed: %w", err)
		}
	}
	defer rows.Close()

	return scanSearchResultsWithMetadata(rows)
}

// SearchEmbeddingWithMetadata searches by embedding similarity with metadata filters.
func (s *SQLiteStore) SearchEmbeddingWithMetadata(ctx context.Context, vector []float32, limit int, minSimilarity float64, project string, filters MetadataSearchFilters) ([]*SearchResult, error) {
	// Get all embeddings + filter in Go (same as current approach)
	results, err := s.SearchEmbeddingWithProject(ctx, vector, limit*3, minSimilarity, project) // overfetch
	if err != nil {
		return nil, err
	}

	// Apply metadata filters in-memory
	if filters.Agent == "" && filters.Channel == "" && filters.After == "" && filters.Before == "" {
		if len(results) > limit {
			return results[:limit], nil
		}
		return results, nil
	}

	var filtered []*SearchResult
	for _, r := range results {
		if matchesMetadataFilters(r.Memory, filters) {
			filtered = append(filtered, r)
			if len(filtered) >= limit {
				break
			}
		}
	}
	return filtered, nil
}

// matchesMetadataFilters checks if a memory matches the given metadata filters.
func matchesMetadataFilters(m Memory, f MetadataSearchFilters) bool {
	if f.Agent != "" {
		if m.Metadata == nil || (m.Metadata.AgentID != f.Agent && m.Metadata.AgentName != f.Agent) {
			return false
		}
	}
	if f.Channel != "" {
		if m.Metadata == nil || (m.Metadata.Channel != f.Channel && m.Metadata.ChannelName != f.Channel) {
			return false
		}
	}
	if f.After != "" {
		if m.ImportedAt.Format("2006-01-02") < f.After {
			return false
		}
	}
	if f.Before != "" {
		if m.ImportedAt.Format("2006-01-02") > f.Before {
			return false
		}
	}
	return true
}

// scanSearchResultsWithMetadata scans search results that include the metadata column.
func scanSearchResultsWithMetadata(rows *sql.Rows) ([]*SearchResult, error) {
	var results []*SearchResult
	for rows.Next() {
		var m Memory
		var deletedAt sql.NullTime
		var metadata sql.NullString
		var score float64

		err := rows.Scan(&m.ID, &m.Content, &m.SourceFile, &m.SourceLine, &m.SourceSection,
			&m.ContentHash, &m.Project, &m.MemoryClass, &metadata, &m.ImportedAt, &m.UpdatedAt, &score)
		if err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		_ = deletedAt // not scanned in search queries

		m.Metadata = unmarshalMetadata(metadata)
		results = append(results, &SearchResult{Memory: m, Score: score})
	}
	return results, rows.Err()
}
