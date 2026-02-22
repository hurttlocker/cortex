package connect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDriveProviderRegistered(t *testing.T) {
	p := DefaultRegistry.Get("drive")
	if p == nil {
		t.Fatal("drive provider not registered")
	}
	if p.Name() != "drive" {
		t.Fatalf("expected name 'drive', got %q", p.Name())
	}
	if p.DisplayName() != "Google Drive" {
		t.Fatalf("expected display name 'Google Drive', got %q", p.DisplayName())
	}
}

func TestDriveDefaultConfig(t *testing.T) {
	p := &DriveProvider{}
	cfg := p.DefaultConfig()

	var parsed DriveConfig
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("default config is not valid JSON: %v", err)
	}
	if parsed.AccessToken != "" {
		t.Fatal("default access_token should be empty")
	}
	if parsed.IncludeShared {
		t.Fatal("default include_shared should be false")
	}
	if parsed.MaxContentKB != 100 {
		t.Fatalf("expected max_content_kb 100, got %d", parsed.MaxContentKB)
	}
}

func TestDriveValidateConfig(t *testing.T) {
	p := &DriveProvider{}

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:    "valid minimal",
			config:  `{"access_token": "ya29.test"}`,
			wantErr: false,
		},
		{
			name:    "valid with folders",
			config:  `{"access_token": "ya29.test", "folder_ids": ["abc123"]}`,
			wantErr: false,
		},
		{
			name:    "missing token",
			config:  `{"access_token": ""}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			config:  `not json`,
			wantErr: true,
		},
		{
			name:    "valid with all options",
			config:  `{"access_token": "ya29.test", "folder_ids": ["a"], "include_shared": true, "include_content": false, "max_content_kb": 200}`,
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

func TestDriveConfigDefaults(t *testing.T) {
	cfg := DriveConfig{AccessToken: "test"}

	// Nil IncludeContent = default true
	if !cfg.includeContent() {
		t.Fatal("expected includeContent default true")
	}

	// Explicit false
	f := false
	cfg.IncludeContent = &f
	if cfg.includeContent() {
		t.Fatal("expected includeContent false when explicitly set")
	}

	// Explicit true
	tr := true
	cfg.IncludeContent = &tr
	if !cfg.includeContent() {
		t.Fatal("expected includeContent true when explicitly set")
	}
}

func TestFileToRecord(t *testing.T) {
	file := driveFile{
		ID:           "doc123",
		Name:         "Q & SB Wedding Planning",
		MimeType:     "application/vnd.google-apps.document",
		ModifiedTime: "2026-02-22T10:00:00Z",
		Owners:       []driveOwner{{DisplayName: "Q", EmailAddress: "q@example.com"}},
		WebViewLink:  "https://docs.google.com/document/d/doc123/edit",
		Description:  "Master wedding checklist",
		Starred:      true,
	}

	r := fileToRecord(file, "personal")

	if r.Content == "" {
		t.Fatal("expected non-empty content")
	}
	if r.Source != "drive/doc123" {
		t.Fatalf("unexpected source: %s", r.Source)
	}
	if r.Section != "Q & SB Wedding Planning" {
		t.Fatalf("unexpected section: %s", r.Section)
	}
	if r.Project != "personal" {
		t.Fatalf("unexpected project: %s", r.Project)
	}
	if r.ExternalID != "gdrive:doc123" {
		t.Fatalf("unexpected external ID: %s", r.ExternalID)
	}

	// Check content includes key details
	if !containsLower(r.Content, "Google Doc") {
		t.Fatal("content missing friendly mime type")
	}
	if !containsLower(r.Content, "Wedding Planning") {
		t.Fatal("content missing file name")
	}
	if !containsLower(r.Content, "Q") {
		t.Fatal("content missing owner")
	}
	if !containsLower(r.Content, "Starred") {
		t.Fatal("content missing starred indicator")
	}
	if !containsLower(r.Content, "Master wedding checklist") {
		t.Fatal("content missing description")
	}
}

func TestFileToRecordRegularFile(t *testing.T) {
	file := driveFile{
		ID:           "pdf456",
		Name:         "Invoice-2026-02.pdf",
		MimeType:     "application/pdf",
		ModifiedTime: "2026-02-20T15:30:00Z",
		Size:         "2500000",
		Owners:       []driveOwner{{EmailAddress: "vendor@example.com"}},
		WebViewLink:  "https://drive.google.com/file/d/pdf456/view",
	}

	r := fileToRecord(file, "")

	if !containsLower(r.Content, "PDF") {
		t.Fatal("content missing PDF type")
	}
	if !containsLower(r.Content, "2.4 MB") {
		t.Fatalf("content missing or wrong file size in: %s", r.Content)
	}
	if r.MemoryClass != "" {
		t.Fatalf("expected empty class for regular file, got %q", r.MemoryClass)
	}
}

func TestClassifyFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{"meeting notes", "Weekly Meeting Notes", "decision"},
		{"policy", "Team Policy Guide", "rule"},
		{"checklist", "Onboarding Checklist", "status"},
		{"roadmap", "2026 Product Roadmap", "decision"},
		{"spec", "API Spec v2", "decision"},
		{"plain file", "photo.jpg", ""},
		{"rfc", "RFC: New Architecture", "decision"},
		{"tracker", "Bug Tracker", "status"},
		{"minutes", "Board Meeting Minutes", "decision"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := driveFile{Name: tt.filename}
			got := classifyFile(file)
			if got != tt.expected {
				t.Fatalf("classifyFile(%q) = %q, want %q", tt.filename, got, tt.expected)
			}
		})
	}
}

func TestIsExportable(t *testing.T) {
	tests := []struct {
		mimeType   string
		exportable bool
	}{
		{"application/vnd.google-apps.document", true},
		{"application/vnd.google-apps.spreadsheet", true},
		{"application/vnd.google-apps.presentation", true},
		{"application/pdf", false},
		{"image/png", false},
		{"text/plain", false},
		{"application/vnd.google-apps.folder", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			got := isExportable(tt.mimeType)
			if got != tt.exportable {
				t.Fatalf("isExportable(%q) = %v, want %v", tt.mimeType, got, tt.exportable)
			}
		})
	}
}

func TestExportMimeType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"application/vnd.google-apps.document", "text/plain"},
		{"application/vnd.google-apps.spreadsheet", "text/csv"},
		{"application/vnd.google-apps.presentation", "text/plain"},
		{"application/pdf", ""},
		{"text/plain", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := exportMimeType(tt.input)
			if got != tt.expected {
				t.Fatalf("exportMimeType(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFriendlyMimeType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"application/vnd.google-apps.document", "Google Doc"},
		{"application/vnd.google-apps.spreadsheet", "Google Sheet"},
		{"application/vnd.google-apps.presentation", "Google Slides"},
		{"application/vnd.google-apps.folder", "Folder"},
		{"application/pdf", "PDF"},
		{"text/plain", "Text"},
		{"text/markdown", "Markdown"},
		{"application/json", "JSON"},
		{"application/octet-stream", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := friendlyMimeType(tt.input)
			if got != tt.expected {
				t.Fatalf("friendlyMimeType(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"0", "0 B"},
		{"500", "500 B"},
		{"1024", "1.0 KB"},
		{"1048576", "1.0 MB"},
		{"1073741824", "1.0 GB"},
		{"2500000", "2.4 MB"},
		{"51200", "50.0 KB"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := formatFileSize(tt.input)
			if got != tt.expected {
				t.Fatalf("formatFileSize(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDriveFetchWithMockServer(t *testing.T) {
	files := driveFileList{
		Files: []driveFile{
			{
				ID:           "doc1",
				Name:         "Meeting Notes",
				MimeType:     "application/vnd.google-apps.document",
				ModifiedTime: "2026-02-22T10:00:00Z",
				Owners:       []driveOwner{{DisplayName: "Q", EmailAddress: "q@test.com"}},
				WebViewLink:  "https://docs.google.com/document/d/doc1/edit",
			},
			{
				ID:           "sheet1",
				Name:         "Budget Tracker",
				MimeType:     "application/vnd.google-apps.spreadsheet",
				ModifiedTime: "2026-02-21T15:00:00Z",
				Owners:       []driveOwner{{DisplayName: "Q", EmailAddress: "q@test.com"}},
			},
			{
				ID:           "pdf1",
				Name:         "contract.pdf",
				MimeType:     "application/pdf",
				ModifiedTime: "2026-02-20T12:00:00Z",
				Size:         "102400",
				Owners:       []driveOwner{{EmailAddress: "vendor@test.com"}},
			},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(files)
	})
	mux.HandleFunc("/files/doc1/export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("These are the meeting notes from today's standup."))
	})
	mux.HandleFunc("/files/sheet1/export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Write([]byte("Category,Amount\nRent,2000\nFood,500"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Override base URL
	oldBase := driveBaseURL
	driveBaseURL = server.URL
	defer func() { driveBaseURL = oldBase }()

	p := &DriveProvider{}
	records, err := p.Fetch(context.Background(),
		json.RawMessage(`{"access_token": "test-token", "include_content": true}`),
		nil,
	)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	// First record: Google Doc with exported content
	r0 := records[0]
	if r0.Section != "Meeting Notes" {
		t.Fatalf("expected 'Meeting Notes', got %q", r0.Section)
	}
	if r0.MemoryClass != "decision" { // "meeting notes" classification
		t.Fatalf("expected 'decision' class, got %q", r0.MemoryClass)
	}
	if !containsLower(r0.Content, "meeting notes from today") {
		t.Fatal("expected exported content in record")
	}

	// Second record: Google Sheet with CSV content
	r1 := records[1]
	if r1.Section != "Budget Tracker" {
		t.Fatalf("expected 'Budget Tracker', got %q", r1.Section)
	}
	if !containsLower(r1.Content, "Rent,2000") {
		t.Fatal("expected CSV content in sheet record")
	}

	// Third record: PDF (metadata only, no content export)
	r2 := records[2]
	if r2.Section != "contract.pdf" {
		t.Fatalf("expected 'contract.pdf', got %q", r2.Section)
	}
	if containsLower(r2.Content, "export") {
		t.Fatal("PDF should not have exported content")
	}
}

func TestDriveFetchNoContent(t *testing.T) {
	files := driveFileList{
		Files: []driveFile{
			{
				ID:           "doc1",
				Name:         "Test Doc",
				MimeType:     "application/vnd.google-apps.document",
				ModifiedTime: "2026-02-22T10:00:00Z",
				Owners:       []driveOwner{{EmailAddress: "q@test.com"}},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(files)
	}))
	defer server.Close()

	oldBase := driveBaseURL
	driveBaseURL = server.URL
	defer func() { driveBaseURL = oldBase }()

	p := &DriveProvider{}
	records, err := p.Fetch(context.Background(),
		json.RawMessage(`{"access_token": "test", "include_content": false}`),
		nil,
	)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	// Should NOT contain exported content
	if containsLower(records[0].Content, "export") {
		t.Fatal("should not have exported content when include_content is false")
	}
}

func TestDriveFetchValidationError(t *testing.T) {
	p := &DriveProvider{}

	_, err := p.Fetch(context.Background(), json.RawMessage(`{"access_token": ""}`), nil)
	if err == nil {
		t.Fatal("expected validation error for missing token")
	}
}

func TestDriveFetchEnvFallback(t *testing.T) {
	p := &DriveProvider{}

	// Without env var
	t.Setenv("GOOGLE_ACCESS_TOKEN", "")
	_, err := p.Fetch(context.Background(), json.RawMessage(`{"access_token": ""}`), nil)
	if err == nil {
		t.Fatal("expected error without token")
	}

	// With env var: passes token check, fails on HTTP (expected)
	t.Setenv("GOOGLE_ACCESS_TOKEN", "ya29.from-env")
	_, err = p.Fetch(context.Background(), json.RawMessage(`{"access_token": ""}`), nil)
	if err == nil {
		return // if somehow worked, fine
	}
	if containsLower(err.Error(), "no access token") {
		t.Fatalf("env var fallback didn't work: %v", err)
	}
}

func TestDriveFetchIncremental(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(driveFileList{})
	}))
	defer server.Close()

	oldBase := driveBaseURL
	driveBaseURL = server.URL
	defer func() { driveBaseURL = oldBase }()

	p := &DriveProvider{}
	since := time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)
	_, err := p.Fetch(context.Background(),
		json.RawMessage(`{"access_token": "test"}`),
		&since,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsLower(receivedQuery, "modifiedTime") {
		t.Fatalf("incremental sync should include modifiedTime filter, got query: %s", receivedQuery)
	}
}

func TestDriveFetchWithFolders(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(driveFileList{})
	}))
	defer server.Close()

	oldBase := driveBaseURL
	driveBaseURL = server.URL
	defer func() { driveBaseURL = oldBase }()

	p := &DriveProvider{}
	_, err := p.Fetch(context.Background(),
		json.RawMessage(`{"access_token": "test", "folder_ids": ["folder1", "folder2"]}`),
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(queries) != 2 {
		t.Fatalf("expected 2 queries (one per folder), got %d", len(queries))
	}

	if !containsLower(queries[0], "folder1") {
		t.Fatalf("first query should reference folder1, got: %s", queries[0])
	}
	if !containsLower(queries[1], "folder2") {
		t.Fatalf("second query should reference folder2, got: %s", queries[1])
	}
}

func TestGoogleClientUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "Invalid Credentials", "code": 401}}`))
	}))
	defer server.Close()

	client := &googleClient{
		accessToken: "bad-token",
		httpClient:  server.Client(),
	}

	var result driveFileList
	err := client.get(context.Background(), server.URL+"/files", &result)
	if err == nil {
		t.Fatal("expected error for unauthorized request")
	}
	if !containsLower(err.Error(), "401") {
		t.Fatalf("expected 401 in error, got: %v", err)
	}
}
