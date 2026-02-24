package connect

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ConnectorStore provides CRUD operations for connector state.
// It operates on the same SQLite database as the main Cortex store.
type ConnectorStore struct {
	db *sql.DB
}

// NewConnectorStore wraps an existing *sql.DB for connector operations.
func NewConnectorStore(db *sql.DB) *ConnectorStore {
	return &ConnectorStore{db: db}
}

// Add registers a new connector. Returns the ID of the inserted row.
func (cs *ConnectorStore) Add(ctx context.Context, provider string, config json.RawMessage) (int64, error) {
	if provider == "" {
		return 0, fmt.Errorf("provider name cannot be empty")
	}
	if config == nil {
		config = json.RawMessage("{}")
	}

	result, err := cs.db.ExecContext(ctx,
		`INSERT INTO connectors (provider, config) VALUES (?, ?)`,
		provider, string(config),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("connector %q already exists", provider)
		}
		return 0, fmt.Errorf("adding connector: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting connector ID: %w", err)
	}
	return id, nil
}

// Get retrieves a connector by provider name.
func (cs *ConnectorStore) Get(ctx context.Context, provider string) (*Connector, error) {
	row := cs.db.QueryRowContext(ctx,
		`SELECT id, provider, config, enabled, last_sync_at, last_error,
		        records_imported, created_at, updated_at
		 FROM connectors WHERE provider = ?`, provider,
	)
	return scanConnector(row)
}

// GetByID retrieves a connector by ID.
func (cs *ConnectorStore) GetByID(ctx context.Context, id int64) (*Connector, error) {
	row := cs.db.QueryRowContext(ctx,
		`SELECT id, provider, config, enabled, last_sync_at, last_error,
		        records_imported, created_at, updated_at
		 FROM connectors WHERE id = ?`, id,
	)
	return scanConnector(row)
}

// List returns all connectors, optionally filtered by enabled state.
func (cs *ConnectorStore) List(ctx context.Context, enabledOnly bool) ([]*Connector, error) {
	query := `SELECT id, provider, config, enabled, last_sync_at, last_error,
	                  records_imported, created_at, updated_at
	           FROM connectors`
	if enabledOnly {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY provider"

	rows, err := cs.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing connectors: %w", err)
	}
	defer rows.Close()

	var connectors []*Connector
	for rows.Next() {
		c, err := scanConnectorRow(rows)
		if err != nil {
			return nil, err
		}
		connectors = append(connectors, c)
	}
	return connectors, rows.Err()
}

// UpdateConfig updates a connector's config.
func (cs *ConnectorStore) UpdateConfig(ctx context.Context, provider string, config json.RawMessage) error {
	result, err := cs.db.ExecContext(ctx,
		`UPDATE connectors SET config = ?, updated_at = CURRENT_TIMESTAMP WHERE provider = ?`,
		string(config), provider,
	)
	if err != nil {
		return fmt.Errorf("updating connector config: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("connector %q not found", provider)
	}
	return nil
}

// SetEnabled enables or disables a connector.
func (cs *ConnectorStore) SetEnabled(ctx context.Context, provider string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	result, err := cs.db.ExecContext(ctx,
		`UPDATE connectors SET enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE provider = ?`,
		val, provider,
	)
	if err != nil {
		return fmt.Errorf("setting connector enabled: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("connector %q not found", provider)
	}
	return nil
}

// RecordSyncSuccess updates the connector after a successful sync.
func (cs *ConnectorStore) RecordSyncSuccess(ctx context.Context, provider string, recordsImported int64) error {
	_, err := cs.db.ExecContext(ctx,
		`UPDATE connectors
		 SET last_sync_at = CURRENT_TIMESTAMP,
		     last_error = '',
		     records_imported = records_imported + ?,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE provider = ?`,
		recordsImported, provider,
	)
	return err
}

// RecordSyncError updates the connector after a failed sync.
func (cs *ConnectorStore) RecordSyncError(ctx context.Context, provider string, syncErr string) error {
	_, err := cs.db.ExecContext(ctx,
		`UPDATE connectors
		 SET last_sync_at = CURRENT_TIMESTAMP,
		     last_error = ?,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE provider = ?`,
		syncErr, provider,
	)
	return err
}

// Remove deletes a connector by provider name.
func (cs *ConnectorStore) Remove(ctx context.Context, provider string) error {
	result, err := cs.db.ExecContext(ctx,
		`DELETE FROM connectors WHERE provider = ?`, provider,
	)
	if err != nil {
		return fmt.Errorf("removing connector: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("connector %q not found", provider)
	}
	return nil
}

// scanConnector scans a single row into a Connector.
func scanConnector(row *sql.Row) (*Connector, error) {
	var c Connector
	var config string
	var enabled int
	var lastSyncAt sql.NullString
	var lastError sql.NullString

	err := row.Scan(
		&c.ID, &c.Provider, &config, &enabled,
		&lastSyncAt, &lastError, &c.RecordsImported,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("connector not found")
		}
		return nil, fmt.Errorf("scanning connector: %w", err)
	}

	c.Config = json.RawMessage(config)
	c.Enabled = enabled == 1
	if lastSyncAt.Valid {
		t, parseErr := time.ParseInLocation("2006-01-02 15:04:05", lastSyncAt.String, time.UTC)
		if parseErr != nil {
			t, _ = time.Parse(time.RFC3339, lastSyncAt.String)
		}
		c.LastSyncAt = &t
	}
	if lastError.Valid {
		c.LastError = lastError.String
	}
	return &c, nil
}

// scanConnectorRow scans from *sql.Rows (for List).
func scanConnectorRow(rows *sql.Rows) (*Connector, error) {
	var c Connector
	var config string
	var enabled int
	var lastSyncAt sql.NullString
	var lastError sql.NullString

	err := rows.Scan(
		&c.ID, &c.Provider, &config, &enabled,
		&lastSyncAt, &lastError, &c.RecordsImported,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning connector row: %w", err)
	}

	c.Config = json.RawMessage(config)
	c.Enabled = enabled == 1
	if lastSyncAt.Valid {
		t, parseErr := time.ParseInLocation("2006-01-02 15:04:05", lastSyncAt.String, time.UTC)
		if parseErr != nil {
			t, _ = time.Parse(time.RFC3339, lastSyncAt.String)
		}
		c.LastSyncAt = &t
	}
	if lastError.Valid {
		c.LastError = lastError.String
	}
	return &c, nil
}
