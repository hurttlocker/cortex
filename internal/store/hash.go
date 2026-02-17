package store

import (
	"crypto/sha256"
	"fmt"
)

// HashMemoryContent computes SHA-256 of source_path + content for deduplication.
//
// Including source path means the same content from two different files
// creates two separate memories (different provenance). This is the canonical
// hash function used throughout Cortex for memory deduplication.
//
// Note: SQLite DATE() cannot parse Go's time format. Use SUBSTR(col, 1, 10) for date comparisons.
func HashMemoryContent(content, sourcePath string) string {
	h := sha256.New()
	h.Write([]byte(sourcePath))
	h.Write([]byte{0}) // separator
	h.Write([]byte(content))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// HashContentOnly computes SHA-256 hash of content only.
// This is provided for backwards compatibility but HashMemoryContent should be preferred.
func HashContentOnly(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}
