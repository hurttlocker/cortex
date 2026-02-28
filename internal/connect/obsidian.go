package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ObsidianProvider imports notes from an Obsidian vault into Cortex.
type ObsidianProvider struct{}

// ObsidianConfig holds the configuration for the Obsidian connector.
type ObsidianConfig struct {
	// VaultPath is the root directory of the Obsidian vault.
	VaultPath string `json:"vault_path"`

	// IncludeDirs limits sync to specific subdirectories (empty = all).
	IncludeDirs []string `json:"include_dirs,omitempty"`

	// ExcludeDirs skips these directories (default: .obsidian, .trash, _cortex).
	ExcludeDirs []string `json:"exclude_dirs,omitempty"`

	// IncludeTags only syncs notes with at least one of these tags.
	IncludeTags []string `json:"include_tags,omitempty"`

	// MaxFileSize is the maximum file size in bytes to import (default: 100KB).
	MaxFileSize int64 `json:"max_file_size,omitempty"`

	// Project is the Cortex project tag for imported memories.
	Project string `json:"project,omitempty"`
}

func init() {
	DefaultRegistry.Register(&ObsidianProvider{})
}

func (o *ObsidianProvider) Name() string        { return "obsidian" }
func (o *ObsidianProvider) DisplayName() string { return "Obsidian" }

func (o *ObsidianProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "vault_path": "~/Documents/MyVault",
  "include_dirs": [],
  "exclude_dirs": [".obsidian", ".trash", "_cortex", "node_modules"],
  "include_tags": [],
  "max_file_size": 102400,
  "project": ""
}`)
}

func (o *ObsidianProvider) ValidateConfig(config json.RawMessage) error {
	var cfg ObsidianConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if cfg.VaultPath == "" {
		return fmt.Errorf("vault_path is required")
	}
	// Expand ~
	vaultPath := expandHome(cfg.VaultPath)
	info, err := os.Stat(vaultPath)
	if err != nil {
		return fmt.Errorf("vault_path %q not found: %w", vaultPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("vault_path %q is not a directory", vaultPath)
	}
	return nil
}

func (o *ObsidianProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	var cfg ObsidianConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	vaultPath := expandHome(cfg.VaultPath)

	// Defaults
	excludeDirs := cfg.ExcludeDirs
	if len(excludeDirs) == 0 {
		excludeDirs = []string{".obsidian", ".trash", "_cortex", "node_modules"}
	}
	maxSize := cfg.MaxFileSize
	if maxSize == 0 {
		maxSize = 102400 // 100KB
	}

	excludeSet := make(map[string]bool)
	for _, d := range excludeDirs {
		excludeSet[d] = true
	}

	includeTagSet := make(map[string]bool)
	for _, t := range cfg.IncludeTags {
		includeTagSet[strings.ToLower(t)] = true
	}

	var records []Record

	// Walk vault
	err := filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip excluded directories
		if info.IsDir() {
			base := info.Name()
			if excludeSet[base] || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			// Check include_dirs filter
			if len(cfg.IncludeDirs) > 0 {
				rel, _ := filepath.Rel(vaultPath, path)
				matched := false
				for _, inc := range cfg.IncludeDirs {
					if strings.HasPrefix(rel, inc) || rel == "." {
						matched = true
						break
					}
				}
				if !matched && rel != "." {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Only markdown files
		if filepath.Ext(path) != ".md" {
			return nil
		}

		// Skip oversized files
		if info.Size() > maxSize {
			return nil
		}

		// Incremental: skip files not modified since last sync
		if since != nil && info.ModTime().Before(*since) {
			return nil
		}

		// Read file
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable
		}
		content := string(data)

		// Parse frontmatter
		fm := parseFrontmatter(content)
		body := stripFrontmatter(content)

		// Tag filter
		if len(includeTagSet) > 0 {
			tags := extractTags(fm, body)
			if !hasMatchingTag(tags, includeTagSet) {
				return nil
			}
		}

		// Build record
		rel, _ := filepath.Rel(vaultPath, path)
		source := fmt.Sprintf("obsidian:%s", rel)
		section := strings.TrimSuffix(filepath.Base(path), ".md")

		// Resolve wikilinks to plain text for better extraction
		cleanBody := resolveWikilinks(body)

		record := Record{
			Content:     cleanBody,
			Source:      source,
			Section:     section,
			Project:     cfg.Project,
			MemoryClass: classifyNoteType(fm, section),
		}

		records = append(records, record)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking vault: %w", err)
	}

	return records, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────

var frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?`)
var wikiLinkRe = regexp.MustCompile(`\[\[([^|\]]+)(?:\|([^\]]+))?\]\]`)

func parseFrontmatter(content string) map[string]interface{} {
	m := frontmatterRe.FindStringSubmatch(content)
	if m == nil {
		return nil
	}
	var fm map[string]interface{}
	if err := yaml.Unmarshal([]byte(m[1]), &fm); err != nil {
		return nil
	}
	return fm
}

func stripFrontmatter(content string) string {
	return frontmatterRe.ReplaceAllString(content, "")
}

func extractTags(fm map[string]interface{}, body string) []string {
	var tags []string

	// From frontmatter
	if fm != nil {
		if v, ok := fm["tags"]; ok {
			switch t := v.(type) {
			case []interface{}:
				for _, item := range t {
					if s, ok := item.(string); ok {
						tags = append(tags, strings.ToLower(strings.TrimPrefix(s, "#")))
					}
				}
			case string:
				for _, part := range strings.Split(t, ",") {
					tags = append(tags, strings.ToLower(strings.TrimSpace(strings.TrimPrefix(part, "#"))))
				}
			}
		}
	}

	// Inline tags (#tag)
	tagRe := regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9/_-]+)`)
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		tags = append(tags, strings.ToLower(m[1]))
	}

	return tags
}

func hasMatchingTag(tags []string, filter map[string]bool) bool {
	for _, t := range tags {
		if filter[t] {
			return true
		}
		// Check parent (e.g., "trading/daily" matches "trading")
		parts := strings.SplitN(t, "/", 2)
		if len(parts) > 1 && filter[parts[0]] {
			return true
		}
	}
	return false
}

func resolveWikilinks(text string) string {
	return wikiLinkRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := wikiLinkRe.FindStringSubmatch(match)
		if len(sub) >= 3 && sub[2] != "" {
			return sub[2] // display text
		}
		if len(sub) >= 2 {
			return sub[1] // target
		}
		return match
	})
}

func classifyNoteType(fm map[string]interface{}, title string) string {
	if fm != nil {
		if v, ok := fm["hub_type"]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	titleLower := strings.ToLower(title)
	if strings.Contains(titleLower, "journal") || strings.Contains(titleLower, "trading") {
		return "trading"
	}
	if strings.Contains(titleLower, "dashboard") || strings.Contains(titleLower, "moc") {
		return "index"
	}
	return ""
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
