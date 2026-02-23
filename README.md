<p align="center">
  <img src="docs/assets/cortex-logo-redpink-transparent.png" alt="Cortex" width="120" height="120">
</p>

<h1 align="center">Cortex</h1>

<p align="center">
  <strong>Memory that forgets — so your agent doesn't have to remember everything forever.</strong>
</p>

<p align="center">
  <a href="https://github.com/hurttlocker/cortex/actions/workflows/ci.yml"><img src="https://github.com/hurttlocker/cortex/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/hurttlocker/cortex/releases"><img src="https://img.shields.io/github/v/release/hurttlocker/cortex?include_prereleases&label=release" alt="Release"></a>
  <a href="https://github.com/hurttlocker/cortex/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
</p>

---

Import your existing files. Search with hybrid retrieval. Watch facts fade and reinforce what matters.

No API keys. No Docker. No config. A single 12MB binary and a SQLite file.

## Install (pick one)

```bash
# Homebrew (macOS)
brew install hurttlocker/cortex/cortex-memory

# MCP server (any platform — no install needed)
npx @cortex-ai/mcp

# Binary (macOS Apple Silicon)
curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-darwin-arm64.tar.gz | tar xz
sudo mv cortex /usr/local/bin/

# Go
go install github.com/hurttlocker/cortex/cmd/cortex@latest
```

<details>
<summary>Other platforms</summary>

| Platform | Command |
|----------|---------|
| macOS Intel | `curl -sSL .../cortex-darwin-amd64.tar.gz \| tar xz && sudo mv cortex /usr/local/bin/` |
| Linux x86_64 | `curl -sSL .../cortex-linux-amd64.tar.gz \| tar xz && sudo mv cortex /usr/local/bin/` |
| Linux ARM64 | `curl -sSL .../cortex-linux-arm64.tar.gz \| tar xz && sudo mv cortex /usr/local/bin/` |
| Windows | Download from [Releases](https://github.com/hurttlocker/cortex/releases/latest) |

Replace `...` with `https://github.com/hurttlocker/cortex/releases/latest/download`
</details>

## Quick start (60 seconds)

```bash
# 1. Import your files
cortex import ~/notes/ --recursive --extract

# 2. Search
cortex search "what did I decide about the API design"

# 3. Connect to your agent (Claude Code, Cursor, etc.)
claude mcp add cortex -- cortex mcp
```

That's it. Your agent now has persistent memory with 16 MCP tools.

### Next steps

```bash
# Connect external sources — GitHub, Gmail, Slack, Calendar, Drive
cortex connect add github --config '{"token": "ghp_...", "repos": ["owner/repo"]}'
cortex connect sync --all

# Set up alerts — get notified when facts conflict or fade
cortex watch add "deployment failures" --threshold 0.7
cortex alerts check-decay

# Multi-agent? Scope facts by agent
cortex import notes.md --agent mister --extract
cortex search "config" --agent mister
```

See [docs/connectors.md](docs/connectors.md) for full connector setup and
[docs/platform.md](docs/platform.md) for alerts, namespaces, and the knowledge graph.

## Why Cortex

**Memory that fades like yours.** Facts decay over time using [Ebbinghaus curves](https://en.wikipedia.org/wiki/Forgetting_curve) — identity facts last years, temporal facts fade in days. When you search, stale facts rank lower. Reinforce what matters; let the rest go.

**Import-first.** Start with the files you already have — `MEMORY.md`, JSON configs, YAML, CSV, conversation logs. Every other tool says "start fresh." Cortex says "bring everything."

**Zero dependencies.** No API keys, no LLM, no Docker, no database server. A single Go binary + SQLite. Semantic search is optional (add Ollama when you want it).

**Observable.** `cortex stats` shows what your agent knows. `cortex stale` shows what's fading. `cortex conflicts` finds contradictions. `cortex alerts` notifies you proactively.

## How it works

```
Your files ──→ Import ──→ Fact extraction ──→ SQLite + FTS5
                                                   │
                              ┌─────────┬──────────┼──────────┐
                              ▼         ▼          ▼          ▼
                           Search    Observe    Alerts     MCP Server
                          (hybrid)  (stats,    (conflict,  (7 tools,
                                    stale,     decay,      any agent)
                                    conflicts) watch)
```

**Search:** BM25 keyword + optional semantic embeddings, fused with Weighted Score Fusion.
**Facts:** Extracted as subject-predicate-object triples with type-aware decay rates.
**Alerts:** Proactive notifications when facts conflict or fade below thresholds.
**Connect:** Sync from GitHub, Gmail, Calendar, Drive — same quality pipeline.

## Feature highlights

| Feature | What it does |
|---------|-------------|
| **Hybrid search** | BM25 + semantic with score fusion. Works without embeddings, better with them. |
| **Ebbinghaus decay** | 7 decay rates by fact type. Identity lasts 693 days, temporal fades in 7. |
| **Fact extraction** | Rule-based + optional LLM. Finds entities, decisions, preferences, relationships. |
| **Conflict detection** | Same subject + predicate, different object → alert. Real-time on ingest. |
| **Decay notifications** | `cortex alerts check-decay` scans for fading facts. Reinforce or let go. |
| **Recursive reasoning** | `cortex reason --recursive` — LLM loops: search → reason → search deeper. |
| **Connectors** | GitHub, Gmail, Calendar, Drive. Import issues/PRs/events into memory. |
| **Provenance** | Every fact tracks source file, line, section, timestamp. Full audit trail. |
| **Export** | JSON, Markdown, CSV. Your memory is yours. No lock-in. |
| **MCP server** | `cortex mcp` — stdio or HTTP. Works with Claude Code, Cursor, any MCP client. |

## vs. alternatives

| | Cortex | Mem0 | Zep | Letta |
|---|:---:|:---:|:---:|:---:|
| Import existing files | ✅ | ❌ | ❌ | ❌ |
| Zero LLM dependency | ✅ | ❌ | ❌ | ❌ |
| Confidence decay | ✅ | ❌ | ❌ | ❌ |
| Conflict detection | ✅ | ❌ | ❌ | ❌ |
| Proactive alerts | ✅ | ❌ | ❌ | ❌ |
| Recursive reasoning | ✅ | ❌ | ❌ | ❌ |
| Self-hosted (single binary) | ✅ | 🟡 | 🟡 | 🟡 |
| Works offline | ✅ | ❌ | ❌ | ❌ |
| Export / portability | ✅ | ❌ | ❌ | ❌ |

## CLI reference

```bash
cortex import <path> [--recursive] [--extract]  # Import files or directories
cortex search <query> [--mode hybrid|bm25|semantic]  # Search memories
cortex reason <query> [--recursive]             # LLM reasoning over memory
cortex stats                                    # What your agent knows
cortex stale [--days 30]                        # Fading facts
cortex conflicts [--resolve highest-confidence] # Contradictions
cortex alerts [check-decay|digest|reinforce]    # Proactive notifications
cortex reinforce <fact-id>                      # Reset decay timer
cortex connect [add|sync|status]                # External service connectors
cortex export [--format json|markdown|csv]      # Take your memory anywhere
cortex mcp [--embed ollama/nomic-embed-text]    # MCP server for agents
cortex cleanup                                  # Purge noise
cortex embed <provider/model>                   # Generate semantic embeddings
```

## Semantic search (optional)

BM25 keyword search works out of the box. For semantic search, add an embedding model:

```bash
ollama pull nomic-embed-text
cortex embed ollama/nomic-embed-text --batch-size 10
cortex search "conceptually related query" --mode hybrid --embed ollama/nomic-embed-text
```

Supports Ollama (free/local), OpenAI, DeepSeek, OpenRouter, or any OpenAI-compatible endpoint.

## Architecture

- **Language:** Go 1.24+ — single binary, no runtime dependencies
- **Storage:** SQLite + FTS5 (pure Go, zero CGO) — `~/.cortex/cortex.db`
- **Search:** BM25 keyword + optional HNSW ANN for semantic
- **Extraction:** Rule-based pipeline + optional LLM assist
- **MCP:** stdio + HTTP/SSE transport — 7 tools, 2 resources
- **Tests:** 525 across 16 packages

## Documentation

| Doc | What's in it |
|-----|-------------|
| [Full feature reference](docs/README-full.md) | Complete documentation (benchmarks, presets, chunking, etc.) |
| [Architecture](docs/ARCHITECTURE.md) | System design and package structure |
| [Deep dive](docs/CORTEX_DEEP_DIVE.md) | Strategic analysis and roadmap |
| [Local LLM guide](docs/LOCAL-LLM-PERFORMANCE.md) | Hardware recommendations for local reasoning |
| [Ops runbook](docs/ops-db-growth-guardrails.md) | Database growth monitoring |
| [Contributing](CONTRIBUTING.md) | How to contribute |
| [Brand assets](docs/branding.md) | Logo and visual identity |

## License

MIT — [LICENSE](LICENSE)

---

<p align="center">
  <sub>Built by <a href="https://github.com/hurttlocker">hurttlocker</a></sub>
</p>
