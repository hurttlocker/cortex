package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	notionDefaultMaxPages = 500
	notionMaxPagesCap     = 2000
	notionVersion         = "2022-06-28"
)

var (
	notionAPIBaseURL    = "https://api.notion.com/v1"
	notionMinRequestGap = 350 * time.Millisecond // ~3 req/sec
)

// NotionProvider imports pages and databases from Notion.
type NotionProvider struct{}

// NotionConfig holds configuration for the Notion connector.
type NotionConfig struct {
	Token            string   `json:"token"`
	RootPageIDs      []string `json:"root_page_ids,omitempty"`
	IncludeDatabases *bool    `json:"include_databases,omitempty"`
	MaxPages         int      `json:"max_pages,omitempty"`
}

func (c *NotionConfig) includeDatabases() bool {
	return c.IncludeDatabases == nil || *c.IncludeDatabases
}

func (c *NotionConfig) maxPages() int {
	if c.MaxPages <= 0 {
		return notionDefaultMaxPages
	}
	if c.MaxPages > notionMaxPagesCap {
		return notionMaxPagesCap
	}
	return c.MaxPages
}

func init() {
	DefaultRegistry.Register(&NotionProvider{})
}

func (p *NotionProvider) Name() string        { return "notion" }
func (p *NotionProvider) DisplayName() string { return "Notion" }

func (p *NotionProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "token": "ntn_...",
  "root_page_ids": ["abc123"],
  "include_databases": true,
  "max_pages": 500
}`)
}

func (p *NotionProvider) ValidateConfig(config json.RawMessage) error {
	var cfg NotionConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return fmt.Errorf("token is required")
	}
	if !strings.HasPrefix(token, "ntn_") && !strings.HasPrefix(token, "secret_") {
		return fmt.Errorf("token should start with ntn_ (or secret_ for legacy integrations)")
	}
	for _, id := range cfg.RootPageIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("root_page_ids cannot contain empty values")
		}
	}

	return nil
}

func (p *NotionProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	if err := p.ValidateConfig(config); err != nil {
		return nil, err
	}

	var cfg NotionConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	client := newNotionClient(strings.TrimSpace(cfg.Token))
	searchResults, err := client.search(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("searching notion workspace: %w", err)
	}

	rootFilter := make(map[string]bool, len(cfg.RootPageIDs))
	for _, id := range cfg.RootPageIDs {
		rootFilter[strings.TrimSpace(id)] = true
	}
	filterRoots := len(rootFilter) > 0

	maxPages := cfg.maxPages()
	records := make([]Record, 0, maxPages)

	for _, item := range searchResults {
		if len(records) >= maxPages {
			break
		}
		if filterRoots && !rootFilter[item.ID] {
			continue
		}
		if since != nil && !item.LastEditedTime.After(*since) {
			continue
		}

		switch item.Object {
		case "page":
			blocks, err := client.getBlockChildren(ctx, item.ID)
			if err != nil {
				continue
			}
			content := strings.TrimSpace(notionBlocksToMarkdown(blocks))
			title := strings.TrimSpace(item.extractTitle())
			if content == "" {
				content = title
			}
			if content == "" {
				continue
			}
			records = append(records, Record{
				ExternalID:  fmt.Sprintf("notion:page/%s", item.ID),
				Content:     content,
				Source:      fmt.Sprintf("pages/%s", item.ID),
				Section:     title,
				MemoryClass: "reference",
				Timestamp:   item.LastEditedTime,
			})
		case "database":
			if !cfg.includeDatabases() {
				continue
			}
			rows, err := client.queryDatabase(ctx, item.ID, since)
			if err != nil {
				continue
			}
			for _, row := range rows {
				if len(records) >= maxPages {
					break
				}
				rowContent := strings.TrimSpace(row.toMarkdown())
				if rowContent == "" {
					continue
				}
				records = append(records, Record{
					ExternalID:  fmt.Sprintf("notion:db/%s/row/%s", item.ID, row.ID),
					Content:     rowContent,
					Source:      fmt.Sprintf("db/%s/row/%s", item.ID, row.ID),
					Section:     row.extractTitle(),
					MemoryClass: "reference",
					Timestamp:   row.LastEditedTime,
				})
			}
		}
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.Before(records[j].Timestamp)
	})

	return records, nil
}

type notionClient struct {
	token      string
	httpClient *http.Client
	mu         sync.Mutex
	lastReq    time.Time
}

func newNotionClient(token string) *notionClient {
	return &notionClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *notionClient) throttle() {
	if notionMinRequestGap <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if !c.lastReq.IsZero() {
		delta := now.Sub(c.lastReq)
		if delta < notionMinRequestGap {
			time.Sleep(notionMinRequestGap - delta)
		}
	}
	c.lastReq = time.Now()
}

func (c *notionClient) doJSON(ctx context.Context, method, path string, query url.Values, payload interface{}, out interface{}) error {
	c.throttle()

	endpoint := strings.TrimRight(notionAPIBaseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notion API %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *notionClient) search(ctx context.Context, since *time.Time) ([]notionSearchResult, error) {
	nextCursor := ""
	var all []notionSearchResult

	for {
		payload := map[string]interface{}{
			"page_size": 100,
			"sort": map[string]string{
				"direction": "descending",
				"timestamp": "last_edited_time",
			},
		}
		if nextCursor != "" {
			payload["start_cursor"] = nextCursor
		}
		if since != nil {
			payload["filter"] = map[string]interface{}{
				"timestamp": "last_edited_time",
				"last_edited_time": map[string]string{
					"on_or_after": since.Format(time.RFC3339),
				},
			}
		}

		var resp notionSearchResponse
		if err := c.doJSON(ctx, http.MethodPost, "/search", nil, payload, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Results...)

		if !resp.HasMore || strings.TrimSpace(resp.NextCursor) == "" {
			break
		}
		nextCursor = resp.NextCursor
	}

	return all, nil
}

func (c *notionClient) getBlockChildren(ctx context.Context, blockID string) ([]notionBlock, error) {
	var all []notionBlock
	nextCursor := ""
	for {
		query := url.Values{}
		query.Set("page_size", "100")
		if nextCursor != "" {
			query.Set("start_cursor", nextCursor)
		}

		var resp notionBlockChildrenResponse
		if err := c.doJSON(ctx, http.MethodGet, "/blocks/"+blockID+"/children", query, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Results...)
		if !resp.HasMore || strings.TrimSpace(resp.NextCursor) == "" {
			break
		}
		nextCursor = resp.NextCursor
	}
	return all, nil
}

func (c *notionClient) queryDatabase(ctx context.Context, dbID string, since *time.Time) ([]notionDatabaseRow, error) {
	payload := map[string]interface{}{"page_size": 100}
	if since != nil {
		payload["filter"] = map[string]interface{}{
			"timestamp": "last_edited_time",
			"last_edited_time": map[string]string{
				"on_or_after": since.Format(time.RFC3339),
			},
		}
	}

	var resp notionDatabaseQueryResponse
	if err := c.doJSON(ctx, http.MethodPost, "/databases/"+dbID+"/query", nil, payload, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

type notionSearchResponse struct {
	Results    []notionSearchResult `json:"results"`
	HasMore    bool                 `json:"has_more"`
	NextCursor string               `json:"next_cursor"`
}

type notionSearchResult struct {
	Object         string                    `json:"object"`
	ID             string                    `json:"id"`
	LastEditedTime time.Time                 `json:"last_edited_time"`
	Title          []notionRichText          `json:"title"`
	Properties     map[string]notionProperty `json:"properties"`
}

func (r notionSearchResult) extractTitle() string {
	if len(r.Title) > 0 {
		return joinNotionRichText(r.Title)
	}
	for _, prop := range r.Properties {
		if prop.Type == "title" && len(prop.Title) > 0 {
			return joinNotionRichText(prop.Title)
		}
	}
	return ""
}

type notionBlockChildrenResponse struct {
	Results    []notionBlock `json:"results"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor"`
}

type notionBlock struct {
	Type             string               `json:"type"`
	Paragraph        *notionTextContainer `json:"paragraph,omitempty"`
	Heading1         *notionTextContainer `json:"heading_1,omitempty"`
	Heading2         *notionTextContainer `json:"heading_2,omitempty"`
	Heading3         *notionTextContainer `json:"heading_3,omitempty"`
	BulletedListItem *notionTextContainer `json:"bulleted_list_item,omitempty"`
	NumberedListItem *notionTextContainer `json:"numbered_list_item,omitempty"`
	Callout          *notionTextContainer `json:"callout,omitempty"`
	Code             *notionCodeBlock     `json:"code,omitempty"`
}

type notionTextContainer struct {
	RichText []notionRichText `json:"rich_text"`
}

type notionCodeBlock struct {
	RichText []notionRichText `json:"rich_text"`
	Language string           `json:"language"`
}

type notionRichText struct {
	PlainText string `json:"plain_text"`
}

func notionBlocksToMarkdown(blocks []notionBlock) string {
	lines := make([]string, 0, len(blocks)*2)
	for _, b := range blocks {
		switch b.Type {
		case "heading_1":
			if b.Heading1 != nil {
				lines = append(lines, "# "+joinNotionRichText(b.Heading1.RichText))
			}
		case "heading_2":
			if b.Heading2 != nil {
				lines = append(lines, "## "+joinNotionRichText(b.Heading2.RichText))
			}
		case "heading_3":
			if b.Heading3 != nil {
				lines = append(lines, "### "+joinNotionRichText(b.Heading3.RichText))
			}
		case "bulleted_list_item":
			if b.BulletedListItem != nil {
				lines = append(lines, "- "+joinNotionRichText(b.BulletedListItem.RichText))
			}
		case "numbered_list_item":
			if b.NumberedListItem != nil {
				lines = append(lines, "1. "+joinNotionRichText(b.NumberedListItem.RichText))
			}
		case "callout":
			if b.Callout != nil {
				lines = append(lines, "> "+joinNotionRichText(b.Callout.RichText))
			}
		case "code":
			if b.Code != nil {
				code := joinNotionRichText(b.Code.RichText)
				lines = append(lines, "```"+strings.TrimSpace(b.Code.Language), code, "```")
			}
		case "paragraph":
			if b.Paragraph != nil {
				lines = append(lines, joinNotionRichText(b.Paragraph.RichText))
			}
		}
	}

	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		clean = append(clean, line)
	}
	return strings.Join(clean, "\n")
}

func joinNotionRichText(parts []notionRichText) string {
	if len(parts) == 0 {
		return ""
	}
	b := strings.Builder{}
	for _, part := range parts {
		b.WriteString(part.PlainText)
	}
	return strings.TrimSpace(b.String())
}

type notionDatabaseQueryResponse struct {
	Results []notionDatabaseRow `json:"results"`
}

type notionDatabaseRow struct {
	ID             string                    `json:"id"`
	LastEditedTime time.Time                 `json:"last_edited_time"`
	Properties     map[string]notionProperty `json:"properties"`
}

func (r notionDatabaseRow) extractTitle() string {
	for _, prop := range r.Properties {
		if prop.Type == "title" && len(prop.Title) > 0 {
			return joinNotionRichText(prop.Title)
		}
	}
	return ""
}

func (r notionDatabaseRow) toMarkdown() string {
	if len(r.Properties) == 0 {
		return ""
	}
	keys := make([]string, 0, len(r.Properties))
	for k := range r.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys)+1)
	title := r.extractTitle()
	if title != "" {
		lines = append(lines, "# "+title)
	}
	for _, key := range keys {
		value := r.Properties[key].asString()
		if value == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", key, value))
	}
	return strings.Join(lines, "\n")
}

type notionProperty struct {
	Type        string           `json:"type"`
	Title       []notionRichText `json:"title,omitempty"`
	RichText    []notionRichText `json:"rich_text,omitempty"`
	Select      *notionSelect    `json:"select,omitempty"`
	MultiSelect []notionSelect   `json:"multi_select,omitempty"`
	Number      *float64         `json:"number,omitempty"`
	Checkbox    *bool            `json:"checkbox,omitempty"`
	URL         string           `json:"url,omitempty"`
	Email       string           `json:"email,omitempty"`
	PhoneNumber string           `json:"phone_number,omitempty"`
	Date        *notionDate      `json:"date,omitempty"`
}

type notionSelect struct {
	Name string `json:"name"`
}

type notionDate struct {
	Start string `json:"start"`
}

func (p notionProperty) asString() string {
	switch p.Type {
	case "title":
		return joinNotionRichText(p.Title)
	case "rich_text":
		return joinNotionRichText(p.RichText)
	case "select":
		if p.Select != nil {
			return strings.TrimSpace(p.Select.Name)
		}
	case "multi_select":
		vals := make([]string, 0, len(p.MultiSelect))
		for _, s := range p.MultiSelect {
			if strings.TrimSpace(s.Name) != "" {
				vals = append(vals, strings.TrimSpace(s.Name))
			}
		}
		return strings.Join(vals, ", ")
	case "number":
		if p.Number != nil {
			return fmt.Sprintf("%v", *p.Number)
		}
	case "checkbox":
		if p.Checkbox != nil {
			if *p.Checkbox {
				return "true"
			}
			return "false"
		}
	case "url":
		return strings.TrimSpace(p.URL)
	case "email":
		return strings.TrimSpace(p.Email)
	case "phone_number":
		return strings.TrimSpace(p.PhoneNumber)
	case "date":
		if p.Date != nil {
			return strings.TrimSpace(p.Date.Start)
		}
	}
	return ""
}
