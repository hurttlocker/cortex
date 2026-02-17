// Package search provides dual search capabilities for Cortex.
//
// Two search modes, both fully local:
// - BM25 keyword search via SQLite FTS5
// - Semantic search via local ONNX embeddings (all-MiniLM-L6-v2)
//
// The default hybrid mode combines both using reciprocal rank fusion,
// giving users the best of exact keyword matching and conceptual similarity.
package search
