package ingest

import (
	"context"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

var tokenSplitRE = regexp.MustCompile(`[^a-z0-9]+`)

// findNearDuplicate checks the recent memory window and returns true when
// cosine similarity meets/exceeds threshold.
func findNearDuplicate(ctx context.Context, s store.Store, content string, opts ImportOptions) (bool, float64, *store.Memory, error) {
	opts.Normalize()
	if !opts.CaptureDedupeEnabled {
		return false, 0, nil, nil
	}
	if strings.TrimSpace(content) == "" {
		return false, 0, nil, nil
	}

	recent, err := s.ListMemories(ctx, store.ListOpts{Limit: 100})
	if err != nil {
		return false, 0, nil, err
	}
	if len(recent) == 0 {
		return false, 0, nil, nil
	}

	windowStart := time.Now().UTC().Add(-time.Duration(opts.CaptureDedupeWindowSec) * time.Second)
	candidateVec := vectorizeText(content)
	if len(candidateVec) == 0 {
		return false, 0, nil, nil
	}

	bestScore := 0.0
	var best *store.Memory

	for _, m := range recent {
		if m == nil {
			continue
		}
		if m.ImportedAt.Before(windowStart) {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}

		score := cosineTextSimilarity(candidateVec, vectorizeText(m.Content))
		if score > bestScore {
			bestScore = score
			best = m
		}
	}

	if best != nil && bestScore >= opts.CaptureSimilarityThreshold {
		return true, bestScore, best, nil
	}
	return false, bestScore, best, nil
}

func vectorizeText(text string) map[string]float64 {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	rawTokens := tokenSplitRE.Split(text, -1)
	vec := make(map[string]float64, len(rawTokens))
	for _, tok := range rawTokens {
		if len(tok) < 2 {
			continue
		}
		vec[tok]++
	}
	if len(vec) == 0 {
		return nil
	}
	return vec
}

func cosineTextSimilarity(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	dot := 0.0
	normA := 0.0
	normB := 0.0

	for k, av := range a {
		dot += av * b[k]
		normA += av * av
	}
	for _, bv := range b {
		normB += bv * bv
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
