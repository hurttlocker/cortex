package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Directive lifecycle constants.
const (
	DirectiveStatusActive   = "active"
	DirectiveStatusArchived = "archived"

	// DirectiveScopeGlobal is the default scope — a directive that applies everywhere.
	DirectiveScopeGlobal = "global"
)

// Directive is an explicit, human-authored governance rule ("always X", "never Y").
// Unlike facts, directives never decay and are never matched by vector search —
// they are surfaced by exact/FTS retrieval and pinned above ranked results.
type Directive struct {
	ID        int64     `json:"id"`
	Rule      string    `json:"rule"`
	Scope     string    `json:"scope"`
	Status    string    `json:"status"`
	Pinned    bool      `json:"pinned"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DirectiveListOpts filters ListDirectives.
type DirectiveListOpts struct {
	// Status filters by lifecycle state: "active" (default when empty), "archived", or "all".
	Status string
	// Scope, when non-empty, restricts to directives with that exact scope.
	Scope string
}

// DirectiveUpdate carries optional edits for UpdateDirective. A nil field is left unchanged.
type DirectiveUpdate struct {
	Rule  *string
	Scope *string
}

func normalizeDirectiveScope(scope string) string {
	s := strings.TrimSpace(scope)
	if s == "" {
		return DirectiveScopeGlobal
	}
	return s
}

// AddDirective inserts a new active directive and records an audit event.
func (s *SQLiteStore) AddDirective(ctx context.Context, d *Directive) (int64, error) {
	if d == nil {
		return 0, fmt.Errorf("directive is required")
	}
	rule := strings.TrimSpace(d.Rule)
	if rule == "" {
		return 0, fmt.Errorf("directive rule cannot be empty")
	}
	scope := normalizeDirectiveScope(d.Scope)
	status := strings.TrimSpace(strings.ToLower(d.Status))
	if status == "" {
		status = DirectiveStatusActive
	}
	if status != DirectiveStatusActive && status != DirectiveStatusArchived {
		return 0, fmt.Errorf("invalid directive status %q (valid: active, archived)", d.Status)
	}
	// Directives are pinned by design (the whole point of the governance layer);
	// creation always pins. There is no unpinning surface in M1.
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO directives (rule, scope, status, pinned, author, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?)`,
		rule, scope, status, strings.TrimSpace(d.Author), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting directive: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting directive id: %w", err)
	}

	d.ID = id
	d.Rule = rule
	d.Scope = scope
	d.Status = status
	d.Pinned = true
	d.CreatedAt = now
	d.UpdatedAt = now

	_ = s.LogEvent(ctx, &MemoryEvent{
		EventType: "add",
		FactID:    id,
		NewValue:  fmt.Sprintf("directive scope:%s rule:%s", scope, truncate(rule, 200)),
		Source:    "directive:add",
	})

	return id, nil
}

// GetDirective returns a single directive by id, or (nil, nil) if not found.
func (s *SQLiteStore) GetDirective(ctx context.Context, id int64) (*Directive, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, rule, scope, status, pinned, author, created_at, updated_at
		 FROM directives WHERE id = ?`, id,
	)
	d, err := scanDirectiveRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting directive %d: %w", id, err)
	}
	return d, nil
}

// ListDirectives returns directives filtered by status/scope, newest first.
func (s *SQLiteStore) ListDirectives(ctx context.Context, opts DirectiveListOpts) ([]*Directive, error) {
	query := `SELECT id, rule, scope, status, pinned, author, created_at, updated_at FROM directives`
	var where []string
	var args []any

	status := strings.TrimSpace(strings.ToLower(opts.Status))
	switch status {
	case "", DirectiveStatusActive:
		where = append(where, "status = ?")
		args = append(args, DirectiveStatusActive)
	case DirectiveStatusArchived:
		where = append(where, "status = ?")
		args = append(args, DirectiveStatusArchived)
	case "all":
		// no status filter
	default:
		return nil, fmt.Errorf("invalid status filter %q (valid: active, archived, all)", opts.Status)
	}

	if scope := strings.TrimSpace(opts.Scope); scope != "" {
		where = append(where, "scope = ?")
		args = append(args, scope)
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing directives: %w", err)
	}
	defer rows.Close()

	var out []*Directive
	for rows.Next() {
		d, err := scanDirectiveRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning directive row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActiveDirectives returns active directives relevant to the given scope, used by
// retrieval pinning. Global directives always apply; a non-empty, non-global scope
// additionally pulls in directives authored for that exact scope.
func (s *SQLiteStore) ActiveDirectives(ctx context.Context, scope string) ([]*Directive, error) {
	scope = strings.TrimSpace(scope)

	query := `SELECT id, rule, scope, status, pinned, author, created_at, updated_at
	          FROM directives WHERE status = ?`
	args := []any{DirectiveStatusActive}

	if scope != "" && scope != DirectiveScopeGlobal {
		query += " AND scope IN (?, ?)"
		args = append(args, DirectiveScopeGlobal, scope)
	} else {
		query += " AND scope = ?"
		args = append(args, DirectiveScopeGlobal)
	}
	// Global rules first, then scope-specific, stable by id.
	query += " ORDER BY (scope = 'global') DESC, id ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing active directives: %w", err)
	}
	defer rows.Close()

	var out []*Directive
	for rows.Next() {
		d, err := scanDirectiveRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning active directive row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDirective edits a directive's rule and/or scope and records an audit event.
func (s *SQLiteStore) UpdateDirective(ctx context.Context, id int64, upd DirectiveUpdate) error {
	if upd.Rule == nil && upd.Scope == nil {
		return fmt.Errorf("nothing to update (provide rule and/or scope)")
	}

	existing, err := s.GetDirective(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("directive %d not found", id)
	}

	var sets []string
	var args []any
	newRule := existing.Rule
	newScope := existing.Scope
	if upd.Rule != nil {
		newRule = strings.TrimSpace(*upd.Rule)
		if newRule == "" {
			return fmt.Errorf("directive rule cannot be empty")
		}
		sets = append(sets, "rule = ?")
		args = append(args, newRule)
	}
	if upd.Scope != nil {
		newScope = normalizeDirectiveScope(*upd.Scope)
		sets = append(sets, "scope = ?")
		args = append(args, newScope)
	}
	now := time.Now().UTC()
	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	args = append(args, id)

	result, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE directives SET %s WHERE id = ?", strings.Join(sets, ", ")),
		args...,
	)
	if err != nil {
		return fmt.Errorf("updating directive %d: %w", id, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("directive %d not found", id)
	}

	_ = s.LogEvent(ctx, &MemoryEvent{
		EventType: "update",
		FactID:    id,
		OldValue:  fmt.Sprintf("scope:%s rule:%s", existing.Scope, truncate(existing.Rule, 200)),
		NewValue:  fmt.Sprintf("scope:%s rule:%s", newScope, truncate(newRule, 200)),
		Source:    "directive:edit",
	})

	return nil
}

// ArchiveDirective marks a directive archived so it drops out of active retrieval.
// The row is preserved for audit; use DeleteDirective for a hard delete.
func (s *SQLiteStore) ArchiveDirective(ctx context.Context, id int64) error {
	existing, err := s.GetDirective(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("directive %d not found", id)
	}
	if existing.Status == DirectiveStatusArchived {
		return nil // already archived — idempotent
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE directives SET status = ?, updated_at = ? WHERE id = ?`,
		DirectiveStatusArchived, now, id,
	); err != nil {
		return fmt.Errorf("archiving directive %d: %w", id, err)
	}

	_ = s.LogEvent(ctx, &MemoryEvent{
		EventType: "update",
		FactID:    id,
		OldValue:  fmt.Sprintf("status:%s", existing.Status),
		NewValue:  "status:archived",
		Source:    "directive:archive",
	})

	return nil
}

// DeleteDirective hard-deletes a directive and records an audit event.
func (s *SQLiteStore) DeleteDirective(ctx context.Context, id int64) error {
	existing, err := s.GetDirective(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("directive %d not found", id)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM directives WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting directive %d: %w", id, err)
	}

	_ = s.LogEvent(ctx, &MemoryEvent{
		EventType: "delete",
		FactID:    id,
		OldValue:  fmt.Sprintf("scope:%s rule:%s", existing.Scope, truncate(existing.Rule, 200)),
		Source:    "directive:rm",
	})

	return nil
}

// scanDirectiveRow scans one directive from *sql.Row or *sql.Rows.
func scanDirectiveRow(sc interface{ Scan(...any) error }) (*Directive, error) {
	d := &Directive{}
	var pinned int
	var author sql.NullString
	if err := sc.Scan(&d.ID, &d.Rule, &d.Scope, &d.Status, &pinned, &author, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.Pinned = pinned != 0
	d.Author = author.String
	return d, nil
}
