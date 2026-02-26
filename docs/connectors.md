# Cortex Connect — Connector Setup Guide

Cortex Connect imports data from external services into your memory store.
All data flows through the standard ingest pipeline: provenance, fact extraction,
confidence decay, and search — everything works automatically.

## Quick Start

```bash
# 1. Initialize connector tables (first time only)
cortex connect init

# 2. Add a connector
cortex connect add github --config '{"token": "ghp_...", "repos": ["owner/repo"]}'

# 3. Sync
cortex connect sync --provider github

# 4. Check status
cortex connect status
```

## Available Providers

| Provider | Data Synced | Auth |
|----------|------------|------|
| **GitHub** | Issues, PRs, comments | Personal Access Token |
| **Gmail** | Email threads (metadata or full body) | gog CLI (OAuth) |
| **Google Calendar** | Events | gog CLI (OAuth) |
| **Google Drive** | Document content | gog CLI (OAuth) |
| **Slack** | Channel messages + threads | Bot User Token |
| **Discord** | Channel messages | Bot Token |
| **Telegram** | Chat messages | Bot Token + Chat ID |
| **Notion** | Page content | Integration Token |

---

## GitHub

Imports issues, pull requests, and comments from GitHub repositories.

### Setup

1. Create a Personal Access Token (classic or fine-grained):
   - Go to [GitHub Settings → Tokens](https://github.com/settings/tokens)
   - For classic tokens: select `repo` scope
   - For fine-grained: grant "Issues" and "Pull requests" read access

2. Add the connector:
```bash
cortex connect add github --config '{
  "token": "ghp_your_token_here",
  "repos": ["owner/repo1", "owner/repo2"],
  "include_issues": true,
  "include_prs": true,
  "include_comments": true,
  "project": "my-project"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | ✅ | — | GitHub PAT (or `GITHUB_TOKEN` env var as fallback) |
| `repos` | ✅ | — | List of `owner/repo` strings |
| `include_issues` | — | `true` | Sync issues |
| `include_prs` | — | `true` | Sync pull requests |
| `include_comments` | — | `true` | Sync issue/PR comments |
| `project` | — | `""` | Cortex project tag for scoped search |

### What Gets Synced

- **Issues**: Title, body, state, labels, milestone, assignees
- **PRs**: Same as issues (GitHub API treats PRs as issues)
- **Comments**: Body, author, timestamp — linked to parent issue/PR
- **Memory class**: Automatically classified (`status` for bugs, `decision` for RFCs/proposals)
- **Source format**: `github:owner/repo/issue/42`, `github:owner/repo/issue/42/comment/123`

### Incremental Sync

After the first full sync, subsequent syncs only fetch items updated since the last sync time.

---

## Gmail

Imports email threads via the [gog CLI](https://github.com/pterm/gog).

### Prerequisites

Install and configure gog:
```bash
# Install
brew install gog  # or: go install github.com/pterm/gog@latest

# Authenticate
gog auth login your@gmail.com
```

### Setup

```bash
cortex connect add gmail --config '{
  "account": "your@gmail.com",
  "query": "newer_than:7d",
  "max_results": 50,
  "include_bodies": false,
  "skip_categories": ["CATEGORY_PROMOTIONS", "CATEGORY_SOCIAL", "CATEGORY_UPDATES"],
  "project": "email"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `account` | ✅ | — | Gmail account email |
| `query` | — | `newer_than:7d` | Gmail search query |
| `max_results` | — | `50` | Max threads per sync (cap: 500) |
| `include_bodies` | — | `false` | Fetch full message bodies (slower, more storage) |
| `skip_categories` | — | `[]` | Gmail categories to skip |
| `project` | — | `""` | Cortex project tag |
| `gog_path` | — | auto | Override gog binary path |

### What Gets Synced

- **Metadata mode** (default): Subject, from, date, labels, message count
- **Full body mode**: Complete message content (truncated at 3K per message, 8K per thread)
- **Source format**: `gmail:thread/THREAD_ID`

### Tips

- Use Gmail search syntax for `query`: `from:boss@company.com`, `label:important`, `after:2026/01/01`
- Start with metadata mode — it's fast and gives you searchable context
- Enable `include_bodies` for threads you need full content from

---

## Google Calendar

Imports calendar events via the gog CLI.

### Setup

```bash
cortex connect add calendar --config '{
  "account": "your@gmail.com",
  "calendar_id": "primary",
  "days_back": 30,
  "days_forward": 14,
  "project": "calendar"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `account` | ✅ | — | Google account email |
| `calendar_id` | — | `primary` | Calendar ID |
| `days_back` | — | `30` | How far back to sync |
| `days_forward` | — | `14` | How far forward to sync |
| `project` | — | `""` | Cortex project tag |

### What Gets Synced

- Event title, start/end time, location, description, attendees
- All-day events, recurring events
- **Source format**: `calendar:event/EVENT_ID`

---

## Google Drive

Imports document content via the gog CLI.

### Setup

```bash
cortex connect add drive --config '{
  "account": "your@gmail.com",
  "folder_id": "",
  "query": "",
  "max_results": 50,
  "project": "docs"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `account` | ✅ | — | Google account email |
| `folder_id` | — | `""` | Specific folder to sync (empty = all) |
| `query` | — | `""` | Drive search query |
| `max_results` | — | `50` | Max files per sync |
| `project` | — | `""` | Cortex project tag |

### What Gets Synced

- Google Docs: exported as plain text
- Google Sheets: exported as CSV
- Other files: title + metadata
- **Source format**: `drive:file/FILE_ID`

---

## Slack

Imports channel messages and thread replies via the Slack Web API.

### Prerequisites

1. Create a Slack App at [api.slack.com/apps](https://api.slack.com/apps)
2. Add Bot Token Scopes:
   - `channels:history` — read public channel messages
   - `groups:history` — read private channel messages (if needed)
   - `im:history` — read DMs (if needed)
   - `mpim:history` — read group DMs (if needed)
3. Install the app to your workspace
4. Copy the **Bot User OAuth Token** (`xoxb-...`)
5. Invite the bot to the channels you want to sync

### Setup

```bash
cortex connect add slack --config '{
  "token": "xoxb-your-bot-token",
  "channels": ["C01234GENERAL", "C56789ENGINEERING"],
  "days_back": 30,
  "include_threads": true,
  "project": "slack"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | ✅ | — | Bot User OAuth Token (`xoxb-...`) or User Token (`xoxp-...`) |
| `channels` | ✅ | — | List of channel IDs to sync |
| `days_back` | — | `30` | How far back for initial sync (cap: 365) |
| `include_threads` | — | `true` | Fetch thread replies |
| `project` | — | `""` | Cortex project tag |

### Finding Channel IDs

Right-click a channel name in Slack → "Copy link" → the ID is the last segment:
`https://your-workspace.slack.com/archives/C01234GENERAL`

### What Gets Synced

- Channel messages (text content, user ID, timestamp)
- Thread replies (linked to parent message)
- File share messages and thread broadcasts are included
- System messages (joins, leaves, topic changes) are filtered out
- **Source format**: `slack:channel/C01234/msg/TS`, `slack:channel/C01234/thread/TS/reply/TS`

### Rate Limits

Slack API Tier 3: ~50 requests/minute. The connector respects this automatically.
A 10-page safety cap prevents runaway pagination.

---

## Discord

Imports channel messages from Discord servers via the Discord Bot API.

### Prerequisites

1. Create a Discord Bot at [discord.com/developers/applications](https://discord.com/developers/applications)
2. Enable **Message Content Intent** under Bot settings
3. Add bot to your server with `Read Message History` permission
4. Copy the **Bot Token**

### Setup

```bash
cortex connect add discord --config '{
  "token": "your-bot-token",
  "channel_ids": ["1234567890123456789"],
  "days_back": 30,
  "project": "discord"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | ✅ | — | Discord Bot Token |
| `channel_ids` | ✅ | — | List of channel IDs to sync |
| `days_back` | — | `30` | How far back for initial sync |
| `project` | — | `""` | Cortex project tag |

### Finding Channel IDs

Enable Developer Mode in Discord (Settings → Advanced → Developer Mode), then right-click a channel → "Copy Channel ID."

### What Gets Synced

- Channel messages (text content, author, timestamp)
- Bot messages are filtered out by default
- **Source format**: `discord:channel/CHANNEL_ID/msg/MESSAGE_ID`

---

## Telegram

Imports chat messages from Telegram via the Bot API.

### Prerequisites

1. Create a bot via [@BotFather](https://t.me/BotFather)
2. Get the bot token
3. Add the bot to your group/channel
4. Get the chat ID (send a message, then check `https://api.telegram.org/bot<TOKEN>/getUpdates`)

### Setup

```bash
cortex connect add telegram --config '{
  "token": "123456:ABC-DEF1234...",
  "chat_ids": ["-1001234567890"],
  "days_back": 30,
  "project": "telegram"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | ✅ | — | Telegram Bot Token |
| `chat_ids` | ✅ | — | List of chat IDs (negative for groups) |
| `days_back` | — | `30` | How far back for initial sync |
| `project` | — | `""` | Cortex project tag |

### What Gets Synced

- Text messages, captions on media messages
- Author name and timestamp
- **Source format**: `telegram:chat/CHAT_ID/msg/MESSAGE_ID`

---

## Notion

Imports page content from Notion workspaces via the Notion API.

### Prerequisites

1. Create an integration at [notion.so/my-integrations](https://www.notion.so/my-integrations)
2. Give it "Read content" capabilities
3. Share the pages/databases you want synced with the integration (click "..." → "Connections" → add your integration)
4. Copy the **Internal Integration Token** (`ntn_...` or `secret_...`)

### Setup

```bash
cortex connect add notion --config '{
  "token": "ntn_your_token_here",
  "root_page_ids": [],
  "include_databases": true,
  "project": "notion"
}'
```

### Config Options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | ✅ | — | Notion Integration Token |
| `root_page_ids` | — | `[]` | Specific pages to sync (empty = all shared pages) |
| `include_databases` | — | `true` | Include database entries |
| `project` | — | `""` | Cortex project tag |

### What Gets Synced

- Page content (title, text blocks, headings, lists, quotes)
- Database entries (as structured records)
- Nested pages (if shared with the integration)
- **Source format**: `notion:page/PAGE_ID`

### Tips

- Only pages explicitly shared with your integration are accessible
- The integration sees child pages of shared parent pages automatically
- Rich content (embeds, files, databases with relations) is simplified to text

---

## Auto-Scheduling

Set up OS-native automatic syncing (launchd on macOS, systemd on Linux):

```bash
# Install auto-sync every 3 hours
cortex connect schedule --every 3h --install

# Check current schedule
cortex connect schedule --show

# Remove auto-sync
cortex connect schedule --uninstall
```

The scheduler syncs all enabled connectors with `--extract` on each run.

---

## Managing Connectors

```bash
# List all connectors
cortex connect status

# Sync specific provider
cortex connect sync --provider github

# Sync all enabled connectors
cortex connect sync --all

# Disable a connector (keeps config, stops syncing)
cortex connect disable github

# Re-enable
cortex connect enable github

# Remove a connector entirely
cortex connect remove github

# List available providers
cortex connect providers
```

## MCP Tools

If using Cortex via MCP (Claude Desktop, Cursor, OpenClaw):

| Tool | Description |
|------|-------------|
| `cortex_connect_list` | List connectors and sync status |
| `cortex_connect_add` | Add a new connector |
| `cortex_connect_sync` | Sync one or all connectors |
| `cortex_connect_status` | Detailed connector status |

## Deduplication

Every record is content-hashed (SHA-256 of content + source). Duplicate imports are
silently skipped. This makes syncs safe to run repeatedly — you'll never get duplicate
memories from the same source content.

## Troubleshooting

**"provider not registered"** — Check `cortex connect providers`. The provider name
is case-sensitive (`github`, not `GitHub`).

**"gog CLI not found"** — Install gog: `brew install gog` or set `gog_path` in config.

**"token is required"** — Check your config JSON. Tokens are validated before the
connector is added.

**Sync shows 0 records** — Check the time window. The default is `newer_than:7d` for
Gmail and 30 days for others. Adjust `query` or `days_back`.
