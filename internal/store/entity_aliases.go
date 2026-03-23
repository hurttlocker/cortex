package store

import (
	"context"
	"fmt"
	"strings"
)

func (s *SQLiteStore) ListEntityAliases(ctx context.Context, entityID int64) ([]EntityAlias, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, entity_id, alias, source, created_at
		FROM entity_aliases
		WHERE entity_id = ?
		ORDER BY LOWER(alias), id
	`, entityID)
	if err != nil {
		return nil, fmt.Errorf("listing entity aliases: %w", err)
	}
	defer rows.Close()

	var aliases []EntityAlias
	for rows.Next() {
		var alias EntityAlias
		if err := rows.Scan(&alias.ID, &alias.EntityID, &alias.Alias, &alias.Source, &alias.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning entity alias: %w", err)
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

func (s *SQLiteStore) upsertEntityAlias(ctx context.Context, entityID int64, alias string, source string) error {
	alias = normalizeEntityName(alias)
	if entityID <= 0 || alias == "" {
		return nil
	}
	if source == "" {
		source = "extracted"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO entity_aliases (entity_id, alias, source)
		VALUES (?, ?, ?)
	`, entityID, alias, strings.ToLower(strings.TrimSpace(source)))
	if err != nil {
		return fmt.Errorf("upserting entity alias %q: %w", alias, err)
	}
	return nil
}
