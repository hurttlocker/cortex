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

func TestNotionProviderRegistered(t *testing.T) {
	p := DefaultRegistry.Get("notion")
	if p == nil {
		t.Fatal("notion provider not registered")
	}
	if p.Name() != "notion" {
		t.Fatalf("expected provider name notion, got %q", p.Name())
	}
	if p.DisplayName() != "Notion" {
		t.Fatalf("expected display name Notion, got %q", p.DisplayName())
	}
}

func TestNotionValidateConfig(t *testing.T) {
	p := &NotionProvider{}

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:    "valid ntn token",
			config:  `{"token":"ntn_abc123","max_pages":100}`,
			wantErr: false,
		},
		{
			name:    "valid legacy token",
			config:  `{"token":"secret_abc123","max_pages":100}`,
			wantErr: false,
		},
		{
			name:    "missing token",
			config:  `{"token":""}`,
			wantErr: true,
		},
		{
			name:    "invalid token prefix",
			config:  `{"token":"abc123"}`,
			wantErr: true,
		},
		{
			name:    "empty root id",
			config:  `{"token":"ntn_abc123","root_page_ids":[""]}`,
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

func TestNotionBlocksToMarkdown(t *testing.T) {
	blocks := []notionBlock{
		{Type: "heading_1", Heading1: &notionTextContainer{RichText: []notionRichText{{PlainText: "Project"}}}},
		{Type: "paragraph", Paragraph: &notionTextContainer{RichText: []notionRichText{{PlainText: "Launch checklist"}}}},
		{Type: "bulleted_list_item", BulletedListItem: &notionTextContainer{RichText: []notionRichText{{PlainText: "Ship v0.7.0"}}}},
		{Type: "code", Code: &notionCodeBlock{Language: "go", RichText: []notionRichText{{PlainText: "fmt.Println(\"hello\")"}}}},
		{Type: "callout", Callout: &notionTextContainer{RichText: []notionRichText{{PlainText: "Watch post-release metrics"}}}},
	}

	md := notionBlocksToMarkdown(blocks)
	for _, want := range []string{"# Project", "Launch checklist", "- Ship v0.7.0", "```go", "fmt.Println(\"hello\")", "> Watch post-release metrics"} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, md)
		}
	}
}

func TestNotionFetchWithMockServer(t *testing.T) {
	now := time.Now().UTC()

	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ntn_test_token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Notion-Version"); got != notionVersion {
			http.Error(w, "bad version", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "results": [
    {
      "object": "page",
      "id": "page-1",
      "last_edited_time": %q,
      "properties": {
        "Name": {
          "type": "title",
          "title": [{"plain_text": "Roadmap"}]
        }
      }
    },
    {
      "object": "database",
      "id": "db-1",
      "last_edited_time": %q,
      "title": [{"plain_text": "Ops DB"}]
    }
  ],
  "has_more": false,
  "next_cursor": null
}`,
			now.Add(-2*time.Hour).Format(time.RFC3339),
			now.Add(-90*time.Minute).Format(time.RFC3339),
		)
	})
	mux.HandleFunc("/blocks/page-1/children", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ntn_test_token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
  "results": [
    {"type": "heading_2", "heading_2": {"rich_text": [{"plain_text": "Project Notes"}]}},
    {"type": "paragraph", "paragraph": {"rich_text": [{"plain_text": "Launch checklist"}]}}
  ],
  "has_more": false,
  "next_cursor": null
}`)
	})
	mux.HandleFunc("/databases/db-1/query", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ntn_test_token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "results": [
    {
      "id": "row-1",
      "last_edited_time": %q,
      "properties": {
        "Title": {"type": "title", "title": [{"plain_text": "Release"}]},
        "Status": {"type": "select", "select": {"name": "Done"}},
        "Owner": {"type": "rich_text", "rich_text": [{"plain_text": "Q"}]}
      }
    }
  ]
}`,
			now.Add(-30*time.Minute).Format(time.RFC3339),
		)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	prevBaseURL := notionAPIBaseURL
	prevGap := notionMinRequestGap
	notionAPIBaseURL = server.URL
	notionMinRequestGap = 0
	defer func() {
		notionAPIBaseURL = prevBaseURL
		notionMinRequestGap = prevGap
	}()

	p := &NotionProvider{}
	cfg := json.RawMessage(`{
  "token": "ntn_test_token",
  "include_databases": true,
  "max_pages": 20
}`)

	records, err := p.Fetch(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Fetch() failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records (page + db row), got %d", len(records))
	}

	var pageRec *Record
	var rowRec *Record
	for i := range records {
		rec := &records[i]
		switch {
		case strings.HasPrefix(rec.ExternalID, "notion:page/"):
			pageRec = rec
		case strings.HasPrefix(rec.ExternalID, "notion:db/"):
			rowRec = rec
		}
		if rec.MemoryClass != "reference" {
			t.Fatalf("expected memory class reference, got %q", rec.MemoryClass)
		}
	}

	if pageRec == nil {
		t.Fatal("expected page record")
	}
	if !strings.Contains(pageRec.Content, "## Project Notes") {
		t.Fatalf("expected page markdown content, got %q", pageRec.Content)
	}
	if pageRec.Source != "pages/page-1" {
		t.Fatalf("unexpected page source: %q", pageRec.Source)
	}

	if rowRec == nil {
		t.Fatal("expected database row record")
	}
	if rowRec.Section != "Release" {
		t.Fatalf("expected row section Release, got %q", rowRec.Section)
	}
	if !strings.Contains(rowRec.Content, "- Status: Done") {
		t.Fatalf("expected row markdown status field, got %q", rowRec.Content)
	}
}

func TestNotionFetchSinceAddsSearchFilter(t *testing.T) {
	var sawFilter bool

	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if filter, ok := body["filter"].(map[string]interface{}); ok {
			if filter["timestamp"] == "last_edited_time" {
				sawFilter = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[],"has_more":false,"next_cursor":null}`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	prevBaseURL := notionAPIBaseURL
	prevGap := notionMinRequestGap
	notionAPIBaseURL = server.URL
	notionMinRequestGap = 0
	defer func() {
		notionAPIBaseURL = prevBaseURL
		notionMinRequestGap = prevGap
	}()

	p := &NotionProvider{}
	cfg := json.RawMessage(`{"token":"ntn_test_token","include_databases":true}`)
	since := time.Now().UTC().Add(-1 * time.Hour)
	_, err := p.Fetch(context.Background(), cfg, &since)
	if err != nil {
		t.Fatalf("Fetch() failed: %v", err)
	}
	if !sawFilter {
		t.Fatal("expected search payload to include last_edited_time filter when since is set")
	}
}
