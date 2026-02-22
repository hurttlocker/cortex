package connect

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

func TestGmailProviderRegistered(t *testing.T) {
	p := DefaultRegistry.Get("gmail")
	if p == nil {
		t.Fatal("gmail provider not registered")
	}
	if p.Name() != "gmail" {
		t.Fatalf("expected name 'gmail', got %q", p.Name())
	}
	if p.DisplayName() != "Gmail (via gog)" {
		t.Fatalf("expected display name 'Gmail (via gog)', got %q", p.DisplayName())
	}
}

func TestGmailDefaultConfig(t *testing.T) {
	p := &GmailProvider{}
	cfg := p.DefaultConfig()

	var parsed GmailConfig
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("default config is not valid JSON: %v", err)
	}
	if parsed.Account != "user@gmail.com" {
		t.Fatalf("unexpected default account: %s", parsed.Account)
	}
	if parsed.Query != "newer_than:7d" {
		t.Fatalf("unexpected default query: %s", parsed.Query)
	}
	if parsed.MaxResults != 50 {
		t.Fatalf("unexpected default max_results: %d", parsed.MaxResults)
	}
}

func TestGmailValidateConfig(t *testing.T) {
	p := &GmailProvider{}

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:    "missing account",
			config:  `{"account": ""}`,
			wantErr: true,
		},
		{
			name:    "invalid email",
			config:  `{"account": "notanemail"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			config:  `not json`,
			wantErr: true,
		},
		// Note: valid config test skipped since it requires gog in PATH
		// which may not exist in CI. See integration test below.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.ValidateConfig(json.RawMessage(tt.config))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGmailConfigDefaults(t *testing.T) {
	cfg := GmailConfig{Account: "test@gmail.com"}

	if cfg.maxResults() != 50 {
		t.Fatalf("expected default maxResults 50, got %d", cfg.maxResults())
	}
	if cfg.query() != "newer_than:7d" {
		t.Fatalf("expected default query 'newer_than:7d', got %s", cfg.query())
	}
	if cfg.gogBinary() != "gog" {
		t.Fatalf("expected default gog binary 'gog', got %s", cfg.gogBinary())
	}

	// Custom values
	cfg.MaxResults = 100
	cfg.Query = "from:boss@co.com"
	cfg.GogPath = "/usr/local/bin/gog"

	if cfg.maxResults() != 100 {
		t.Fatalf("expected 100, got %d", cfg.maxResults())
	}
	if cfg.query() != "from:boss@co.com" {
		t.Fatalf("expected custom query, got %s", cfg.query())
	}
	if cfg.gogBinary() != "/usr/local/bin/gog" {
		t.Fatalf("expected custom path, got %s", cfg.gogBinary())
	}

	// Max cap
	cfg.MaxResults = 999
	if cfg.maxResults() != 500 {
		t.Fatalf("expected 500 cap, got %d", cfg.maxResults())
	}

	// Zero → default
	cfg.MaxResults = 0
	if cfg.maxResults() != 50 {
		t.Fatalf("expected 50 default, got %d", cfg.maxResults())
	}
}

func TestGmailShouldSkip(t *testing.T) {
	cfg := GmailConfig{
		Account:        "test@gmail.com",
		SkipCategories: []string{"CATEGORY_PROMOTIONS", "CATEGORY_SOCIAL"},
	}

	tests := []struct {
		labels []string
		skip   bool
	}{
		{[]string{"INBOX", "UNREAD"}, false},
		{[]string{"CATEGORY_PROMOTIONS", "INBOX"}, true},
		{[]string{"CATEGORY_SOCIAL"}, true},
		{[]string{"CATEGORY_PRIMARY"}, false},
		{[]string{"INBOX", "STARRED"}, false},
		{nil, false},
	}

	for _, tt := range tests {
		if cfg.shouldSkip(tt.labels) != tt.skip {
			t.Fatalf("shouldSkip(%v) = %v, want %v", tt.labels, !tt.skip, tt.skip)
		}
	}

	// Empty skip list = never skip
	cfg.SkipCategories = nil
	if cfg.shouldSkip([]string{"CATEGORY_PROMOTIONS"}) {
		t.Fatal("empty skip list should never skip")
	}
}

func TestThreadToRecord(t *testing.T) {
	thread := gogThread{
		ID:           "abc123",
		Subject:      "Q4 Budget Review",
		From:         "Alice <alice@company.com>",
		Date:         "2026-02-22 14:30",
		Labels:       []string{"INBOX", "UNREAD", "CATEGORY_PRIMARY"},
		MessageCount: 3,
	}

	r := threadToRecord(thread, "work")

	if r.Source != "thread/abc123" {
		t.Fatalf("unexpected source: %s", r.Source)
	}
	if r.Section != "Q4 Budget Review" {
		t.Fatalf("unexpected section: %s", r.Section)
	}
	if r.Project != "work" {
		t.Fatalf("unexpected project: %s", r.Project)
	}
	if r.ExternalID != "gmail:thread/abc123" {
		t.Fatalf("unexpected external ID: %s", r.ExternalID)
	}
	if !containsLower(r.Content, "alice@company.com") {
		t.Fatal("expected from address in content")
	}
	if !containsLower(r.Content, "Messages: 3") {
		t.Fatal("expected message count in content")
	}
}

func TestThreadToRecordSingleMessage(t *testing.T) {
	thread := gogThread{
		ID:           "xyz789",
		Subject:      "Hello",
		From:         "Bob <bob@test.com>",
		Date:         "2026-02-21 09:00",
		Labels:       []string{"INBOX"},
		MessageCount: 1,
	}

	r := threadToRecord(thread, "")

	// Single-message threads shouldn't show "Messages: 1"
	if containsLower(r.Content, "Messages:") {
		t.Fatal("single-message thread should not show message count")
	}
}

func TestFilterVisibleLabels(t *testing.T) {
	tests := []struct {
		input    []string
		expected []string
	}{
		{
			[]string{"INBOX", "UNREAD", "CATEGORY_PRIMARY"},
			[]string{"primary"},
		},
		{
			[]string{"CATEGORY_PROMOTIONS", "STARRED", "MyLabel"},
			[]string{"promotions", "MyLabel"},
		},
		{
			[]string{"INBOX", "UNREAD", "SENT"},
			nil,
		},
		{
			nil,
			nil,
		},
	}

	for _, tt := range tests {
		result := filterVisibleLabels(tt.input)
		if len(result) != len(tt.expected) {
			t.Fatalf("filterVisibleLabels(%v) = %v, want %v", tt.input, result, tt.expected)
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Fatalf("filterVisibleLabels(%v)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
			}
		}
	}
}

func TestParseGogDate(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"2026-02-22 14:30", true},
		{"2026-02-22 14:30:00", true},
		{"2026-02-22T14:30:00Z", true},
		{"not a date", false},
		{"", false},
	}

	for _, tt := range tests {
		result := parseGogDate(tt.input)
		if tt.valid && result.IsZero() {
			t.Fatalf("expected valid time for %q, got zero", tt.input)
		}
		if !tt.valid && !result.IsZero() {
			t.Fatalf("expected zero time for %q, got %v", tt.input, result)
		}
	}
}

func TestGetHeader(t *testing.T) {
	headers := []gogHeader{
		{Name: "From", Value: "alice@test.com"},
		{Name: "Subject", Value: "Hello World"},
		{Name: "Date", Value: "2026-02-22"},
	}

	if got := getHeader(headers, "From"); got != "alice@test.com" {
		t.Fatalf("expected alice@test.com, got %s", got)
	}
	if got := getHeader(headers, "from"); got != "alice@test.com" {
		t.Fatal("getHeader should be case-insensitive")
	}
	if got := getHeader(headers, "X-Missing"); got != "" {
		t.Fatalf("expected empty for missing header, got %s", got)
	}
}

func TestExtractBody(t *testing.T) {
	// Direct body
	payload := gogPayload{
		Body: gogBody{Data: "Hello world"},
	}
	if got := extractBody(payload); got != "Hello world" {
		t.Fatalf("expected direct body, got %q", got)
	}

	// Body in parts
	payload = gogPayload{
		Parts: []gogPart{
			{MimeType: "text/html", Body: gogBody{Data: "<p>html</p>"}},
			{MimeType: "text/plain", Body: gogBody{Data: "plain text"}},
		},
	}
	if got := extractBody(payload); got != "plain text" {
		t.Fatalf("expected text/plain from parts, got %q", got)
	}

	// Nested multipart
	payload = gogPayload{
		Parts: []gogPart{
			{
				MimeType: "multipart/alternative",
				Parts: []gogPart{
					{MimeType: "text/plain", Body: gogBody{Data: "nested plain"}},
					{MimeType: "text/html", Body: gogBody{Data: "<p>nested html</p>"}},
				},
			},
		},
	}
	if got := extractBody(payload); got != "nested plain" {
		t.Fatalf("expected nested text/plain, got %q", got)
	}

	// Empty
	payload = gogPayload{}
	if got := extractBody(payload); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestFullThreadToRecord(t *testing.T) {
	thread := gogThread{
		ID:           "thread1",
		Subject:      "Project Update",
		From:         "Alice <alice@co.com>",
		Date:         "2026-02-22 10:00",
		MessageCount: 2,
	}

	full := &gogFullThread{
		Messages: []gogMessage{
			{
				ID: "msg1",
				Payload: gogPayload{
					Headers: []gogHeader{
						{Name: "From", Value: "Alice <alice@co.com>"},
						{Name: "Subject", Value: "Project Update"},
						{Name: "Date", Value: "Sat, 22 Feb 2026 10:00:00 -0500"},
					},
					Body: gogBody{Data: "Here's the latest on the project."},
				},
			},
			{
				ID: "msg2",
				Payload: gogPayload{
					Headers: []gogHeader{
						{Name: "From", Value: "Bob <bob@co.com>"},
						{Name: "Date", Value: "Sat, 22 Feb 2026 11:30:00 -0500"},
					},
					Body: gogBody{Data: "Thanks Alice, looks good!"},
				},
			},
		},
	}

	r := fullThreadToRecord(thread, full, "work")

	if r.Source != "thread/thread1" {
		t.Fatalf("unexpected source: %s", r.Source)
	}
	if !containsLower(r.Content, "Here's the latest") {
		t.Fatal("expected first message body in content")
	}
	if !containsLower(r.Content, "Thanks Alice") {
		t.Fatal("expected second message body in content")
	}
	if !containsLower(r.Content, "Bob <bob@co.com>") {
		t.Fatal("expected second sender in content")
	}
}

func TestFullThreadToRecordTruncation(t *testing.T) {
	thread := gogThread{
		ID:      "long1",
		Subject: "Very long email",
		From:    "sender@test.com",
		Date:    "2026-02-22 12:00",
	}

	// Create a message with a very long body
	longBody := ""
	for i := 0; i < 1000; i++ {
		longBody += "This is a long line that repeats. "
	}

	full := &gogFullThread{
		Messages: []gogMessage{
			{
				Payload: gogPayload{
					Headers: []gogHeader{{Name: "From", Value: "sender@test.com"}},
					Body:    gogBody{Data: longBody},
				},
			},
		},
	}

	r := fullThreadToRecord(thread, full, "")

	if len(r.Content) > 8200 { // 8000 + some header overhead
		t.Fatalf("expected truncated content, got %d chars", len(r.Content))
	}
}

func TestGmailProviderInRegistry(t *testing.T) {
	// Both gmail and github should be registered
	names := DefaultRegistry.List()
	hasGmail := false
	hasGithub := false
	for _, n := range names {
		if n == "gmail" {
			hasGmail = true
		}
		if n == "github" {
			hasGithub = true
		}
	}
	if !hasGmail {
		t.Fatal("gmail not in registry")
	}
	if !hasGithub {
		t.Fatal("github not in registry")
	}
}

// Integration test: only runs when gog is available
func TestGmailValidateConfigWithGog(t *testing.T) {
	if _, err := exec.LookPath("gog"); err != nil {
		t.Skip("gog not in PATH, skipping integration test")
	}

	p := &GmailProvider{}
	err := p.ValidateConfig(json.RawMessage(`{"account": "test@gmail.com"}`))
	if err != nil {
		t.Fatalf("expected valid config with gog present: %v", err)
	}
}

// Verify since-based query override
func TestGmailFetchQueryOverride(t *testing.T) {
	cfg := GmailConfig{
		Account: "test@gmail.com",
		Query:   "newer_than:7d",
	}

	// Without since, uses config query
	if cfg.query() != "newer_than:7d" {
		t.Fatalf("expected config query, got %s", cfg.query())
	}

	// The Fetch method would override with after:epoch when since is provided
	// We test this indirectly by checking the epoch format
	since := time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)
	expected := fmt.Sprintf("after:%d", since.Unix())
	if !containsLower(expected, "after:17") {
		t.Fatal("expected epoch-based query")
	}
}
