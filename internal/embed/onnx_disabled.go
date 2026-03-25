//go:build !cgo

package embed

import "fmt"

func NewONNXEmbedder(cfg *EmbedConfig) (Embedder, error) {
	return nil, fmt.Errorf("%w: cortex was built without cgo support", ErrORTUnavailable)
}
