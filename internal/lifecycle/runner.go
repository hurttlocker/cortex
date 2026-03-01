package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"

	cfgresolver "github.com/hurttlocker/cortex/internal/config"
	"github.com/hurttlocker/cortex/internal/store"
)

type Action struct {
	Policy    string `json:"policy"`
	Action    string `json:"action"`
	FactID    int64  `json:"fact_id,omitempty"`
	WinnerID  int64  `json:"winner_id,omitempty"`
	LoserID   int64  `json:"loser_id,omitempty"`
	FromState string `json:"from_state,omitempty"`
	ToState   string `json:"to_state,omitempty"`
	Reason    string `json:"reason"`
	Applied   bool   `json:"applied"`
}

type Report struct {
	DryRun     bool     `json:"dry_run"`
	Scanned    int      `json:"scanned"`
	Applied    int      `json:"applied"`
	Actions    []Action `json:"actions"`
	PolicyRuns struct {
		ReinforcePromote  int `json:"reinforce_promote"`
		DecayRetire       int `json:"decay_retire"`
		ConflictSupersede int `json:"conflict_supersede"`
	} `json:"policy_runs"`
}

type Runner struct {
	st       store.Store
	sqlite   *store.SQLiteStore
	policies cfgresolver.PolicyConfig
	now      time.Time
}

func NewRunner(st store.Store, policies cfgresolver.PolicyConfig) (*Runner, error) {
	sqlite, ok := st.(*store.SQLiteStore)
	if !ok {
		return nil, fmt.Errorf("lifecycle runner requires sqlite store")
	}
	return &Runner{st: st, sqlite: sqlite, policies: policies, now: time.Now().UTC()}, nil
}

func (r *Runner) Run(ctx context.Context, dryRun bool) (*Report, error) {
	report := &Report{DryRun: dryRun, Actions: make([]Action, 0, 64)}

	if err := ValidatePolicyTargets(r.policies); err != nil {
		return nil, err
	}

	if r.policies.ReinforcePromote.Enabled {
		actions, scanned, err := r.applyReinforcePromote(ctx, dryRun)
		if err != nil {
			return nil, err
		}
		report.Scanned += scanned
		report.PolicyRuns.ReinforcePromote = len(actions)
		report.Actions = append(report.Actions, actions...)
	}

	if r.policies.DecayRetire.Enabled {
		actions, scanned, err := r.applyDecayRetire(ctx, dryRun)
		if err != nil {
			return nil, err
		}
		report.Scanned += scanned
		report.PolicyRuns.DecayRetire = len(actions)
		report.Actions = append(report.Actions, actions...)
	}

	if r.policies.ConflictSupersede.Enabled {
		actions, scanned, err := r.applyConflictSupersede(ctx, dryRun)
		if err != nil {
			return nil, err
		}
		report.Scanned += scanned
		report.PolicyRuns.ConflictSupersede = len(actions)
		report.Actions = append(report.Actions, actions...)
	}

	for _, a := range report.Actions {
		if a.Applied {
			report.Applied++
		}
	}
	return report, nil
}

func (r *Runner) applyReinforcePromote(ctx context.Context, dryRun bool) ([]Action, int, error) {
	cfg := r.policies.ReinforcePromote
	actions := []Action{}
	rows, err := r.sqlite.GetDB().QueryContext(ctx, `
		WITH source_counts AS (
			SELECT LOWER(subject) AS lsub, LOWER(predicate) AS lpred, LOWER(object) AS lobj,
			       COUNT(DISTINCT memory_id) AS source_count
			FROM facts
			WHERE superseded_by IS NULL
			GROUP BY LOWER(subject), LOWER(predicate), LOWER(object)
		)
		SELECT f.id, f.state,
		       COALESCE((SELECT COUNT(*) FROM fact_accesses_v1 a
		                 WHERE a.fact_id = f.id AND a.access_type IN ('reinforce','reference','import')), 0) AS reinforcement_count,
		       COALESCE(sc.source_count, 0) AS source_count
		FROM facts f
		LEFT JOIN source_counts sc
		  ON LOWER(f.subject) = sc.lsub
		 AND LOWER(f.predicate) = sc.lpred
		 AND LOWER(f.object) = sc.lobj
		WHERE f.superseded_by IS NULL AND LOWER(f.state) = 'active'`)
	if err != nil {
		return nil, 0, fmt.Errorf("query reinforce-promote candidates: %w", err)
	}
	scanned := 0
	for rows.Next() {
		var factID int64
		var state string
		var reinforcementCount, sourceCount int
		if err := rows.Scan(&factID, &state, &reinforcementCount, &sourceCount); err != nil {
			return nil, scanned, fmt.Errorf("scan reinforce-promote row: %w", err)
		}
		scanned++
		if reinforcementCount < cfg.MinReinforcements || sourceCount < cfg.MinSources {
			continue
		}
		act := Action{
			Policy:    "reinforce-promote",
			Action:    "state_transition",
			FactID:    factID,
			FromState: state,
			ToState:   cfg.TargetState,
			Reason:    fmt.Sprintf("reinforcements=%d >= %d and sources=%d >= %d", reinforcementCount, cfg.MinReinforcements, sourceCount, cfg.MinSources),
			Applied:   false,
		}
		actions = append(actions, act)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, scanned, err
	}
	if err := rows.Close(); err != nil {
		return nil, scanned, fmt.Errorf("close reinforce-promote rows: %w", err)
	}
	if !dryRun {
		for i := range actions {
			if err := r.st.UpdateFactState(ctx, actions[i].FactID, cfg.TargetState); err != nil {
				actions[i].Reason += "; apply_error: " + err.Error()
			} else {
				actions[i].Applied = true
			}
		}
	}
	return actions, scanned, nil
}

func (r *Runner) applyDecayRetire(ctx context.Context, dryRun bool) ([]Action, int, error) {
	cfg := r.policies.DecayRetire
	actions := []Action{}
	rows, err := r.sqlite.GetDB().QueryContext(ctx, `
		SELECT f.id, f.state, f.confidence,
		       COALESCE(MAX(a.created_at), f.last_reinforced) AS last_access
		FROM facts f
		LEFT JOIN fact_accesses_v1 a ON a.fact_id = f.id
		WHERE f.superseded_by IS NULL
		  AND LOWER(f.state) IN ('active','core')
		GROUP BY f.id, f.state, f.confidence, f.last_reinforced`)
	if err != nil {
		return nil, 0, fmt.Errorf("query decay-retire candidates: %w", err)
	}
	scanned := 0
	for rows.Next() {
		var factID int64
		var state string
		var confidence float64
		var lastAccessRaw string
		if err := rows.Scan(&factID, &state, &confidence, &lastAccessRaw); err != nil {
			return nil, scanned, fmt.Errorf("scan decay-retire row: %w", err)
		}
		lastAccess, err := parseSQLiteTime(lastAccessRaw)
		if err != nil {
			return nil, scanned, fmt.Errorf("parse decay-retire last_access %q: %w", lastAccessRaw, err)
		}
		scanned++
		days := int(r.now.Sub(lastAccess).Hours() / 24)
		if days < cfg.InactiveDays || confidence >= cfg.ConfidenceBelow {
			continue
		}
		act := Action{
			Policy:    "decay-retire",
			Action:    "state_transition",
			FactID:    factID,
			FromState: state,
			ToState:   cfg.TargetState,
			Reason:    fmt.Sprintf("inactive_days=%d >= %d and confidence=%.3f < %.3f", days, cfg.InactiveDays, confidence, cfg.ConfidenceBelow),
			Applied:   false,
		}
		actions = append(actions, act)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, scanned, err
	}
	if err := rows.Close(); err != nil {
		return nil, scanned, fmt.Errorf("close decay-retire rows: %w", err)
	}
	if !dryRun {
		for i := range actions {
			if err := r.st.UpdateFactState(ctx, actions[i].FactID, cfg.TargetState); err != nil {
				actions[i].Reason += "; apply_error: " + err.Error()
			} else {
				actions[i].Applied = true
			}
		}
	}
	return actions, scanned, nil
}

func (r *Runner) applyConflictSupersede(ctx context.Context, dryRun bool) ([]Action, int, error) {
	cfg := r.policies.ConflictSupersede
	conflicts, err := r.st.GetAttributeConflictsLimitWithSuperseded(ctx, 1000, false)
	if err != nil {
		return nil, 0, fmt.Errorf("get attribute conflicts: %w", err)
	}

	actions := make([]Action, 0)
	supersededLosers := map[int64]struct{}{}
	for _, c := range conflicts {
		f1 := c.Fact1
		f2 := c.Fact2
		confDelta := f1.Confidence - f2.Confidence
		if confDelta < 0 {
			confDelta = -confDelta
		}
		if confDelta < cfg.MinConfidenceDelta {
			continue
		}

		winner := f1
		loser := f2
		if f2.Confidence > f1.Confidence {
			winner = f2
			loser = f1
		}
		if cfg.RequireStrictlyNewer && !winner.CreatedAt.After(loser.CreatedAt) {
			continue
		}
		if _, exists := supersededLosers[loser.ID]; exists {
			continue
		}

		act := Action{
			Policy:   "conflict-supersede",
			Action:   "supersede",
			WinnerID: winner.ID,
			LoserID:  loser.ID,
			Reason:   fmt.Sprintf("winner confidence %.3f > %.3f by %.3f%s", winner.Confidence, loser.Confidence, confDelta, newerSuffix(cfg.RequireStrictlyNewer)),
			Applied:  false,
		}
		if !dryRun {
			if err := r.st.SupersedeFact(ctx, loser.ID, winner.ID, "lifecycle conflict-supersede"); err != nil {
				act.Reason += "; apply_error: " + err.Error()
			} else {
				act.Applied = true
				supersededLosers[loser.ID] = struct{}{}
			}
		}
		actions = append(actions, act)
	}
	return actions, len(conflicts), nil
}

func newerSuffix(required bool) string {
	if required {
		return " and newer timestamp"
	}
	return ""
}

func ValidatePolicyTargets(p cfgresolver.PolicyConfig) error {
	for _, target := range []string{p.ReinforcePromote.TargetState, p.DecayRetire.TargetState} {
		s := strings.ToLower(strings.TrimSpace(target))
		if s == "" {
			continue
		}
		if s != store.FactStateActive && s != store.FactStateCore && s != store.FactStateRetired {
			return fmt.Errorf("invalid policy target_state %q (valid: active, core, retired)", target)
		}
	}
	return nil
}

func parseSQLiteTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format")
}
