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
// Splits on any header level (h2, h3, h4, etc.). Each section becomes one memory unit.
// Builds hierarchical section paths like "Trading > Crypto > Strategy".
// If no headers (h2+) are found, splits on double newlines (paragraphs).
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

	// Try splitting on headers (any level h2+)
	if hasHeaders(body) {
		sections := splitOnAllHeaders(body, absPath, metadata)
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

// headerRe matches any markdown header level 1-6.
var headerRe = regexp.MustCompile(`^(#{1,6})\s+(.+)`)

// splitOnAllHeaders splits Markdown content on any header (h2, h3, h4, h5, h6).
// h1 is treated as a document title, not a section boundary.
// Builds hierarchical section paths: "Trading > Crypto > Strategy" for nested headers.
func splitOnAllHeaders(content string, absPath string, metadata map[string]string) []RawMemory {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var memories []RawMemory

	// headerStack tracks the current header hierarchy.
	// Index 0 = h2, index 1 = h3, index 2 = h4, etc.
	headerStack := make([]string, 5) // h2 through h6

	var currentLines []string
	var sectionStartLine int
	var currentSectionPath string
	lineNum := 0
	inCodeBlock := false

	flushSection := func() {
		if len(currentLines) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if text == "" {
			return
		}
		mem := RawMemory{
			Content:       text,
			SourceFile:    absPath,
			SourceLine:    sectionStartLine,
			SourceSection: currentSectionPath,
			Metadata:      copyMetadata(metadata),
		}
		memories = append(memories, mem)
		currentLines = nil
	}

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

		if match := headerRe.FindStringSubmatch(line); match != nil {
			level := len(match[1]) // number of # characters
			title := strings.TrimSpace(match[2])

			if level == 1 {
				// h1 = document title, store as metadata, not a section boundary
				if metadata == nil {
					metadata = make(map[string]string)
				}
				metadata["title"] = title
				if sectionStartLine == 0 {
					sectionStartLine = lineNum + 1
				}
				continue
			}

			// h2+ = section boundary. Flush current section.
			flushSection()

			// Update header stack. Clear all levels below this one.
			// h2 = index 0, h3 = index 1, h4 = index 2, etc.
			stackIdx := level - 2
			if stackIdx >= 0 && stackIdx < len(headerStack) {
				headerStack[stackIdx] = title
				// Clear deeper levels
				for i := stackIdx + 1; i < len(headerStack); i++ {
					headerStack[i] = ""
				}
			}

			// Build section path from stack: "Trading > Crypto > Strategy"
			currentSectionPath = buildSectionPath(headerStack)
			sectionStartLine = lineNum + 1
			continue
		}

		currentLines = append(currentLines, line)
		if sectionStartLine == 0 {
			sectionStartLine = lineNum
		}
	}

	// Flush last section
	flushSection()

	return memories
}

// buildSectionPath creates a hierarchical path from the header stack.
// Skips empty levels so h2→h4 (no h3) still produces "Trading > Strategy".
// e.g., ["Trading", "", "Strategy", "", ""] → "Trading > Strategy"
func buildSectionPath(stack []string) string {
	var parts []string
	for _, h := range stack {
		if h != "" {
			parts = append(parts, h)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " > ")
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

// hasHeaders checks if the content contains any h2+ headers.
func hasHeaders(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	re := regexp.MustCompile(`^#{2,6}\s+(.+)`)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
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
