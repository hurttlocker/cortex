package ingest

import (
	"context"
	"fmt"
	"strings"

	"github.com/hurttlocker/cortex/internal/store"
)

type activeFactMatch struct {
	ID     int64
	Object string
}

func findActiveFactMatchesBySubjectPredicate(ctx context.Context, s store.Store, subject, predicate, agentID string) ([]activeFactMatch, error) {
	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		return nil, nil
	}

	rows, err := sqlStore.GetDB().QueryContext(ctx,
		`SELECT id, object
		   FROM facts
		  WHERE superseded_by IS NULL
		    AND LOWER(TRIM(subject)) = LOWER(TRIM(?))
		    AND LOWER(TRIM(predicate)) = LOWER(TRIM(?))
		    AND COALESCE(agent_id, '') = ?
		    AND COALESCE(LOWER(state), 'active') IN ('active', 'core')
		  ORDER BY created_at DESC, id DESC`,
		subject, predicate, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying active fact matches: %w", err)
	}
	defer rows.Close()

	matches := make([]activeFactMatch, 0)
	for rows.Next() {
		var match activeFactMatch
		if err := rows.Scan(&match.ID, &match.Object); err != nil {
			return nil, fmt.Errorf("scanning active fact match: %w", err)
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active fact matches: %w", err)
	}

	return matches, nil
}

// StoreExtractedFact writes an extracted fact through the ingest-layer conflict-prevention policy.
//
// Rules:
//   - If no active same subject+predicate fact exists, insert the candidate.
//   - If an active fact exists with the same object, skip inserting a duplicate.
//   - If active facts exist with different objects, supersede them in favor of the current object.
//   - If the current object already exists, reuse the newest matching active fact as winner and
//     supersede only the conflicting older-object facts.
func StoreExtractedFact(ctx context.Context, s store.Store, fact *store.Fact) (factID int64, stored bool, err error) {
	if fact == nil {
		return 0, false, fmt.Errorf("fact is nil")
	}

	subject := strings.TrimSpace(fact.Subject)
	predicate := strings.TrimSpace(fact.Predicate)
	if subject == "" || predicate == "" {
		factID, err := s.AddFact(ctx, fact)
		return factID, err == nil, err
	}

	matches, err := findActiveFactMatchesBySubjectPredicate(ctx, s, subject, predicate, fact.AgentID)
	if err != nil {
		return 0, false, err
	}

	candidateObject := strings.TrimSpace(fact.Object)
	var winnerID int64
	conflictingIDs := make([]int64, 0, len(matches))
	for _, match := range matches {
		if strings.TrimSpace(match.Object) == candidateObject {
			if winnerID == 0 {
				winnerID = match.ID
			}
			continue
		}
		conflictingIDs = append(conflictingIDs, match.ID)
	}

	if winnerID != 0 {
		for _, oldFactID := range conflictingIDs {
			if oldFactID <= 0 || oldFactID == winnerID {
				continue
			}
			if err := s.SupersedeFact(ctx, oldFactID, winnerID, "auto-supersede on extract: existing active fact matches imported object"); err != nil {
				return winnerID, false, err
			}
		}
		return winnerID, false, nil
	}

	factID, err = s.AddFact(ctx, fact)
	if err != nil {
		return 0, false, err
	}

	for _, oldFactID := range conflictingIDs {
		if oldFactID <= 0 || oldFactID == factID {
			continue
		}
		if err := s.SupersedeFact(ctx, oldFactID, factID, "auto-supersede on extract: updated object for same subject+predicate"); err != nil {
			return factID, true, err
		}
	}

	return factID, true, nil
}
