package ingest

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	cfgresolver "github.com/hurttlocker/cortex/internal/config"
	"github.com/hurttlocker/cortex/internal/store"
	"github.com/hurttlocker/cortex/internal/temporal"
)

var (
	deniedExtractedFactSubjects = map[string]struct{}{
		"current time": {},
		"current_time": {},
		"current date": {},
	}
	deniedExtractedFactSubjectRE = regexp.MustCompile(`(?i)^current.*(?:time|date|timestamp)`)
	heartbeatFactSubjectRE       = regexp.MustCompile(`(?i)^(heartbeat|heartbeat_status)`)
	specificDatetimeRE           = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[ t]\d{2}:\d{2}(?::\d{2})?(?:\.\d+)?)?(?:z|[+-]\d{2}:?\d{2}| utc)?\b|\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+\d{1,2}(?:st|nd|rd|th)?(?:,\s+\d{4})?(?:\s+[—-]\s+\d{1,2}:\d{2}\s*(?:am|pm))?`)
	bareNumberObjectRE           = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
	objectTokenSplitRE           = regexp.MustCompile(`[^a-z0-9]+`)

	configuredFactSuppressions []cfgresolver.DenylistEntry
)

// IsDeniedExtractedFactSubject returns true when an extracted fact subject
// is known ingestion noise and should never be persisted.
func IsDeniedExtractedFactSubject(subject string) bool {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return false
	}

	lower := strings.ToLower(trimmed)
	if _, ok := deniedExtractedFactSubjects[lower]; ok {
		return true
	}
	if normalized := strings.ReplaceAll(lower, "_", " "); normalized != lower {
		if _, ok := deniedExtractedFactSubjects[normalized]; ok {
			return true
		}
	}
	return deniedExtractedFactSubjectRE.MatchString(trimmed)
}

func SetConfiguredFactSuppressions(entries []cfgresolver.DenylistEntry) {
	configuredFactSuppressions = append([]cfgresolver.DenylistEntry(nil), entries...)
}

func matchesConfiguredFactSuppression(fact *store.Fact) bool {
	if fact == nil || len(configuredFactSuppressions) == 0 {
		return false
	}
	text := strings.TrimSpace(strings.Join([]string{fact.Subject, fact.Predicate, fact.Object, fact.SourceQuote}, " "))
	for _, entry := range configuredFactSuppressions {
		if entry.Matches(text) {
			return true
		}
	}
	return false
}

func hasSpecificDatetime(s string) bool {
	return specificDatetimeRE.MatchString(strings.ToLower(strings.TrimSpace(s)))
}

func isBareNumberObject(s string) bool {
	return bareNumberObjectRE.MatchString(strings.TrimSpace(s))
}

// ShouldStoreExtractedFact applies hard ingest-level deny rules for extracted
// facts. This is intentionally narrower than generic AddFact validation.
func ShouldStoreExtractedFact(fact *store.Fact) bool {
	if fact == nil {
		return false
	}
	subject := strings.TrimSpace(fact.Subject)
	object := strings.TrimSpace(fact.Object)
	if IsDeniedExtractedFactSubject(subject) {
		return false
	}
	if heartbeatFactSubjectRE.MatchString(subject) {
		return false
	}
	if isBareNumberObject(object) {
		return false
	}
	if matchesConfiguredFactSuppression(fact) {
		return false
	}
	return true
}

type activeFactMatch struct {
	ID         int64
	Object     string
	Confidence float64
}

func findActiveFactMatchesBySubjectPredicate(ctx context.Context, s store.Store, subject, predicate, agentID string) ([]activeFactMatch, error) {
	sqlStore, ok := s.(*store.SQLiteStore)
	if !ok {
		return nil, nil
	}

	rows, err := sqlStore.GetDB().QueryContext(ctx,
		`SELECT id, object, confidence
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
		if err := rows.Scan(&match.ID, &match.Object, &match.Confidence); err != nil {
			return nil, fmt.Errorf("scanning active fact match: %w", err)
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active fact matches: %w", err)
	}

	return matches, nil
}

func normalizedObjectTokens(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range objectTokenSplitRE.Split(strings.ToLower(strings.TrimSpace(s)), -1) {
		if len(tok) < 2 {
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

func objectWordOverlap(a, b string) float64 {
	left := normalizedObjectTokens(a)
	right := normalizedObjectTokens(b)
	if len(left) == 0 || len(right) == 0 {
		if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
			return 1
		}
		return 0
	}
	intersection := 0
	for tok := range left {
		if _, ok := right[tok]; ok {
			intersection++
		}
	}
	denom := math.Min(float64(len(left)), float64(len(right)))
	if denom == 0 {
		return 0
	}
	return float64(intersection) / denom
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
	populateTemporalNorm(ctx, s, fact)
	if !ShouldStoreExtractedFact(fact) {
		return 0, false, nil
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
	bestOverlap := 0.0
	bestConfidence := -1.0
	for _, match := range matches {
		overlap := objectWordOverlap(strings.TrimSpace(match.Object), candidateObject)
		if overlap >= 0.80 {
			if winnerID == 0 || overlap > bestOverlap || (overlap == bestOverlap && match.Confidence > bestConfidence) {
				winnerID = match.ID
				bestOverlap = overlap
				bestConfidence = match.Confidence
			}
			continue
		}
	}

	if winnerID != 0 {
		if err := s.ReinforceFact(ctx, winnerID); err != nil {
			return winnerID, false, err
		}
		return winnerID, false, nil
	}

	factID, err = s.AddFact(ctx, fact)
	if err != nil {
		return 0, false, err
	}

	return factID, true, nil
}

func populateTemporalNorm(ctx context.Context, s store.Store, fact *store.Fact) {
	if fact == nil || !strings.EqualFold(strings.TrimSpace(fact.FactType), "temporal") || fact.TemporalNorm != nil {
		return
	}
	mem, err := s.GetMemory(ctx, fact.MemoryID)
	if err != nil || mem == nil {
		return
	}
	anchor := ""
	if mem.Metadata != nil {
		anchor = strings.TrimSpace(mem.Metadata.TimestampStart)
	}
	if anchor == "" {
		anchor = temporal.TimestampStartFromSection(mem.SourceSection)
	}
	fact.TemporalNorm = temporal.NormalizeLiteral(fact.Object, anchor)
	if fact.TemporalNorm == nil && fact.SourceQuote != "" {
		fact.TemporalNorm = temporal.NormalizeLiteral(fact.SourceQuote, anchor)
	}
}
