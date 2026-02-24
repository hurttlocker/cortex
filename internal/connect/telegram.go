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
	"time"
)

const (
	telegramDefaultLookback = 30
	telegramDefaultMaxMsgs  = 5000
	telegramMaxLookbackDays = 365
	telegramMaxMessagesCap  = 20000
	telegramPollDelay       = 100 * time.Millisecond
)

var telegramAPIBaseURL = "https://api.telegram.org"

// TelegramProvider imports messages from Telegram Bot API.
type TelegramProvider struct{}

// TelegramConfig holds configuration for the Telegram connector.
type TelegramConfig struct {
	BotToken             string  `json:"bot_token"`
	ChatIDs              []int64 `json:"chat_ids"`
	LookbackDays         int     `json:"lookback_days,omitempty"`
	MaxMessages          int     `json:"max_messages,omitempty"`
	IncludeMediaCaptions bool    `json:"include_media_captions,omitempty"`
	SkipBotMessages      bool    `json:"skip_bot_messages,omitempty"`
}

func (c *TelegramConfig) lookbackDays() int {
	if c.LookbackDays <= 0 {
		return telegramDefaultLookback
	}
	if c.LookbackDays > telegramMaxLookbackDays {
		return telegramMaxLookbackDays
	}
	return c.LookbackDays
}

func (c *TelegramConfig) maxMessages() int {
	if c.MaxMessages <= 0 {
		return telegramDefaultMaxMsgs
	}
	if c.MaxMessages > telegramMaxMessagesCap {
		return telegramMaxMessagesCap
	}
	return c.MaxMessages
}

func init() {
	DefaultRegistry.Register(&TelegramProvider{})
}

func (p *TelegramProvider) Name() string        { return "telegram" }
func (p *TelegramProvider) DisplayName() string { return "Telegram" }

func (p *TelegramProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "bot_token": "123456:ABC-DEF...",
  "chat_ids": [-1003608365071],
  "lookback_days": 30,
  "max_messages": 5000,
  "include_media_captions": true,
  "skip_bot_messages": false
}`)
}

func (p *TelegramProvider) ValidateConfig(config json.RawMessage) error {
	var cfg TelegramConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	token := strings.TrimSpace(cfg.BotToken)
	if token == "" {
		return fmt.Errorf("bot_token is required")
	}
	if err := validateTelegramBotToken(token); err != nil {
		return err
	}
	if len(cfg.ChatIDs) == 0 {
		return fmt.Errorf("chat_ids must contain at least one chat ID")
	}
	for _, id := range cfg.ChatIDs {
		if id == 0 {
			return fmt.Errorf("chat_ids cannot contain 0")
		}
	}

	return nil
}

func (p *TelegramProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	if err := p.ValidateConfig(config); err != nil {
		return nil, err
	}

	var cfg TelegramConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	client := &telegramClient{
		baseURL:    strings.TrimRight(telegramAPIBaseURL, "/"),
		botToken:   strings.TrimSpace(cfg.BotToken),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	chatAllowed := make(map[int64]bool, len(cfg.ChatIDs))
	for _, id := range cfg.ChatIDs {
		chatAllowed[id] = true
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -cfg.lookbackDays())
	if since != nil {
		cutoff = *since
	}

	recordMap := make(map[string]Record)
	offset := int64(0)
	maxMessages := cfg.maxMessages()

	for len(recordMap) < maxMessages {
		resp, err := client.getUpdates(ctx, offset, 100)
		if err != nil {
			return nil, err
		}
		if len(resp.Result) == 0 {
			break
		}

		for _, upd := range resp.Result {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}

			msg := upd.Message
			if msg == nil {
				msg = upd.EditedMessage
			}
			if msg == nil {
				continue
			}
			if !chatAllowed[msg.Chat.ID] {
				continue
			}
			if cfg.SkipBotMessages && msg.From != nil && msg.From.IsBot {
				continue
			}

			ts := time.Unix(msg.Date, 0).UTC()
			if !ts.After(cutoff) {
				continue
			}

			body := buildTelegramMessageBody(msg, cfg.IncludeMediaCaptions)
			if strings.TrimSpace(body) == "" {
				continue
			}

			sender := telegramSenderName(msg.From)
			content := fmt.Sprintf("%s (%s): %s", sender, ts.Format(time.RFC3339), body)

			section := ""
			if msg.ReplyToMessage != nil {
				section = fmt.Sprintf("reply:%d", msg.ReplyToMessage.MessageID)
			}

			rec := Record{
				ExternalID: fmt.Sprintf("telegram:chat/%d/msg/%d", msg.Chat.ID, msg.MessageID),
				Content:    content,
				Source:     fmt.Sprintf("chat/%d/msg/%d", msg.Chat.ID, msg.MessageID),
				Section:    section,
				Timestamp:  ts,
			}
			recordMap[rec.ExternalID] = rec
			if len(recordMap) >= maxMessages {
				break
			}
		}

		if len(resp.Result) < 100 {
			break
		}
	}

	records := make([]Record, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.Before(records[j].Timestamp)
	})

	return records, nil
}

func validateTelegramBotToken(token string) error {
	parts := strings.Split(token, ":")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("bot_token must look like '<digits>:<secret>'")
	}
	for _, ch := range parts[0] {
		if ch < '0' || ch > '9' {
			return fmt.Errorf("bot_token prefix must be numeric")
		}
	}
	if len(parts[1]) < 8 {
		return fmt.Errorf("bot_token secret looks too short")
	}
	return nil
}

func buildTelegramMessageBody(msg *telegramMessage, includeCaptions bool) string {
	if msg == nil {
		return ""
	}

	forwardPrefix := ""
	if msg.ForwardFrom != nil || strings.TrimSpace(msg.ForwardSenderName) != "" {
		forwardPrefix = "[forwarded] "
	}

	body := ""
	switch {
	case strings.TrimSpace(msg.Text) != "":
		body = strings.TrimSpace(msg.Text)
	case len(msg.Photo) > 0:
		if includeCaptions && strings.TrimSpace(msg.Caption) != "" {
			body = "[photo] " + strings.TrimSpace(msg.Caption)
		}
	case msg.Document != nil:
		filename := strings.TrimSpace(msg.Document.FileName)
		if filename == "" {
			filename = "file"
		}
		body = "[document] " + filename
	default:
		if includeCaptions && strings.TrimSpace(msg.Caption) != "" {
			body = strings.TrimSpace(msg.Caption)
		}
	}

	if strings.TrimSpace(body) == "" {
		return ""
	}
	if msg.ReplyToMessage != nil {
		replyPreview := strings.TrimSpace(msg.ReplyToMessage.Text)
		if replyPreview == "" && strings.TrimSpace(msg.ReplyToMessage.Caption) != "" {
			replyPreview = strings.TrimSpace(msg.ReplyToMessage.Caption)
		}
		if replyPreview != "" {
			replyPreview = truncateSection(replyPreview, 60)
			body = fmt.Sprintf("%s (reply to #%d: %s)", body, msg.ReplyToMessage.MessageID, replyPreview)
		}
	}

	return forwardPrefix + body
}

func telegramSenderName(from *telegramUser) string {
	if from == nil {
		return "unknown"
	}
	if strings.TrimSpace(from.FirstName) != "" && strings.TrimSpace(from.LastName) != "" {
		return strings.TrimSpace(from.FirstName + " " + from.LastName)
	}
	if strings.TrimSpace(from.FirstName) != "" {
		return strings.TrimSpace(from.FirstName)
	}
	if strings.TrimSpace(from.Username) != "" {
		return strings.TrimSpace(from.Username)
	}
	if from.ID != 0 {
		return strconv.FormatInt(from.ID, 10)
	}
	return "unknown"
}

type telegramClient struct {
	baseURL    string
	botToken   string
	httpClient *http.Client
}

func (c *telegramClient) getUpdates(ctx context.Context, offset int64, limit int) (*telegramUpdatesResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("timeout", "0")

	endpoint := fmt.Sprintf("%s/bot%s/getUpdates?%s", c.baseURL, c.botToken, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	time.Sleep(telegramPollDelay)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("telegram API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out telegramUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding getUpdates response: %w", err)
	}
	if !out.OK {
		if out.Description == "" {
			out.Description = "unknown error"
		}
		return nil, fmt.Errorf("telegram API error: %s", out.Description)
	}

	return &out, nil
}

type telegramUpdatesResponse struct {
	OK          bool             `json:"ok"`
	Description string           `json:"description"`
	Result      []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *telegramMessage `json:"message"`
	EditedMessage *telegramMessage `json:"edited_message"`
}

type telegramMessage struct {
	MessageID         int64             `json:"message_id"`
	Date              int64             `json:"date"`
	Text              string            `json:"text"`
	Caption           string            `json:"caption"`
	From              *telegramUser     `json:"from"`
	Chat              telegramChat      `json:"chat"`
	Photo             []telegramPhoto   `json:"photo"`
	Document          *telegramDocument `json:"document"`
	ForwardFrom       *telegramUser     `json:"forward_from"`
	ForwardSenderName string            `json:"forward_sender_name"`
	ReplyToMessage    *telegramMessage  `json:"reply_to_message"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type telegramPhoto struct {
	FileID string `json:"file_id"`
}

type telegramDocument struct {
	FileName string `json:"file_name"`
}
