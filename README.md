<p align="center">
  <h1 align="center">🧠 Cortex</h1>
  <p align="center">
    <strong>Import-first, zero-dependency, observable memory layer for AI agents</strong>
  </p>
  <p align="center">
    <a href="https://github.com/hurttlocker/cortex/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
    <a href="https://github.com/hurttlocker/cortex/releases"><img src="https://img.shields.io/github/v/release/hurttlocker/cortex?include_prereleases" alt="Release"></a>
    <a href="https://goreportcard.com/report/github.com/hurttlocker/cortex"><img src="https://goreportcard.com/badge/github.com/hurttlocker/cortex" alt="Go Report Card"></a>
  </p>
</p>

---

**30 seconds to import your existing AI agent memory. No API keys. No LLM. No vendor lock-in.**

Cortex is a single-binary memory layer that does what no other tool does: it starts with what you already have. Import your `MEMORY.md`, your conversation logs, your JSON configs — and get instant searchable, observable memory. Works offline. Works everywhere.

## The Problem

You've been working with AI agents for months. You have memory scattered everywhere:

- A `MEMORY.md` that Claude Code maintains
- JSON files from custom agent workflows  
- Conversation logs from various platforms
- YAML configs tracking preferences and context

Now you want to:
- **Search** across all of it semantically
- **See** what your agent actually knows (and what's stale)
- **Move** your context to a different tool or platform
- **Not** pay for API calls just to store a preference

**Every existing tool says: start fresh.** Cortex says: **bring everything.**

## Quick Start

### Install

```bash
# Go install
go install github.com/hurttlocker/cortex/cmd/cortex@latest

# Or download the binary
curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-$(uname -s)-$(uname -m) -o cortex
chmod +x cortex && sudo mv cortex /usr/local/bin/
```

### Import → Search → Observe

```bash
# Import your existing memory (any format)
cortex import ~/agents/MEMORY.md
cortex import ~/exports/chat-history.json
cortex import ~/notes/ --recursive

# Search with hybrid BM25 + semantic search
cortex search "deployment process"
cortex search "what timezone" --mode semantic

# See what your agent knows
cortex stats
# ┌─────────────────────────────────┐
# │ Total memories:        1,847    │
# │ Sources:               12 files │
# │ Last import:           2 min ago│
# │ Stale (>30d):          23       │
# │ Potential conflicts:   3        │
# └─────────────────────────────────┘

# Find stale and contradictory memories
cortex stale
cortex conflicts

# Export to take your memory anywhere
cortex export --format json > my-memory.json
cortex export --format markdown > MEMORY-PORTABLE.md
```

## Features

### 📥 Import Engine
Parse and ingest memory from formats you already use:
- Markdown (`.md`) — MEMORY.md, daily notes, Obsidian vaults
- JSON / YAML — structured data, configs, agent state
- Plain text — conversation logs, terminal output
- CSV — spreadsheets, exported tables

Every import tracks provenance: source file, line number, original timestamp.

### 🔍 Dual Search
Two search modes, both fully local:
- **BM25** via SQLite FTS5 — fast keyword matching, boolean queries
- **Semantic** via local ONNX embeddings — find related concepts even without keyword overlap

Zero API keys. Zero network calls. Works on an airplane.

### 🔬 Fact Extraction
Local NLP-based extraction (no LLM required):
- Key-value pairs, relationships, preferences
- Temporal facts and dates
- Full source tracking back to original file and line

### 👁️ Observability
Finally answer: *what does my agent actually know?*
- `cortex stats` — overview of your memory store
- `cortex stale` — find outdated entries
- `cortex conflicts` — detect contradictions

### 📤 Export & Portability
Your memory is yours. Export anytime:
- JSON (structured, machine-readable)
- Markdown (human-readable, portable)
- Take it to any other tool, platform, or agent framework

## How It Works

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Your Files  │────▶│   Importers  │────▶│  Extraction  │
│  .md .json   │     │  Parse each  │     │  Facts, KV,  │
│  .yaml .csv  │     │  format      │     │  entities    │
└──────────────┘     └──────────────┘     └──────┬───────┘
                                                  │
                     ┌──────────────┐     ┌───────▼───────┐
                     │   Search     │◀────│   SQLite DB   │
                     │  BM25 +      │     │  + FTS5       │
                     │  Semantic    │     │  + Embeddings │
                     └──────────────┘     └───────────────┘
```

Everything runs locally. Single SQLite database. Single binary. No services to manage.

## vs. Alternatives

| Feature | Cortex | Mem0 | Zep | Letta | Engram |
|---------|--------|------|-----|-------|--------|
| **Import existing memory** | ✅ Core feature | ❌ | ❌ | ❌ | ❌ |
| **Zero LLM dependency** | ✅ | ❌ | ❌ | ❌ | ✅ |
| **Observability** | ✅ | ❌ | ❌ | Basic | ❌ |
| **Self-hosted** | ✅ Single binary | 🟡 | 🟡 | 🟡 | ✅ |
| **Semantic search** | ✅ Local | ✅ Cloud | ✅ Cloud | ✅ | ❌ |
| **Works offline** | ✅ | ❌ | ❌ | ❌ | ✅ |
| **Export/portability** | ✅ | ❌ | ❌ | ❌ | 🟡 |

Cortex isn't trying to replace these tools — it's solving the problem they don't address: **what happens to the memory you already have?**

## Tech Stack

- **Go** — single binary, no runtime dependencies
- **SQLite + FTS5** — embedded database with full-text search
- **ONNX Runtime** — local semantic embeddings (~80MB model)
- **Zero external services** — no Docker, no Postgres, no API keys

## What Makes Cortex Different

Beyond import-first architecture and zero dependencies, Cortex introduces concepts from cognitive science and distributed systems that no other memory tool implements:

### 🧬 Provenance Chains
Every fact tracks not just where it came from, but what it influenced. Ask "what decisions were based on this fact?" and "if this changed, what breaks?"

### 📉 Confidence Decay
Facts fade over time unless reinforced — just like human memory. Identity facts persist for years. Meeting times decay in days. Based on Ebbinghaus's forgetting curve from cognitive science.

### 🔭 Memory Lenses
Context-dependent views of the same memory store. Your trading lens shows market data. Your personal lens shows relationships. Auto-detects from query context.

### 📜 Differential Memory
Git for your agent's knowledge. Diff what changed, log every event, snapshot and restore. Full audit trail.

### 🌐 Cortex Memory Protocol (CMP)
The long game: standardize how agents talk to memory, like LSP standardized how editors talk to language intelligence. Cortex CLI is the reference implementation.

## Roadmap

See [docs/MVP.md](docs/MVP.md) for detailed scope and [docs/NOVEL-IDEAS.md](docs/NOVEL-IDEAS.md) for the full vision.

**Phase 1 (Current):** Import engine, dual search, CLI, basic observability, LLM-assist extraction  
**Phase 2:** Web dashboard, MCP server, provenance chains, confidence decay, additional importers  
**Phase 3:** Memory lenses, differential memory (diff/log/snapshot), plugin ecosystem  
**Phase 4:** Cortex Memory Protocol (CMP) specification, multi-agent support, graph memory

## Contributing

Cortex is in early development. We welcome contributions!

1. Fork the repo
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Commit your changes (`git commit -am 'Add my feature'`)
4. Push to the branch (`git push origin feat/my-feature`)
5. Open a Pull Request

Please read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for an overview of the codebase structure.

### Development

```bash
git clone https://github.com/hurttlocker/cortex.git
cd cortex
go build ./cmd/cortex/
./cortex --help
```

## License

MIT — see [LICENSE](LICENSE) for details.

---

<p align="center">
  <strong>Your agent's memory shouldn't be locked in. Import it. Search it. Own it.</strong>
</p>
