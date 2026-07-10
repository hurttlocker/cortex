package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Directive proposal lifecycle constants.
const (
	ProposalStatusPending   = "pending"
	ProposalStatusAccepted  = "accepted"
	ProposalStatusDismissed = "dismissed"

	// DefaultProposalMinOccurrences is the recurrence threshold a fix pattern
	// must reach within the scan window before it becomes a candidate proposal.
	DefaultProposalMinOccurrences = 3

	// proposalAuthor tags directives created by accepting a proposal so the
	// provenance ("this rule came from the proposer") is visible in the audit trail.
	proposalAuthor = "cortex-proposer"
)

// DirectiveProposal is a candidate directive the proposer derived from recurring
// session-ledger fix patterns. It is inert until a human accepts it: acceptance
// is the ONLY path from a proposal to a directives row. A bad proposal costs a
// dismiss, never a bad write.
type DirectiveProposal struct {
	ID            int64     `json:"id"`
	CandidateRule string    `json:"candidate_rule"`
	PatternKey    string    `json:"pattern_key"`
	Occurrences   int       `json:"occurrences"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
	// Evidence holds the contributing session_ledger row ids.
	Evidence []int64 `json:"evidence"`
	Status   string  `json:"status"`
	// CreatedDirectiveID is set once the proposal is accepted (nil otherwise).
	CreatedDirectiveID *int64     `json:"created_directive_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
}

// ProposalListOpts filters ListProposals.
type ProposalListOpts struct {
	// Status filters by lifecycle state: "pending" (default when empty),
	// "accepted", "dismissed", or "all".
	Status string
}

// CreateProposal inserts a new pending proposal. Callers (the scan) are
// responsible for dedupe; this is a straight insert.
func (s *SQLiteStore) CreateProposal(ctx context.Context, p *DirectiveProposal) (int64, error) {
	if p == nil {
		return 0, fmt.Errorf("proposal is required")
	}
	rule := strings.TrimSpace(p.CandidateRule)
	if rule == "" {
		return 0, fmt.Errorf("candidate_rule cannot be empty")
	}
	patternKey := strings.TrimSpace(p.PatternKey)
	if patternKey == "" {
		return 0, fmt.Errorf("pattern_key cannot be empty")
	}
	status := strings.TrimSpace(strings.ToLower(p.Status))
	if status == "" {
		status = ProposalStatusPending
	}
	if status != ProposalStatusPending && status != ProposalStatusAccepted && status != ProposalStatusDismissed {
		return 0, fmt.Errorf("invalid proposal status %q", p.Status)
	}

	evidenceJSON := "[]"
	if len(p.Evidence) > 0 {
		b, err := json.Marshal(p.Evidence)
		if err != nil {
			return 0, fmt.Errorf("marshaling evidence: %w", err)
		}
		evidenceJSON = string(b)
	}

	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO directive_proposals
		   (candidate_rule, pattern_key, occurrences, window_start, window_end, evidence, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rule, patternKey, p.Occurrences, p.WindowStart.UTC(), p.WindowEnd.UTC(), evidenceJSON, status, now,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting directive proposal: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting proposal id: %w", err)
	}

	p.ID = id
	p.CandidateRule = rule
	p.PatternKey = patternKey
	p.Status = status
	p.CreatedAt = now
	return id, nil
}

// GetProposal returns a single proposal by id, or (nil, nil) if not found.
func (s *SQLiteStore) GetProposal(ctx context.Context, id int64) (*DirectiveProposal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, candidate_rule, pattern_key, occurrences, window_start, window_end,
		        evidence, status, created_directive_id, created_at, resolved_at
		 FROM directive_proposals WHERE id = ?`, id,
	)
	p, err := scanProposalRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting proposal %d: %w", id, err)
	}
	return p, nil
}

// ListProposals returns proposals filtered by status, newest first.
func (s *SQLiteStore) ListProposals(ctx context.Context, opts ProposalListOpts) ([]*DirectiveProposal, error) {
	query := `SELECT id, candidate_rule, pattern_key, occurrences, window_start, window_end,
	                 evidence, status, created_directive_id, created_at, resolved_at
	          FROM directive_proposals`
	var args []any

	status := strings.TrimSpace(strings.ToLower(opts.Status))
	switch status {
	case "", ProposalStatusPending:
		query += " WHERE status = ?"
		args = append(args, ProposalStatusPending)
	case ProposalStatusAccepted, ProposalStatusDismissed:
		query += " WHERE status = ?"
		args = append(args, status)
	case "all":
		// no status filter
	default:
		return nil, fmt.Errorf("invalid status filter %q (valid: pending, accepted, dismissed, all)", opts.Status)
	}
	query += " ORDER BY created_at DESC, id DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing proposals: %w", err)
	}
	defer rows.Close()

	var out []*DirectiveProposal
	for rows.Next() {
		p, err := scanProposalRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning proposal row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AcceptProposal is the ONLY path from a proposal to a directive. It creates a
// directive from the proposal's frozen candidate_rule (via the existing
// AddDirective store API, so the new directive inherits pinning + FTS + audit),
// then marks the proposal accepted with the created directive id and a resolved
// timestamp. Only pending proposals can be accepted.
//
// AddDirective + the guarded UPDATE are ordered so a re-run or concurrent accept
// can't double-resolve the same proposal: the UPDATE only fires WHERE status is
// still 'pending'. (Accept is CLI-only and single-caller in practice — never
// exposed over MCP — so this ordering is belt-and-suspenders, not load-bearing.)
func (s *SQLiteStore) AcceptProposal(ctx context.Context, id int64) (int64, error) {
	p, err := s.GetProposal(ctx, id)
	if err != nil {
		return 0, err
	}
	if p == nil {
		return 0, fmt.Errorf("proposal %d not found", id)
	}
	if p.Status != ProposalStatusPending {
		return 0, fmt.Errorf("proposal %d is %s; only pending proposals can be accepted", id, p.Status)
	}

	directiveID, err := s.AddDirective(ctx, &Directive{Rule: p.CandidateRule, Author: proposalAuthor})
	if err != nil {
		return 0, fmt.Errorf("creating directive from proposal %d: %w", id, err)
	}

	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE directive_proposals
		   SET status = 'accepted', created_directive_id = ?, resolved_at = ?
		 WHERE id = ? AND status = 'pending'`,
		directiveID, now, id,
	)
	if err != nil {
		return 0, fmt.Errorf("marking proposal %d accepted: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, fmt.Errorf("proposal %d was resolved concurrently", id)
	}

	return directiveID, nil
}

// DismissProposal marks a pending proposal dismissed. Dismissing writes no
// directive — a bad proposal costs exactly this. Only pending proposals can be
// dismissed.
func (s *SQLiteStore) DismissProposal(ctx context.Context, id int64) error {
	p, err := s.GetProposal(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("proposal %d not found", id)
	}
	if p.Status != ProposalStatusPending {
		return fmt.Errorf("proposal %d is %s; only pending proposals can be dismissed", id, p.Status)
	}

	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE directive_proposals SET status = 'dismissed', resolved_at = ? WHERE id = ? AND status = 'pending'`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("dismissing proposal %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("proposal %d was resolved concurrently", id)
	}
	return nil
}

// ScanOptions configures a proposer scan.
type ScanOptions struct {
	// Window is the trailing time span of ledger rows to consider.
	Window time.Duration
	// MinOccurrences is the recurrence threshold (defaults to
	// DefaultProposalMinOccurrences when <= 0).
	MinOccurrences int
	// DryRun computes candidates but persists NOTHING.
	DryRun bool
}

// ScanResult reports what a scan found.
type ScanResult struct {
	Window         time.Duration
	MinOccurrences int
	DryRun         bool
	// Candidates are every distinct fix pattern that met the threshold this scan,
	// in descending occurrence order. IDs are 0 for dry runs and for candidates
	// suppressed by dedupe.
	Candidates []*DirectiveProposal
	// Created holds the proposals actually persisted this scan (empty on dry run).
	Created []*DirectiveProposal
	// SkippedExisting lists pattern keys suppressed because an overlapping
	// proposal already exists for them.
	SkippedExisting []string
}

// ScanForProposals is the proposer loop's core. It groups session-ledger rows in
// the trailing window by EXACT fix_pattern string — deterministic, no LLM, no
// fuzzy matching, no network — and turns patterns that recur at least
// MinOccurrences times into PENDING proposals.
//
// GOVERNANCE INVARIANT: this method's only write is CreateProposal. It NEVER
// writes a directive. The path scan → directive does not exist; only
// AcceptProposal crosses that boundary.
//
// candidate_rule derivation (mechanical, frozen at scan time): the fix-pattern
// text prefixed with "Recurring fix pattern: ". The count + window live in the
// structured columns (surfaced by `propose list`), not baked into the rule, so
// an accepted directive reads cleanly and can be edited afterward.
//
// Re-proposal / dedupe rule: a candidate is suppressed when ANY existing
// proposal (pending, accepted, or dismissed) for the same pattern_key has a
// stored [window_start, window_end] that OVERLAPS the candidate's window. The
// candidate window is [earliest, latest] created_at among the contributing rows.
// Consequence: a repeat scan over the same evidence never duplicates a proposal
// (identical window overlaps itself), while a later scan whose evidence has fully
// rolled forward past the old window MAY re-propose. A dismissed proposal
// therefore blocks re-proposal for its own evidence window but not forever.
func (s *SQLiteStore) ScanForProposals(ctx context.Context, opts ScanOptions) (*ScanResult, error) {
	window := opts.Window
	if window <= 0 {
		return nil, fmt.Errorf("window must be positive")
	}
	minOccur := opts.MinOccurrences
	if minOccur <= 0 {
		minOccur = DefaultProposalMinOccurrences
	}

	entries, err := s.LedgerEntriesByPattern(ctx, window)
	if err != nil {
		return nil, fmt.Errorf("reading ledger patterns: %w", err)
	}

	// Group by exact fix_pattern. Preserve first-seen order for stable output.
	type group struct {
		pattern   string
		ids       []int64
		minAt     time.Time
		maxAt     time.Time
		count     int
		seenOrder int
	}
	groups := make(map[string]*group)
	var order []string
	for _, e := range entries {
		pattern := e.FixPattern // exact string; LedgerEntriesByPattern already excludes empties
		g, ok := groups[pattern]
		if !ok {
			g = &group{pattern: pattern, minAt: e.CreatedAt, maxAt: e.CreatedAt, seenOrder: len(order)}
			groups[pattern] = g
			order = append(order, pattern)
		}
		g.ids = append(g.ids, e.ID)
		g.count++
		if e.CreatedAt.Before(g.minAt) {
			g.minAt = e.CreatedAt
		}
		if e.CreatedAt.After(g.maxAt) {
			g.maxAt = e.CreatedAt
		}
	}

	result := &ScanResult{
		Window:         window,
		MinOccurrences: minOccur,
		DryRun:         opts.DryRun,
	}

	for _, pattern := range order {
		g := groups[pattern]
		if g.count < minOccur {
			continue
		}

		candidate := &DirectiveProposal{
			CandidateRule: deriveCandidateRule(pattern),
			PatternKey:    pattern,
			Occurrences:   g.count,
			WindowStart:   g.minAt,
			WindowEnd:     g.maxAt,
			Evidence:      g.ids,
			Status:        ProposalStatusPending,
		}
		result.Candidates = append(result.Candidates, candidate)

		if opts.DryRun {
			continue
		}

		overlaps, err := s.hasOverlappingProposal(ctx, pattern, g.minAt, g.maxAt)
		if err != nil {
			return nil, err
		}
		if overlaps {
			result.SkippedExisting = append(result.SkippedExisting, pattern)
			continue
		}

		if _, err := s.CreateProposal(ctx, candidate); err != nil {
			return nil, fmt.Errorf("creating proposal for pattern %q: %w", pattern, err)
		}
		result.Created = append(result.Created, candidate)
	}

	return result, nil
}

// deriveCandidateRule is the single mechanical mapping from a recurring fix
// pattern to a candidate directive rule. Kept deliberately dumb and honest.
func deriveCandidateRule(fixPattern string) string {
	return "Recurring fix pattern: " + strings.TrimSpace(fixPattern)
}

// hasOverlappingProposal reports whether any proposal (any status) already exists
// for pattern_key whose stored window overlaps [start, end]. See the dedupe rule
// documented on ScanForProposals.
func (s *SQLiteStore) hasOverlappingProposal(ctx context.Context, patternKey string, start, end time.Time) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM directive_proposals
		 WHERE pattern_key = ?
		   AND window_start <= ?
		   AND window_end   >= ?`,
		patternKey, end.UTC(), start.UTC(),
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking existing proposals for pattern %q: %w", patternKey, err)
	}
	return count > 0, nil
}

// scanProposalRow scans one proposal from *sql.Row or *sql.Rows.
func scanProposalRow(sc interface{ Scan(...any) error }) (*DirectiveProposal, error) {
	p := &DirectiveProposal{}
	var evidenceJSON string
	var windowStart, windowEnd, resolvedAt sql.NullTime
	var createdDirectiveID sql.NullInt64
	if err := sc.Scan(
		&p.ID, &p.CandidateRule, &p.PatternKey, &p.Occurrences,
		&windowStart, &windowEnd, &evidenceJSON, &p.Status,
		&createdDirectiveID, &p.CreatedAt, &resolvedAt,
	); err != nil {
		return nil, err
	}
	if windowStart.Valid {
		p.WindowStart = windowStart.Time
	}
	if windowEnd.Valid {
		p.WindowEnd = windowEnd.Time
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		p.ResolvedAt = &t
	}
	if createdDirectiveID.Valid {
		v := createdDirectiveID.Int64
		p.CreatedDirectiveID = &v
	}
	if evidenceJSON != "" {
		_ = json.Unmarshal([]byte(evidenceJSON), &p.Evidence)
	}
	return p, nil
}
