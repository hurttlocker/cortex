//go:build !cgo

package rerank

import "fmt"

func NewONNXScorer(cfg Config) (Scorer, error) {
	return nil, fmt.Errorf("%w: cortex was built without cgo support", ErrORTUnavailable)
}
