package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type dedupFactRow struct {
	ID             int64
	Subject        string
	Predicate      string
	Object         string
	Confidence     float64
	LastReinforced string
}

// DedupFacts finds near-duplicate facts (same subject+predicate with similar objects)
// and supersedes lower-quality duplicates. In dry-run mode it only reports candidates.
func (s *SQLiteStore) DedupFacts(ctx context.Context, opts DedupFactOptions) (*DedupFactReport, error) {
	if opts.Threshold <= 0 {
		opts.Threshold = 0.90
	}
	if opts.Threshold > 1 {
		opts.Threshold = 1
	}
	if opts.MaxPreview <= 0 {
		opts.MaxPreview = 25
	}

	query := `
		SELECT id, subject, predicate, object, confidence, COALESCE(last_reinforced, '')
		FROM facts
		WHERE superseded_by IS NULL
	`
	args := []interface{}{}
	if strings.TrimSpace(opts.Agent) != "" {
		query += ` AND agent_id = ?`
		args = append(args, strings.TrimSpace(opts.Agent))
	}
	query += `
		ORDER BY LOWER(TRIM(subject)), LOWER(TRIM(predicate)), confidence DESC, last_reinforced DESC, id ASC
	`

	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying facts for dedup: %w", err)
	}
	defer rows.Close()

	groups := map[string][]dedupFactRow{}
	groupOrder := []string{}
	for rows.Next() {
		var r dedupFactRow
		if err := rows.Scan(&r.ID, &r.Subject, &r.Predicate, &r.Object, &r.Confidence, &r.LastReinforced); err != nil {
			return nil, fmt.Errorf("scanning fact row: %w", err)
		}
		key := normalizeFactKey(r.Subject, r.Predicate)
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating fact rows: %w", err)
	}

	report := &DedupFactReport{GroupsScanned: len(groupOrder)}
	merges := make([]DedupFactMerge, 0)

	for _, key := range groupOrder {
		facts := groups[key]
		if len(facts) < 2 {
			continue
		}

		// ensure deterministic winner precedence
		sort.SliceStable(facts, func(i, j int) bool {
			if facts[i].Confidence != facts[j].Confidence {
				return facts[i].Confidence > facts[j].Confidence
			}
			if facts[i].LastReinforced != facts[j].LastReinforced {
				return facts[i].LastReinforced > facts[j].LastReinforced
			}
			return facts[i].ID < facts[j].ID
		})

		winners := []dedupFactRow{facts[0]}
		for _, candidate := range facts[1:] {
			bestWinnerIdx := -1
			bestSimilarity := 0.0
			for idx, w := range winners {
				sim := factObjectSimilarity(w.Object, candidate.Object)
				report.PairsCompared++
				if sim >= opts.Threshold && sim > bestSimilarity {
					bestSimilarity = sim
					bestWinnerIdx = idx
				}
			}
			if bestWinnerIdx == -1 {
				winners = append(winners, candidate)
				continue
			}

			winner := winners[bestWinnerIdx]
			merges = append(merges, DedupFactMerge{
				Subject:          winner.Subject,
				Predicate:        winner.Predicate,
				WinnerID:         winner.ID,
				WinnerObject:     winner.Object,
				WinnerConfidence: winner.Confidence,
				LoserID:          candidate.ID,
				LoserObject:      candidate.Object,
				LoserConfidence:  candidate.Confidence,
				Similarity:       bestSimilarity,
			})
		}
	}

	report.Merges = len(merges)
	if len(merges) > opts.MaxPreview {
		report.Preview = append(report.Preview, merges[:opts.MaxPreview]...)
	} else {
		report.Preview = append(report.Preview, merges...)
	}

	if opts.DryRun {
		return report, nil
	}

	for _, m := range merges {
		reason := fmt.Sprintf("dedup-facts similarity=%.3f", m.Similarity)
		if err := s.SupersedeFact(ctx, m.LoserID, m.WinnerID, reason); err != nil {
			return nil, fmt.Errorf("superseding loser fact %d with winner %d: %w", m.LoserID, m.WinnerID, err)
		}
	}

	return report, nil
}

func normalizeFactKey(subject, predicate string) string {
	return normalizeFactObject(subject) + "\x00" + normalizeFactObject(predicate)
}

func normalizeFactObject(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(v))
	lastSpace := false
	for _, r := range v {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r), r == '-', r == '_', r == '/':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func factObjectSimilarity(a, b string) float64 {
	aNorm := normalizeFactObject(a)
	bNorm := normalizeFactObject(b)
	if aNorm == "" || bNorm == "" {
		return 0
	}
	if aNorm == bNorm {
		return 1
	}
	j := tokenJaccard(aNorm, bNorm)
	l := normalizedLevenshtein(aNorm, bNorm)
	if j > l {
		return j
	}
	return l
}

func tokenJaccard(a, b string) float64 {
	aSet := map[string]struct{}{}
	for _, t := range strings.Fields(a) {
		aSet[t] = struct{}{}
	}
	bSet := map[string]struct{}{}
	for _, t := range strings.Fields(b) {
		bSet[t] = struct{}{}
	}
	if len(aSet) == 0 && len(bSet) == 0 {
		return 1
	}
	inter := 0
	for t := range aSet {
		if _, ok := bSet[t]; ok {
			inter++
		}
	}
	union := len(aSet) + len(bSet) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func normalizedLevenshtein(a, b string) float64 {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 && len(br) == 0 {
		return 1
	}
	maxLen := len(ar)
	if len(br) > maxLen {
		maxLen = len(br)
	}
	if maxLen == 0 {
		return 0
	}
	d := make([][]int, len(ar)+1)
	for i := range d {
		d[i] = make([]int, len(br)+1)
	}
	for i := 0; i <= len(ar); i++ {
		d[i][0] = i
	}
	for j := 0; j <= len(br); j++ {
		d[0][j] = j
	}
	for i := 1; i <= len(ar); i++ {
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			del := d[i-1][j] + 1
			ins := d[i][j-1] + 1
			sub := d[i-1][j-1] + cost
			d[i][j] = minInt(del, minInt(ins, sub))
		}
	}
	dist := d[len(ar)][len(br)]
	return 1 - float64(dist)/float64(maxLen)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
