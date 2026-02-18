package observe

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

// ============================================================================
// Resolution Strategies
// ============================================================================

// Strategy defines how a conflict should be resolved.
type Strategy string

const (
	// StrategyLastWrite keeps the most recently created fact, drops the older one.
	StrategyLastWrite Strategy = "last-write-wins"

	// StrategyHighestConfidence keeps the fact with highest effective confidence.
	StrategyHighestConfidence Strategy = "highest-confidence"

	// StrategyNewest keeps the fact from the most recently imported memory.
	StrategyNewest Strategy = "newest"

	// StrategyManual flags the conflict for human review (no auto-resolution).
	StrategyManual Strategy = "manual"
)

// ParseStrategy converts a string to a Strategy, returning an error for unknown values.
func ParseStrategy(s string) (Strategy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "last-write-wins", "last-write", "lww":
		return StrategyLastWrite, nil
	case "highest-confidence", "confidence", "hcw":
		return StrategyHighestConfidence, nil
	case "newest":
		return StrategyNewest, nil
	case "manual", "review":
		return StrategyManual, nil
	default:
		return "", fmt.Errorf("unknown strategy %q (use: last-write-wins, highest-confidence, newest, manual)", s)
	}
}

// ============================================================================
// Resolution Result
// ============================================================================

// Resolution describes the outcome of resolving one conflict.
type Resolution struct {
	Conflict Conflict `json:"conflict"`
	Strategy Strategy `json:"strategy"`
	Winner   string   `json:"winner"`    // "fact1", "fact2", or "manual"
	WinnerID int64    `json:"winner_id"` // ID of the winning fact
	LoserID  int64    `json:"loser_id"`  // ID of the losing fact (0 if manual)
	Reason   string   `json:"reason"`    // Human-readable explanation
	Applied  bool     `json:"applied"`   // Whether the resolution was applied to the DB
}

// ResolveBatch holds the results of resolving multiple conflicts.
type ResolveBatch struct {
	Total    int          `json:"total"`
	Resolved int          `json:"resolved"`
	Skipped  int          `json:"skipped"` // manual strategy → not auto-resolved
	Errors   int          `json:"errors"`
	Results  []Resolution `json:"results"`
}

// ============================================================================
// Resolver
// ============================================================================

// Resolver handles conflict detection and resolution.
type Resolver struct {
	store  store.Store
	engine *Engine
}

// NewResolver creates a new conflict resolver.
func NewResolver(s store.Store, engine *Engine) *Resolver {
	return &Resolver{store: s, engine: engine}
}

// DetectAndResolve finds all conflicts and resolves them with the given strategy.
// If dryRun is true, resolutions are computed but not applied.
func (r *Resolver) DetectAndResolve(ctx context.Context, strategy Strategy, dryRun bool) (*ResolveBatch, error) {
	conflicts, err := r.engine.GetConflicts(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting conflicts: %w", err)
	}

	return r.ResolveConflicts(ctx, conflicts, strategy, dryRun)
}

// ResolveConflicts applies a resolution strategy to a set of conflicts.
func (r *Resolver) ResolveConflicts(ctx context.Context, conflicts []Conflict, strategy Strategy, dryRun bool) (*ResolveBatch, error) {
	batch := &ResolveBatch{
		Total:   len(conflicts),
		Results: make([]Resolution, 0, len(conflicts)),
	}

	for _, c := range conflicts {
		resolution := r.resolveOne(c, strategy)

		if !dryRun && resolution.Winner != "manual" && resolution.LoserID > 0 {
			// Apply tombstone semantics: old conflicting fact is superseded (not deleted).
			err := r.store.SupersedeFact(ctx, resolution.LoserID, resolution.WinnerID, fmt.Sprintf("strategy:%s", strategy))
			if err != nil {
				batch.Errors++
				resolution.Reason += fmt.Sprintf(" (apply error: %v)", err)
			} else {
				resolution.Applied = true

				// Reinforce the winner
				_ = r.store.ReinforceFact(ctx, resolution.WinnerID)
			}
			batch.Resolved++
		} else if resolution.Winner == "manual" {
			batch.Skipped++
		} else {
			batch.Resolved++
		}

		batch.Results = append(batch.Results, resolution)
	}

	return batch, nil
}

// resolveOne picks a winner for a single conflict based on strategy.
func (r *Resolver) resolveOne(c Conflict, strategy Strategy) Resolution {
	res := Resolution{
		Conflict: c,
		Strategy: strategy,
	}

	switch strategy {
	case StrategyLastWrite:
		res = r.resolveLastWrite(c, res)
	case StrategyHighestConfidence:
		res = r.resolveHighestConfidence(c, res)
	case StrategyNewest:
		res = r.resolveNewest(c, res)
	case StrategyManual:
		res.Winner = "manual"
		res.Reason = "Flagged for manual review"
	default:
		res.Winner = "manual"
		res.Reason = fmt.Sprintf("Unknown strategy %q, defaulting to manual", strategy)
	}

	return res
}

// resolveLastWrite: the fact created most recently wins.
func (r *Resolver) resolveLastWrite(c Conflict, res Resolution) Resolution {
	if c.Fact1.CreatedAt.After(c.Fact2.CreatedAt) {
		res.Winner = "fact1"
		res.WinnerID = c.Fact1.ID
		res.LoserID = c.Fact2.ID
		res.Reason = fmt.Sprintf("Fact %d created %s (newer than fact %d created %s)",
			c.Fact1.ID, c.Fact1.CreatedAt.Format(time.RFC3339),
			c.Fact2.ID, c.Fact2.CreatedAt.Format(time.RFC3339))
	} else {
		res.Winner = "fact2"
		res.WinnerID = c.Fact2.ID
		res.LoserID = c.Fact1.ID
		res.Reason = fmt.Sprintf("Fact %d created %s (newer than fact %d created %s)",
			c.Fact2.ID, c.Fact2.CreatedAt.Format(time.RFC3339),
			c.Fact1.ID, c.Fact1.CreatedAt.Format(time.RFC3339))
	}
	return res
}

// resolveHighestConfidence: the fact with higher effective confidence wins.
// Uses Ebbinghaus decay formula to compute current confidence.
func (r *Resolver) resolveHighestConfidence(c Conflict, res Resolution) Resolution {
	now := time.Now().UTC()
	eff1 := effectiveConfidence(c.Fact1, now)
	eff2 := effectiveConfidence(c.Fact2, now)

	if eff1 >= eff2 {
		res.Winner = "fact1"
		res.WinnerID = c.Fact1.ID
		res.LoserID = c.Fact2.ID
		res.Reason = fmt.Sprintf("Fact %d effective confidence %.3f > fact %d at %.3f",
			c.Fact1.ID, eff1, c.Fact2.ID, eff2)
	} else {
		res.Winner = "fact2"
		res.WinnerID = c.Fact2.ID
		res.LoserID = c.Fact1.ID
		res.Reason = fmt.Sprintf("Fact %d effective confidence %.3f > fact %d at %.3f",
			c.Fact2.ID, eff2, c.Fact1.ID, eff1)
	}
	return res
}

// resolveNewest: the fact from the most recently imported memory wins.
// Falls back to fact creation time if memory timestamps are equal.
func (r *Resolver) resolveNewest(c Conflict, res Resolution) Resolution {
	// For newest, we compare fact creation times (proxy for memory import time)
	// since facts are created during import
	return r.resolveLastWrite(c, res)
}

// effectiveConfidence computes the current confidence using Ebbinghaus decay.
// Formula: C(t) = C₀ · exp(-λ · t) where t is days since last reinforcement.
func effectiveConfidence(f store.Fact, now time.Time) float64 {
	daysSince := now.Sub(f.LastReinforced).Hours() / 24.0
	if daysSince < 0 {
		daysSince = 0
	}
	return f.Confidence * exp(-f.DecayRate*daysSince)
}

// exp is a simple wrapper for clarity.
func exp(x float64) float64 {
	if x < -50 {
		return 0 // Avoid underflow
	}
	return math.Exp(x)
}

// ============================================================================
// Resolve by IDs (manual/interactive use)
// ============================================================================

// ResolveByID resolves a specific conflict by keeping one fact and suppressing the other.
// winnerID is the fact to keep, loserID is the fact to suppress (confidence → 0).
func (r *Resolver) ResolveByID(ctx context.Context, winnerID, loserID int64) (*Resolution, error) {
	// Validate both facts exist
	winnerFact, err := r.store.GetFact(ctx, winnerID)
	if err != nil || winnerFact == nil {
		return nil, fmt.Errorf("winner fact %d not found", winnerID)
	}
	loserFact, err := r.store.GetFact(ctx, loserID)
	if err != nil || loserFact == nil {
		return nil, fmt.Errorf("loser fact %d not found", loserID)
	}

	// Tombstone the loser fact (superseded, not deleted).
	if err := r.store.SupersedeFact(ctx, loserID, winnerID, "strategy:manual"); err != nil {
		return nil, fmt.Errorf("superseding fact %d: %w", loserID, err)
	}

	// Reinforce the winner
	_ = r.store.ReinforceFact(ctx, winnerID)

	return &Resolution{
		Strategy: StrategyManual,
		Winner:   "manual",
		WinnerID: winnerID,
		LoserID:  loserID,
		Reason: fmt.Sprintf("Manually resolved: kept fact %d (%s: %s), superseded fact %d (%s: %s)",
			winnerID, winnerFact.Predicate, winnerFact.Object,
			loserID, loserFact.Predicate, loserFact.Object),
		Applied: true,
	}, nil
}
