<p align="center">
  <img src="https://via.placeholder.com/120x120.png?text=🧠" alt="Cortex Logo" width="120" height="120">
</p>

<h1 align="center">Cortex</h1>

<p align="center">
  <strong>Memory that thinks like you do.</strong><br>
  <em>An import-first, zero-dependency, observable memory layer for AI agents — inspired by cognitive science.</em>
</p>

<p align="center">
  <a href="https://github.com/hurttlocker/cortex/actions/workflows/ci.yml"><img src="https://github.com/hurttlocker/cortex/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/hurttlocker/cortex/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://github.com/hurttlocker/cortex/releases"><img src="https://img.shields.io/github/v/release/hurttlocker/cortex?include_prereleases&label=release" alt="Release"></a>
  <a href="https://goreportcard.com/report/github.com/hurttlocker/cortex"><img src="https://goreportcard.com/badge/github.com/hurttlocker/cortex" alt="Go Report Card"></a>
  <a href="https://pkg.go.dev/github.com/hurttlocker/cortex"><img src="https://pkg.go.dev/badge/github.com/hurttlocker/cortex.svg" alt="Go Reference"></a>
</p>

<p align="center">
  <a href="#-get-started-in-30-seconds">Get Started</a> •
  <a href="#-features">Features</a> •
  <a href="#-architecture">Architecture</a> •
  <a href="#-how-cortex-is-different">What's Different</a> •
  <a href="#-vs-alternatives">Comparison</a> •
  <a href="#-roadmap">Roadmap</a> •
  <a href="#-contributing">Contributing</a>
</p>

---

## The Problem

You've been working with AI agents for months. You've built up a rich context — a `MEMORY.md` that Claude Code maintains, JSON configs from custom workflows, conversation logs, YAML files tracking your preferences.

Then one day you want to:

- **Search** across all of it semantically — not just `grep`
- **See** what your agent actually knows (and what's gone stale)
- **Move** to a different tool without losing months of context
- **Stop paying** for API calls just to store a preference

You look at your options. Every tool says the same thing: **start fresh.**

Cortex says: **bring everything.**

---

## 🚀 Get Started in 30 Seconds

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
# 1. Import your existing memory (any format)
cortex import ~/agents/MEMORY.md
cortex import ~/exports/chat-history.json
cortex import ~/notes/ --recursive

# 2. Search with hybrid BM25 + semantic search
cortex search "deployment process"
cortex search "what timezone" --mode semantic

# 3. See what your agent actually knows
cortex stats
# ┌─────────────────────────────────┐
# │ Total memories:        1,847    │
# │ Sources:               12 files │
# │ Last import:           2 min ago│
# │ Avg confidence:        0.82     │
# │ Stale (>30d):          23       │
# │ Potential conflicts:   3        │
# └─────────────────────────────────┘

# 4. Find stale and contradictory memories
cortex stale          # Facts fading from memory — reinforce or forget
cortex conflicts      # Contradictions to resolve

# 5. Export — take your memory anywhere
cortex export --format json > my-memory.json
cortex export --format markdown > MEMORY-PORTABLE.md
```

**No API keys. No LLM. No Docker. No config files.** Just `cortex import` and go.

---

## ✨ Features

### 📥 Import Engine — Start With What You Have

Parse and ingest memory from formats you already use. This is the headline feature — the one nobody else has.

| Format | Extensions | What Gets Extracted |
|--------|------------|-------------------|
| Markdown | `.md`, `.markdown` | Headers → categories, bullets → facts, key:value pairs |
| JSON | `.json` | Keys → attributes, nested objects → relationships |
| YAML | `.yaml`, `.yml` | Same as JSON, multi-document support |
| CSV | `.csv`, `.tsv` | Headers → keys, rows → fact sets |
| Plain text | `.txt`, `.log` | Sentences, paragraphs, chat patterns |

Every import tracks **provenance**: source file, line number, section header, and timestamp. You always know where a fact came from.

```bash
cortex import ~/notes/ --recursive    # Walk an entire directory
cortex import chat.txt --llm ollama/gemma2:2b   # Optional LLM-assist for unstructured text
```

### 🔍 Dual Search — Two Engines, Zero API Keys

| Mode | Engine | Best For |
|------|--------|----------|
| **Keyword** | BM25 via SQLite FTS5 | Exact matches, boolean queries (`AND`, `OR`, `NOT`) |
| **Semantic** | Local ONNX embeddings (all-MiniLM-L6-v2) | Finding related concepts without keyword overlap |
| **Hybrid** (default) | Reciprocal Rank Fusion | Best of both — precision + recall |

Everything runs locally. Works on an airplane. Works in a submarine. No network calls, ever.

### 📉 Confidence Decay — Memory That Fades Like Yours

Inspired by [Ebbinghaus's forgetting curve](https://en.wikipedia.org/wiki/Forgetting_curve) from cognitive science. Facts decay over time unless reinforced — just like human memory.

| Fact Type | Half-Life | Example |
|-----------|-----------|---------|
| Identity | 693 days | "Name: Alex Chen" |
| Decision | 347 days | "Chose Go over Rust" |
| Relationship | 231 days | "Jordan is my manager" |
| Location | 139 days | "Lives in San Francisco" |
| Preference | 69 days | "Prefers dark mode" |
| State | 14 days | "Working on Project Alpha" |
| Temporal | 7 days | "Meeting on Tuesday" |

When you search, results are weighted by confidence. Stale facts fade. Important facts persist. `cortex stale` shows you what's fading so you can reinforce or forget.

### 🧬 Provenance Chains — Know Where Every Fact Came From

Every fact tracks its full lineage:

```
"Q lives in Philadelphia" (MEMORY.md:4)
  ├── Confirmed by: conversation on 2025-09-22
  ├── Used in: wedding venue search → influenced flight routing
  ├── Used in: timezone detection → EST assumption in scheduling
  ├── Recall count: 47
  └── Confidence: 0.98
```

Ask questions nobody else can answer: *"What decisions were influenced by this fact?"* and *"If this changed, what breaks?"*

### 🔭 Memory Lenses — Context-Dependent Views

The same memory store, different views for different contexts:

```bash
cortex search "what's the plan?" --lens trading    # → positions, strategy, risk
cortex search "what's the plan?" --lens personal   # → wedding, travel, family
cortex search "what's the plan?" --lens technical  # → architecture, roadmap, PRs
```

Lenses filter, boost, and shape results without duplicating data.

### 👁️ Observability — Finally See What Your Agent Knows

```bash
cortex stats        # Overview: counts, freshness, storage, top facts
cortex stale        # What's fading — reinforce, delete, or skip
cortex conflicts    # Contradictions — merge, keep both, or delete one
```

No more black-box memory. No more hoping the agent remembers correctly.

### 📤 Export & Portability — Your Memory Is Yours

```bash
cortex export --format json       # Machine-readable
cortex export --format markdown   # Human-readable
cortex export --format csv        # Spreadsheet-friendly
```

Take your memory to any other tool, platform, or agent framework. No lock-in. Ever.

---

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         cortex CLI                              │
│   import · search · list · export · stats · stale · conflicts   │
└──────────────────────────┬──────────────────────────────────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
        ▼                  ▼                  ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│   Importers  │  │    Search    │  │  Observability│
│              │  │              │  │              │
│ Markdown     │  │ BM25 (FTS5)  │  │ Stats        │
│ JSON / YAML  │  │ Semantic     │  │ Stale        │
│ CSV / Text   │  │ Hybrid (RRF) │  │ Conflicts    │
└──────┬───────┘  └──────┬───────┘  └──────┬───────┘
       │                 │                 │
       ▼                 │                 │
┌──────────────┐         │                 │
│  Extraction  │         │                 │
│              │         │                 │
│ Tier 1: Rules│         │                 │
│ Tier 2: LLM  │         │                 │
│   (optional) │         │                 │
└──────┬───────┘         │                 │
       │                 │                 │
       ▼                 ▼                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                     SQLite + FTS5                               │
│                                                                 │
│  memories │ facts │ embeddings │ recall_log │ memory_events     │
│                                                                 │
│  Single file: ~/.cortex/cortex.db                               │
│  WAL mode · Zero config · Trivially portable                   │
└─────────────────────────────────────────────────────────────────┘
```

**Design principles:**
- Every novel feature maps to SQL — no magic, everything queryable
- All tables are additive — new features never break existing ones
- Interfaces first — every layer is swappable independently
- Local by default, cloud by choice — nothing phones home unless you ask

---

## 🧠 How Cortex Is Different

Cortex isn't just another memory store. It brings ideas from **cognitive science** and **distributed systems** that no other tool implements:

| Concept | Inspiration | What It Does |
|---------|------------|--------------|
| **Confidence Decay** | Ebbinghaus forgetting curve | Facts fade unless reinforced — type-aware decay rates |
| **Provenance Chains** | Academic citation graphs | Track what facts influenced, cascade analysis |
| **Memory Lenses** | Database views | Context-dependent filtering and boosting |
| **Differential Memory** | Git version control | Diff, log, snapshot, restore — full audit trail |
| **Import-First** | Migration tooling | Your existing memory IS the starting point |
| **Cortex Memory Protocol** | LSP (Language Server Protocol) | Standardize how agents talk to memory |

---

## 📊 vs. Alternatives

| Feature | Cortex | Mem0 | Zep | Letta | Engram |
|---------|:------:|:----:|:---:|:-----:|:------:|
| **Import existing memory** | ✅ Core feature | ❌ Start fresh | ❌ | ❌ | ❌ |
| **Zero LLM dependency** | ✅ | ❌ Needs GPT | ❌ Needs LLM | ❌ Needs LLM | ✅ |
| **LLM-assist (optional)** | ✅ Any provider | 🟡 GPT only | ❌ | Depends | ❌ |
| **Observability** | ✅ Stats/stale/conflicts | ❌ | ❌ | Basic | ❌ |
| **Confidence decay** | ✅ Ebbinghaus curve | ❌ | ❌ | ❌ | ❌ |
| **Provenance tracking** | ✅ Full chains | ❌ | ❌ | ❌ | ❌ |
| **Self-hosted** | ✅ Single binary | 🟡 Complex | 🟡 Postgres | 🟡 Framework | ✅ |
| **Semantic search** | ✅ Local ONNX | ✅ Cloud | ✅ Cloud | ✅ | ❌ |
| **Works offline** | ✅ Fully | ❌ | ❌ | ❌ | ✅ |
| **Export / portability** | ✅ JSON, MD, CSV | ❌ Locked in | ❌ | ❌ | 🟡 |
| **Cross-platform** | ✅ Any framework | 🟡 Python-first | 🟡 | ❌ Letta only | 🟡 |

> **Cortex isn't trying to replace these tools.** It solves the problem they don't address: *what happens to the memory you already have?*

---

## 🛠️ Tech Stack

| Component | Choice | Why |
|-----------|--------|-----|
| **Language** | Go | Single binary, no runtime deps, fast compilation |
| **Storage** | SQLite + FTS5 | Embedded, zero config, battle-tested full-text search |
| **Embeddings** | ONNX Runtime + all-MiniLM-L6-v2 | Local inference, ~80MB model, no API keys |
| **CLI** | Cobra | Standard Go CLI framework |
| **NLP** | prose (Go) + custom rules | Local extraction, no external dependencies |

No Docker. No Postgres. No Redis. No API keys. **Just a binary and a SQLite file.**

---

## 🗺️ Roadmap

### Phase 1 — Foundation *(current)*
Import engine (Markdown, JSON, YAML, CSV) · Dual search (BM25 + semantic) · Fact extraction (rule-based + LLM-assist) · CLI · Basic observability (`stats`, `stale`, `conflicts`)

### Phase 2 — Intelligence
Web dashboard · MCP server · Provenance chains · Confidence decay model · Additional importers (PDF, DOCX, HTML)

### Phase 3 — Context
Memory lenses (manual + auto-detect) · Differential memory (diff, log, snapshot, restore) · Plugin ecosystem for custom importers/extractors

### Phase 4 — Protocol
Cortex Memory Protocol (CMP) specification · Multi-agent memory scoping · Graph memory layer · Community reference implementations

See [docs/MVP.md](docs/MVP.md) for detailed Phase 1 scope and [docs/NOVEL-IDEAS.md](docs/NOVEL-IDEAS.md) for the full vision.

---

## 🤝 Contributing

Cortex is built for multi-agent development — AI agents and humans contributing in parallel. We welcome both!

```bash
# Get started
git clone https://github.com/hurttlocker/cortex.git
cd cortex
go build ./cmd/cortex/
go test ./...
```

- 📖 Read [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines
- 🤖 AI agents: see [docs/AGENTS.md](docs/AGENTS.md) for coordination conventions
- 📋 Feature specs: see [docs/prd/](docs/prd/) for detailed PRDs
- 🏛️ Architecture: see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for system design
- 📝 Decisions: see [docs/DECISIONS.md](docs/DECISIONS.md) for ADRs

**Good first issues** are tagged and ready — jump in!

---

## 📄 License

MIT — see [LICENSE](LICENSE) for details.

---

<p align="center">
  <strong>Your agent's memory shouldn't be locked in a black box.<br>Import it. Search it. Observe it. Own it.</strong>
</p>

<p align="center">
  <sub>Built with ❤️ by <a href="https://github.com/hurttlocker">hurttlocker</a></sub>
</p>
