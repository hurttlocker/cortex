// Package observe provides memory observability for Cortex.
//
// Three core capabilities:
// - Stats: total entries, sources, freshness distribution, storage size
// - Stale detection: entries not referenced or updated within a threshold
// - Conflict detection: pairs of facts that may contradict each other
//
// This package answers the question: "What does my agent actually know?"
package observe
