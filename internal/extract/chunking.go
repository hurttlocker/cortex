package extract

import (
	"strings"
)

// ChunkDocument splits a document into chunks that fit within the context window.
// Uses a rough token estimation of len(text) / 4.
func ChunkDocument(text string, contextWindow int) []string {
	if text == "" {
		return []string{}
	}

	// Estimate tokens: rough approximation of chars / 4
	estimatedTokens := len(text) / 4

	// Use 75% of context window to leave room for system prompt and response
	maxTokensPerChunk := (contextWindow * 3) / 4

	// If document fits in single chunk, return as-is
	if estimatedTokens <= maxTokensPerChunk {
		return []string{text}
	}

	// Calculate chunk size in characters
	maxCharsPerChunk := maxTokensPerChunk * 4

	// Overlap of ~50 tokens (200 chars) between chunks
	overlapChars := 200

	var chunks []string
	pos := 0

	for pos < len(text) {
		// Calculate end position for this chunk
		end := pos + maxCharsPerChunk
		if end > len(text) {
			end = len(text)
		}

		chunk := text[pos:end]

		// If this isn't the last chunk, try to split at a paragraph boundary
		// to avoid breaking in the middle of a thought
		if end < len(text) {
			// Look for paragraph break (\n\n) in the last 1/3 of the chunk
			searchStart := len(chunk) * 2 / 3
			if searchStart < len(chunk) {
				if idx := strings.LastIndex(chunk[searchStart:], "\n\n"); idx != -1 {
					// Found a paragraph break, split there
					actualIdx := searchStart + idx
					chunk = chunk[:actualIdx]
					end = pos + actualIdx
				}
			}
		}

		// Add chunk if it's not empty
		if strings.TrimSpace(chunk) != "" {
			chunks = append(chunks, chunk)
		}

		// Move to next position with overlap
		if end >= len(text) {
			break
		}
		pos = end - overlapChars
		if pos < 0 {
			pos = 0
		}
	}

	// Return at least one chunk, even if empty
	if len(chunks) == 0 {
		chunks = []string{text}
	}

	return chunks
}
