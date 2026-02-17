package ingest

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CSVImporter handles .csv and .tsv files.
type CSVImporter struct{}

// CanHandle returns true for CSV/TSV file extensions.
func (c *CSVImporter) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".csv" || ext == ".tsv"
}

// Import parses a CSV file into memory chunks.
// First row is treated as headers (become fact keys).
// Each subsequent row becomes one memory unit with headers as metadata keys.
func (c *CSVImporter) Import(ctx context.Context, path string) ([]RawMemory, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)

	// Auto-detect TSV
	if strings.ToLower(filepath.Ext(path)) == ".tsv" {
		reader.Comma = '\t'
	}

	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV %s: %w", path, err)
	}

	if len(records) < 2 {
		// Need at least headers + one row
		return nil, nil
	}

	headers := records[0]
	var memories []RawMemory

	for i, row := range records[1:] {
		meta := make(map[string]string)
		rowData := make(map[string]string)

		for j, val := range row {
			if j < len(headers) && strings.TrimSpace(val) != "" {
				key := strings.TrimSpace(headers[j])
				val = strings.TrimSpace(val)
				meta[key] = val
				rowData[key] = val
			}
		}

		if len(rowData) == 0 {
			continue
		}

		// Produce a readable content string
		pretty, _ := json.MarshalIndent(rowData, "", "  ")

		mem := RawMemory{
			Content:       string(pretty),
			SourceFile:    absPath,
			SourceLine:    i + 2, // 1-indexed, skip header row
			SourceSection: fmt.Sprintf("row-%d", i+1),
			Metadata:      meta,
		}
		memories = append(memories, mem)
	}

	return memories, nil
}
