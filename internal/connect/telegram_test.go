package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTelegramProviderRegistered(t *testing.T) {
	p := DefaultRegistry.Get("telegram")
	if p == nil {
		t.Fatal("telegram provider not registered")
	}
	if p.Name() != "telegram" {
		t.Fatalf("expected provider name telegram, got %q", p.Name())
	}
	if p.DisplayName() != "Telegram" {
		t.Fatalf("expected display name Telegram, got %q", p.DisplayName())
	}
}

func TestTelegramValidateConfig(t *testing.T) {
	p := &TelegramProvider{}

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:    "valid",
			config:  `{"bot_token":"123456:ABCDEF123456","chat_ids":[-100123]}`,
			wantErr: false,
		},
		{
			name:    "missing token",
			config:  `{"bot_token":"","chat_ids":[-100123]}`,
			wantErr: true,
		},
		{
			name:    "invalid token prefix",
			config:  `{"bot_token":"abc:ABCDEF123456","chat_ids":[-100123]}`,
			wantErr: true,
		},
		{
			name:    "missing chat ids",
			config:  `{"bot_token":"123456:ABCDEF123456","chat_ids":[]}`,
			wantErr: true,
		},
		{
			name:    "zero chat id",
			config:  `{"bot_token":"123456:ABCDEF123456","chat_ids":[0]}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			config:  `not-json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.ValidateConfig(json.RawMessage(tt.config))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConfig() error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildTelegramMessageBody(t *testing.T) {
	msgText := &telegramMessage{Text: "hello world"}
	if got := buildTelegramMessageBody(msgText, true); got != "hello world" {
		t.Fatalf("expected text message body, got %q", got)
	}

	msgPhoto := &telegramMessage{Photo: []telegramPhoto{{FileID: "p1"}}, Caption: "daily chart"}
	if got := buildTelegramMessageBody(msgPhoto, true); got != "[photo] daily chart" {
		t.Fatalf("expected photo caption body, got %q", got)
	}
	if got := buildTelegramMessageBody(msgPhoto, false); got != "" {
		t.Fatalf("expected empty photo body when captions disabled, got %q", got)
	}

	msgDoc := &telegramMessage{Document: &telegramDocument{FileName: "report.pdf"}}
	if got := buildTelegramMessageBody(msgDoc, true); got != "[document] report.pdf" {
		t.Fatalf("expected document body, got %q", got)
	}

	msgForwardReply := &telegramMessage{
		Text:              "agreed",
		ForwardSenderName: "source-user",
		ReplyToMessage: &telegramMessage{
			MessageID: 42,
			Text:      "can you review the plan?",
		},
	}
	got := buildTelegramMessageBody(msgForwardReply, true)
	if !strings.HasPrefix(got, "[forwarded] ") {
		t.Fatalf("expected forwarded prefix, got %q", got)
	}
	if !strings.Contains(got, "reply to #42") {
		t.Fatalf("expected reply context in body, got %q", got)
	}
}

func TestTelegramFetchWithMockServer(t *testing.T) {
	now := time.Now().UTC()
	chatID := int64(-1003608365071)

	mux := http.NewServeMux()
	mux.HandleFunc("/bot123456:ABCDEF123456/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "ok": true,
  "result": [
    {
      "update_id": 1001,
      "message": {
        "message_id": 11,
        "date": %d,
        "text": "hello team",
        "from": {"id": 1, "first_name": "Alice", "is_bot": false},
        "chat": {"id": %d, "type": "group", "title": "Alpha"}
      }
    },
    {
      "update_id": 1002,
      "message": {
        "message_id": 12,
        "date": %d,
        "caption": "status chart",
        "photo": [{"file_id":"p1"}],
        "from": {"id": 2, "first_name": "Bob", "is_bot": false},
        "chat": {"id": %d, "type": "group", "title": "Alpha"}
      }
    },
    {
      "update_id": 1003,
      "message": {
        "message_id": 13,
        "date": %d,
        "document": {"file_name":"report.pdf"},
        "from": {"id": 3, "first_name": "Charlie", "is_bot": false},
        "chat": {"id": %d, "type": "group", "title": "Alpha"}
      }
    },
    {
      "update_id": 1004,
      "message": {
        "message_id": 14,
        "date": %d,
        "text": "agreed",
        "forward_sender_name": "origin-user",
        "reply_to_message": {"message_id": 10, "text": "please review"},
        "from": {"id": 4, "first_name": "Dana", "is_bot": false},
        "chat": {"id": %d, "type": "group", "title": "Alpha"}
      }
    },
    {
      "update_id": 1005,
      "message": {
        "message_id": 99,
        "date": %d,
        "text": "outside chat",
        "from": {"id": 5, "first_name": "Eve", "is_bot": false},
        "chat": {"id": -1009999999999, "type": "group", "title": "Other"}
      }
    }
  ]
}`,
			now.Add(-5*time.Minute).Unix(), chatID,
			now.Add(-4*time.Minute).Unix(), chatID,
			now.Add(-3*time.Minute).Unix(), chatID,
			now.Add(-2*time.Minute).Unix(), chatID,
			now.Add(-1*time.Minute).Unix(),
		)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	prevBaseURL := telegramAPIBaseURL
	telegramAPIBaseURL = server.URL
	defer func() { telegramAPIBaseURL = prevBaseURL }()

	p := &TelegramProvider{}
	cfg := json.RawMessage(fmt.Sprintf(`{
  "bot_token": "123456:ABCDEF123456",
  "chat_ids": [%d],
  "lookback_days": 30,
  "max_messages": 50,
  "include_media_captions": true,
  "skip_bot_messages": false
}`,
		chatID,
	))

	records, err := p.Fetch(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Fetch() failed: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("expected 4 records (one disallowed chat filtered), got %d", len(records))
	}

	var sawPhoto, sawDoc, sawReply bool
	for _, rec := range records {
		if !strings.HasPrefix(rec.ExternalID, "telegram:chat/") {
			t.Fatalf("unexpected external id format: %q", rec.ExternalID)
		}
		if !strings.HasPrefix(rec.Source, "chat/") {
			t.Fatalf("unexpected source format: %q", rec.Source)
		}
		if strings.Contains(rec.Content, "[photo] status chart") {
			sawPhoto = true
		}
		if strings.Contains(rec.Content, "[document] report.pdf") {
			sawDoc = true
		}
		if strings.Contains(rec.Content, "[forwarded]") {
			sawReply = true
			if rec.Section != "reply:10" {
				t.Fatalf("expected reply section reply:10, got %q", rec.Section)
			}
		}
	}

	if !sawPhoto {
		t.Fatal("expected photo-caption record")
	}
	if !sawDoc {
		t.Fatal("expected document record")
	}
	if !sawReply {
		t.Fatal("expected forwarded/reply record")
	}
}

func TestTelegramFetchSinceFiltering(t *testing.T) {
	now := time.Now().UTC()
	chatID := int64(-1003608365071)

	mux := http.NewServeMux()
	mux.HandleFunc("/bot123456:ABCDEF123456/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "ok": true,
  "result": [
    {
      "update_id": 2001,
      "message": {
        "message_id": 21,
        "date": %d,
        "text": "old message",
        "from": {"id": 1, "first_name": "Alice", "is_bot": false},
        "chat": {"id": %d, "type": "group", "title": "Alpha"}
      }
    },
    {
      "update_id": 2002,
      "message": {
        "message_id": 22,
        "date": %d,
        "text": "new message",
        "from": {"id": 2, "first_name": "Bob", "is_bot": false},
        "chat": {"id": %d, "type": "group", "title": "Alpha"}
      }
    }
  ]
}`,
			now.Add(-2*time.Hour).Unix(), chatID,
			now.Add(-1*time.Minute).Unix(), chatID,
		)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	prevBaseURL := telegramAPIBaseURL
	telegramAPIBaseURL = server.URL
	defer func() { telegramAPIBaseURL = prevBaseURL }()

	p := &TelegramProvider{}
	cfg := json.RawMessage(`{
  "bot_token": "123456:ABCDEF123456",
  "chat_ids": [-1003608365071],
  "lookback_days": 30,
  "max_messages": 50,
  "include_media_captions": true,
  "skip_bot_messages": false
}`)

	since := now.Add(-10 * time.Minute)
	records, err := p.Fetch(context.Background(), cfg, &since)
	if err != nil {
		t.Fatalf("Fetch() failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record after since filtering, got %d", len(records))
	}
	if !strings.Contains(records[0].Content, "new message") {
		t.Fatalf("expected only new message, got %q", records[0].Content)
	}
	if !strings.Contains(records[0].ExternalID, "msg/22") {
		t.Fatalf("expected message id 22, got %q", records[0].ExternalID)
	}

	parts := strings.Split(records[0].ExternalID, "/")
	last := parts[len(parts)-1]
	if _, err := strconv.ParseInt(last, 10, 64); err != nil {
		t.Fatalf("expected numeric telegram message id in external ID, got %q", records[0].ExternalID)
	}
}
