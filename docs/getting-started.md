# Getting Started

Zero to searching in under 5 minutes.

## 1. Install

Pick one:

```bash
# Homebrew (macOS)
brew install hurttlocker/cortex/cortex-memory

# npm (any platform — MCP server, no install needed)
npx @cortex-ai/mcp

# Binary (macOS Apple Silicon)
curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-darwin-arm64.tar.gz | tar xz
sudo mv cortex /usr/local/bin/

# Go
go install github.com/hurttlocker/cortex/cmd/cortex@latest
```

Verify:
```bash
cortex version
# cortex v0.9.0 (darwin/arm64)
```

## 2. Import Your Files

```bash
# Import a single file
cortex import ~/notes/MEMORY.md --extract

# Import a whole directory (markdown + text files)
cortex import ~/notes/ --recursive --extract

# Import specific file types only
cortex import ~/docs/ --recursive --extract --ext md,txt,yaml
```

`--extract` tells Cortex to pull out structured facts (people, dates, decisions, configs) from your text. Without it, content is stored but facts aren't extracted.

## 3. Search

```bash
# Find what you know about something
cortex search "what did I decide about the API design"

# Search with more results
cortex search "deployment config" --limit 10

# Keyword-only search (fastest)
cortex search "PostgreSQL migration" --mode bm25
```

## 4. Connect to Your Agent

```bash
# Claude Code
claude mcp add cortex -- cortex mcp

# Cursor (add to .cursor/mcp.json)
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["mcp"]
    }
  }
}

# Any MCP client (stdio)
cortex mcp

# Any MCP client (HTTP+SSE)
cortex mcp --port 8080
```

Your agent now has persistent memory with 17 tools.

## 5. Check What You Know

```bash
# Overview
cortex stats

# What's fading?
cortex stale --days 30

# Any contradictions?
cortex conflicts

# Health check
cortex doctor
```

---

## Optional: Connect External Sources

Pull in data from where you already work:

```bash
# GitHub issues and PRs
cortex connect add github --config '{"token": "ghp_...", "repos": ["owner/repo"]}'
cortex connect sync --provider github --extract

# Set up auto-sync every 3 hours
cortex connect schedule --every 3h --install
```

See [connectors.md](connectors.md) for all 8 providers (GitHub, Gmail, Calendar, Drive, Slack, Discord, Telegram, Notion).

## Optional: Explore Your Knowledge Graph

```bash
cortex graph --serve --port 8090
# Open http://localhost:8090 in your browser
```

Interactive 2D explorer with graph, table, subjects, clusters, and search views.

## Optional: Multi-Agent

If you run multiple agents, scope their memories:

```bash
# Import as a specific agent
cortex import notes.md --agent mister --extract

# Search only that agent's memories
cortex search "config" --agent mister

# Each agent gets its own MCP scope
cortex mcp --agent mister
```

## Optional: LLM Enrichment

By default (v0.9.0+), importing with `--extract` also runs LLM enrichment to find facts that rules miss. This requires an LLM provider in your config:

```yaml
# ~/.cortex/config.yaml
llm:
  provider: openrouter/grok-4.1-fast
  api_key: or-...
```

To import without LLM calls:
```bash
cortex import notes.md --extract --no-enrich --no-classify
```

---

## What's Next

- [Deep Dive](CORTEX_DEEP_DIVE.md) — Full technical documentation
- [Architecture](ARCHITECTURE.md) — Package structure and data flow
- [Connectors](connectors.md) — Detailed setup for all 8 providers
- [Migration Guide](migration.md) — Upgrading from older versions
