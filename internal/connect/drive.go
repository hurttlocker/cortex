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

// DriveProvider imports file metadata and content from Google Drive.
type DriveProvider struct{}

// DriveConfig holds the configuration for the Google Drive connector.
type DriveConfig struct {
	// AccessToken is a Google OAuth 2.0 access token with drive.readonly scope.
	AccessToken string `json:"access_token"`

	// FolderIDs limits sync to specific folders. Empty = all user files.
	FolderIDs []string `json:"folder_ids,omitempty"`

	// IncludeShared also syncs files shared with the user (default: false).
	IncludeShared bool `json:"include_shared,omitempty"`

	// IncludeContent exports text content from Google Docs (default: true).
	IncludeContent *bool `json:"include_content,omitempty"`

	// MaxContentKB caps exported content size in KB (default: 100).
	MaxContentKB int `json:"max_content_kb,omitempty"`

	// Project is the Cortex project tag for imported memories.
	Project string `json:"project,omitempty"`
}

func (c *DriveConfig) includeContent() bool { return c.IncludeContent == nil || *c.IncludeContent }

func init() {
	DefaultRegistry.Register(&DriveProvider{})
}

func (p *DriveProvider) Name() string        { return "drive" }
func (p *DriveProvider) DisplayName() string { return "Google Drive" }

func (p *DriveProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{
  "access_token": "",
  "folder_ids": [],
  "include_shared": false,
  "include_content": true,
  "max_content_kb": 100,
  "project": ""
}`)
}

func (p *DriveProvider) ValidateConfig(config json.RawMessage) error {
	var cfg DriveConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if cfg.AccessToken == "" {
		return fmt.Errorf("access_token is required (Google OAuth 2.0 token with drive.readonly scope)")
	}
	return nil
}

func (p *DriveProvider) Fetch(ctx context.Context, config json.RawMessage, since *time.Time) ([]Record, error) {
	var cfg DriveConfig
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

	maxContentKB := cfg.MaxContentKB
	if maxContentKB <= 0 {
		maxContentKB = 100
	}

	client := newGoogleClient(cfg.AccessToken)

	if len(cfg.FolderIDs) > 0 {
		// Sync specific folders
		var allRecords []Record
		for _, folderID := range cfg.FolderIDs {
			records, err := p.fetchFiles(ctx, client, &cfg, folderID, since, maxContentKB)
			if err != nil {
				return nil, fmt.Errorf("fetching files for folder %s: %w", folderID, err)
			}
			allRecords = append(allRecords, records...)
		}
		return allRecords, nil
	}

	// Sync all user files
	return p.fetchFiles(ctx, client, &cfg, "", since, maxContentKB)
}

// driveBaseURL is the Google Drive API base. Variable for test injection.
var driveBaseURL = "https://www.googleapis.com/drive/v3"

func (p *DriveProvider) fetchFiles(ctx context.Context, client *googleClient, cfg *DriveConfig, folderID string, since *time.Time, maxContentKB int) ([]Record, error) {
	params := url.Values{}
	params.Set("pageSize", "100")
	params.Set("fields", "nextPageToken,files(id,name,mimeType,modifiedTime,size,owners,webViewLink,description,starred)")

	// Build query
	var queryParts []string
	queryParts = append(queryParts, "trashed = false")

	if folderID != "" {
		queryParts = append(queryParts, fmt.Sprintf("'%s' in parents", folderID))
	}

	if cfg.IncludeShared {
		// Don't filter by ownership — include shared files
	} else {
		queryParts = append(queryParts, "'me' in owners")
	}

	if since != nil {
		queryParts = append(queryParts, fmt.Sprintf("modifiedTime > '%s'", since.Format(time.RFC3339)))
	}

	params.Set("q", strings.Join(queryParts, " and "))
	params.Set("orderBy", "modifiedTime desc")

	var allRecords []Record
	pages := 0

	for {
		reqURL := driveBaseURL + "/files?" + params.Encode()

		var result driveFileList
		if err := client.get(ctx, reqURL, &result); err != nil {
			return nil, err
		}

		for _, file := range result.Files {
			record := fileToRecord(file, cfg.Project)

			// Export content for Google Docs if enabled
			if cfg.includeContent() && isExportable(file.MimeType) {
				content, err := p.exportContent(ctx, client, file.ID, file.MimeType, int64(maxContentKB)*1024)
				if err == nil && content != "" {
					record.Content += "\n\n" + content
				}
				// Non-fatal: if export fails, we still have metadata
			}

			allRecords = append(allRecords, record)
		}

		if result.NextPageToken == "" {
			break
		}
		params.Set("pageToken", result.NextPageToken)
		pages++
		if pages > 10 {
			break // safety cap: 1100 files max
		}
	}

	return allRecords, nil
}

// exportContent exports a Google Workspace file as plain text.
func (p *DriveProvider) exportContent(ctx context.Context, client *googleClient, fileID, mimeType string, maxBytes int64) (string, error) {
	exportMime := exportMimeType(mimeType)
	if exportMime == "" {
		return "", nil
	}

	exportURL := fmt.Sprintf("%s/files/%s/export?mimeType=%s", driveBaseURL, url.PathEscape(fileID), url.QueryEscape(exportMime))

	content, err := client.getRaw(ctx, exportURL, maxBytes)
	if err != nil {
		return "", err
	}

	// Truncate if needed
	if len(content) > 2000 {
		content = content[:2000] + "\n... (truncated)"
	}

	return content, nil
}

// fileToRecord converts a Google Drive file to a Cortex Record (metadata).
func fileToRecord(file driveFile, project string) Record {
	var sb strings.Builder

	fmt.Fprintf(&sb, "[Google Drive] %s\n", file.Name)
	fmt.Fprintf(&sb, "Type: %s", friendlyMimeType(file.MimeType))

	if file.Size != "" && file.Size != "0" {
		fmt.Fprintf(&sb, " | Size: %s", formatFileSize(file.Size))
	}

	modTime := parseGoogleTime(file.ModifiedTime)
	if !modTime.IsZero() {
		fmt.Fprintf(&sb, " | Modified: %s", modTime.Format("Jan 2, 2006"))
	}
	sb.WriteString("\n")

	// Owner
	if len(file.Owners) > 0 {
		owner := file.Owners[0]
		name := owner.DisplayName
		if name == "" {
			name = owner.EmailAddress
		}
		fmt.Fprintf(&sb, "Owner: %s\n", name)
	}

	// Link
	if file.WebViewLink != "" {
		fmt.Fprintf(&sb, "Link: %s\n", file.WebViewLink)
	}

	// Description
	if file.Description != "" {
		desc := file.Description
		if len(desc) > 500 {
			desc = desc[:500] + "..."
		}
		fmt.Fprintf(&sb, "Description: %s\n", desc)
	}

	// Starred
	if file.Starred {
		sb.WriteString("⭐ Starred\n")
	}

	ts := modTime
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	return Record{
		Content:     sb.String(),
		Source:      fmt.Sprintf("drive/%s", file.ID),
		Section:     file.Name,
		Project:     project,
		MemoryClass: classifyFile(file),
		Timestamp:   ts,
		ExternalID:  fmt.Sprintf("gdrive:%s", file.ID),
	}
}

// classifyFile assigns a memory class based on file characteristics.
func classifyFile(file driveFile) string {
	lower := strings.ToLower(file.Name)

	switch {
	case strings.Contains(lower, "meeting notes") || strings.Contains(lower, "minutes"):
		return "decision"
	case strings.Contains(lower, "policy") || strings.Contains(lower, "rules"):
		return "rule"
	case strings.Contains(lower, "checklist") || strings.Contains(lower, "tracker"):
		return "status"
	case strings.Contains(lower, "plan") || strings.Contains(lower, "roadmap"):
		return "decision"
	case strings.Contains(lower, "spec") || strings.Contains(lower, "rfc"):
		return "decision"
	}

	return ""
}

// isExportable returns true if the mime type is a Google Workspace format
// that can be exported as text.
func isExportable(mimeType string) bool {
	return exportMimeType(mimeType) != ""
}

// exportMimeType returns the export format for a Google Workspace mime type.
func exportMimeType(mimeType string) string {
	switch mimeType {
	case "application/vnd.google-apps.document":
		return "text/plain"
	case "application/vnd.google-apps.spreadsheet":
		return "text/csv"
	case "application/vnd.google-apps.presentation":
		return "text/plain"
	default:
		return ""
	}
}

// friendlyMimeType returns a human-readable name for common mime types.
func friendlyMimeType(mimeType string) string {
	switch mimeType {
	case "application/vnd.google-apps.document":
		return "Google Doc"
	case "application/vnd.google-apps.spreadsheet":
		return "Google Sheet"
	case "application/vnd.google-apps.presentation":
		return "Google Slides"
	case "application/vnd.google-apps.folder":
		return "Folder"
	case "application/vnd.google-apps.form":
		return "Google Form"
	case "application/pdf":
		return "PDF"
	case "text/plain":
		return "Text"
	case "text/markdown":
		return "Markdown"
	case "application/json":
		return "JSON"
	default:
		return mimeType
	}
}

// formatFileSize converts a byte count string to a human-readable size.
func formatFileSize(sizeStr string) string {
	// Parse as int
	var size int64
	for _, c := range sizeStr {
		if c >= '0' && c <= '9' {
			size = size*10 + int64(c-'0')
		}
	}

	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// --- Google Drive API types ---

type driveFileList struct {
	Files         []driveFile `json:"files"`
	NextPageToken string      `json:"nextPageToken"`
}

type driveFile struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	MimeType     string        `json:"mimeType"`
	ModifiedTime string        `json:"modifiedTime"`
	Size         string        `json:"size"`
	Owners       []driveOwner  `json:"owners"`
	WebViewLink  string        `json:"webViewLink"`
	Description  string        `json:"description"`
	Starred      bool          `json:"starred"`
}

type driveOwner struct {
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}
