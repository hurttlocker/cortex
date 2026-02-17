package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// PlainTextImporter handles .txt, .log, and any unrecognized text format.
type PlainTextImporter struct{}

// CanHandle returns true for plain text extensions. Also acts as fallback.
func (t *PlainTextImporter) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".txt" || ext == ".log" || ext == ""
}

// Import parses a plain text file into memory chunks.
// Splits on double newlines (paragraphs). Each paragraph becomes one memory unit.
func (t *PlainTextImporter) Import(ctx context.Context, path string) ([]RawMemory, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	return splitTextIntoParagraphs(content, absPath), nil
}

// splitTextIntoParagraphs splits text on double newlines and tracks line numbers.
func splitTextIntoParagraphs(content string, absPath string) []RawMemory {
	var memories []RawMemory

	// Normalize line endings
	content = strings.ReplaceAll(content, "\r\n", "\n")

	paragraphs := strings.Split(content, "\n\n")
	lineNum := 1

	for _, para := range paragraphs {
		text := strings.TrimSpace(para)
		if text == "" {
			lineNum += strings.Count(para, "\n") + 2
			continue
		}

		mem := RawMemory{
			Content:    text,
			SourceFile: absPath,
			SourceLine: lineNum,
		}
		memories = append(memories, mem)
		lineNum += strings.Count(para, "\n") + 2
	}

	return memories
}
