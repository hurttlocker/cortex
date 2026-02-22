<p align="center">
  <img src="docs/assets/cortex-logo-redpink-transparent.png" alt="Cortex Logo" width="120" height="120">
</p>

<h1 align="center">CORTEX</h1>

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

### 1. Install

Pick your platform — one command, no dependencies:

| Platform | Command |
|----------|---------|
| **macOS (Apple Silicon)** | `curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-darwin-arm64.tar.gz \| tar xz && sudo mv cortex /usr/local/bin/` |
| **macOS (Intel)** | `curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-darwin-amd64.tar.gz \| tar xz && sudo mv cortex /usr/local/bin/` |
| **Linux (x86_64)** | `curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-linux-amd64.tar.gz \| tar xz && sudo mv cortex /usr/local/bin/` |
| **Linux (ARM64)** | `curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-linux-arm64.tar.gz \| tar xz && sudo mv cortex /usr/local/bin/` |
| **Windows** | Download `cortex-windows-amd64.tar.gz` from [Releases](https://github.com/hurttlocker/cortex/releases/latest) |
| **Go install** | `go install github.com/hurttlocker/cortex/cmd/cortex@latest` |

**No sudo?** Move to any directory on your PATH instead: `mv cortex ~/bin/` or `mv cortex ~/.local/bin/`

Verify: `cortex version` → should print the installed version

### 2. Import your data

```bash
cortex import ~/my-notes/ --recursive        # folder of markdown/json/yaml/txt
cortex import ~/MEMORY.md                     # single file
cortex import ~/chat-export.json              # JSON works too
```

### 3. Connect to Claude Code (MCP)

```bash
claude mcp add cortex -- cortex mcp
```

**That's it.** Claude Code now has these tools:

| Tool | What it does |
|------|-------------|
| `cortex_search` | Search memories (keyword, semantic, or hybrid) |
| `cortex_import` | Save new memories |
| `cortex_reason` | LLM reasoning over memories (single-pass or recursive) |
| `cortex_stats` | Memory statistics |
| `cortex_facts` | Query extracted facts |
| `cortex_stale` | Find fading/outdated facts |
| `cortex_reinforce` | Reset decay timer on important facts |

<details>
<summary><b>Claude Desktop / Cursor setup</b></summary>

Add to your MCP config file:
- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["mcp"]
    }
  }
}
```

Restart Claude Desktop after saving.

</details>

<details>
<summary><b>Optional: Enable semantic search</b></summary>

Semantic search needs an embedding model. If you have [Ollama](https://ollama.com):

```bash
ollama pull nomic-embed-text

# one-time bootstrap
cortex embed ollama/nomic-embed-text --batch-size 10

# keep embeddings fresh every 30 minutes (recommended for 24/7 agents)
cortex embed ollama/nomic-embed-text --watch --interval 30m --batch-size 10

claude mcp add cortex -- cortex mcp --embed ollama/nomic-embed-text
```

Without this, Cortex uses BM25 keyword search (fast, no extra dependencies).

</details>

### Import → Search → Observe

```bash
# 1. Import your existing memory (any format)
cortex import ~/agents/MEMORY.md
cortex import ~/exports/chat-history.json
cortex import ~/notes/ --recursive
cortex import ./operating-rules.md --class rule

# 2. Search with hybrid BM25 + semantic search
cortex search "deployment process"
cortex search "what timezone" --mode semantic
cortex search "deploy checklist" --class rule,decision

# 3. Generate embeddings for semantic search (optional, needs Ollama)
cortex embed ollama/nomic-embed-text --batch-size 10

# 3b. Production mode: keep semantic index fresh automatically
cortex embed ollama/nomic-embed-text --watch --interval 30m --batch-size 10

# 4. See what your agent actually knows
cortex stats
# {
#   "memories": 967,
#   "facts": 5220, 
#   "sources": 32,
#   "storage_bytes": 8306688,
#   "avg_confidence": 0.86,
#   "facts_by_type": {
#     "identity": 234,
#     "kv": 4619,
#     "temporal": 367
#   }
# }

# 4. Find stale and contradictory memories
cortex stale          # Facts fading from memory — reinforce or forget
cortex conflicts      # Contradictions to resolve

# 5. Clean up garbage data
cortex cleanup        # Purge short/numeric junk + headless facts

# 6. Export — take your memory anywhere
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
cortex import /tmp/auto-capture.md --capture-dedupe --similarity-threshold 0.95 --dedupe-window-sec 300
```

### 🔍 Dual Search — Two Engines, Your Choice of Model

| Mode | Engine | Best For |
|------|--------|----------|
| **Keyword** | BM25 via SQLite FTS5 | Exact matches, boolean queries with AND→OR fallback |
| **Semantic** | Embeddings via Ollama, OpenAI, or any provider | Finding related concepts without keyword overlap |
| **Hybrid** (default) | Weighted Score Fusion | Best of both — precision + recall |

```bash
# Generate embeddings (one-time bootstrap)
cortex embed ollama/nomic-embed-text --batch-size 10

# Optional daemon mode (recommended for always-on agents)
cortex embed ollama/nomic-embed-text --watch --interval 30m --batch-size 10

# Search modes
cortex search "deployment process"                           # BM25 keyword (instant)
cortex search "what timezone" --mode semantic --embed ollama/nomic-embed-text  # Semantic
cortex search "deployment" --mode hybrid --embed ollama/nomic-embed-text       # Both
cortex search "merge policy" --class rule,decision            # Class-filtered retrieval
cortex search "merge policy" --explain                        # Provenance + rank factors
```

Embedding is provider-agnostic: Ollama (local, free), OpenAI, DeepSeek, OpenRouter, or any custom endpoint. In watch mode, Cortex only processes memories missing embeddings, applies exponential backoff if the provider is down, and rebuilds the HNSW ANN index automatically when new vectors land. BM25 search works with zero setup — no embeddings needed.

### 🧭 Class-Aware Retrieval — Prioritize Rules and Decisions

Cortex now supports optional memory classes to reduce retrieval noise in long-lived stores:

- `rule`, `decision`, `preference`, `identity`, `status`, `scratch`

Use class labels at import time or let Cortex auto-classify heuristically when no class is provided:

```bash
cortex import rules.md --class rule
cortex import decision-log.md --class decision
cortex import notes/ --recursive                      # auto-classify fallback

cortex search "deploy requirements" --class rule,decision
cortex search "deploy requirements" --no-class-boost   # disable weighting when needed
cortex list --class rule,decision
```

Unclassified data remains fully searchable (backward compatible). On startup, Cortex backfills legacy `NULL memory_class` rows to `''` and normalizes scan paths so mixed historical/new datasets stay query-safe. Class boosts are conservative defaults and can be disabled per-query.

### 🔎 Retrieval Explainability — Why This Result Ranked

Need trust signals before memory gets injected into context? Use explain mode:

```bash
cortex search "deployment policy" --explain
cortex search "deployment policy" --json --explain
```

Explain mode includes:
- provenance (`source`, `timestamp`, `age_days`)
- confidence signals (`confidence`, `effective_confidence`)
- rank components (`bm25`/`semantic`/hybrid contributions, class boost multiplier, pre/post confidence scores)
- a short `why` summary for fast operator review

By default (without `--explain`) search stays on the fast path and does not include explainability payloads.

### 📋 Metadata-Enriched Capture — Know Who Said What, Where

Every memory can carry structured metadata: which agent created it, what channel, which model, token usage, and timestamps. This enables precise queries across your entire memory:

```bash
# Import with metadata
cortex import notes.md --metadata '{"agent_id":"sage","channel":"discord","model":"sonnet-4.5"}'

# Search with metadata filters
cortex search "trading analysis" --agent mister        # Only Mister's memories
cortex search "research report" --channel telegram     # Only from Telegram
cortex search "decisions" --after 2026-02-15           # Only recent
cortex search "anything" --show-metadata               # See agent/channel/model in output
```

The OpenClaw plugin automatically captures session context on every conversation — agent ID, channel, model, token usage — with zero configuration. Over time, your memory becomes a structured knowledge graph of *who knew what, when, and where*.

### 🧹 Auto-Capture Hygiene — Keep Memory Clean at Scale

For high-volume auto-capture workflows, Cortex supports hygiene controls to reduce noisy repetition:

```bash
# Server-side near-duplicate suppression on import
cortex import /tmp/auto-capture.md --capture-dedupe --similarity-threshold 0.95 --dedupe-window-sec 300
```

The OpenClaw plugin also supports:
- near-duplicate suppression (cosine threshold on recent captures)
- burst coalescing windows for short rapid-fire turns
- low-signal acknowledgement filters (`ok`, `got it`, `HEARTBEAT_OK`, `fire the test`)
- recall-side dedupe before `<cortex-memories>` injection

You can also update an existing memory in place:

```bash
cortex update 123 --content "Decision: use HNSW over FAISS" --extract
# or
cortex update 123 --file updated-note.md --extract
```

### 🪦 Superseded/Tombstone Facts — Keep History, Hide Stale Truth

When a fact is replaced, you can mark the old fact as superseded without deleting it:

```bash
cortex supersede 12345 --by 12399 --reason "policy updated"
```

By default, superseded facts are excluded from active listings/conflict scans and from search results tied only to superseded facts. Use `--include-superseded` for historical/debug views:

```bash
cortex list --facts --include-superseded
cortex conflicts --include-superseded
cortex search "old policy" --include-superseded
```

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

When you search, results are weighted by effective confidence — stale facts rank lower. Facts are automatically reinforced when recalled (searched and returned). Use `cortex reinforce <id>` to manually reset the decay timer. `cortex stale` shows what's fading so you can reinforce or forget. `cortex stats` shows the full confidence distribution.

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

### 🔄 Recursive Reasoning (RLM) — Memory That Thinks

Inspired by the [Recursive Language Models paper](https://arxiv.org/abs/2512.24601) (MIT, Dec 2025). Instead of a single LLM call, Cortex reason can **loop** — searching for more context, decomposing sub-questions, and synthesizing iteratively until it has a complete answer.

```bash
# Single-pass reasoning (fast, simple queries)
cortex reason "What happened today?" --preset daily-digest --embed ollama/nomic-embed-text

# Recursive reasoning (deep, complex queries)
cortex reason "How has our trading strategy evolved?" --recursive -v --embed ollama/nomic-embed-text

# Full control
cortex reason "Analyze all project risks" \
  --recursive \
  --max-iterations 12 \
  --max-depth 2 \
  --model google/gemini-2.5-flash \
  --project myproject \
  --embed ollama/nomic-embed-text \
  -v
```

**How it works:**

```
Iteration 1: LLM reviews initial search results
             → "I need more context about crypto strategies"
             → SEARCH(crypto SRB ML220 strategy)

Iteration 2: LLM reviews new results + previous context
             → "Now I need options performance data"  
             → SEARCH(0DTE options strategy performance)

Iteration 3: LLM reviews all accumulated context
             → "I have enough. Here's my synthesis."
             → FINAL(complete structured analysis)
```

The LLM has 5 actions available in each iteration:

| Action | What It Does |
|--------|-------------|
| `SEARCH(query)` | Run a new Cortex search with different terms |
| `FACTS(keyword)` | Search extracted facts (subject-predicate-object triples) |
| `PEEK(memory_id)` | Retrieve full content of a specific memory |
| `SUB_QUERY(question)` | Recursive sub-call for component questions (depth-limited) |
| `FINAL(answer)` | Return the synthesized answer |

**Confidence-aware prompting** — the LLM sees decay scores (`[0.95]` fresh, `[0.45] ⚠️ STALE`) and can weight its reasoning accordingly. No other tool does this.

**5 built-in presets** — or define your own in `~/.cortex/presets.yaml`:

| Preset | Purpose | Default Model |
|--------|---------|---------------|
| `daily-digest` | Daily activity summary | gemini-2.5-flash |
| `fact-audit` | Find stale/contradictory facts | deepseek-v3.2 |
| `weekly-dive` | Deep analysis of a topic | deepseek-v3.2 |
| `conflict-check` | Find contradictions | gemini-2.5-flash |
| `agent-review` | Review agent performance | gemini-2.5-flash |

**Use any LLM** — local (Ollama) or cloud (OpenRouter):

```bash
# Local (free, private — great for GPU users)
cortex reason "query" --recursive --model phi4-mini --embed ollama/nomic-embed-text

# Cloud (fast, cheap)
cortex reason "query" --recursive --model google/gemini-2.5-flash --embed ollama/nomic-embed-text

# Smart defaults: set OPENROUTER_API_KEY and Cortex auto-selects the best model per preset
export OPENROUTER_API_KEY=sk-or-...
cortex reason "query" --recursive --preset weekly-dive  # → auto-selects deepseek-v3.2
```

**Local models work great for scheduled/cron use** — even on CPU-only hardware, a 4B model can run recursive reasoning in 60-90s, perfect for nightly digests and audits. Users with GPUs (especially Apple Silicon with Metal) get interactive-speed local reasoning.

For hardware-specific recommendations and benchmark workflow, see **[docs/LOCAL-LLM-PERFORMANCE.md](docs/LOCAL-LLM-PERFORMANCE.md)**.

**Built-in reason telemetry (new):** every `cortex reason` run appends a JSONL event to `~/.cortex/reason-telemetry.jsonl` with mode (`one-shot` vs `recursive`), model/provider, tokens, durations, and estimated cost (when pricing is known). Disable with:

```bash
export CORTEX_REASON_TELEMETRY=off
```

### ✅ Reason quality eval pack (CI / nightly)

Run the first-pass quality harness (30 realistic prompts, signal-based scoring on actionability, grounding, contradiction handling, usefulness):

```bash
python3 scripts/reason_quality_eval.py \
  --binary ./cortex \
  --fixture tests/fixtures/reason/eval-set-v1.json \
  --model google/gemini-3-flash-preview \
  --embed ollama/nomic-embed-text \
  --output /tmp/reason-quality-report.json
```

- Exits non-zero when suite thresholds fail (safe for CI gates).
- For nightly local runs, swap `--model` to an Ollama model (for example: `--model phi4-mini`).

Add reliability guardrail checks (Track 2):

```bash
python3 scripts/reason_guardrail_gate.py \
  --report /tmp/reason-quality-report.json
```

Add outcome-loop KPI rollups (Track 3):

```bash
python3 scripts/reason_outcome_rollup.py \
  --input tests/fixtures/reason/outcomes-template.jsonl
```

### ⚙️ Codex rollout operating mode

Use this as the default decision rule while Codex rollout is active:

- **Codex one-shot (interactive default):** fastest turnaround for normal dev Q&A, quick audits, and lightweight synthesis.
- **Cloud recursive (reliability default):** use `--recursive` for deep or high-stakes prompts (multi-step analysis, conflict-heavy memory, weekly/fact audits).
- **When to switch from one-shot → recursive:**
  - answer quality is shallow or misses context,
  - query needs decomposition/sub-questions,
  - you need stronger consistency over speed.

Rollout report command (telemetry summary by mode, p50/p95 latency, cost, provider/model mix):

```bash
# Runtime CLI subcommand (recommended)
cortex codex-rollout-report

# Script wrapper (backward-compatible)
scripts/codex_rollout_report.sh

# Legacy helper binary still works
go run ./cmd/codex-rollout-report --file ~/.cortex/reason-telemetry.jsonl
```

Optional quality gates (for CI/cron checks):

- one-shot p95 latency threshold (default `20000ms`)
- recursive known-cost completeness threshold (default `0.80`)
- warn-only mode (default `true`) vs strict non-zero exit mode

```bash
# Warn-only (default): emit warnings, exit 0
cortex codex-rollout-report --warn-only

# Strict mode: exit non-zero when guardrails fail
cortex codex-rollout-report --warn-only=false

# Tuned thresholds
cortex codex-rollout-report \
  --one-shot-p95-warn-ms 15000 \
  --recursive-known-cost-min-share 0.90 \
  --warn-only=false
```

### 📊 Benchmark Command — Test Any Model

```bash
# Compare models on your own memory
cortex bench --models "google/gemini-2.5-flash,deepseek/deepseek-chat" \
  --embed ollama/nomic-embed-text \
  --output benchmark-report.md

# Quick A/B (diff-style compare section in report)
cortex bench --compare "google/gemini-2.5-flash,deepseek/deepseek-v3.2" --output ab-report.md

# Benchmark recursive reasoning mode
cortex bench --recursive --max-iterations 8 --max-depth 1 --output recursive-bench.md

# Include local models
cortex bench --local --embed ollama/nomic-embed-text
```

Generates a publication-ready markdown report with summary table, per-preset breakdown, winners by category, cost analysis, and (when using `--compare`) an A/B diff section. Default runs cover all 5 presets: `daily-digest`, `fact-audit`, `conflict-check`, `weekly-dive`, and `agent-review`.

### 👁️ Observability — Finally See What Your Agent Knows

```bash
cortex stats        # Overview: counts, freshness, growth trends (24h/7d), storage, alerts
cortex stale        # What's fading — reinforce, delete, or skip
cortex conflicts    # Contradictions among active facts (compact grouped output)
cortex conflicts --verbose  # Full per-conflict detail (no compacting)
cortex optimize     # Manual maintenance: integrity_check + VACUUM + ANALYZE
cortex conflicts --resolve highest-confidence  # Auto-resolve by confidence
cortex conflicts --resolve newest --dry-run    # Preview before applying
cortex conflicts --keep 12345 --drop 12346     # Surgical manual resolution
cortex supersede 12345 --by 12399 --reason "policy updated"
cortex search "deployment policy" --include-superseded
```

No more black-box memory. No more hoping the agent remembers correctly.

For ops posture at scale, see the DB growth runbook: [`docs/ops-db-growth-guardrails.md`](docs/ops-db-growth-guardrails.md).
For checkpoint timing artifacts, run: `scripts/slo_snapshot.sh --warn-stats-ms 3000 --warn-search-ms 5000 --warn-conflicts-ms 5000 --fail-stats-ms 7000 --fail-search-ms 10000 --fail-conflicts-ms 12000 --output /tmp/slo.json --markdown /tmp/slo.md`.
A scheduled CI canary uploads daily SLO artifacts, trend comparisons, and budget-policy results against previous successful runs (`.github/workflows/slo-canary.yml`).

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
┌─────────────────────────────────────────────────────────────────────────┐
│                              cortex CLI                                 │
│  import · search · reason · bench · stats · stale · conflicts · mcp     │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │
     ┌───────────────┬───────────┼───────────┬───────────────┐
     │               │           │           │               │
     ▼               ▼           ▼           ▼               ▼
┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐
│ Importers│  │  Search   │  │  Reason  │  │Observabi-│  │   MCP    │
│          │  │          │  │  Engine   │  │  lity    │  │  Server  │
│ Markdown │  │ BM25     │  │          │  │          │  │          │
│ JSON     │  │ Semantic │  │ Single-  │  │ Stats    │  │ 7 tools  │
│ YAML     │  │ Hybrid   │  │  pass    │  │ Stale    │  │ 2 res.   │
│ CSV/Text │  │ (WSF)    │  │ Recursive│  │ Conflicts│  │ stdio+   │
│          │  │          │  │  (RLM)   │  │          │  │ HTTP/SSE │
└────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘
     │             │             │              │              │
     │             │      ┌──────┴──────┐       │              │
     │             │      ▼             ▼       │              │
     │             │  ┌────────┐  ┌─────────┐   │              │
     │             │  │  LLM   │  │ Presets  │   │              │
     │             │  │        │  │          │   │              │
     │             │  │ Ollama │  │ Built-in │   │              │
     │             │  │ OpenAI │  │  Custom  │   │              │
     │             │  │ OpenR. │  │ (~/.yaml)│   │              │
     │             │  └────────┘  └─────────┘   │              │
     ▼             │                            │              │
┌──────────┐       │                            │              │
│Extraction│       │                            │              │
│          │       │                            │              │
│ Rules    │       │                            │              │
│ LLM opt. │       │                            │              │
└────┬─────┘       │                            │              │
     │             │                            │              │
     ▼             ▼                            ▼              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                          SQLite + FTS5                                   │
│                                                                         │
│  memories │ facts │ embeddings │ recall_log │ memory_events │ projects  │
│                                                                         │
│  Single file: ~/.cortex/cortex.db                                       │
│  WAL mode · Zero config · Trivially portable                           │
└─────────────────────────────────────────────────────────────────────────┘
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
| **Recursive Reasoning** | [RLM Paper](https://arxiv.org/abs/2512.24601) (MIT) | Iterative search→reason loop — LLM decides what to search next |
| **Confidence Decay** | Ebbinghaus forgetting curve | Facts fade unless reinforced — type-aware decay rates |
| **Confidence-Aware Prompts** | Cognitive load theory | LLM sees `[0.95]` vs `[0.45] ⚠️ STALE` — weights reasoning accordingly |
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
| **Recursive reasoning (RLM)** | ✅ Built-in | ❌ | ❌ | ❌ | ❌ |
| **Confidence-aware prompting** | ✅ Decay scores in prompt | ❌ | ❌ | ❌ | ❌ |
| **Model benchmarking** | ✅ `cortex bench` | ❌ | ❌ | ❌ | ❌ |
| **Observability** | ✅ Stats/stale/conflicts | ❌ | ❌ | Basic | ❌ |
| **Conflict resolution** | ✅ 4 strategies + dry-run | ❌ | ❌ | ❌ | ❌ |
| **Confidence decay** | ✅ Ebbinghaus curve | ❌ | ❌ | ❌ | ❌ |
| **Provenance tracking** | ✅ Full chains | ❌ | ❌ | ❌ | ❌ |
| **Self-hosted** | ✅ Single binary | 🟡 Complex | 🟡 Postgres | 🟡 Framework | ✅ |
| **Semantic search** | ✅ Local or API | ✅ Cloud | ✅ Cloud | ✅ | ❌ |
| **Works offline** | ✅ Fully | ❌ | ❌ | ❌ | ✅ |
| **Export / portability** | ✅ JSON, MD, CSV | ❌ Locked in | ❌ | ❌ | 🟡 |
| **Cross-platform** | ✅ Any framework | 🟡 Python-first | 🟡 | ❌ Letta only | 🟡 |

> **Cortex isn't trying to replace these tools.** It solves the problem they don't address: *what happens to the memory you already have?*

---

## 🛠️ Tech Stack

| Component | Choice | Why |
|-----------|--------|-----|
| **Language** | Go 1.24+ | Single binary, no runtime deps, fast compilation |
| **Storage** | SQLite + FTS5 (modernc.org/sqlite) | Pure Go, zero CGO, embedded, battle-tested |
| **Embeddings** | API-based (Ollama, OpenAI, etc.) | Provider-agnostic, no bundled model weight |
| **CLI** | Custom (no framework) | Zero dependencies, minimal binary size |
| **NLP** | Custom rules + optional LLM | Rule-based extraction, LLM optional |

No Docker. No Postgres. No Redis. No CGO. **Just a ~12MB binary and a SQLite file.**

### Embedding Providers

| Provider | Endpoint | API Key | Notes |
|----------|----------|---------|-------|
| **Ollama** (recommended) | `localhost:11434` | None | Free, local, `ollama pull nomic-embed-text` |
| **OpenAI** | `api.openai.com` | `OPENAI_API_KEY` | `text-embedding-3-small` |
| **DeepSeek** | `api.deepseek.com` | `DEEPSEEK_API_KEY` | Budget-friendly |
| **OpenRouter** | `openrouter.ai` | `OPENROUTER_API_KEY` | Any model |
| **Custom** | `CORTEX_EMBED_ENDPOINT` | `CORTEX_EMBED_API_KEY` | Any OpenAI-compatible API |

### Smart Chunking + Context Enrichment

Cortex automatically chunks content for optimal search and embedding:

- **Max 1500 chars per chunk** — fits within token limits of most embedding models (768d+ like `nomic-embed-text`)
- **Splits on paragraph boundaries** (`\n\n`), falls back to line breaks (`\n`), then word boundaries
- **Merges tiny fragments** (<50 chars) with neighbors to avoid noise
- **Preserves provenance** — every chunk tracks source file, line number, and section header
- **Multi-column FTS5** — BM25 searches content, source file, AND section header (not just chunk body)
- **Context-enriched embeddings** — `[filename > Section]` prefix prepended before embedding, giving semantic search topic signal from parent document

```bash
# After upgrading, re-embed with context enrichment:
cortex embed ollama/nomic-embed-text --force
```

---

## 📊 Search Benchmark (v0.1.3)

Real-world benchmark on 967 memories from a production agent workspace. Embedding model: `nomic-embed-text` (768 dimensions) via Ollama.

| Query | BM25 | Semantic | Hybrid | Winner | Why |
|-------|:----:|:-------:|:------:|--------|-----|
| "wedding venue" | 0.829 | 0.603 | 0.876 | **Hybrid** | BM25 keyword + semantic venue context fused for best result |
| "what model does Hawk use" | 0.799 | 0.713 | 0.806 | **Hybrid** | Semantic found capital-instructions (authoritative), hybrid boosted it |
| "crypto trading strategy" | 0.805 | 0.638 | 0.700 | **BM25** | Exact keywords match well; semantic found backtester details |
| "Spear monthly revenue" | 0.791 | 0.687 | 0.822 | **Hybrid** | Semantic surfaced PAYE payment math; hybrid combined both angles |
| "eBay dispute" | 0.660 | 0.626 | 0.700 | **Hybrid** | Both engines weak here; hybrid had best fusion |
| "Q's sleep schedule" | 0.601 | 0.601 | 0.700 | **Hybrid** | Neither engine strong; hybrid best of weak results |

**Key findings (v0.1.2 with Weighted Score Fusion):**
- **BM25**: Fastest, excellent for exact terms and proper nouns
- **Semantic**: Finds conceptually related content BM25 misses entirely (e.g., capital-instructions for agent model queries)
- **Hybrid (WSF)**: Best overall — won 4/6 queries by combining keyword precision with semantic understanding
- **Nomic-embed-text** (768d) handles longer chunks reliably vs all-minilm (384d)

> **Recommendation:** Use `hybrid` as default mode. Fall back to `bm25` for exact keyword lookups, `semantic` for exploratory/conceptual queries.

## 🗺️ Roadmap

### ✅ Phase 1 — Foundation *(Complete)*
Core memory platform is shipped and stable:
- SQLite + FTS5 storage, multi-format import, extraction, hybrid retrieval
- CLI + MCP server + observability (stats/stale/conflicts)
- Data hygiene and recovery commands (`cleanup`, `reimport`, `embed`, `index`)

### ✅ Phase 2 — Intelligence *(Complete)*
- Recursive reasoning (`cortex reason --recursive`) and model benchmarking
- Metadata/project-aware capture and retrieval
- Conflict detection + resolution workflows
- Context-aware search (multi-column FTS + context-enriched embeddings)
- ANN/HNSW indexing path shipped (issue #18 closed)

### ✅ Phase 3 — Reliability & Release Hardening *(Complete for v0.3.5)*
- External audit hardening wave delivered and promoted to stable `v0.3.5`
- RC + delta audit process codified with go/no-go docs
- Release artifact verification and reproducible smoke paths

### 🚀 Phase 4 — Ops Maturity *(Complete and promoted in v0.3.5 stable)*
Shipped on `main` and now in stable release path:
- **Lane 1:** `cortex optimize` maintenance command
- **Lane 2:** `scripts/slo_snapshot.sh` report artifacts (JSON/markdown)
- **Lane 3:** CI guard for go/no-go doc/status drift (`scripts/ci_release_guard.sh`)
- **Lane 4:** tag-release checklist enforcement before publish (`scripts/release_checklist.sh`)
- **Lane 5:** scheduled SLO canary workflow with artifact uploads
- **Lane 6:** thresholded canary warn/fail bands (`PASS|WARN|FAIL`)
- **Lane 7:** deterministic runtime connectivity smoke gate (`scripts/connectivity_smoke.sh`)
- **Lane 8:** one-command external audit preflight artifact (`scripts/audit_preflight.sh`)
- **Lane 9:** hostile-audit packet + immutable-target handoff docs (`docs/audits/v0.3.5-rc2-*.md`, `docs/releases/v0.3.5-rc2.md`)

### 🔭 Phase 5 — Next Priorities
- SLO trend comparison across canary history (relative regression detection)
- Codex real-work dogfooding loop (collect evidence, tune thresholds/prompting only when data justifies)
- Dashboard-grade visibility for release gates + canary trend history
- UX polish for operator-facing audit/release evidence surfaces

### Current State
- Latest stable release: **`v0.3.5`**
- External hostile audit on immutable RC target (`v0.3.5-rc2`) verdict: **GO**
- Current source fallback version: **`0.3.5-dev`**
- Open issues: **none**

See [docs/CORTEX_DEEP_DIVE.md](docs/CORTEX_DEEP_DIVE.md) for the full strategic deep dive and [docs/prd/](docs/prd/) for detailed implementation specs.

---

## 🤝 Contributing

Cortex is built for multi-agent development — AI agents and humans contributing in parallel. We welcome both!

```bash
# Get started
git clone https://github.com/hurttlocker/cortex.git
cd cortex
go build ./cmd/cortex/
go test ./...
scripts/connectivity_smoke.sh   # end-to-end runtime gate (import→extract→search→optimize)
scripts/audit_break_harness.sh  # adversarial sanity checks (missing paths + lock/concurrency regressions)
scripts/audit_preflight.sh --tag v0.3.5       # generate audit-ready markdown + logs
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
