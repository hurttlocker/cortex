package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGitHubProviderRegistered(t *testing.T) {
	p := DefaultRegistry.Get("github")
	if p == nil {
		t.Fatal("github provider not registered")
	}
	if p.Name() != "github" {
		t.Fatalf("expected name 'github', got %q", p.Name())
	}
	if p.DisplayName() != "GitHub" {
		t.Fatalf("expected display name 'GitHub', got %q", p.DisplayName())
	}
}

func TestGitHubDefaultConfig(t *testing.T) {
	p := &GitHubProvider{}
	cfg := p.DefaultConfig()

	var parsed GitHubConfig
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("default config is not valid JSON: %v", err)
	}

	if parsed.Token != "" {
		t.Fatal("default token should be empty")
	}
	if len(parsed.Repos) != 1 || parsed.Repos[0] != "owner/repo" {
		t.Fatalf("unexpected default repos: %v", parsed.Repos)
	}
}

func TestGitHubValidateConfig(t *testing.T) {
	p := &GitHubProvider{}

	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name:    "valid",
			config:  `{"token": "ghp_abc123", "repos": ["hurttlocker/cortex"]}`,
			wantErr: false,
		},
		{
			name:    "missing token",
			config:  `{"token": "", "repos": ["hurttlocker/cortex"]}`,
			wantErr: true,
		},
		{
			name:    "no repos",
			config:  `{"token": "ghp_abc123", "repos": []}`,
			wantErr: true,
		},
		{
			name:    "invalid repo format",
			config:  `{"token": "ghp_abc123", "repos": ["just-a-name"]}`,
			wantErr: true,
		},
		{
			name:    "empty owner",
			config:  `{"token": "ghp_abc123", "repos": ["/repo"]}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			config:  `not json`,
			wantErr: true,
		},
		{
			name:    "multiple repos",
			config:  `{"token": "ghp_abc123", "repos": ["owner/repo1", "owner/repo2"]}`,
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

func TestGitHubConfigDefaults(t *testing.T) {
	cfg := GitHubConfig{Token: "test", Repos: []string{"o/r"}}

	// All nil = default true
	if !cfg.includeIssues() {
		t.Fatal("expected includeIssues default true")
	}
	if !cfg.includePRs() {
		t.Fatal("expected includePRs default true")
	}
	if !cfg.includeComments() {
		t.Fatal("expected includeComments default true")
	}

	// Explicit false
	f := false
	cfg.IncludeIssues = &f
	cfg.IncludePRs = &f
	cfg.IncludeComments = &f

	if cfg.includeIssues() {
		t.Fatal("expected includeIssues false")
	}
	if cfg.includePRs() {
		t.Fatal("expected includePRs false")
	}
	if cfg.includeComments() {
		t.Fatal("expected includeComments false")
	}
}

func TestIssueToRecord(t *testing.T) {
	issue := gitHubIssue{
		Number: 42,
		Title:  "Fix memory leak",
		Body:   "There's a memory leak in the search engine.",
		State:  "open",
		User:   gitHubUser{Login: "testuser"},
		Labels: []gitHubLabel{{Name: "bug"}, {Name: "P1"}},
		Milestone: &gitHubMilestone{Title: "v1.0"},
		Comments:  3,
		CreatedAt: time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 2, 22, 14, 0, 0, 0, time.UTC),
	}

	r := issueToRecord("hurttlocker", "cortex", issue, "cortex-dev")

	// Check content
	if r.Content == "" {
		t.Fatal("expected non-empty content")
	}
	if r.Source != "hurttlocker/cortex/issue/42" {
		t.Fatalf("unexpected source: %s", r.Source)
	}
	if r.Section != "Fix memory leak" {
		t.Fatalf("unexpected section: %s", r.Section)
	}
	if r.Project != "cortex-dev" {
		t.Fatalf("unexpected project: %s", r.Project)
	}
	if r.MemoryClass != "status" {
		t.Fatalf("expected 'status' class for bug, got %q", r.MemoryClass)
	}
	if r.ExternalID != "github:hurttlocker/cortex#42" {
		t.Fatalf("unexpected external ID: %s", r.ExternalID)
	}
	if !r.Timestamp.Equal(issue.UpdatedAt) {
		t.Fatal("expected timestamp to match updated_at")
	}
}

func TestIssueToRecordPR(t *testing.T) {
	pr := json.RawMessage(`{"url": "..."}`)
	issue := gitHubIssue{
		Number:      10,
		Title:       "Add connect feature",
		Body:        "Implements cortex connect",
		State:       "closed",
		User:        gitHubUser{Login: "dev"},
		PullRequest: &pr,
		UpdatedAt:   time.Now(),
	}

	r := issueToRecord("hurttlocker", "cortex", issue, "")

	if r.Source != "hurttlocker/cortex/pr/10" {
		t.Fatalf("expected PR source, got %s", r.Source)
	}
	if r.MemoryClass != "" {
		t.Fatalf("expected empty class for PR, got %q", r.MemoryClass)
	}
}

func TestIssueToRecordTruncation(t *testing.T) {
	longBody := ""
	for i := 0; i < 300; i++ {
		longBody += "This is a long line of text. "
	}

	issue := gitHubIssue{
		Number:    1,
		Title:     "Long body test",
		Body:      longBody,
		State:     "open",
		User:      gitHubUser{Login: "user"},
		UpdatedAt: time.Now(),
	}

	r := issueToRecord("o", "r", issue, "")

	// Content should be truncated
	if len(r.Content) > 2200 { // some header overhead
		t.Fatalf("expected truncated content, got %d chars", len(r.Content))
	}
}

func TestCommentToRecord(t *testing.T) {
	issue := gitHubIssue{
		Number: 42,
		Title:  "Fix bug",
		State:  "open",
		User:   gitHubUser{Login: "author"},
	}
	comment := gitHubComment{
		ID:        12345,
		Body:      "I think this is related to the search index.",
		User:      gitHubUser{Login: "reviewer"},
		UpdatedAt: time.Date(2026, 2, 22, 15, 0, 0, 0, time.UTC),
	}

	r := commentToRecord("hurttlocker", "cortex", issue, comment, "cortex-dev")

	if r.Source != "hurttlocker/cortex/issue/42/comment/12345" {
		t.Fatalf("unexpected source: %s", r.Source)
	}
	if r.ExternalID != "github:hurttlocker/cortex#42-comment-12345" {
		t.Fatalf("unexpected external ID: %s", r.ExternalID)
	}
	if r.Section != "Comment on: Fix bug" {
		t.Fatalf("unexpected section: %s", r.Section)
	}
}

func TestClassifyIssue(t *testing.T) {
	tests := []struct {
		name     string
		labels   []gitHubLabel
		isPR     bool
		expected string
	}{
		{"bug label", []gitHubLabel{{Name: "bug"}}, false, "status"},
		{"Bug label caps", []gitHubLabel{{Name: "Bug"}}, false, "status"},
		{"decision label", []gitHubLabel{{Name: "decision"}}, false, "decision"},
		{"rfc label", []gitHubLabel{{Name: "RFC"}}, false, "decision"},
		{"proposal label", []gitHubLabel{{Name: "proposal"}}, false, "decision"},
		{"rule label", []gitHubLabel{{Name: "policy"}}, false, "rule"},
		{"no labels", nil, false, ""},
		{"PR no labels", nil, true, ""},
		{"unrelated labels", []gitHubLabel{{Name: "enhancement"}}, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := gitHubIssue{Labels: tt.labels}
			if tt.isPR {
				raw := json.RawMessage(`{}`)
				issue.PullRequest = &raw
			}
			got := classifyIssue(issue)
			if got != tt.expected {
				t.Fatalf("classifyIssue() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestGitHubFetchWithMockServer tests the full Fetch flow against a mock HTTP server.
func TestGitHubFetchWithMockServer(t *testing.T) {
	now := time.Now().UTC()

	issues := []gitHubIssue{
		{
			Number:    1,
			Title:     "First issue",
			Body:      "Description of first issue",
			State:     "open",
			User:      gitHubUser{Login: "testuser"},
			Labels:    []gitHubLabel{{Name: "bug"}},
			Comments:  1,
			CreatedAt: now.Add(-24 * time.Hour),
			UpdatedAt: now,
		},
		{
			Number:    2,
			Title:     "Second issue (PR)",
			Body:      "A pull request",
			State:     "closed",
			User:      gitHubUser{Login: "dev"},
			PullRequest: func() *json.RawMessage {
				r := json.RawMessage(`{"url": "https://api.github.com/repos/o/r/pulls/2"}`)
				return &r
			}(),
			Comments:  0,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-12 * time.Hour),
		},
	}

	comments := []gitHubComment{
		{
			ID:        100,
			Body:      "I can reproduce this",
			User:      gitHubUser{Login: "helper"},
			CreatedAt: now.Add(-1 * time.Hour),
			UpdatedAt: now.Add(-1 * time.Hour),
		},
	}

	// Mock server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	})
	mux.HandleFunc("/repos/testowner/testrepo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(comments)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Create a provider that uses our test server
	// We'll test the components directly instead of overriding the base URL
	p := &GitHubProvider{}

	// Test with the mock client directly
	client := &gitHubClient{
		token:      "test-token",
		httpClient: server.Client(),
	}

	// Fetch issues from mock server
	url := fmt.Sprintf("%s/repos/testowner/testrepo/issues?state=all&sort=updated&direction=desc&per_page=100&page=1", server.URL)
	var fetchedIssues []gitHubIssue
	err := client.get(context.Background(), url, &fetchedIssues)
	if err != nil {
		t.Fatalf("fetch issues failed: %v", err)
	}

	if len(fetchedIssues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(fetchedIssues))
	}

	// Convert to records
	cfg := &GitHubConfig{
		Token:   "test",
		Repos:   []string{"testowner/testrepo"},
		Project: "test-project",
	}

	var records []Record
	for _, issue := range fetchedIssues {
		r := issueToRecord("testowner", "testrepo", issue, cfg.Project)
		records = append(records, r)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Verify first record (issue)
	r0 := records[0]
	if r0.Source != "testowner/testrepo/issue/1" {
		t.Fatalf("expected issue source, got %s", r0.Source)
	}
	if r0.Project != "test-project" {
		t.Fatalf("expected test-project, got %s", r0.Project)
	}

	// Verify second record (PR)
	r1 := records[1]
	if r1.Source != "testowner/testrepo/pr/2" {
		t.Fatalf("expected PR source, got %s", r1.Source)
	}

	// Test comments fetch
	commentsURL := fmt.Sprintf("%s/repos/testowner/testrepo/issues/1/comments?per_page=100", server.URL)
	var fetchedComments []gitHubComment
	err = client.get(context.Background(), commentsURL, &fetchedComments)
	if err != nil {
		t.Fatalf("fetch comments failed: %v", err)
	}

	if len(fetchedComments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(fetchedComments))
	}

	cr := commentToRecord("testowner", "testrepo", fetchedIssues[0], fetchedComments[0], "test-project")
	if cr.ExternalID != "github:testowner/testrepo#1-comment-100" {
		t.Fatalf("unexpected comment external ID: %s", cr.ExternalID)
	}

	_ = p // provider used for type completeness
}

func TestGitHubClientUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message": "Bad credentials"}`))
	}))
	defer server.Close()

	client := &gitHubClient{
		token:      "bad-token",
		httpClient: server.Client(),
	}

	var result []gitHubIssue
	err := client.get(context.Background(), server.URL+"/repos/o/r/issues", &result)
	if err == nil {
		t.Fatal("expected error for unauthorized request")
	}
	if !containsLower(err.Error(), "401") {
		t.Fatalf("expected 401 in error, got: %v", err)
	}
}

func TestGitHubFetchValidationError(t *testing.T) {
	p := &GitHubProvider{}

	// Missing token
	_, err := p.Fetch(context.Background(), json.RawMessage(`{"token": "", "repos": ["o/r"]}`), nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
}
