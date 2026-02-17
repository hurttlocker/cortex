package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// JSONImporter handles .json files.
type JSONImporter struct{}

// CanHandle returns true for JSON file extensions.
func (j *JSONImporter) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".json"
}

// Import parses a JSON file into memory chunks.
// - Array of objects: each element becomes one memory unit.
// - Single object: each top-level key becomes one memory unit.
// - Nested objects are flattened with dot notation.
func (j *JSONImporter) Import(ctx context.Context, path string) ([]RawMemory, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, nil
	}

	// Try parsing as a generic JSON value
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}

	var memories []RawMemory

	switch v := raw.(type) {
	case []interface{}:
		// Array: each element becomes one memory unit
		for i, elem := range v {
			pretty, _ := json.MarshalIndent(elem, "", "  ")
			section := fmt.Sprintf("[%d]", i)
			mem := RawMemory{
				Content:       string(pretty),
				SourceFile:    absPath,
				SourceLine:    1,
				SourceSection: section,
			}
			memories = append(memories, mem)
		}

	case map[string]interface{}:
		// Object: each top-level key becomes one memory unit
		// Sort keys for deterministic output
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			val := v[key]
			pretty, _ := json.MarshalIndent(val, "", "  ")
			section := key

			// For nested objects, also produce flattened key-value metadata
			meta := make(map[string]string)
			flattenJSON(key, val, meta)

			mem := RawMemory{
				Content:       string(pretty),
				SourceFile:    absPath,
				SourceLine:    1,
				SourceSection: section,
				Metadata:      meta,
			}
			memories = append(memories, mem)
		}

	default:
		// Scalar value — single memory
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		memories = append(memories, RawMemory{
			Content:    string(pretty),
			SourceFile: absPath,
			SourceLine: 1,
		})
	}

	return memories, nil
}

// flattenJSON recursively flattens a JSON value into dot-notation key-value pairs.
func flattenJSON(prefix string, val interface{}, out map[string]string) {
	switch v := val.(type) {
	case map[string]interface{}:
		for k, inner := range v {
			flattenJSON(prefix+"."+k, inner, out)
		}
	case []interface{}:
		for i, elem := range v {
			flattenJSON(fmt.Sprintf("%s[%d]", prefix, i), elem, out)
		}
	case string:
		out[prefix] = v
	case float64:
		out[prefix] = fmt.Sprintf("%g", v)
	case bool:
		out[prefix] = fmt.Sprintf("%t", v)
	case nil:
		out[prefix] = "null"
	default:
		out[prefix] = fmt.Sprintf("%v", v)
	}
}
