// Package ingest provides the import engine for Cortex.
//
// Each supported format (Markdown, JSON, YAML, CSV, plain text) has its own
// importer that implements the Importer interface. The engine auto-detects
// formats by file extension and dispatches to the correct parser.
//
// All importers preserve provenance: source file path, line number, and
// original timestamps are tracked for every memory unit.
package ingest
