package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAMLImporter handles .yaml and .yml files.
type YAMLImporter struct{}

// CanHandle returns true for YAML file extensions.
func (y *YAMLImporter) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// Import parses a YAML file into memory chunks.
// Multi-document YAML (separated by ---) produces one memory per document.
// Single documents are handled like JSON (parsed to map, then extracted).
func (y *YAMLImporter) Import(ctx context.Context, path string) ([]RawMemory, error) {
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

	// Split on YAML document separators
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var memories []RawMemory
	docNum := 0

	for {
		var doc interface{}
		err := decoder.Decode(&doc)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("invalid YAML in %s (document %d): %w", path, docNum+1, err)
		}
		if doc == nil {
			docNum++
			continue
		}

		docNum++

		switch v := doc.(type) {
		case map[string]interface{}:
			// Each top-level key becomes one memory unit
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, key := range keys {
				val := v[key]
				pretty, _ := json.MarshalIndent(val, "", "  ")
				meta := make(map[string]string)
				flattenJSON(key, val, meta)

				mem := RawMemory{
					Content:       string(pretty),
					SourceFile:    absPath,
					SourceLine:    1,
					SourceSection: key,
					Metadata:      meta,
				}
				memories = append(memories, mem)
			}

		default:
			// Non-map document — store as-is
			pretty, _ := json.MarshalIndent(doc, "", "  ")
			section := ""
			if docNum > 1 {
				section = fmt.Sprintf("document-%d", docNum)
			}
			mem := RawMemory{
				Content:       string(pretty),
				SourceFile:    absPath,
				SourceLine:    1,
				SourceSection: section,
			}
			memories = append(memories, mem)
		}
	}

	return memories, nil
}
