package connect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCalendarProviderRegistered(t *testing.T) {
	p := DefaultRegistry.Get("calendar")
	if p == nil {
		t.Fatal("calendar provider not registered")
	}
	if p.Name() != "calendar" {
		t.Fatalf("expected name 'calendar', got %q", p.Name())
	}
	if p.DisplayName() != "Google Calendar" {
		t.Fatalf("expected display name 'Google Calendar', got %q", p.DisplayName())
	}
}

func TestCalendarDefaultConfig(t *testing.T) {
	p := &CalendarProvider{}
	cfg := p.DefaultConfig()

	var parsed CalendarConfig
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("default config is not valid JSON: %v", err)
	}
	if parsed.AccessToken != "" {
		t.Fatal("default access_token should be empty")
	}
	if len(parsed.Calendars) != 1 || parsed.Calendars[0] != "primary" {
		t.Fatalf("unexpected default calendars: %v", parsed.Calendars)
	}
	if parsed.DaysBack != 90 {
		t.Fatalf("expected days_back 90, got %d", parsed.DaysBack)
	}
	if parsed.DaysForward != 30 {
		t.Fatalf("expected days_forward 30, got %d", parsed.DaysForward)
	}
}

func TestCalendarValidateConfig(t *testing.T) {
	p := &CalendarProvider{}

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:    "valid",
			config:  `{"access_token": "ya29.test", "calendars": ["primary"]}`,
			wantErr: false,
		},
		{
			name:    "valid with email calendar",
			config:  `{"access_token": "ya29.test", "calendars": ["user@gmail.com"]}`,
			wantErr: false,
		},
		{
			name:    "missing token",
			config:  `{"access_token": "", "calendars": ["primary"]}`,
			wantErr: true,
		},
		{
			name:    "no calendars",
			config:  `{"access_token": "ya29.test", "calendars": []}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			config:  `not json`,
			wantErr: true,
		},
		{
			name:    "multiple calendars",
			config:  `{"access_token": "ya29.test", "calendars": ["primary", "work@company.com"]}`,
			wantErr: false,
		},
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

func TestEventToRecord(t *testing.T) {
	event := calendarEvent{
		ID:          "event123",
		Summary:     "Sprint Planning",
		Description: "Discuss Q1 goals and task assignments.",
		Location:    "Conference Room B",
		Status:      "confirmed",
		Start: calendarEventTime{
			DateTime: "2026-02-22T10:00:00-05:00",
		},
		End: calendarEventTime{
			DateTime: "2026-02-22T11:00:00-05:00",
		},
		Organizer: calendarPerson{
			DisplayName: "Q",
			Email:       "q@example.com",
		},
		Attendees: []calendarAttendee{
			{DisplayName: "Q", Email: "q@example.com", ResponseStatus: "accepted"},
			{DisplayName: "SB", Email: "sb@example.com", ResponseStatus: "tentative"},
		},
		Updated: "2026-02-22T09:00:00Z",
	}

	r := eventToRecord("primary", event, "work")

	if r.Content == "" {
		t.Fatal("expected non-empty content")
	}
	if r.Source != "calendar/primary/event123" {
		t.Fatalf("unexpected source: %s", r.Source)
	}
	if r.Section != "Sprint Planning" {
		t.Fatalf("unexpected section: %s", r.Section)
	}
	if r.Project != "work" {
		t.Fatalf("unexpected project: %s", r.Project)
	}
	if r.ExternalID != "gcal:primary:event123" {
		t.Fatalf("unexpected external ID: %s", r.ExternalID)
	}
	// Multi-attendee → decision class
	if r.MemoryClass != "decision" {
		t.Fatalf("expected 'decision' class for multi-attendee event, got %q", r.MemoryClass)
	}

	// Check content includes key details
	if !containsLower(r.Content, "Sprint Planning") {
		t.Fatal("content missing event title")
	}
	if !containsLower(r.Content, "Conference Room B") {
		t.Fatal("content missing location")
	}
	if !containsLower(r.Content, "Q1 goals") {
		t.Fatal("content missing description")
	}
}

func TestEventToRecordAllDay(t *testing.T) {
	event := calendarEvent{
		ID:      "allday1",
		Summary: "Company Holiday",
		Status:  "confirmed",
		Start: calendarEventTime{
			Date: "2026-12-25",
		},
		End: calendarEventTime{
			Date: "2026-12-26",
		},
		Updated: "2026-01-01T00:00:00Z",
	}

	r := eventToRecord("primary", event, "")

	if !containsLower(r.Content, "all day") {
		t.Fatal("expected all-day indicator in content")
	}
	if r.Section != "Company Holiday" {
		t.Fatalf("unexpected section: %s", r.Section)
	}
}

func TestEventToRecordNoTitle(t *testing.T) {
	event := calendarEvent{
		ID:      "notitle",
		Summary: "",
		Status:  "confirmed",
		Start: calendarEventTime{
			DateTime: "2026-02-22T14:00:00Z",
		},
		Updated: "2026-02-22T14:00:00Z",
	}

	r := eventToRecord("primary", event, "")

	if !containsLower(r.Content, "(No title)") {
		t.Fatal("expected '(No title)' for event without summary")
	}
	if r.Section != "(No title)" {
		t.Fatalf("unexpected section: %s", r.Section)
	}
}

func TestEventToRecordTruncation(t *testing.T) {
	longDesc := ""
	for i := 0; i < 300; i++ {
		longDesc += "This is a long description line. "
	}

	event := calendarEvent{
		ID:          "long1",
		Summary:     "Long Description Test",
		Description: longDesc,
		Status:      "confirmed",
		Start:       calendarEventTime{DateTime: "2026-02-22T10:00:00Z"},
		Updated:     "2026-02-22T10:00:00Z",
	}

	r := eventToRecord("primary", event, "")

	if len(r.Content) > 2400 { // header + truncated description
		t.Fatalf("expected truncated content, got %d chars", len(r.Content))
	}
}

func TestClassifyEvent(t *testing.T) {
	tests := []struct {
		name      string
		summary   string
		numPeople int
		expected  string
	}{
		{"multi-attendee", "Team meeting", 3, "decision"},
		{"deadline", "Project deadline", 0, "status"},
		{"review", "Code Review", 0, "decision"},
		{"standup", "Daily Standup", 0, "status"},
		{"1:1", "1:1 with Q", 0, "decision"},
		{"plain event", "Lunch", 0, ""},
		{"sync", "Team Sync", 0, "status"},
		{"retro", "Sprint Retro", 0, "decision"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := calendarEvent{Summary: tt.summary}
			for i := 0; i < tt.numPeople; i++ {
				event.Attendees = append(event.Attendees, calendarAttendee{
					Email: "person@example.com",
				})
			}
			got := classifyEvent(event)
			if got != tt.expected {
				t.Fatalf("classifyEvent(%q) = %q, want %q", tt.summary, got, tt.expected)
			}
		})
	}
}

func TestFormatEventTime(t *testing.T) {
	tests := []struct {
		name     string
		et       calendarEventTime
		contains string
	}{
		{"datetime", calendarEventTime{DateTime: "2026-02-22T10:00:00Z"}, "Feb 22, 2026"},
		{"date only", calendarEventTime{Date: "2026-02-22"}, "all day"},
		{"empty", calendarEventTime{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatEventTime(tt.et)
			if tt.contains == "" {
				if got != "" {
					t.Fatalf("expected empty, got %q", got)
				}
				return
			}
			if !containsLower(got, tt.contains) {
				t.Fatalf("formatEventTime() = %q, expected to contain %q", got, tt.contains)
			}
		})
	}
}

func TestParseGoogleTime(t *testing.T) {
	// Standard RFC3339
	t1 := parseGoogleTime("2026-02-22T10:00:00Z")
	if t1.IsZero() {
		t.Fatal("failed to parse standard RFC3339")
	}

	// With fractional seconds (Google format)
	t2 := parseGoogleTime("2026-02-22T10:00:00.000Z")
	if t2.IsZero() {
		t.Fatal("failed to parse RFC3339 with fractional seconds")
	}

	// With timezone offset
	t3 := parseGoogleTime("2026-02-22T10:00:00-05:00")
	if t3.IsZero() {
		t.Fatal("failed to parse RFC3339 with timezone offset")
	}

	// Empty
	t4 := parseGoogleTime("")
	if !t4.IsZero() {
		t.Fatal("expected zero time for empty string")
	}

	// Invalid
	t5 := parseGoogleTime("not a time")
	if !t5.IsZero() {
		t.Fatal("expected zero time for invalid string")
	}
}

func TestCalendarFetchWithMockServer(t *testing.T) {
	events := calendarEventsList{
		Items: []calendarEvent{
			{
				ID:      "evt1",
				Summary: "Team Standup",
				Status:  "confirmed",
				Start:   calendarEventTime{DateTime: "2026-02-22T09:00:00Z"},
				End:     calendarEventTime{DateTime: "2026-02-22T09:15:00Z"},
				Updated: "2026-02-22T08:00:00Z",
				Organizer: calendarPerson{
					DisplayName: "Q",
					Email:       "q@example.com",
				},
			},
			{
				ID:      "evt2",
				Summary: "Cancelled Meeting",
				Status:  "cancelled",
				Start:   calendarEventTime{DateTime: "2026-02-22T14:00:00Z"},
				Updated: "2026-02-22T08:00:00Z",
			},
			{
				ID:      "evt3",
				Summary: "Sprint Review",
				Status:  "confirmed",
				Start:   calendarEventTime{Date: "2026-02-23"},
				Updated: "2026-02-22T08:00:00Z",
				Attendees: []calendarAttendee{
					{DisplayName: "Q", Email: "q@test.com"},
					{DisplayName: "SB", Email: "sb@test.com"},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(events)
	}))
	defer server.Close()

	// Test using mock client directly
	client := &googleClient{
		accessToken: "test-token",
		httpClient:  server.Client(),
	}

	var result calendarEventsList
	err := client.get(context.Background(), server.URL+"/calendars/primary/events?singleEvents=true", &result)
	if err != nil {
		t.Fatalf("fetch events failed: %v", err)
	}

	if len(result.Items) != 3 {
		t.Fatalf("expected 3 events, got %d", len(result.Items))
	}

	// Convert non-cancelled events to records
	var records []Record
	for _, event := range result.Items {
		if event.Status == "cancelled" {
			continue
		}
		records = append(records, eventToRecord("primary", event, "test"))
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records (excluding cancelled), got %d", len(records))
	}

	// First record: standup
	if records[0].Section != "Team Standup" {
		t.Fatalf("expected 'Team Standup', got %q", records[0].Section)
	}
	if records[0].MemoryClass != "status" { // "standup" keyword
		t.Fatalf("expected 'status' class, got %q", records[0].MemoryClass)
	}

	// Second record: sprint review (multi-attendee)
	if records[1].Section != "Sprint Review" {
		t.Fatalf("expected 'Sprint Review', got %q", records[1].Section)
	}
	if records[1].MemoryClass != "decision" { // multi-attendee
		t.Fatalf("expected 'decision' class, got %q", records[1].MemoryClass)
	}
}

func TestCalendarFetchValidationError(t *testing.T) {
	p := &CalendarProvider{}

	// Missing token
	_, err := p.Fetch(context.Background(), json.RawMessage(`{"access_token": "", "calendars": ["primary"]}`), nil)
	if err == nil {
		t.Fatal("expected validation error for missing token")
	}

	// No calendars
	_, err = p.Fetch(context.Background(), json.RawMessage(`{"access_token": "ya29.test", "calendars": []}`), nil)
	if err == nil {
		t.Fatal("expected validation error for empty calendars")
	}
}

func TestCalendarFetchEnvFallback(t *testing.T) {
	p := &CalendarProvider{}

	// Without env var: should fail
	t.Setenv("GOOGLE_ACCESS_TOKEN", "")
	_, err := p.Fetch(context.Background(), json.RawMessage(`{"access_token": "", "calendars": ["primary"]}`), nil)
	if err == nil {
		t.Fatal("expected error without token")
	}

	// With env var: should get past token check (will fail on HTTP, but that's fine)
	t.Setenv("GOOGLE_ACCESS_TOKEN", "ya29.from-env")
	_, err = p.Fetch(context.Background(), json.RawMessage(`{"access_token": "", "calendars": ["primary"]}`), nil)
	if err == nil {
		// If it somehow succeeded (unlikely without real API), that's also fine
		return
	}
	// Should NOT be a "no access token" error
	if containsLower(err.Error(), "no access token") {
		t.Fatalf("env var fallback didn't work: %v", err)
	}
}

func TestCalendarFetchIncremental(t *testing.T) {
	// Verify that since parameter is passed through
	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(calendarEventsList{})
	}))
	defer server.Close()

	// Override base URL for this test
	oldBase := calendarBaseURL
	calendarBaseURL = server.URL
	defer func() { calendarBaseURL = oldBase }()

	p := &CalendarProvider{}
	since := time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)
	_, err := p.Fetch(context.Background(),
		json.RawMessage(`{"access_token": "test", "calendars": ["primary"]}`),
		&since,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsLower(receivedURL, "updatedMin") {
		t.Fatalf("incremental sync should include updatedMin, got URL: %s", receivedURL)
	}
}
