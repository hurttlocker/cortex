package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// WatchMatchResult describes what a watch matched.
type WatchMatchResult struct {
	WatchID  int64   `json:"watch_id"`
	Query    string  `json:"query"`
	MemoryID int64   `json:"memory_id"`
	Score    float64 `json:"score"`
	Snippet  string  `json:"snippet"`
}

// CheckWatchesForMemory runs all active watch queries against a newly imported memory.
// Uses FTS5 BM25 to score relevance. Returns matches above each watch's threshold.
func (s *SQLiteStore) CheckWatchesForMemory(ctx context.Context, memoryID int64) ([]WatchMatchResult, error) {
	// Get the memory content
	var content string
	err := s.db.QueryRowContext(ctx,
		"SELECT content FROM memories WHERE id = ?", memoryID,
	).Scan(&content)
	if err != nil {
		return nil, fmt.Errorf("memory %d not found: %w", memoryID, err)
	}

	// Get all active watches
	watches, err := s.GetActiveWatchQueries(ctx)
	if err != nil {
		return nil, err
	}

	if len(watches) == 0 {
		return nil, nil
	}

	var matches []WatchMatchResult

	for _, w := range watches {
		score := bm25MatchScore(content, w.Query)

		if score >= w.Threshold {
			snippet := extractWatchSnippet(content, w.Query, 120)

			matches = append(matches, WatchMatchResult{
				WatchID:  w.ID,
				Query:    w.Query,
				MemoryID: memoryID,
				Score:    score,
				Snippet:  snippet,
			})

			// Update watch stats
			s.RecordWatchMatch(ctx, w.ID)

			// Create alert
			detail := map[string]interface{}{
				"watch_id":  w.ID,
				"query":     w.Query,
				"memory_id": memoryID,
				"score":     score,
				"snippet":   snippet,
			}
			detailJSON, _ := json.Marshal(detail)

			alert := &Alert{
				AlertType: AlertTypeMatch,
				Severity:  AlertSeverityInfo,
				AgentID:   w.AgentID,
				Message:   fmt.Sprintf("Watch matched: %q — new content scores %.0f%% (memory #%d)", w.Query, score*100, memoryID),
				Details:   string(detailJSON),
			}
			if err := s.CreateAlert(ctx, alert); err != nil {
				// Log but don't fail the import
				_ = err
			}
		}
	}

	return matches, nil
}

// CheckWatchesForMemories runs watch matching against multiple new memories (batch import).
func (s *SQLiteStore) CheckWatchesForMemories(ctx context.Context, memoryIDs []int64) ([]WatchMatchResult, error) {
	var allMatches []WatchMatchResult

	for _, id := range memoryIDs {
		matches, err := s.CheckWatchesForMemory(ctx, id)
		if err != nil {
			continue // Don't fail the whole import on watch errors
		}
		allMatches = append(allMatches, matches...)
	}

	return allMatches, nil
}

// Bm25MatchScoreExported is the exported version of bm25MatchScore for CLI testing.
func Bm25MatchScoreExported(content, query string) float64 {
	return bm25MatchScore(content, query)
}

// ExtractSnippetExported is the exported version of extractWatchSnippet for CLI testing.
func ExtractSnippetExported(content, query string, maxLen int) string {
	return extractWatchSnippet(content, query, maxLen)
}

// bm25MatchScore computes a simple term-frequency relevance score between content and query.
// Returns a normalized score between 0 and 1.
// This is a lightweight scoring function for watch matching — not the full search engine.
func bm25MatchScore(content, query string) float64 {
	contentLower := strings.ToLower(content)
	terms := strings.Fields(strings.ToLower(query))

	if len(terms) == 0 {
		return 0
	}

	matchedTerms := 0
	totalOccurrences := 0

	for _, term := range terms {
		count := strings.Count(contentLower, term)
		if count > 0 {
			matchedTerms++
			totalOccurrences += count
		}
	}

	if matchedTerms == 0 {
		return 0
	}

	// Score = (matched terms / total terms) * frequency boost
	termCoverage := float64(matchedTerms) / float64(len(terms))

	// Frequency boost: logarithmic, capped at 1.5x
	freqBoost := 1.0
	if totalOccurrences > len(terms) {
		freqBoost = 1.0 + 0.1*float64(totalOccurrences-len(terms))
		if freqBoost > 1.5 {
			freqBoost = 1.5
		}
	}

	// Phrase proximity bonus: if the full query appears as a substring
	phraseBonus := 0.0
	if strings.Contains(contentLower, strings.ToLower(query)) {
		phraseBonus = 0.2
	}

	score := termCoverage * freqBoost
	score += phraseBonus
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// extractSnippet pulls a relevant snippet from content around the first matching term.
func extractWatchSnippet(content, query string, maxLen int) string {
	contentLower := strings.ToLower(content)
	terms := strings.Fields(strings.ToLower(query))

	bestIdx := -1
	for _, term := range terms {
		idx := strings.Index(contentLower, term)
		if idx >= 0 && (bestIdx < 0 || idx < bestIdx) {
			bestIdx = idx
		}
	}

	if bestIdx < 0 {
		if len(content) > maxLen {
			return content[:maxLen] + "..."
		}
		return content
	}

	// Center the snippet around the match
	start := bestIdx - maxLen/3
	if start < 0 {
		start = 0
	}
	end := start + maxLen
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}

	// Replace newlines with spaces for cleaner display
	snippet = strings.ReplaceAll(snippet, "\n", " ")

	return snippet
}

// PendingWatchDigest returns a summary of recent watch matches.
func (s *SQLiteStore) PendingWatchDigest(ctx context.Context, since time.Duration) ([]WatchDigestEntry, error) {
	cutoff := time.Now().UTC().Add(-since)

	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.query, COUNT(a.id) as match_count, MAX(a.created_at) as last_match
		 FROM watches_v1 w
		 JOIN alerts a ON a.alert_type = 'match' AND a.acknowledged = 0
		   AND json_extract(a.details, '$.watch_id') = w.id
		   AND a.created_at > ?
		 WHERE w.active = 1
		 GROUP BY w.id
		 ORDER BY match_count DESC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("querying watch digest: %w", err)
	}
	defer rows.Close()

	var entries []WatchDigestEntry
	for rows.Next() {
		var e WatchDigestEntry
		if err := rows.Scan(&e.WatchID, &e.Query, &e.RecentMatches, &e.LastMatch); err != nil {
			return nil, fmt.Errorf("scanning watch digest: %w", err)
		}
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

// WatchDigestEntry summarizes recent matches for a single watch.
type WatchDigestEntry struct {
	WatchID       int64     `json:"watch_id"`
	Query         string    `json:"query"`
	RecentMatches int       `json:"recent_matches"`
	LastMatch     time.Time `json:"last_match"`
}
