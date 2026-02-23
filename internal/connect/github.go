package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GitHubProvider imports issues and PR activity from GitHub repositories.
type GitHubProvider struct{}

// GitHubConfig holds the configuration for the GitHub connector.
type GitHubConfig struct {
	// Token is a GitHub personal access token (PAT) or fine-grained token.
	Token string `json:"token"`

	// Repos is a list of "owner/repo" strings to sync.
	Repos []string `json:"repos"`

	// IncludeIssues controls whether issues are synced (default: true).
	IncludeIssues *bool `json:"include_issues,omitempty"`

	// IncludePRs controls whether pull requests are synced (default: true).
	IncludePRs *bool `json:"include_prs,omitempty"`

	// IncludeComments controls whether issue/PR comments are included (default: true).
	IncludeComments *bool `json:"include_comments,omitempty"`

	// Project is the Cortex project tag for imported memories.
	Project string `json:"project,omitempty"`
}

func (c *GitHubConfig) includeIssues() bool   { return c.IncludeIssues == nil || *c.IncludeIssues }
func (c *GitHubConfig) includePRs() bool      { return c.IncludePRs == nil || *c.IncludePRs }
func (c *GitHubConfig) includeComments() bool { return c.IncludeComments == nil || *c.IncludeComments }

func init() {
	DefaultRegistry.Register(&GitHubProvider{})
}

func (g *GitHubProvider) Name() string        { return "github" }
func (g *GitHubProvider) DisplayName() string { return "GitHub" }

func (g *GitHubProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "token": "",
  "repos": ["owner/repo"],
  "include_issues": true,
  "include_prs": true,
  "include_comments": true,
  "project": ""
}`)
}

func (g *GitHubProvider) ValidateConfig(config json.RawMessage) error {
	var cfg GitHubConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if cfg.Token == "" {
		return fmt.Errorf("token is required (GitHub PAT or fine-grained token)")
	}
	if len(cfg.Repos) == 0 {
		return fmt.Errorf("at least one repo is required (format: owner/repo)")
	}
	for _, repo := range cfg.Repos {
		parts := strings.SplitN(repo, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid repo format %q (expected owner/repo)", repo)
		}
	}
	return nil
}

func (g *GitHubProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	var cfg GitHubConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Fall back to GITHUB_TOKEN env var if no token in config
	if cfg.Token == "" {
		cfg.Token = os.Getenv("GITHUB_TOKEN")
	}

	if err := g.ValidateConfig(config); err != nil {
		// Re-validate with the env token injected
		if cfg.Token == "" {
			return nil, fmt.Errorf("no token provided: set in config or GITHUB_TOKEN env var")
		}
		if len(cfg.Repos) == 0 {
			return nil, fmt.Errorf("at least one repo is required (format: owner/repo)")
		}
	}

	client := &gitHubClient{
		token:      cfg.Token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	var allRecords []Record

	for _, repo := range cfg.Repos {
		parts := strings.SplitN(repo, "/", 2)
		owner, name := parts[0], parts[1]

		if cfg.includeIssues() || cfg.includePRs() {
			records, err := g.fetchIssuesAndPRs(ctx, client, owner, name, since, &cfg)
			if err != nil {
				return nil, fmt.Errorf("fetching issues/PRs for %s: %w", repo, err)
			}
			allRecords = append(allRecords, records...)
		}
	}

	return allRecords, nil
}

// fetchIssuesAndPRs fetches issues and/or PRs from a GitHub repo.
// GitHub's issues API returns both issues and PRs (PRs have pull_request field).
func (g *GitHubProvider) fetchIssuesAndPRs(ctx context.Context, client *gitHubClient, owner, repo string, since *time.Time, cfg *GitHubConfig) ([]Record, error) {
	// Build API URL
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?state=all&sort=updated&direction=desc&per_page=100", owner, repo)
	if since != nil {
		url += "&since=" + since.Format(time.RFC3339)
	}

	var allRecords []Record
	page := 1

	for {
		pageURL := fmt.Sprintf("%s&page=%d", url, page)

		var issues []gitHubIssue
		if err := client.get(ctx, pageURL, &issues); err != nil {
			return nil, err
		}

		if len(issues) == 0 {
			break
		}

		for _, issue := range issues {
			isPR := issue.PullRequest != nil

			// Filter based on config
			if isPR && !cfg.includePRs() {
				continue
			}
			if !isPR && !cfg.includeIssues() {
				continue
			}

			record := issueToRecord(owner, repo, issue, cfg.Project)
			allRecords = append(allRecords, record)

			// Fetch comments if enabled and there are any
			if cfg.includeComments() && issue.Comments > 0 {
				comments, err := g.fetchComments(ctx, client, owner, repo, issue.Number, since)
				if err != nil {
					// Non-fatal: log and continue
					continue
				}
				for _, comment := range comments {
					cr := commentToRecord(owner, repo, issue, comment, cfg.Project)
					allRecords = append(allRecords, cr)
				}
			}
		}

		if len(issues) < 100 {
			break // last page
		}
		page++

		// Safety cap: don't fetch more than 10 pages (1000 issues) in a single sync
		if page > 10 {
			break
		}
	}

	return allRecords, nil
}

func (g *GitHubProvider) fetchComments(ctx context.Context, client *gitHubClient, owner, repo string, issueNumber int, since *time.Time) ([]gitHubComment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments?per_page=100", owner, repo, issueNumber)
	if since != nil {
		url += "&since=" + since.Format(time.RFC3339)
	}

	var comments []gitHubComment
	if err := client.get(ctx, url, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// issueToRecord converts a GitHub issue/PR to a Cortex Record.
func issueToRecord(owner, repo string, issue gitHubIssue, project string) Record {
	isPR := issue.PullRequest != nil

	kind := "issue"
	if isPR {
		kind = "pr"
	}

	// Build rich content
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s/%s#%d] %s\n", owner, repo, issue.Number, issue.Title)
	fmt.Fprintf(&sb, "Type: %s | State: %s", kind, issue.State)
	if issue.User.Login != "" {
		fmt.Fprintf(&sb, " | Author: @%s", issue.User.Login)
	}
	if len(issue.Labels) > 0 {
		labels := make([]string, len(issue.Labels))
		for i, l := range issue.Labels {
			labels[i] = l.Name
		}
		fmt.Fprintf(&sb, " | Labels: %s", strings.Join(labels, ", "))
	}
	if issue.Milestone != nil {
		fmt.Fprintf(&sb, " | Milestone: %s", issue.Milestone.Title)
	}
	sb.WriteString("\n")

	if issue.Body != "" {
		// Truncate very long bodies
		body := issue.Body
		if len(body) > 2000 {
			body = body[:2000] + "\n... (truncated)"
		}
		sb.WriteString("\n")
		sb.WriteString(body)
	}

	return Record{
		Content:     sb.String(),
		Source:      fmt.Sprintf("%s/%s/%s/%d", owner, repo, kind, issue.Number),
		Section:     issue.Title,
		Project:     project,
		MemoryClass: classifyIssue(issue),
		Timestamp:   issue.UpdatedAt,
		ExternalID:  fmt.Sprintf("github:%s/%s#%d", owner, repo, issue.Number),
	}
}

// commentToRecord converts a GitHub comment to a Cortex Record.
func commentToRecord(owner, repo string, issue gitHubIssue, comment gitHubComment, project string) Record {
	isPR := issue.PullRequest != nil
	kind := "issue"
	if isPR {
		kind = "pr"
	}

	body := comment.Body
	if len(body) > 2000 {
		body = body[:2000] + "\n... (truncated)"
	}

	content := fmt.Sprintf("[%s/%s#%d comment] @%s:\n%s",
		owner, repo, issue.Number, comment.User.Login, body)

	return Record{
		Content:    content,
		Source:     fmt.Sprintf("%s/%s/%s/%d/comment/%d", owner, repo, kind, issue.Number, comment.ID),
		Section:    fmt.Sprintf("Comment on: %s", issue.Title),
		Project:    project,
		Timestamp:  comment.UpdatedAt,
		ExternalID: fmt.Sprintf("github:%s/%s#%d-comment-%d", owner, repo, issue.Number, comment.ID),
	}
}

// classifyIssue assigns a memory class based on issue characteristics.
func classifyIssue(issue gitHubIssue) string {
	for _, label := range issue.Labels {
		name := strings.ToLower(label.Name)
		switch {
		case strings.Contains(name, "bug"):
			return "status" // bug = current state
		case strings.Contains(name, "decision"):
			return "decision"
		case strings.Contains(name, "rfc") || strings.Contains(name, "proposal"):
			return "decision"
		case strings.Contains(name, "rule") || strings.Contains(name, "policy"):
			return "rule"
		}
	}
	if issue.PullRequest != nil {
		return "" // PRs don't have a strong class signal
	}
	return "" // Default: no class (let search/ranking handle it)
}

// --- GitHub API types ---

type gitHubIssue struct {
	Number      int              `json:"number"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	State       string           `json:"state"`
	User        gitHubUser       `json:"user"`
	Labels      []gitHubLabel    `json:"labels"`
	Milestone   *gitHubMilestone `json:"milestone"`
	PullRequest *json.RawMessage `json:"pull_request"`
	Comments    int              `json:"comments"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	ClosedAt    *time.Time       `json:"closed_at"`
}

type gitHubComment struct {
	ID        int64      `json:"id"`
	Body      string     `json:"body"`
	User      gitHubUser `json:"user"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type gitHubUser struct {
	Login string `json:"login"`
}

type gitHubLabel struct {
	Name string `json:"name"`
}

type gitHubMilestone struct {
	Title string `json:"title"`
}

// --- HTTP client ---

type gitHubClient struct {
	token      string
	httpClient *http.Client
}

func (c *gitHubClient) get(ctx context.Context, url string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	return nil
}
