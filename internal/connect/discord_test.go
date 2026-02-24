package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscordProviderRegistered(t *testing.T) {
	p := DefaultRegistry.Get("discord")
	if p == nil {
		t.Fatal("discord provider not registered")
	}
	if p.Name() != "discord" {
		t.Fatalf("expected provider name discord, got %q", p.Name())
	}
	if p.DisplayName() != "Discord" {
		t.Fatalf("expected display name Discord, got %q", p.DisplayName())
	}
}

func TestDiscordDefaultConfig(t *testing.T) {
	p := &DiscordProvider{}
	cfg := p.DefaultConfig()

	var parsed DiscordConfig
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("default config is invalid JSON: %v", err)
	}
	if !strings.HasPrefix(parsed.Token, "Bot ") {
		t.Fatalf("expected bot token placeholder, got %q", parsed.Token)
	}
	if parsed.GuildID == "" {
		t.Fatal("expected guild_id placeholder")
	}
}

func TestDiscordValidateConfig(t *testing.T) {
	p := &DiscordProvider{}

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:    "valid",
			config:  `{"token":"Bot abc.def.ghi","guild_id":"123","channel_ids":["456"]}`,
			wantErr: false,
		},
		{
			name:    "missing token",
			config:  `{"token":"","guild_id":"123"}`,
			wantErr: true,
		},
		{
			name:    "token missing bot prefix",
			config:  `{"token":"abc.def.ghi","guild_id":"123"}`,
			wantErr: true,
		},
		{
			name:    "missing guild",
			config:  `{"token":"Bot abc.def.ghi","guild_id":""}`,
			wantErr: true,
		},
		{
			name:    "empty channel id",
			config:  `{"token":"Bot abc.def.ghi","guild_id":"123","channel_ids":[""]}`,
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

func TestDiscordSnowflakeConversion(t *testing.T) {
	base := time.Date(2024, 1, 15, 12, 34, 56, 0, time.UTC)
	snowflake := timeToDiscordSnowflake(base)
	parsed, err := discordSnowflakeToTime(snowflake)
	if err != nil {
		t.Fatalf("snowflake parse failed: %v", err)
	}

	delta := parsed.Sub(base)
	if delta < 0 {
		delta = -delta
	}
	if delta > time.Millisecond {
		t.Fatalf("expected <=1ms drift, got %v", delta)
	}

	if _, err := discordSnowflakeToTime("not-a-snowflake"); err == nil {
		t.Fatal("expected parse error for invalid snowflake")
	}
}

func TestDiscordRateLimitDelay(t *testing.T) {
	h1 := make(http.Header)
	h1.Set("X-RateLimit-Remaining", "0")
	h1.Set("X-RateLimit-Reset-After", "0.25")
	if got := discordRateLimitDelay(h1); got != 250*time.Millisecond {
		t.Fatalf("expected 250ms, got %v", got)
	}

	h2 := make(http.Header)
	h2.Set("X-RateLimit-Remaining", "1")
	h2.Set("X-RateLimit-Reset-After", "2")
	if got := discordRateLimitDelay(h2); got != 0 {
		t.Fatalf("expected 0 delay when remaining > 0, got %v", got)
	}

	h3 := make(http.Header)
	h3.Set("X-RateLimit-Remaining", "0")
	h3.Set("X-RateLimit-Reset-After", "bad")
	if got := discordRateLimitDelay(h3); got != 0 {
		t.Fatalf("expected 0 delay for invalid reset-after, got %v", got)
	}
}

func TestDiscordFetchWithMockServer(t *testing.T) {
	now := time.Now().UTC()
	msg1Time := now.Add(-2 * time.Hour)
	msg2Time := now.Add(-1 * time.Hour)
	threadTime := now.Add(-30 * time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/guilds/g1", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"g1","name":"Acme Guild"}`)
	})
	mux.HandleFunc("/guilds/g1/channels", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
  {"id":"c1","type":0,"name":"general"},
  {"id":"c2","type":0,"name":"random"}
]`)
	})
	mux.HandleFunc("/channels/c1/messages", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query().Get("after")
		if q == "" {
			t.Fatalf("expected after query parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[
  {"id":"200000000000000000","content":"hello team","timestamp":"%s","pinned":false,"author":{"username":"alice"}},
  {"id":"200000000000000001","content":"roadmap update","timestamp":"%s","pinned":true,"author":{"username":"bob"}}
]`, msg1Time.Format(time.RFC3339Nano), msg2Time.Format(time.RFC3339Nano))
	})
	mux.HandleFunc("/channels/c1/pins", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[
  {"id":"200000000000000001","content":"roadmap update","timestamp":"%s","pinned":true,"author":{"username":"bob"}}
]`, msg2Time.Format(time.RFC3339Nano))
	})
	mux.HandleFunc("/channels/c1/threads/archived/public", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"threads":[{"id":"t1","type":11,"parent_id":"c1","name":"launch-thread"}]}`)
	})
	mux.HandleFunc("/channels/t1/messages", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[
  {"id":"300000000000000001","content":"thread status update","timestamp":"%s","pinned":false,"author":{"username":"charlie"}}
]`, threadTime.Format(time.RFC3339Nano))
	})
	mux.HandleFunc("/channels/t1/pins", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	prevBaseURL := discordAPIBaseURL
	discordAPIBaseURL = server.URL
	defer func() { discordAPIBaseURL = prevBaseURL }()

	p := &DiscordProvider{}
	cfg := json.RawMessage(`{
  "token": "Bot test-token",
  "guild_id": "g1",
  "channel_ids": ["c1"],
  "include_threads": true,
  "include_pins": true,
  "lookback_days": 30,
  "max_messages": 50
}`)

	records, err := p.Fetch(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Fetch() failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	var foundPinned bool
	var foundThread bool
	for _, rec := range records {
		if strings.Contains(rec.ExternalID, "discord:guild/g1/channel/c1/msg/200000000000000001") {
			if !strings.HasPrefix(rec.Content, "[PINNED]") {
				t.Fatalf("expected pinned prefix, got %q", rec.Content)
			}
			foundPinned = true
		}
		if strings.Contains(rec.ExternalID, "discord:guild/g1/channel/t1/msg/300000000000000001") {
			if rec.Section != "launch-thread" {
				t.Fatalf("expected thread section launch-thread, got %q", rec.Section)
			}
			foundThread = true
		}
		if !strings.Contains(rec.Source, "guild/acme-guild/channel/general/msg/") {
			t.Fatalf("unexpected source format: %q", rec.Source)
		}
	}

	if !foundPinned {
		t.Fatal("expected to find pinned record")
	}
	if !foundThread {
		t.Fatal("expected to find thread record")
	}
}
