// Package store provides the SQLite + FTS5 storage layer for Cortex.
//
// All memory data lives in a single SQLite database file, including:
// - Raw imported content with provenance
// - Extracted facts (key-value pairs, relationships, etc.)
// - FTS5 full-text search index
// - Embedding vectors for semantic search
package store
