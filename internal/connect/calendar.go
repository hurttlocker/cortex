package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// CalendarProvider imports events from Google Calendar.
type CalendarProvider struct{}

// CalendarConfig holds the configuration for the Google Calendar connector.
type CalendarConfig struct {
	// AccessToken is a Google OAuth 2.0 access token with calendar.readonly scope.
	AccessToken string `json:"access_token"`

	// Calendars is a list of calendar IDs to sync (default: ["primary"]).
	Calendars []string `json:"calendars"`

	// DaysBack controls how far back to sync on full sync (default: 90).
	DaysBack int `json:"days_back,omitempty"`

	// DaysForward controls how far ahead to sync on full sync (default: 30).
	DaysForward int `json:"days_forward,omitempty"`

	// Project is the Cortex project tag for imported memories.
	Project string `json:"project,omitempty"`
}

func init() {
	DefaultRegistry.Register(&CalendarProvider{})
}

func (p *CalendarProvider) Name() string        { return "calendar" }
func (p *CalendarProvider) DisplayName() string { return "Google Calendar" }

func (p *CalendarProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "access_token": "",
  "calendars": ["primary"],
  "days_back": 90,
  "days_forward": 30,
  "project": ""
}`)
}

func (p *CalendarProvider) ValidateConfig(config json.RawMessage) error {
	var cfg CalendarConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if cfg.AccessToken == "" {
		return fmt.Errorf("access_token is required (Google OAuth 2.0 token with calendar.readonly scope)")
	}
	if len(cfg.Calendars) == 0 {
		return fmt.Errorf("at least one calendar ID is required (use \"primary\" for default)")
	}
	return nil
}

func (p *CalendarProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	var cfg CalendarConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Fall back to GOOGLE_ACCESS_TOKEN env var if no token in config
	if cfg.AccessToken == "" {
		cfg.AccessToken = os.Getenv("GOOGLE_ACCESS_TOKEN")
	}
	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("no access token provided: set in config or GOOGLE_ACCESS_TOKEN env var")
	}
	if len(cfg.Calendars) == 0 {
		return nil, fmt.Errorf("at least one calendar is required")
	}

	daysBack := cfg.DaysBack
	if daysBack <= 0 {
		daysBack = 90
	}
	daysForward := cfg.DaysForward
	if daysForward <= 0 {
		daysForward = 30
	}

	client := newGoogleClient(cfg.AccessToken)

	var allRecords []Record
	for _, calID := range cfg.Calendars {
		records, err := p.fetchEvents(ctx, client, calID, since, daysBack, daysForward, cfg.Project)
		if err != nil {
			return nil, fmt.Errorf("fetching events for calendar %s: %w", calID, err)
		}
		allRecords = append(allRecords, records...)
	}

	return allRecords, nil
}

// calendarBaseURL is the Google Calendar API base. Variable for test injection.
var calendarBaseURL = "https://www.googleapis.com/calendar/v3"

func (p *CalendarProvider) fetchEvents(ctx context.Context, client *googleClient, calendarID string, since *time.Time, daysBack, daysForward int, project string) ([]Record, error) {
	now := time.Now().UTC()

	// Build API URL with proper encoding of calendar ID (email addresses contain @)
	baseURL := fmt.Sprintf("%s/calendars/%s/events", calendarBaseURL, url.PathEscape(calendarID))

	params := url.Values{}
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")
	params.Set("maxResults", "250")

	if since != nil {
		// Incremental sync: events updated since last sync
		params.Set("updatedMin", since.Format(time.RFC3339))
		// Still bound the time window to avoid ancient events
		params.Set("timeMin", now.Add(-time.Duration(daysBack)*24*time.Hour).Format(time.RFC3339))
	} else {
		// Full sync: bounded window
		params.Set("timeMin", now.Add(-time.Duration(daysBack)*24*time.Hour).Format(time.RFC3339))
		params.Set("timeMax", now.Add(time.Duration(daysForward)*24*time.Hour).Format(time.RFC3339))
	}

	var allRecords []Record
	pages := 0

	for {
		reqURL := baseURL + "?" + params.Encode()

		var result calendarEventsList
		if err := client.get(ctx, reqURL, &result); err != nil {
			return nil, err
		}

		for _, event := range result.Items {
			if event.Status == "cancelled" {
				continue
			}
			record := eventToRecord(calendarID, event, project)
			allRecords = append(allRecords, record)
		}

		if result.NextPageToken == "" {
			break
		}
		params.Set("pageToken", result.NextPageToken)
		pages++
		if pages > 10 {
			break // safety cap
		}
	}

	return allRecords, nil
}

// eventToRecord converts a Google Calendar event to a Cortex Record.
func eventToRecord(calendarID string, event calendarEvent, project string) Record {
	var sb strings.Builder

	summary := event.Summary
	if summary == "" {
		summary = "(No title)"
	}
	fmt.Fprintf(&sb, "[Calendar Event] %s\n", summary)

	// Date/time
	start := formatEventTime(event.Start)
	end := formatEventTime(event.End)
	if start != "" {
		fmt.Fprintf(&sb, "When: %s", start)
		if end != "" {
			fmt.Fprintf(&sb, " → %s", end)
		}
		sb.WriteString("\n")
	}

	// Location
	if event.Location != "" {
		fmt.Fprintf(&sb, "Where: %s\n", event.Location)
	}

	// Status
	fmt.Fprintf(&sb, "Status: %s\n", event.Status)

	// Organizer
	if event.Organizer.DisplayName != "" || event.Organizer.Email != "" {
		name := event.Organizer.DisplayName
		if name == "" {
			name = event.Organizer.Email
		}
		fmt.Fprintf(&sb, "Organizer: %s\n", name)
	}

	// Attendees
	if len(event.Attendees) > 0 {
		names := make([]string, 0, len(event.Attendees))
		for _, a := range event.Attendees {
			name := a.DisplayName
			if name == "" {
				name = a.Email
			}
			if a.ResponseStatus != "" && a.ResponseStatus != "needsAction" {
				name += " (" + a.ResponseStatus + ")"
			}
			names = append(names, name)
		}
		if len(names) > 20 {
			names = append(names[:20], fmt.Sprintf("... +%d more", len(names)-20))
		}
		fmt.Fprintf(&sb, "Attendees: %s\n", strings.Join(names, ", "))
	}

	// Description
	if event.Description != "" {
		desc := event.Description
		if len(desc) > 2000 {
			desc = desc[:2000] + "\n... (truncated)"
		}
		fmt.Fprintf(&sb, "\n%s", desc)
	}

	// Determine record timestamp
	ts := parseGoogleTime(event.Updated)
	if ts.IsZero() {
		ts = parseGoogleTime(event.Start.DateTime)
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
	}

	return Record{
		Content:     sb.String(),
		Source:      fmt.Sprintf("calendar/%s/%s", calendarID, event.ID),
		Section:     summary,
		Project:     project,
		MemoryClass: classifyEvent(event),
		Timestamp:   ts,
		ExternalID:  fmt.Sprintf("gcal:%s:%s", calendarID, event.ID),
	}
}

// classifyEvent assigns a memory class based on event characteristics.
func classifyEvent(event calendarEvent) string {
	lower := strings.ToLower(event.Summary)

	// Multi-person meetings often produce decisions
	if len(event.Attendees) > 1 {
		return "decision"
	}

	switch {
	case strings.Contains(lower, "deadline") || strings.Contains(lower, "due"):
		return "status"
	case strings.Contains(lower, "review") || strings.Contains(lower, "retro"):
		return "decision"
	case strings.Contains(lower, "standup") || strings.Contains(lower, "sync"):
		return "status"
	case strings.Contains(lower, "1:1") || strings.Contains(lower, "1-on-1"):
		return "decision"
	}

	return ""
}

// formatEventTime formats a Google Calendar event time for display.
func formatEventTime(et calendarEventTime) string {
	if et.DateTime != "" {
		t := parseGoogleTime(et.DateTime)
		if !t.IsZero() {
			return t.Format("Mon Jan 2, 2006 3:04 PM MST")
		}
		return et.DateTime
	}
	if et.Date != "" {
		return et.Date + " (all day)"
	}
	return ""
}

// --- Google Calendar API types ---

type calendarEventsList struct {
	Items         []calendarEvent `json:"items"`
	NextPageToken string          `json:"nextPageToken"`
}

type calendarEvent struct {
	ID          string             `json:"id"`
	Summary     string             `json:"summary"`
	Description string             `json:"description"`
	Location    string             `json:"location"`
	Status      string             `json:"status"`
	Start       calendarEventTime  `json:"start"`
	End         calendarEventTime  `json:"end"`
	Organizer   calendarPerson     `json:"organizer"`
	Attendees   []calendarAttendee `json:"attendees"`
	Updated     string             `json:"updated"`
	Created     string             `json:"created"`
	HtmlLink    string             `json:"htmlLink"`
}

type calendarEventTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
	TimeZone string `json:"timeZone"`
}

type calendarPerson struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

type calendarAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
	ResponseStatus string `json:"responseStatus"`
	Organizer      bool   `json:"organizer"`
	Self           bool   `json:"self"`
}
