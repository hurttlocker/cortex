package ingest

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MarkdownImporter handles .md and .markdown files.
type MarkdownImporter struct{}

// CanHandle returns true for Markdown file extensions.
func (m *MarkdownImporter) CanHandle(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// Import parses a Markdown file into memory chunks.
// Splits on ## (h2) headers. Each section becomes one memory unit.
// If no h2 headers are found, splits on double newlines (paragraphs).
func (m *MarkdownImporter) Import(ctx context.Context, path string) ([]RawMemory, error) {
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

	// Strip YAML front matter
	metadata, body := stripFrontMatter(content)

	// Detect date from filename (e.g., 2024-01-15.md)
	baseName := filepath.Base(path)
	dateRe := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})`)
	if match := dateRe.FindString(strings.TrimSuffix(baseName, filepath.Ext(baseName))); match != "" {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata["date"] = match
	}

	// Try splitting on h2 headers (only if h2 headers exist)
	if hasH2Headers(body) {
		sections := splitOnHeaders(body, absPath, metadata)
		if len(sections) > 0 {
			return sections, nil
		}
	}

	// Fallback: split on double newlines (paragraphs)
	return splitOnParagraphs(body, absPath, metadata), nil
}

// stripFrontMatter removes YAML front matter (--- delimited) from content.
// Returns metadata map and remaining body.
func stripFrontMatter(content string) (map[string]string, string) {
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return nil, content
	}

	trimmed := strings.TrimSpace(content)
	// Find the closing ---
	rest := trimmed[3:] // skip opening ---
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, content
	}

	fmContent := strings.TrimSpace(rest[:idx])
	body := rest[idx+4:] // skip \n---

	// Parse simple key: value pairs from front matter
	metadata := make(map[string]string)
	for _, line := range strings.Split(fmContent, "\n") {
		line = strings.TrimSpace(line)
		if colonIdx := strings.Index(line, ":"); colonIdx > 0 {
			key := strings.TrimSpace(line[:colonIdx])
			val := strings.TrimSpace(line[colonIdx+1:])
			if key != "" && val != "" {
				metadata[key] = val
			}
		}
	}

	return metadata, body
}

// splitOnHeaders splits Markdown content on ## (h2) headers.
// Each h2 section becomes one memory unit. h3/h4 are kept within the h2 section.
func splitOnHeaders(content string, absPath string, metadata map[string]string) []RawMemory {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var memories []RawMemory

	var currentSection string
	var currentLines []string
	var sectionStartLine int
	lineNum := 0
	inCodeBlock := false
	h2Re := regexp.MustCompile(`^##\s+(.+)`)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Track code blocks to avoid splitting inside them
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeBlock = !inCodeBlock
		}

		if inCodeBlock {
			currentLines = append(currentLines, line)
			continue
		}

		if match := h2Re.FindStringSubmatch(line); match != nil {
			// Save previous section if non-empty
			if currentSection != "" || len(currentLines) > 0 {
				text := strings.TrimSpace(strings.Join(currentLines, "\n"))
				if text != "" {
					mem := RawMemory{
						Content:       text,
						SourceFile:    absPath,
						SourceLine:    sectionStartLine,
						SourceSection: currentSection,
						Metadata:      copyMetadata(metadata),
					}
					memories = append(memories, mem)
				}
			}

			currentSection = strings.TrimSpace(match[1])
			currentLines = nil
			sectionStartLine = lineNum + 1 // content starts on next line
			continue
		}

		// Skip h1 headers (top-level title, not a section boundary)
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") && currentSection == "" {
			// Store as metadata if it's the document title
			if metadata == nil {
				metadata = make(map[string]string)
			}
			metadata["title"] = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			sectionStartLine = lineNum + 1
			continue
		}

		currentLines = append(currentLines, line)
		if sectionStartLine == 0 {
			sectionStartLine = lineNum
		}
	}

	// Save last section
	if currentSection != "" || len(currentLines) > 0 {
		text := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if text != "" {
			mem := RawMemory{
				Content:       text,
				SourceFile:    absPath,
				SourceLine:    sectionStartLine,
				SourceSection: currentSection,
				Metadata:      copyMetadata(metadata),
			}
			memories = append(memories, mem)
		}
	}

	return memories
}

// splitOnParagraphs splits content on double newlines.
func splitOnParagraphs(content string, absPath string, metadata map[string]string) []RawMemory {
	var memories []RawMemory
	paragraphs := strings.Split(content, "\n\n")
	lineNum := 1

	for _, para := range paragraphs {
		text := strings.TrimSpace(para)
		if text == "" {
			lineNum += strings.Count(para, "\n") + 2
			continue
		}

		mem := RawMemory{
			Content:       text,
			SourceFile:    absPath,
			SourceLine:    lineNum,
			SourceSection: "",
			Metadata:      copyMetadata(metadata),
		}
		memories = append(memories, mem)
		lineNum += strings.Count(para, "\n") + 2
	}

	return memories
}

// hasH2Headers checks if the content contains any h2 (## ) headers.
func hasH2Headers(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	h2Re := regexp.MustCompile(`^##\s+(.+)`)
	for scanner.Scan() {
		if h2Re.MatchString(scanner.Text()) {
			return true
		}
	}
	return false
}

// copyMetadata creates a copy of the metadata map (or nil if empty).
func copyMetadata(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
