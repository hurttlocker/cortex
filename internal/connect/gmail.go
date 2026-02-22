package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GmailProvider imports email threads from Gmail via the `gog` CLI.
// This is a gog-bridge connector — it uses the gog CLI (already configured
// for OpenClaw users) instead of implementing OAuth from scratch.
type GmailProvider struct{}

// GmailConfig holds the configuration for the Gmail connector.
type GmailConfig struct {
	// Account is the Gmail account email (e.g., "user@gmail.com").
	Account string `json:"account"`

	// Query is the Gmail search query (e.g., "newer_than:7d", "from:boss@co.com").
	// Default: "newer_than:7d"
	Query string `json:"query,omitempty"`

	// MaxResults limits the number of threads per sync. Default: 50.
	MaxResults int `json:"max_results,omitempty"`

	// IncludeBodies controls whether full message bodies are fetched.
	// When false (default), only thread metadata (subject, from, date, labels) is stored.
	// When true, fetches full message content (slower, more storage).
	IncludeBodies bool `json:"include_bodies,omitempty"`

	// SkipCategories is a list of Gmail categories to skip (e.g., ["CATEGORY_PROMOTIONS", "CATEGORY_SOCIAL"]).
	SkipCategories []string `json:"skip_categories,omitempty"`

	// Project is the Cortex project tag for imported memories.
	Project string `json:"project,omitempty"`

	// GogPath overrides the gog binary path. Default: auto-detected from PATH.
	GogPath string `json:"gog_path,omitempty"`
}

func (c *GmailConfig) maxResults() int {
	if c.MaxResults <= 0 {
		return 50
	}
	if c.MaxResults > 500 {
		return 500
	}
	return c.MaxResults
}

func (c *GmailConfig) query() string {
	if c.Query == "" {
		return "newer_than:7d"
	}
	return c.Query
}

func (c *GmailConfig) gogBinary() string {
	if c.GogPath != "" {
		return c.GogPath
	}
	return "gog"
}

func (c *GmailConfig) shouldSkip(labels []string) bool {
	if len(c.SkipCategories) == 0 {
		return false
	}
	skipSet := make(map[string]bool, len(c.SkipCategories))
	for _, cat := range c.SkipCategories {
		skipSet[strings.ToUpper(cat)] = true
	}
	for _, label := range labels {
		if skipSet[strings.ToUpper(label)] {
			return true
		}
	}
	return false
}

func init() {
	DefaultRegistry.Register(&GmailProvider{})
}

func (g *GmailProvider) Name() string        { return "gmail" }
func (g *GmailProvider) DisplayName() string { return "Gmail (via gog)" }

func (g *GmailProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "account": "user@gmail.com",
  "query": "newer_than:7d",
  "max_results": 50,
  "include_bodies": false,
  "skip_categories": ["CATEGORY_PROMOTIONS", "CATEGORY_SOCIAL", "CATEGORY_UPDATES"],
  "project": ""
}`)
}

func (g *GmailProvider) ValidateConfig(config json.RawMessage) error {
	var cfg GmailConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if cfg.Account == "" {
		return fmt.Errorf("account email is required")
	}
	if !strings.Contains(cfg.Account, "@") {
		return fmt.Errorf("account must be a valid email address")
	}

	// Check gog is available
	gogPath := cfg.gogBinary()
	if _, err := exec.LookPath(gogPath); err != nil {
		return fmt.Errorf("gog CLI not found (looked for %q). Install gog or set gog_path in config. See: https://github.com/pterm/gog", gogPath)
	}

	return nil
}

func (g *GmailProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	var cfg GmailConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := g.ValidateConfig(config); err != nil {
		return nil, err
	}

	// Build query — if we have a since timestamp, override the query window
	query := cfg.query()
	if since != nil {
		// Gmail's after: uses epoch seconds
		query = fmt.Sprintf("after:%d", since.Unix())
	}

	// Step 1: Search for threads
	threads, err := gogGmailSearch(ctx, cfg.gogBinary(), cfg.Account, query, cfg.maxResults())
	if err != nil {
		return nil, fmt.Errorf("gmail search failed: %w", err)
	}

	var records []Record
	for _, thread := range threads {
		// Skip unwanted categories
		if cfg.shouldSkip(thread.Labels) {
			continue
		}

		if cfg.IncludeBodies {
			// Fetch full thread content
			fullThread, err := gogGmailThreadGet(ctx, cfg.gogBinary(), cfg.Account, thread.ID)
			if err != nil {
				// Non-fatal: fall back to metadata-only
				records = append(records, threadToRecord(thread, cfg.Project))
				continue
			}
			records = append(records, fullThreadToRecord(thread, fullThread, cfg.Project))
		} else {
			records = append(records, threadToRecord(thread, cfg.Project))
		}
	}

	return records, nil
}

// threadToRecord converts a Gmail thread listing to a metadata-only Cortex Record.
func threadToRecord(t gogThread, project string) Record {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Email: %s\n", t.Subject)
	fmt.Fprintf(&sb, "From: %s\n", t.From)
	fmt.Fprintf(&sb, "Date: %s\n", t.Date)
	if t.MessageCount > 1 {
		fmt.Fprintf(&sb, "Messages: %d\n", t.MessageCount)
	}
	if len(t.Labels) > 0 {
		// Filter out internal labels for readability
		visible := filterVisibleLabels(t.Labels)
		if len(visible) > 0 {
			fmt.Fprintf(&sb, "Labels: %s\n", strings.Join(visible, ", "))
		}
	}

	return Record{
		Content:    sb.String(),
		Source:     fmt.Sprintf("thread/%s", t.ID),
		Section:    t.Subject,
		Project:    project,
		Timestamp:  parseGogDate(t.Date),
		ExternalID: fmt.Sprintf("gmail:thread/%s", t.ID),
	}
}

// fullThreadToRecord converts a thread with full message bodies to a Cortex Record.
func fullThreadToRecord(t gogThread, full *gogFullThread, project string) Record {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Email: %s\n", t.Subject)
	fmt.Fprintf(&sb, "From: %s\n", t.From)
	fmt.Fprintf(&sb, "Date: %s\n", t.Date)
	if t.MessageCount > 1 {
		fmt.Fprintf(&sb, "Messages: %d\n", t.MessageCount)
	}
	sb.WriteString("\n")

	// Add message bodies
	for i, msg := range full.Messages {
		from := getHeader(msg.Payload.Headers, "From")
		subject := getHeader(msg.Payload.Headers, "Subject")
		date := getHeader(msg.Payload.Headers, "Date")

		if i > 0 {
			sb.WriteString("\n---\n")
		}
		if from != "" {
			fmt.Fprintf(&sb, "From: %s\n", from)
		}
		if subject != "" && i == 0 {
			fmt.Fprintf(&sb, "Subject: %s\n", subject)
		}
		if date != "" {
			fmt.Fprintf(&sb, "Date: %s\n", date)
		}
		sb.WriteString("\n")

		body := extractBody(msg.Payload)
		if body != "" {
			// Truncate very long bodies
			if len(body) > 3000 {
				body = body[:3000] + "\n... (truncated)"
			}
			sb.WriteString(body)
		}
	}

	content := sb.String()
	// Cap total content
	if len(content) > 8000 {
		content = content[:8000] + "\n... (truncated)"
	}

	return Record{
		Content:    content,
		Source:     fmt.Sprintf("thread/%s", t.ID),
		Section:    t.Subject,
		Project:    project,
		Timestamp:  parseGogDate(t.Date),
		ExternalID: fmt.Sprintf("gmail:thread/%s", t.ID),
	}
}

// --- gog CLI interface ---

// gogThread represents a thread from `gog gmail search -j --results-only`.
type gogThread struct {
	ID           string   `json:"id"`
	Subject      string   `json:"subject"`
	From         string   `json:"from"`
	Date         string   `json:"date"`
	Labels       []string `json:"labels"`
	MessageCount int      `json:"messageCount"`
}

// gogFullThread represents the full thread from `gog gmail thread get -j --results-only --full`.
type gogFullThread struct {
	Messages []gogMessage `json:"messages"`
}

type gogMessage struct {
	ID      string     `json:"id"`
	Payload gogPayload `json:"payload"`
}

type gogPayload struct {
	Headers []gogHeader `json:"headers"`
	Body    gogBody     `json:"body"`
	Parts   []gogPart   `json:"parts,omitempty"`
}

type gogHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gogBody struct {
	Data string `json:"data,omitempty"`
	Size int    `json:"size"`
}

type gogPart struct {
	MimeType string  `json:"mimeType"`
	Body     gogBody `json:"body"`
	Parts    []gogPart `json:"parts,omitempty"`
}

// gogGmailSearch runs `gog gmail search` and parses the JSON output.
func gogGmailSearch(ctx context.Context, gogPath, account, query string, maxResults int) ([]gogThread, error) {
	args := []string{
		"gmail", "search", query,
		"--account", account,
		"-j", "--results-only",
		"--max", fmt.Sprintf("%d", maxResults),
		"--no-input",
	}

	cmd := exec.CommandContext(ctx, gogPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gog gmail search failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("running gog: %w", err)
	}

	var threads []gogThread
	if err := json.Unmarshal(out, &threads); err != nil {
		return nil, fmt.Errorf("parsing gog output: %w", err)
	}

	return threads, nil
}

// gogGmailThreadGet runs `gog gmail thread get` and parses the JSON output.
func gogGmailThreadGet(ctx context.Context, gogPath, account, threadID string) (*gogFullThread, error) {
	args := []string{
		"gmail", "thread", "get", threadID,
		"--account", account,
		"-j", "--results-only", "--full",
		"--no-input",
	}

	cmd := exec.CommandContext(ctx, gogPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gog thread get failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("running gog: %w", err)
	}

	// gog wraps the thread in {"thread": {...}, "downloaded": ...}
	var wrapper struct {
		Thread gogFullThread `json:"thread"`
	}
	if err := json.Unmarshal(out, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing gog thread output: %w", err)
	}

	return &wrapper.Thread, nil
}

// --- helpers ---

func getHeader(headers []gogHeader, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// extractBody walks the MIME parts tree to find text/plain content.
func extractBody(payload gogPayload) string {
	// Direct body
	if payload.Body.Data != "" {
		return payload.Body.Data
	}

	// Walk parts for text/plain
	for _, part := range payload.Parts {
		if part.MimeType == "text/plain" && part.Body.Data != "" {
			return part.Body.Data
		}
		// Recurse into multipart
		if len(part.Parts) > 0 {
			for _, sub := range part.Parts {
				if sub.MimeType == "text/plain" && sub.Body.Data != "" {
					return sub.Body.Data
				}
			}
		}
	}

	return ""
}

// filterVisibleLabels removes internal Gmail labels for cleaner display.
func filterVisibleLabels(labels []string) []string {
	var visible []string
	for _, l := range labels {
		switch l {
		case "INBOX", "UNREAD", "SENT", "DRAFT", "SPAM", "TRASH",
			"STARRED", "IMPORTANT":
			// Skip standard system labels
			continue
		default:
			// Show category labels in readable form
			if strings.HasPrefix(l, "CATEGORY_") {
				visible = append(visible, strings.ToLower(strings.TrimPrefix(l, "CATEGORY_")))
			} else {
				visible = append(visible, l)
			}
		}
	}
	return visible
}

// parseGogDate parses the date format from gog output ("2026-02-22 11:21").
func parseGogDate(s string) time.Time {
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
