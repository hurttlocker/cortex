package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// InferenceResult holds the outcome of running the inference engine.
type InferenceResult struct {
	EdgesCreated int            `json:"edges_created"`
	EdgesSkipped int            `json:"edges_skipped"` // Already existed
	RulesApplied map[string]int `json:"rules_applied"`
	Proposals    []EdgeProposal `json:"proposals,omitempty"` // For dry-run
}

// EdgeProposal represents a proposed edge from inference.
type EdgeProposal struct {
	SourceFactID int64    `json:"source_fact_id"`
	TargetFactID int64    `json:"target_fact_id"`
	EdgeType     EdgeType `json:"edge_type"`
	Confidence   float64  `json:"confidence"`
	Rule         string   `json:"rule"`
	Reason       string   `json:"reason"`
}

// InferenceOpts controls inference behavior.
type InferenceOpts struct {
	DryRun        bool    // Preview only, don't create edges
	MinConfidence float64 // Minimum confidence for inferred edges (default: 0.3)
	MaxEdges      int     // Maximum edges to create/propose per run (default: 100)
}

// DefaultInferenceOpts returns sensible defaults.
func DefaultInferenceOpts() InferenceOpts {
	return InferenceOpts{
		MinConfidence: 0.3,
		MaxEdges:      100,
	}
}

// edgeCount returns the current count of edges created or proposals emitted.
func (r *InferenceResult) edgeCount(dryRun bool) int {
	if dryRun {
		return len(r.Proposals)
	}
	return r.EdgesCreated
}

// RunInference applies all inference rules and creates (or proposes) edges.
func (s *SQLiteStore) RunInference(ctx context.Context, opts InferenceOpts) (*InferenceResult, error) {
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = 0.3
	}
	if opts.MaxEdges <= 0 {
		opts.MaxEdges = 100
	}

	result := &InferenceResult{
		RulesApplied: make(map[string]int),
	}

	rules := []struct {
		name string
		fn   func(context.Context, InferenceOpts, *InferenceResult) error
	}{
		{"cooccurrence", s.inferFromCooccurrence},
		{"subject_clustering", s.inferFromSubjectClustering},
		{"supersession", s.inferFromSupersession},
	}

	for _, rule := range rules {
		if result.edgeCount(opts.DryRun) >= opts.MaxEdges {
			break
		}
		if err := rule.fn(ctx, opts, result); err != nil {
			return result, fmt.Errorf("rule %s: %w", rule.name, err)
		}
	}

	return result, nil
}

// Rule 1: Co-occurrence → relates_to
// Facts that co-occur >= 5 times get a relates_to edge.
func (s *SQLiteStore) inferFromCooccurrence(ctx context.Context, opts InferenceOpts, result *InferenceResult) error {
	suggestions, err := s.SuggestEdgesFromCooccurrence(ctx, 5)
	if err != nil {
		return err
	}

	for _, pair := range suggestions {
		if result.edgeCount(opts.DryRun) >= opts.MaxEdges {
			break
		}

		// Confidence based on co-occurrence count (5=0.5, 10=0.7, 20+=0.9)
		conf := 0.5 + float64(pair.Count-5)*0.02
		if conf > 0.9 {
			conf = 0.9
		}
		if conf < opts.MinConfidence {
			continue
		}

		proposal := EdgeProposal{
			SourceFactID: pair.FactIDA,
			TargetFactID: pair.FactIDB,
			EdgeType:     EdgeTypeRelatesTo,
			Confidence:   conf,
			Rule:         "cooccurrence",
			Reason:       fmt.Sprintf("Co-occurred %d times", pair.Count),
		}

		if opts.DryRun {
			result.Proposals = append(result.Proposals, proposal)
			result.RulesApplied["cooccurrence"]++
			continue
		}

		err := s.AddEdge(ctx, &FactEdge{
			SourceFactID: pair.FactIDA,
			TargetFactID: pair.FactIDB,
			EdgeType:     EdgeTypeRelatesTo,
			Confidence:   conf,
			Source:       EdgeSourceInferred,
		})
		if errors.Is(err, ErrEdgeExists) {
			result.EdgesSkipped++
		} else if err != nil {
			result.EdgesSkipped++
		} else {
			result.EdgesCreated++
			result.RulesApplied["cooccurrence"]++
		}
	}

	return nil
}

// Rule 2: Subject clustering → relates_to
// Facts with the same subject but different predicates are structurally related.
func (s *SQLiteStore) inferFromSubjectClustering(ctx context.Context, opts InferenceOpts, result *InferenceResult) error {
	// Find subjects with multiple distinct predicates
	rows, err := s.db.QueryContext(ctx,
		`SELECT LOWER(subject), COUNT(DISTINCT predicate) as pred_count
		 FROM facts
		 WHERE superseded_by IS NULL AND confidence > 0 AND subject != ''
		 GROUP BY LOWER(subject)
		 HAVING COUNT(DISTINCT predicate) >= 2
		 ORDER BY pred_count DESC
		 LIMIT 50`,
	)
	if err != nil {
		return fmt.Errorf("querying subject clusters: %w", err)
	}
	defer rows.Close()

	var subjects []string
	for rows.Next() {
		var subject string
		var count int
		if err := rows.Scan(&subject, &count); err != nil {
			return fmt.Errorf("scanning subject cluster: %w", err)
		}
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating subject clusters: %w", err)
	}

	for _, subject := range subjects {
		if result.edgeCount(opts.DryRun) >= opts.MaxEdges {
			break
		}

		// Get facts for this subject
		factRows, err := s.db.QueryContext(ctx,
			`SELECT id FROM facts
			 WHERE LOWER(subject) = ? AND superseded_by IS NULL AND confidence > 0
			 ORDER BY created_at DESC LIMIT 10`,
			subject,
		)
		if err != nil {
			continue
		}

		var factIDs []int64
		for factRows.Next() {
			var id int64
			if err := factRows.Scan(&id); err != nil {
				factRows.Close()
				return fmt.Errorf("scanning fact id for subject %q: %w", subject, err)
			}
			factIDs = append(factIDs, id)
		}
		factRows.Close()

		// Create relates_to edges between facts with same subject
		for i := 0; i < len(factIDs); i++ {
			for j := i + 1; j < len(factIDs); j++ {
				if result.edgeCount(opts.DryRun) >= opts.MaxEdges {
					return nil
				}

				conf := 0.4 // Lower confidence for subject clustering
				if conf < opts.MinConfidence {
					continue
				}

				proposal := EdgeProposal{
					SourceFactID: factIDs[i],
					TargetFactID: factIDs[j],
					EdgeType:     EdgeTypeRelatesTo,
					Confidence:   conf,
					Rule:         "subject_clustering",
					Reason:       fmt.Sprintf("Same subject: %q", subject),
				}

				if opts.DryRun {
					result.Proposals = append(result.Proposals, proposal)
					result.RulesApplied["subject_clustering"]++
					continue
				}

				err := s.AddEdge(ctx, &FactEdge{
					SourceFactID: factIDs[i],
					TargetFactID: factIDs[j],
					EdgeType:     EdgeTypeRelatesTo,
					Confidence:   conf,
					Source:       EdgeSourceInferred,
				})
				if errors.Is(err, ErrEdgeExists) {
					result.EdgesSkipped++
				} else if err != nil {
					result.EdgesSkipped++
				} else {
					result.EdgesCreated++
					result.RulesApplied["subject_clustering"]++
				}
			}
		}
	}

	return nil
}

// Rule 3: Supersession patterns → supersedes
// Facts with same subject+predicate but different objects where one is newer.
func (s *SQLiteStore) inferFromSupersession(ctx context.Context, opts InferenceOpts, result *InferenceResult) error {
	// Find subject+predicate pairs with multiple active facts
	rows, err := s.db.QueryContext(ctx,
		`SELECT LOWER(subject), LOWER(predicate), COUNT(*) as cnt
		 FROM facts
		 WHERE superseded_by IS NULL AND confidence > 0 AND subject != ''
		 GROUP BY LOWER(subject), LOWER(predicate)
		 HAVING COUNT(DISTINCT object) > 1
		 ORDER BY cnt DESC
		 LIMIT 50`,
	)
	if err != nil {
		return fmt.Errorf("querying supersession candidates: %w", err)
	}
	defer rows.Close()

	type spPair struct{ subject, predicate string }
	var pairs []spPair
	for rows.Next() {
		var p spPair
		var cnt int
		if err := rows.Scan(&p.subject, &p.predicate, &cnt); err != nil {
			return fmt.Errorf("scanning supersession pair: %w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating supersession pairs: %w", err)
	}

	for _, p := range pairs {
		if result.edgeCount(opts.DryRun) >= opts.MaxEdges {
			break
		}

		factRows, err := s.db.QueryContext(ctx,
			`SELECT id, object, created_at FROM facts
			 WHERE LOWER(subject) = ? AND LOWER(predicate) = ?
			   AND superseded_by IS NULL AND confidence > 0
			 ORDER BY created_at DESC LIMIT 5`,
			p.subject, p.predicate,
		)
		if err != nil {
			continue
		}

		type factInfo struct {
			id        int64
			object    string
			createdAt time.Time
		}
		var facts []factInfo
		for factRows.Next() {
			var f factInfo
			var createdStr string
			if err := factRows.Scan(&f.id, &f.object, &createdStr); err != nil {
				factRows.Close()
				return fmt.Errorf("scanning supersession fact: %w", err)
			}
			if t, err := time.Parse("2006-01-02 15:04:05", createdStr); err == nil {
				f.createdAt = t
			}
			facts = append(facts, f)
		}
		factRows.Close()

		// Newest fact supersedes older ones with different objects
		if len(facts) >= 2 {
			newest := facts[0]
			for _, older := range facts[1:] {
				if result.edgeCount(opts.DryRun) >= opts.MaxEdges {
					return nil
				}

				if strings.EqualFold(newest.object, older.object) {
					continue
				}

				conf := 0.6
				if conf < opts.MinConfidence {
					continue
				}

				proposal := EdgeProposal{
					SourceFactID: newest.id,
					TargetFactID: older.id,
					EdgeType:     EdgeTypeSupersedes,
					Confidence:   conf,
					Rule:         "supersession",
					Reason:       fmt.Sprintf("%s %s: %q → %q", p.subject, p.predicate, older.object, newest.object),
				}

				if opts.DryRun {
					result.Proposals = append(result.Proposals, proposal)
					result.RulesApplied["supersession"]++
					continue
				}

				err := s.AddEdge(ctx, &FactEdge{
					SourceFactID: newest.id,
					TargetFactID: older.id,
					EdgeType:     EdgeTypeSupersedes,
					Confidence:   conf,
					Source:       EdgeSourceInferred,
				})
				if errors.Is(err, ErrEdgeExists) {
					result.EdgesSkipped++
				} else if err != nil {
					result.EdgesSkipped++
				} else {
					result.EdgesCreated++
					result.RulesApplied["supersession"]++
				}
			}
		}
	}

	return nil
}
