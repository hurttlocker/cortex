// Package extract provides local NLP-based fact extraction for Cortex.
//
// The extraction pipeline identifies structured information from raw text
// without requiring an LLM or external API:
// - Key-value pairs ("preferred editor: vim")
// - Relationships ("Alice works at Acme")
// - Preferences ("prefers dark mode")
// - Temporal facts ("meeting on Tuesday")
//
// Each extracted fact links back to its source memory unit for full traceability.
package extract
