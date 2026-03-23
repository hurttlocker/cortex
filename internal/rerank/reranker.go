package rerank

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrORTUnavailable = errors.New("onnxruntime unavailable")
	ErrModelNotReady  = errors.New("reranker model not ready")
)

type Mode string

const (
	ModeAuto Mode = "auto"
	ModeOn   Mode = "on"
	ModeOff  Mode = "off"
)

func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return ModeAuto, nil
	case "on", "true", "1":
		return ModeOn, nil
	case "off", "false", "0":
		return ModeOff, nil
	default:
		return "", fmt.Errorf("invalid rerank mode %q (valid: auto, on, off)", raw)
	}
}

type Candidate struct {
	Index     int
	BaseScore float64
	Text      string
}

type ScoredCandidate struct {
	Candidate
	RerankScore float64
}

type Scorer interface {
	Name() string
	Available() bool
	Score(ctx context.Context, query string, docs []string) ([]float64, error)
	Close() error
}

type Service struct {
	scorer        Scorer
	maxCandidates int
}

func NewService(scorer Scorer, maxCandidates int) *Service {
	if maxCandidates <= 0 {
		maxCandidates = 30
	}
	return &Service{
		scorer:        scorer,
		maxCandidates: maxCandidates,
	}
}

func (s *Service) Name() string {
	if s == nil || s.scorer == nil {
		return ""
	}
	return s.scorer.Name()
}

func (s *Service) Available() bool {
	return s != nil && s.scorer != nil && s.scorer.Available()
}

func (s *Service) Close() error {
	if s == nil || s.scorer == nil {
		return nil
	}
	return s.scorer.Close()
}

func (s *Service) MaxCandidates() int {
	if s == nil || s.maxCandidates <= 0 {
		return 30
	}
	return s.maxCandidates
}

func (s *Service) Rerank(ctx context.Context, query string, candidates []Candidate, limit int) ([]ScoredCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	out := make([]ScoredCandidate, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, ScoredCandidate{Candidate: c, RerankScore: c.BaseScore})
	}

	if !s.Available() || len(candidates) == 1 {
		if limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	}

	if strings.TrimSpace(query) == "" {
		if limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	}

	prefixSize := len(candidates)
	if prefixSize > s.MaxCandidates() {
		prefixSize = s.MaxCandidates()
	}

	docs := make([]string, 0, prefixSize)
	for _, c := range candidates[:prefixSize] {
		docs = append(docs, c.Text)
	}

	scores, err := s.scorer.Score(ctx, query, docs)
	if err != nil {
		return nil, err
	}
	if len(scores) != len(docs) {
		return nil, fmt.Errorf("reranker returned %d scores for %d candidates", len(scores), len(docs))
	}

	for i := 0; i < prefixSize; i++ {
		out[i].RerankScore = scores[i]
	}

	sort.SliceStable(out[:prefixSize], func(i, j int) bool {
		if out[i].RerankScore != out[j].RerankScore {
			return out[i].RerankScore > out[j].RerankScore
		}
		if out[i].BaseScore != out[j].BaseScore {
			return out[i].BaseScore > out[j].BaseScore
		}
		return out[i].Index < out[j].Index
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
