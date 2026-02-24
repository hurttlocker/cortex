package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	discordSnowflakeEpochMs = int64(1420070400000)
	discordDefaultLookback  = 30
	discordDefaultMaxMsgs   = 5000
	discordMaxLookbackDays  = 365
	discordMaxMessagesCap   = 20000
	discordGlobalReqDelay   = 20 * time.Millisecond // 50 req/sec fallback
)

var discordAPIBaseURL = "https://discord.com/api/v10"

// DiscordProvider imports channel messages and threads from Discord.
type DiscordProvider struct{}

// DiscordConfig holds configuration for the Discord connector.
type DiscordConfig struct {
	Token          string   `json:"token"`
	GuildID        string   `json:"guild_id"`
	ChannelIDs     []string `json:"channel_ids,omitempty"`
	IncludeThreads *bool    `json:"include_threads,omitempty"`
	IncludePins    *bool    `json:"include_pins,omitempty"`
	LookbackDays   int      `json:"lookback_days,omitempty"`
	MaxMessages    int      `json:"max_messages,omitempty"`
}

func (c *DiscordConfig) includeThreads() bool {
	return c.IncludeThreads == nil || *c.IncludeThreads
}

func (c *DiscordConfig) includePins() bool {
	return c.IncludePins == nil || *c.IncludePins
}

func (c *DiscordConfig) lookbackDays() int {
	if c.LookbackDays <= 0 {
		return discordDefaultLookback
	}
	if c.LookbackDays > discordMaxLookbackDays {
		return discordMaxLookbackDays
	}
	return c.LookbackDays
}

func (c *DiscordConfig) maxMessages() int {
	if c.MaxMessages <= 0 {
		return discordDefaultMaxMsgs
	}
	if c.MaxMessages > discordMaxMessagesCap {
		return discordMaxMessagesCap
	}
	return c.MaxMessages
}

func init() {
	DefaultRegistry.Register(&DiscordProvider{})
}

func (p *DiscordProvider) Name() string        { return "discord" }
func (p *DiscordProvider) DisplayName() string { return "Discord" }

func (p *DiscordProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "token": "Bot MTk...",
  "guild_id": "132962...",
  "channel_ids": ["147340..."],
  "include_threads": true,
  "include_pins": true,
  "lookback_days": 30,
  "max_messages": 5000
}`)
}

func (p *DiscordProvider) ValidateConfig(config json.RawMessage) error {
	var cfg DiscordConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return fmt.Errorf("token is required (Discord bot token prefixed with 'Bot ')")
	}
	if !strings.HasPrefix(token, "Bot ") {
		return fmt.Errorf("token must start with 'Bot '")
	}
	if strings.TrimSpace(strings.TrimPrefix(token, "Bot ")) == "" {
		return fmt.Errorf("token is missing bot credential after 'Bot '")
	}

	if strings.TrimSpace(cfg.GuildID) == "" {
		return fmt.Errorf("guild_id is required")
	}

	for _, ch := range cfg.ChannelIDs {
		if strings.TrimSpace(ch) == "" {
			return fmt.Errorf("channel_ids cannot contain empty values")
		}
	}

	return nil
}

func (p *DiscordProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	if err := p.ValidateConfig(config); err != nil {
		return nil, err
	}

	var cfg DiscordConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	client := newDiscordClient(strings.TrimSpace(cfg.Token))

	guildName, err := client.getGuildName(ctx, cfg.GuildID)
	if err != nil || strings.TrimSpace(guildName) == "" {
		guildName = cfg.GuildID
	}

	channels, err := client.listGuildChannels(ctx, cfg.GuildID)
	if err != nil {
		return nil, fmt.Errorf("listing guild channels: %w", err)
	}

	channelByID := make(map[string]discordChannel, len(channels))
	for _, ch := range channels {
		channelByID[ch.ID] = ch
	}

	selected := pickDiscordChannels(channels, cfg.ChannelIDs)
	if len(selected) == 0 {
		return nil, nil
	}

	var allRecords []Record
	for _, channel := range selected {
		records, err := p.fetchChannelRecords(ctx, client, cfg, guildName, channel, since, "")
		if err != nil {
			return nil, fmt.Errorf("fetching channel %s: %w", channel.ID, err)
		}
		allRecords = append(allRecords, records...)

		if cfg.includeThreads() {
			threads, err := client.listArchivedPublicThreads(ctx, channel.ID)
			if err != nil {
				continue // non-fatal
			}
			for _, thread := range threads {
				sourceChannelName := channel.Name
				if parent, ok := channelByID[thread.ParentID]; ok && strings.TrimSpace(parent.Name) != "" {
					sourceChannelName = parent.Name
				}
				threadRecords, err := p.fetchChannelRecords(ctx, client, cfg, guildName, discordChannel{
					ID:   thread.ID,
					Name: sourceChannelName,
				}, since, thread.Name)
				if err != nil {
					continue // non-fatal
				}
				allRecords = append(allRecords, threadRecords...)
			}
		}
	}

	sort.Slice(allRecords, func(i, j int) bool {
		return allRecords[i].Timestamp.Before(allRecords[j].Timestamp)
	})

	return allRecords, nil
}

func (p *DiscordProvider) fetchChannelRecords(ctx context.Context, client *discordClient, cfg DiscordConfig, guildName string, channel discordChannel, since *time.Time, threadSection string) ([]Record, error) {
	messages, err := client.listChannelMessages(ctx, channel.ID, since, cfg.lookbackDays(), cfg.maxMessages())
	if err != nil {
		return nil, err
	}

	msgByID := make(map[string]discordMessage, len(messages))
	for _, m := range messages {
		msgByID[m.ID] = m
	}

	if cfg.includePins() {
		pins, err := client.listPinnedMessages(ctx, channel.ID)
		if err == nil {
			for _, pin := range pins {
				pin.Pinned = true
				msgByID[pin.ID] = pin
			}
		}
	}

	keys := make([]string, 0, len(msgByID))
	for k := range msgByID {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	records := make([]Record, 0, len(keys))
	for _, id := range keys {
		msg := msgByID[id]
		ts, err := parseDiscordTimestamp(msg.Timestamp)
		if err != nil {
			ts, err = discordSnowflakeToTime(msg.ID)
			if err != nil {
				continue
			}
		}

		if since != nil && !ts.After(*since) {
			continue
		}

		text := strings.TrimSpace(msg.Content)
		if text == "" && len(msg.Attachments) > 0 {
			parts := make([]string, 0, len(msg.Attachments))
			for _, a := range msg.Attachments {
				if strings.TrimSpace(a.Filename) != "" {
					parts = append(parts, a.Filename)
				}
			}
			if len(parts) > 0 {
				text = "[attachments] " + strings.Join(parts, ", ")
			}
		}
		if text == "" {
			continue
		}

		author := strings.TrimSpace(msg.Author.Username)
		if author == "" {
			author = "unknown"
		}
		content := fmt.Sprintf("%s (%s): %s", author, ts.Format(time.RFC3339), text)
		if msg.Pinned {
			content = "[PINNED] " + content
		}

		sourceChannel := sanitizeDiscordPart(channel.Name)
		if sourceChannel == "" {
			sourceChannel = sanitizeDiscordPart(channel.ID)
		}
		sourceGuild := sanitizeDiscordPart(guildName)
		if sourceGuild == "" {
			sourceGuild = sanitizeDiscordPart(cfg.GuildID)
		}

		records = append(records, Record{
			ExternalID: fmt.Sprintf("discord:guild/%s/channel/%s/msg/%s", cfg.GuildID, channel.ID, msg.ID),
			Content:    content,
			Source:     fmt.Sprintf("guild/%s/channel/%s/msg/%s", sourceGuild, sourceChannel, msg.ID),
			Section:    strings.TrimSpace(threadSection),
			Timestamp:  ts,
		})
	}

	return records, nil
}

func pickDiscordChannels(all []discordChannel, configured []string) []discordChannel {
	if len(configured) == 0 {
		selected := make([]discordChannel, 0)
		for _, ch := range all {
			if isDiscordTextChannel(ch.Type) {
				selected = append(selected, ch)
			}
		}
		return selected
	}

	lookup := make(map[string]discordChannel, len(all))
	for _, ch := range all {
		lookup[ch.ID] = ch
	}

	selected := make([]discordChannel, 0, len(configured))
	for _, id := range configured {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if ch, ok := lookup[id]; ok {
			selected = append(selected, ch)
			continue
		}
		selected = append(selected, discordChannel{ID: id, Name: id, Type: 0})
	}
	return selected
}

func isDiscordTextChannel(channelType int) bool {
	return channelType == 0 || channelType == 5
}

func sanitizeDiscordPart(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

func parseDiscordTimestamp(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// discordSnowflakeToTime converts a Discord snowflake ID to timestamp.
func discordSnowflakeToTime(snowflake string) (time.Time, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(snowflake), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid snowflake %q: %w", snowflake, err)
	}
	ms := (id >> 22) + discordSnowflakeEpochMs
	return time.UnixMilli(ms).UTC(), nil
}

// timeToDiscordSnowflake converts timestamp to a Discord snowflake lower bound.
func timeToDiscordSnowflake(t time.Time) string {
	ms := t.UTC().UnixMilli() - discordSnowflakeEpochMs
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatInt(ms<<22, 10)
}

func discordRateLimitDelay(h http.Header) time.Duration {
	if strings.TrimSpace(h.Get("X-RateLimit-Remaining")) != "0" {
		return 0
	}
	resetAfter := strings.TrimSpace(h.Get("X-RateLimit-Reset-After"))
	if resetAfter == "" {
		return 0
	}
	seconds, err := strconv.ParseFloat(resetAfter, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

type discordClient struct {
	token      string
	httpClient *http.Client
	mu         sync.Mutex
	lastReq    time.Time
}

func newDiscordClient(token string) *discordClient {
	return &discordClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *discordClient) throttle() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if !c.lastReq.IsZero() {
		delta := now.Sub(c.lastReq)
		if delta < discordGlobalReqDelay {
			time.Sleep(discordGlobalReqDelay - delta)
		}
	}
	c.lastReq = time.Now()
}

func (c *discordClient) getJSON(ctx context.Context, path string, query url.Values, out interface{}) error {
	attempts := 0
	for {
		attempts++
		c.throttle()

		endpoint := strings.TrimRight(discordAPIBaseURL, "/") + path
		if query != nil && len(query) > 0 {
			endpoint += "?" + query.Encode()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Authorization", c.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("executing request: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryDelay := discordRateLimitDelay(resp.Header)
			if retryDelay == 0 {
				var rl struct {
					RetryAfter float64 `json:"retry_after"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&rl)
				if rl.RetryAfter > 0 {
					retryDelay = time.Duration(rl.RetryAfter * float64(time.Second))
				}
			}
			resp.Body.Close()
			if attempts >= 3 {
				return fmt.Errorf("discord API rate limited after %d attempts", attempts)
			}
			if retryDelay > 0 {
				time.Sleep(retryDelay)
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("discord API %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}

		decErr := json.NewDecoder(resp.Body).Decode(out)
		resp.Body.Close()
		if decErr != nil {
			return fmt.Errorf("decoding response: %w", decErr)
		}

		if delay := discordRateLimitDelay(resp.Header); delay > 0 {
			time.Sleep(delay)
		}

		return nil
	}
}

func (c *discordClient) getGuildName(ctx context.Context, guildID string) (string, error) {
	var guild struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.getJSON(ctx, "/guilds/"+guildID, nil, &guild); err != nil {
		return "", err
	}
	return guild.Name, nil
}

func (c *discordClient) listGuildChannels(ctx context.Context, guildID string) ([]discordChannel, error) {
	var channels []discordChannel
	if err := c.getJSON(ctx, "/guilds/"+guildID+"/channels", nil, &channels); err != nil {
		return nil, err
	}
	return channels, nil
}

func (c *discordClient) listArchivedPublicThreads(ctx context.Context, channelID string) ([]discordChannel, error) {
	var resp struct {
		Threads []discordChannel `json:"threads"`
	}
	query := url.Values{}
	query.Set("limit", "100")
	if err := c.getJSON(ctx, "/channels/"+channelID+"/threads/archived/public", query, &resp); err != nil {
		return nil, err
	}
	return resp.Threads, nil
}

func (c *discordClient) listPinnedMessages(ctx context.Context, channelID string) ([]discordMessage, error) {
	var msgs []discordMessage
	if err := c.getJSON(ctx, "/channels/"+channelID+"/pins", nil, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (c *discordClient) listChannelMessages(ctx context.Context, channelID string, since *time.Time, lookbackDays, maxMessages int) ([]discordMessage, error) {
	after := time.Now().AddDate(0, 0, -lookbackDays)
	if since != nil {
		after = *since
	}
	cursor := timeToDiscordSnowflake(after)

	messages := make([]discordMessage, 0)
	seen := make(map[string]bool)
	for len(messages) < maxMessages {
		q := url.Values{}
		q.Set("limit", "100")
		q.Set("after", cursor)

		var page []discordMessage
		if err := c.getJSON(ctx, "/channels/"+channelID+"/messages", q, &page); err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}

		maxID := cursor
		for _, msg := range page {
			if !seen[msg.ID] {
				seen[msg.ID] = true
				messages = append(messages, msg)
			}
			if msg.ID > maxID {
				maxID = msg.ID
			}
			if len(messages) >= maxMessages {
				break
			}
		}
		if maxID == cursor {
			break
		}
		cursor = maxID
		if len(page) < 100 {
			break
		}
	}
	return messages, nil
}

type discordChannel struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	GuildID  string `json:"guild_id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

type discordMessage struct {
	ID          string               `json:"id"`
	Content     string               `json:"content"`
	Timestamp   string               `json:"timestamp"`
	Pinned      bool                 `json:"pinned"`
	Author      discordMessageAuthor `json:"author"`
	Attachments []discordAttachment  `json:"attachments"`
}

type discordMessageAuthor struct {
	Username string `json:"username"`
}

type discordAttachment struct {
	Filename string `json:"filename"`
}
