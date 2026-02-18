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
	var chunks []RawMemory
	if hasHeaders(body) {
		chunks = splitOnAllHeaders(body, absPath, metadata)
	}
	if len(chunks) == 0 {
		// Fallback: split on double newlines (paragraphs)
		chunks = splitOnParagraphs(body, absPath, metadata)
	}

	// Normalize: split oversized chunks, merge tiny fragments
	return normalizeChunks(chunks, 50, 1500), nil
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

// isGarbageChunk returns true for content that should be filtered out during import:
// - very short strings (< 20 chars after trimming)
// - purely numeric content (timestamps, IDs)
// - a single word (possibly quoted)
func isGarbageChunk(content string) bool {
	s := strings.TrimSpace(content)
	if len(s) < 20 {
		return true
	}
	// Purely numeric (timestamps, IDs)
	allDigits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	// Single word, optionally quoted (e.g. "ALERT" or ALERT)
	stripped := strings.Trim(s, `"'`+"`")
	if !strings.ContainsAny(stripped, " \t\n") {
		return true
	}
	return false
}

// normalizeChunks post-processes raw memory chunks to improve search quality:
// 1. Splits oversized chunks (>maxChars) on paragraph boundaries
// 2. Merges tiny chunks (<minChars) with neighbors
// This preserves provenance (source file, line, section) while keeping chunks
// in the sweet spot for both BM25 and semantic search.
func normalizeChunks(memories []RawMemory, minChars, maxChars int) []RawMemory {
	if minChars <= 0 {
		minChars = 50
	}
	if maxChars <= 0 {
		maxChars = 1500
	}

	// Phase 1: Split oversized chunks
	var split []RawMemory
	for _, mem := range memories {
		if len(mem.Content) <= maxChars {
			split = append(split, mem)
			continue
		}
		// Split on double newlines (paragraph boundaries)
		paragraphs := strings.Split(mem.Content, "\n\n")
		var current []string
		currentLen := 0
		lineOffset := 0

		flush := func() {
			if len(current) == 0 {
				return
			}
			text := strings.TrimSpace(strings.Join(current, "\n\n"))
			if text == "" {
				return
			}
			split = append(split, RawMemory{
				Content:       text,
				SourceFile:    mem.SourceFile,
				SourceLine:    mem.SourceLine + lineOffset,
				SourceSection: mem.SourceSection,
				Metadata:      copyMetadata(mem.Metadata),
			})
			lineOffset += countLines(strings.Join(current, "\n\n"))
			current = nil
			currentLen = 0
		}

		for _, para := range paragraphs {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			// If a single paragraph itself exceeds max, split it further
			if len(para) > maxChars {
				flush() // flush anything accumulated
				// Try splitting on single newlines first
				lines := strings.Split(para, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					// If a single line still exceeds max, hard-cut it
					for len(line) > maxChars {
						cut := maxChars
						// Try to cut at a space boundary
						if idx := strings.LastIndex(line[:cut], " "); idx > maxChars/2 {
							cut = idx
						}
						split = append(split, RawMemory{
							Content:       strings.TrimSpace(line[:cut]),
							SourceFile:    mem.SourceFile,
							SourceLine:    mem.SourceLine + lineOffset,
							SourceSection: mem.SourceSection,
							Metadata:      copyMetadata(mem.Metadata),
						})
						line = strings.TrimSpace(line[cut:])
					}
					if currentLen > 0 && currentLen+len(line)+1 > maxChars {
						flush()
					}
					current = append(current, line)
					currentLen += len(line) + 1
				}
				continue
			}
			// If adding this paragraph would exceed max, flush first
			if currentLen > 0 && currentLen+len(para)+2 > maxChars {
				flush()
			}
			current = append(current, para)
			currentLen += len(para) + 2
		}
		flush()
	}

	// Phase 2: Merge tiny chunks with neighbors (skip garbage)
	if len(split) <= 1 {
		if len(split) == 1 && isGarbageChunk(split[0].Content) {
			return nil
		}
		return split
	}
	var merged []RawMemory
	for i := 0; i < len(split); i++ {
		if isGarbageChunk(split[i].Content) {
			continue
		}
		if len(split[i].Content) >= minChars {
			merged = append(merged, split[i])
			continue
		}
		// Tiny chunk — merge with previous if same source file and won't exceed max, otherwise next
		if len(merged) > 0 && merged[len(merged)-1].SourceFile == split[i].SourceFile &&
			len(merged[len(merged)-1].Content)+len(split[i].Content)+2 < maxChars {
			merged[len(merged)-1].Content += "\n\n" + split[i].Content
		} else if i+1 < len(split) && split[i+1].SourceFile == split[i].SourceFile &&
			len(split[i+1].Content)+len(split[i].Content)+2 < maxChars {
			split[i+1].Content = split[i].Content + "\n\n" + split[i+1].Content
			split[i+1].SourceLine = split[i].SourceLine
		} else {
			// Orphan tiny chunk from a different file — keep it
			merged = append(merged, split[i])
		}
	}

	// Final pass: remove any remaining garbage chunks
	var clean []RawMemory
	for _, m := range merged {
		if !isGarbageChunk(m.Content) {
			clean = append(clean, m)
		}
	}
	return clean
}

// countLines returns the number of newlines in a string.
func countLines(s string) int {
	return strings.Count(s, "\n") + 1
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
