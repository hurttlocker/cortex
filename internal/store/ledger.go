package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Session ledger outcome values.
const (
	LedgerOutcomeSuccess = "success"
	LedgerOutcomePartial = "partial"
	LedgerOutcomeFailure = "failure"
)

// LedgerEntry is one append-only row in the session ledger — an agent's
// end-of-task outcome record (the implicit memory layer). Rows are never
// updated or deleted; a later milestone reads them to propose directives.
type LedgerEntry struct {
	ID           int64
	SessionID    string
	TaskSummary  string
	Outcome      string
	FilesTouched []string
	FixPattern   string
	AgentID      string
	Project      string
	CreatedAt    time.Time
}

func normalizeLedgerOutcome(outcome string) (string, error) {
	o := strings.ToLower(strings.TrimSpace(outcome))
	switch o {
	case LedgerOutcomeSuccess, LedgerOutcomePartial, LedgerOutcomeFailure:
		return o, nil
	default:
		return "", fmt.Errorf("invalid ledger outcome %q (valid: success, partial, failure)", outcome)
	}
}

// RecordLedgerEntry appends a new session ledger row. This is the only write
// path for session_ledger — there is intentionally no update or delete
// function anywhere in the store for this table.
func (s *SQLiteStore) RecordLedgerEntry(ctx context.Context, e *LedgerEntry) (int64, error) {
	if e == nil {
		return 0, fmt.Errorf("ledger entry is required")
	}
	summary := strings.TrimSpace(e.TaskSummary)
	if summary == "" {
		return 0, fmt.Errorf("task_summary is required")
	}
	outcome, err := normalizeLedgerOutcome(e.Outcome)
	if err != nil {
		return 0, err
	}

	filesJSON := "[]"
	if len(e.FilesTouched) > 0 {
		b, err := json.Marshal(e.FilesTouched)
		if err != nil {
			return 0, fmt.Errorf("marshaling files_touched: %w", err)
		}
		filesJSON = string(b)
	}

	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO session_ledger (session_id, task_summary, outcome, files_touched, fix_pattern, agent_id, project, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(e.SessionID), summary, outcome, filesJSON,
		strings.TrimSpace(e.FixPattern), strings.TrimSpace(e.AgentID), strings.TrimSpace(e.Project), now,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting session ledger entry: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}

	e.ID = id
	e.TaskSummary = summary
	e.Outcome = outcome
	e.SessionID = strings.TrimSpace(e.SessionID)
	e.FixPattern = strings.TrimSpace(e.FixPattern)
	e.AgentID = strings.TrimSpace(e.AgentID)
	e.Project = strings.TrimSpace(e.Project)
	e.CreatedAt = now
	return id, nil
}

// ListLedgerEntries returns session ledger rows created at or after since
// (zero value = no lower bound), optionally scoped to a project, newest
// first, capped at limit (default 100 when limit <= 0).
func (s *SQLiteStore) ListLedgerEntries(ctx context.Context, since time.Time, project string, limit int) ([]*LedgerEntry, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `SELECT id, session_id, task_summary, outcome, files_touched, fix_pattern, agent_id, project, created_at
	          FROM session_ledger`
	var where []string
	args := []interface{}{}

	if !since.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, since.UTC())
	}
	project = strings.TrimSpace(project)
	if project != "" {
		where = append(where, "project = ?")
		args = append(args, project)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing session ledger entries: %w", err)
	}
	defer rows.Close()

	return scanLedgerRows(rows)
}

// LedgerEntriesByPattern returns ledger rows within the trailing window that
// carry a non-empty fix_pattern — the raw material a later proposer scans for
// recurring fixes. Callers group/count by FixPattern themselves.
func (s *SQLiteStore) LedgerEntriesByPattern(ctx context.Context, window time.Duration) ([]*LedgerEntry, error) {
	if window <= 0 {
		return nil, fmt.Errorf("window must be positive")
	}
	since := time.Now().UTC().Add(-window)

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, task_summary, outcome, files_touched, fix_pattern, agent_id, project, created_at
		 FROM session_ledger
		 WHERE created_at >= ? AND fix_pattern != ''
		 ORDER BY created_at DESC`,
		since,
	)
	if err != nil {
		return nil, fmt.Errorf("listing ledger entries by pattern: %w", err)
	}
	defer rows.Close()

	return scanLedgerRows(rows)
}

func scanLedgerRows(rows *sql.Rows) ([]*LedgerEntry, error) {
	var entries []*LedgerEntry
	for rows.Next() {
		e := &LedgerEntry{}
		var filesJSON string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TaskSummary, &e.Outcome, &filesJSON,
			&e.FixPattern, &e.AgentID, &e.Project, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning session ledger row: %w", err)
		}
		if filesJSON != "" {
			_ = json.Unmarshal([]byte(filesJSON), &e.FilesTouched)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
