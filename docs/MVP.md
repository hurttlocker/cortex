# Cortex MVP Scope

> **Version:** 0.1 — Draft  
> **Target:** 4–6 weeks  
> **Status:** Scoping

---

## Core Problem Statement

AI agent memory is broken. Not in a subtle way — in a fundamental, "nobody-can-move-their-data" way.

1. **Memory is fragmented.** Every tool invents its own format: `MEMORY.md`, Mem0's internal store, Zep's Postgres tables, custom JSON blobs, raw conversation logs. There's no common ground.

2. **Import doesn't exist.** Want to switch from Mem0 to Zep? Start over. Built up months of context in Claude Code's `MEMORY.md`? That knowledge is trapped. No existing tool has an import story — they all assume you're starting fresh.

3. **No observability.** Users can't answer basic questions: What does my agent know? Is any of it stale? Are there contradictions? Memory is a black box you hope is working.

4. **LLM dependency everywhere.** Mem0 needs GPT to extract facts. Zep needs an LLM for summaries. Even storing a simple preference requires an API call and a credit card. This is architecturally backwards.

5. **Platform lock-in.** Memory built in OpenClaw can't move to Cursor. Context from Claude Code can't flow to a custom agent. Your agent's knowledge is held hostage by whatever platform you started on.

---

## MVP Features (Phase 1)

### 1. Import Engine

The headline feature. Parse and ingest memory from formats people actually use:

- **Markdown files** — `MEMORY.md`, daily notes, Obsidian-style vaults
- **JSON / YAML files** — structured data, config-style memory
- **Plain text conversation logs** — chat exports, terminal logs
- **CSV files** — spreadsheets, exported tables

Each import tracks **provenance**: source file, line number, original timestamp, import timestamp.

```bash
cortex import ~/agents/MEMORY.md
cortex import ~/exports/chat-history.json
cortex import ~/notes/ --recursive
```

### 2. Fact Extraction

Local NLP-based entity and fact extraction. No LLM required.

- **Key-value pairs** — "preferred language: Go", "timezone: EST"
- **Relationships** — "Alice works at Acme Corp", "Bob is Alice's manager"
- **Preferences** — "prefers dark mode", "uses vim keybindings"
- **Temporal facts** — "meeting on Tuesday", "birthday: March 15"
- **Source tracking** — every fact links back to its origin (file, line, timestamp)

The extraction pipeline uses rule-based NLP (dependency parsing, NER) with no external API calls.

### 2b. LLM-Assist Mode (Optional, Day One)

For unstructured content where rule-based extraction falls short, Cortex offers LLM-assisted extraction via any OpenAI-compatible API:

```bash
# Use any provider
cortex import chat-log.txt --llm ollama/gemma2:2b       # Free, local
cortex import chat-log.txt --llm openai/gpt-4.1-nano    # ~$0.10/M tokens
cortex import chat-log.txt --llm anthropic/haiku         # High quality, cheap
cortex import chat-log.txt --llm deepseek/v3             # Dirt cheap
cortex import chat-log.txt --llm openrouter/any-model    # Any model
```

The LLM receives structured extraction prompts and returns typed JSON. Cortex validates the output. The LLM never sees your full memory store — only the document being imported.

**Key design decisions:**
- LLM-assist is OPTIONAL. Local extraction is always the default.
- Supports ANY OpenAI-compatible API endpoint.
- The LLM is used for extraction only — search is always local.
- Extraction prompts are versioned and reproducible.

### 3. Dual Search

Two search modes, both local, both fast:

- **BM25 keyword search** — via SQLite FTS5. Exact matches, boolean queries, familiar behavior.
- **Semantic search** — via local ONNX embeddings (all-MiniLM-L6-v2, ~80MB model). Find conceptually related memories even when keywords don't match.

Both search modes hit the same SQLite database. Zero API keys. Zero network calls.

```bash
cortex search "deployment process"        # hybrid (default)
cortex search "deployment" --mode keyword  # BM25 only
cortex search "how we ship code" --mode semantic  # embeddings only
```

### 4. CLI Interface

Clean, Unix-philosophy CLI:

| Command | Description |
|---------|-------------|
| `cortex import <path>` | Import memory from file or directory |
| `cortex search <query>` | Search memory (hybrid by default) |
| `cortex list` | List all memory entries with metadata |
| `cortex export` | Export memory in standard formats (JSON, Markdown) |
| `cortex stats` | Memory statistics and health overview |
| `cortex stale` | Find outdated or potentially stale entries |
| `cortex conflicts` | Detect contradictory facts |

### 5. Basic Observability

Answer the questions nobody else lets you ask:

- **`cortex stats`** — Total entries, sources, freshness distribution, storage size
- **`cortex stale`** — Entries that haven't been referenced or updated in N days
- **`cortex conflicts`** — Pairs of facts that may contradict each other (e.g., "timezone: EST" vs "timezone: PST")

---

## MVP Non-Goals (Phase 2+)

These are important but explicitly out of scope for the initial release:

- Web dashboard UI
- MCP (Model Context Protocol) server
- OpenClaw plugin / integration
- Obsidian / Notion / Roam importers (beyond basic markdown)
- Graph-based memory (entity relationship graphs)
- Multi-user / multi-agent scoping
- Cloud / SaaS features
- Real-time sync or streaming ingest
- Memory summarization or compression

---

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Language** | Go | Single binary, no runtime deps, fast compilation, good SQLite bindings |
| **Storage** | SQLite + FTS5 | Embedded, zero config, battle-tested full-text search |
| **Embeddings** | ONNX Runtime + all-MiniLM-L6-v2 | Local inference, ~80MB model, no API keys |
| **CLI framework** | cobra | Standard Go CLI library |
| **NLP extraction** | prose (Go) + custom rules | Local, no external dependencies |

### Why Go?

- Single binary distribution — `curl | tar` and you're running
- No Python virtualenvs, no Node.js, no Docker required
- Excellent SQLite bindings via `modernc.org/sqlite` (pure Go, no CGO)
- Cross-compilation to Linux/macOS/Windows from one machine
- Matches the philosophy: zero dependencies, just works

### Why Not Rust?

Rust would work too, but Go wins on:
- Faster iteration for an MVP
- Lower barrier for community contributors
- Better library ecosystem for NLP/text processing
- We can always rewrite hot paths in Rust later if needed

---

## Competitive Landscape

| Feature | Cortex | Mem0 | Zep | Letta | Engram |
|---------|--------|------|-----|-------|--------|
| Import existing memory | ✅ Headline feature | ❌ Start fresh | ❌ | ❌ | ❌ |
| Zero LLM dependency | ✅ Local NLP + ONNX | ❌ Needs GPT | ❌ Needs LLM | ❌ Needs LLM | ✅ |
| Observability | ✅ Stats, stale, conflicts | ❌ | ❌ | Basic | ❌ |
| Self-hosted | ✅ Single binary | 🟡 Complex setup | 🟡 Needs Postgres | 🟡 Full framework | ✅ |
| Semantic search | ✅ Local ONNX | ✅ Cloud-based | ✅ Cloud-based | ✅ | ❌ BM25 only |
| Cross-platform | ✅ Any framework | 🟡 Python-first | 🟡 | ❌ Letta-only | 🟡 |
| Export / portability | ✅ Standard formats | ❌ Locked in | ❌ | ❌ | 🟡 |
| Offline capable | ✅ Fully offline | ❌ | ❌ | ❌ | ✅ |

### Positioning

Cortex isn't trying to replace any of these tools. It's solving a problem none of them address: **what happens to the memory you already have?**

If you've been using Claude Code for 6 months and have a rich `MEMORY.md`, Cortex lets you bring that context with you — anywhere. If you're evaluating Mem0 vs Zep, Cortex gives you a portable foundation that doesn't lock you in.

---

## Success Criteria

The MVP is successful if:

1. A user can `cortex import MEMORY.md` and get searchable, observable memory in under 30 seconds
2. Search results are relevant (BM25 for precision, semantic for recall)
3. `cortex stats` gives a clear picture of what the agent knows
4. The entire tool works offline with zero configuration
5. Installation is a single binary download

---

## Timeline (Rough)

| Week | Focus |
|------|-------|
| 1–2 | Storage layer (SQLite + FTS5), import engine (Markdown + JSON) |
| 3 | Fact extraction pipeline, YAML/CSV importers |
| 4 | Dual search (BM25 + ONNX embeddings) |
| 5 | CLI polish, observability commands |
| 6 | Testing, documentation, first release |

---

## Open Questions

- [ ] Should we embed the ONNX model in the binary or download on first run?
- [ ] What's the right default for "stale" — 30 days? 90 days? Configurable?
- [ ] Should `cortex export` support a "universal memory format" we define, or stick to JSON/Markdown?
- [ ] How aggressive should fact extraction be? (High recall + noise vs. high precision + missed facts)
