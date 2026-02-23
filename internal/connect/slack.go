package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SlackProvider imports channel messages and threads from Slack.
type SlackProvider struct{}

// SlackConfig holds the configuration for the Slack connector.
type SlackConfig struct {
	// Token is a Slack Bot User OAuth Token (xoxb-...).
	Token string `json:"token"`

	// Channels is a list of channel IDs to sync (e.g., ["C01234GENERAL"]).
	Channels []string `json:"channels"`

	// DaysBack controls how far back the initial full sync goes. Default: 30.
	DaysBack int `json:"days_back,omitempty"`

	// IncludeThreads controls whether thread replies are fetched. Default: true.
	IncludeThreads *bool `json:"include_threads,omitempty"`

	// Project is the Cortex project tag for imported memories.
	Project string `json:"project,omitempty"`
}

func (c *SlackConfig) daysBack() int {
	if c.DaysBack <= 0 {
		return 30
	}
	if c.DaysBack > 365 {
		return 365
	}
	return c.DaysBack
}

func (c *SlackConfig) includeThreads() bool {
	return c.IncludeThreads == nil || *c.IncludeThreads
}

func init() {
	DefaultRegistry.Register(&SlackProvider{})
}

func (s *SlackProvider) Name() string        { return "slack" }
func (s *SlackProvider) DisplayName() string { return "Slack" }

func (s *SlackProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "token": "xoxb-your-bot-token",
  "channels": ["C01234GENERAL"],
  "days_back": 30,
  "include_threads": true,
  "project": ""
}`)
}

func (s *SlackProvider) ValidateConfig(config json.RawMessage) error {
	var cfg SlackConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if cfg.Token == "" {
		return fmt.Errorf("token is required (Slack Bot User OAuth Token, xoxb-...)")
	}
	if !strings.HasPrefix(cfg.Token, "xoxb-") && !strings.HasPrefix(cfg.Token, "xoxp-") {
		return fmt.Errorf("token should start with xoxb- (bot) or xoxp- (user)")
	}
	if len(cfg.Channels) == 0 {
		return fmt.Errorf("at least one channel ID is required")
	}
	for _, ch := range cfg.Channels {
		if ch == "" {
			return fmt.Errorf("channel ID cannot be empty")
		}
	}
	return nil
}

func (s *SlackProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	var cfg SlackConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Determine sync window
	var oldest string
	if since != nil {
		oldest = fmt.Sprintf("%d.000000", since.Unix())
	} else {
		cutoff := time.Now().AddDate(0, 0, -cfg.daysBack())
		oldest = fmt.Sprintf("%d.000000", cutoff.Unix())
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var allRecords []Record

	for _, channelID := range cfg.Channels {
		records, err := s.fetchChannel(ctx, client, cfg, channelID, oldest)
		if err != nil {
			return nil, fmt.Errorf("fetching channel %s: %w", channelID, err)
		}
		allRecords = append(allRecords, records...)
	}

	return allRecords, nil
}

// fetchChannel retrieves messages from a single Slack channel with pagination.
func (s *SlackProvider) fetchChannel(ctx context.Context, client *http.Client, cfg SlackConfig, channelID, oldest string) ([]Record, error) {
	var records []Record
	cursor := ""
	pageCount := 0
	const maxPages = 10 // safety cap consistent with other providers

	for {
		if pageCount >= maxPages {
			break
		}
		pageCount++

		msgs, nextCursor, err := slackConversationsHistory(ctx, client, cfg.Token, channelID, oldest, cursor)
		if err != nil {
			return nil, err
		}

		for _, msg := range msgs {
			// Skip bot messages and system messages
			if msg.SubType != "" && msg.SubType != "file_share" && msg.SubType != "thread_broadcast" {
				continue
			}

			record := slackMessageToRecord(msg, channelID, cfg.Project)
			records = append(records, record)

			// Fetch thread replies if this message has a thread
			if cfg.includeThreads() && msg.ReplyCount > 0 {
				threadRecords, err := s.fetchThread(ctx, client, cfg, channelID, msg.TS)
				if err != nil {
					// Non-fatal: skip thread on error
					continue
				}
				records = append(records, threadRecords...)
			}
		}

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return records, nil
}

// fetchThread retrieves replies in a thread.
func (s *SlackProvider) fetchThread(ctx context.Context, client *http.Client, cfg SlackConfig, channelID, threadTS string) ([]Record, error) {
	var records []Record
	cursor := ""
	pageCount := 0
	const maxPages = 5 // threads shouldn't be too deep

	for {
		if pageCount >= maxPages {
			break
		}
		pageCount++

		replies, nextCursor, err := slackConversationsReplies(ctx, client, cfg.Token, channelID, threadTS, cursor)
		if err != nil {
			return nil, err
		}

		for _, reply := range replies {
			// Skip the parent message (already captured)
			if reply.TS == threadTS {
				continue
			}

			record := slackReplyToRecord(reply, channelID, threadTS, cfg.Project)
			records = append(records, record)
		}

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return records, nil
}

// --- Slack API types ---

type slackMessage struct {
	Type       string `json:"type"`
	SubType    string `json:"subtype"`
	User       string `json:"user"`
	Text       string `json:"text"`
	TS         string `json:"ts"`
	ReplyCount int    `json:"reply_count"`
	ThreadTS   string `json:"thread_ts"`
}

type slackHistoryResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error"`
	Messages         []slackMessage `json:"messages"`
	HasMore          bool           `json:"has_more"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

type slackRepliesResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error"`
	Messages         []slackMessage `json:"messages"`
	HasMore          bool           `json:"has_more"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// --- Slack API calls ---

func slackConversationsHistory(ctx context.Context, client *http.Client, token, channelID, oldest, cursor string) ([]slackMessage, string, error) {
	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("limit", "200")
	if oldest != "" {
		params.Set("oldest", oldest)
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, err := slackAPICall(ctx, client, token, "conversations.history", params)
	if err != nil {
		return nil, "", err
	}

	var resp slackHistoryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("parsing Slack response: %w", err)
	}
	if !resp.OK {
		return nil, "", fmt.Errorf("Slack API error: %s", resp.Error)
	}

	nextCursor := ""
	if resp.HasMore {
		nextCursor = resp.ResponseMetadata.NextCursor
	}

	return resp.Messages, nextCursor, nil
}

func slackConversationsReplies(ctx context.Context, client *http.Client, token, channelID, threadTS, cursor string) ([]slackMessage, string, error) {
	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("ts", threadTS)
	params.Set("limit", "200")
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, err := slackAPICall(ctx, client, token, "conversations.replies", params)
	if err != nil {
		return nil, "", err
	}

	var resp slackRepliesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("parsing Slack replies: %w", err)
	}
	if !resp.OK {
		return nil, "", fmt.Errorf("Slack API error: %s", resp.Error)
	}

	nextCursor := ""
	if resp.HasMore {
		nextCursor = resp.ResponseMetadata.NextCursor
	}

	return resp.Messages, nextCursor, nil
}

func slackAPICall(ctx context.Context, client *http.Client, token, method string, params url.Values) ([]byte, error) {
	apiURL := fmt.Sprintf("https://slack.com/api/%s?%s", method, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Slack API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("Slack rate limited (429) — retry later")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Slack API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading Slack response: %w", err)
	}

	return body, nil
}

// --- Record conversion ---

func slackMessageToRecord(msg slackMessage, channelID, project string) Record {
	var sb strings.Builder
	if msg.User != "" {
		fmt.Fprintf(&sb, "User: %s\n", msg.User)
	}
	fmt.Fprintf(&sb, "Channel: %s\n", channelID)
	fmt.Fprintf(&sb, "Time: %s\n", parseSlackTS(msg.TS).Format(time.RFC3339))
	if msg.ReplyCount > 0 {
		fmt.Fprintf(&sb, "Thread replies: %d\n", msg.ReplyCount)
	}
	sb.WriteString("\n")
	sb.WriteString(msg.Text)

	content := sb.String()
	if len(content) > 8000 {
		content = content[:8000] + "\n... (truncated)"
	}

	return Record{
		Content:    content,
		Source:     fmt.Sprintf("channel/%s/msg/%s", channelID, msg.TS),
		Section:    truncateSection(msg.Text, 100),
		Project:    project,
		Timestamp:  parseSlackTS(msg.TS),
		ExternalID: fmt.Sprintf("slack:channel/%s/msg/%s", channelID, msg.TS),
	}
}

func slackReplyToRecord(msg slackMessage, channelID, threadTS, project string) Record {
	var sb strings.Builder
	if msg.User != "" {
		fmt.Fprintf(&sb, "User: %s\n", msg.User)
	}
	fmt.Fprintf(&sb, "Channel: %s\n", channelID)
	fmt.Fprintf(&sb, "Thread: %s\n", threadTS)
	fmt.Fprintf(&sb, "Time: %s\n", parseSlackTS(msg.TS).Format(time.RFC3339))
	sb.WriteString("\n")
	sb.WriteString(msg.Text)

	content := sb.String()
	if len(content) > 3000 {
		content = content[:3000] + "\n... (truncated)"
	}

	return Record{
		Content:    content,
		Source:     fmt.Sprintf("channel/%s/thread/%s/reply/%s", channelID, threadTS, msg.TS),
		Section:    truncateSection(msg.Text, 100),
		Project:    project,
		Timestamp:  parseSlackTS(msg.TS),
		ExternalID: fmt.Sprintf("slack:channel/%s/thread/%s/%s", channelID, threadTS, msg.TS),
	}
}

// parseSlackTS converts a Slack timestamp (e.g., "1234567890.123456") to time.Time.
func parseSlackTS(ts string) time.Time {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) == 0 {
		return time.Time{}
	}
	var sec int64
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return time.Time{}
		}
		sec = sec*10 + int64(c-'0')
	}
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// truncateSection returns the first N chars of text as a section header.
func truncateSection(text string, maxLen int) string {
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > maxLen {
		return text[:maxLen] + "..."
	}
	return text
}
