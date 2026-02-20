# openclaw-cortex

> Cortex memory plugin for OpenClaw — local, free, observable AI memory with hybrid search and confidence decay.

**Zero cloud dependencies.** Uses the local `cortex` binary + ollama embeddings. Your data never leaves your machine.

## Install

```bash
# From the cortex repo
openclaw plugins install ./plugin

# Or from npm (after publish)
openclaw plugins install openclaw-cortex
```

## Setup

1. **Install Cortex** — download from [GitHub Releases](https://github.com/hurttlocker/cortex/releases) or build from source:
   ```bash
   # Download (macOS ARM64 example)
   curl -L https://github.com/hurttlocker/cortex/releases/latest/download/cortex_darwin_arm64 -o ~/bin/cortex
   chmod +x ~/bin/cortex
   ```

2. **Install ollama + nomic-embed-text** (for hybrid search):
   ```bash
   # Install ollama: https://ollama.ai
   ollama pull nomic-embed-text
   ```

3. **Verify setup:**
   ```bash
   openclaw cortex setup
   ```

## Configuration

Add to your OpenClaw config (`~/.openclaw/openclaw.json`):

```json
{
  "plugins": {
    "entries": {
      "openclaw-cortex": {
        "enabled": true,
        "config": {
          "autoRecall": true,
          "autoCapture": true,
          "extractFacts": true,
          "searchMode": "hybrid",
          "embedProvider": "ollama/nomic-embed-text",
          "capture": {
            "dedupe": { "enabled": true },
            "similarityThreshold": 0.95,
            "dedupeWindowSec": 300,
            "coalesceWindowSec": 20
          }
        }
      }
    }
  }
}
```

> ⚠️ **Important:** Plugin settings MUST go under a `config` key, not at the top level alongside `enabled`. Placing them at the wrong level will crash the gateway on every startup attempt — including all agent heartbeats.

### Options

| Key | Default | Description |
|-----|---------|-------------|
| `binaryPath` | `~/bin/cortex` | Path to cortex binary |
| `dbPath` | `~/.cortex/cortex.db` | SQLite database path |
| `embedProvider` | `ollama/nomic-embed-text` | Embedding model for hybrid/semantic search |
| `searchMode` | `hybrid` | Default: `hybrid`, `bm25`, or `semantic` |
| `autoRecall` | `true` | Inject relevant memories before each AI turn |
| `autoCapture` | `false` | Capture conversation exchanges after each AI turn |
| `extractFacts` | `true` | Extract structured facts from captured content |
| `recallLimit` | `3` | Max memories injected per turn |
| `minScore` | `0.3` | Minimum score for auto-recall results |
| `captureMaxChars` | `2000` | Max message length for auto-capture |
| `capture.dedupe.enabled` | `true` | Enable near-duplicate suppression for auto-capture |
| `capture.similarityThreshold` | `0.95` | Cosine similarity cutoff for duplicate suppression |
| `capture.dedupeWindowSec` | `300` | Recent lookback window for dedupe checks |
| `capture.coalesceWindowSec` | `20` | Coalesce short rapid-fire captures into one memory |
| `capture.minCaptureChars` | `20` | Suppress short low-signal captures below this length |
| `capture.lowSignalPatterns` | built-in list | Additional low-signal phrases to suppress |
| `recallDedupe.enabled` | `true` | Deduplicate exact/near-duplicate recall memories |
| `recallDedupe.similarityThreshold` | `0.98` | Similarity cutoff used for recall dedupe |

## Features

### Auto-Recall
Before each AI response, Cortex searches for relevant memories using your query and injects them as context. The AI sees past decisions, preferences, and facts without being asked.

### Auto-Capture
After each AI turn, the conversation exchange is captured into Cortex with automatic fact extraction. Preferences, decisions, identities, and temporal facts are extracted and indexed.

### Auto-Capture Hygiene (Issue #36)
Capture hygiene reduces memory bloat and retrieval noise:

- **Near-duplicate suppression** with cosine similarity against recent captures
- **Burst coalescing** for rapid-fire short exchanges
- **Low-signal filter** for trivial acknowledgements/commands (`ok`, `yes`, `got it`, `HEARTBEAT_OK`, `fire the test`)
- **Minimum capture length guard** (`capture.minCaptureChars`, default `20`)
- **Recall-side dedupe** before `<cortex-memories>` injection
- **Server-side dedupe** flags passed into `cortex import` for defense-in-depth

These controls are configurable under `capture.*` and `recallDedupe.*`.

### AI Tools
The plugin registers 4 tools the AI can use:

| Tool | Description |
|------|-------------|
| `cortex_search` | Search memories with hybrid/BM25/semantic modes |
| `cortex_store` | Save information with fact extraction |
| `cortex_stats` | View memory statistics and health |
| `cortex_profile` | Build user profile from aggregated facts |

### CLI Commands
```bash
openclaw cortex search "wedding venue"    # Search memories
openclaw cortex stats                      # Show statistics
openclaw cortex setup                      # Verify configuration
```

## Migrating from Supermemory

```bash
# 1. Remove Supermemory plugin
openclaw plugins uninstall @supermemory/openclaw-supermemory

# 2. Install Cortex plugin
openclaw plugins install openclaw-cortex

# 3. Import your existing memories
cortex import ~/path/to/memories --recursive --extract

# 4. Enable auto-capture (replaces Supermemory's auto-capture)
# Add to config: "autoCapture": true
```

### Comparison

| Feature | Supermemory | Cortex |
|---------|------------|--------|
| Cost | $19/mo (Pro) | **Free forever** |
| Data location | Cloud | **Local (your machine)** |
| Search | Semantic | **Hybrid (BM25 + semantic)** |
| Confidence decay | ❌ | **✅ Ebbinghaus per-type** |
| Fact extraction | ✅ | **✅ 8 fact types** |
| Observability | Cloud dashboard | **SQLite (full access)** |
| User profiles | ✅ | **✅ via cortex_profile** |
| Auto-capture | ✅ | **✅** |
| Auto-recall | ✅ | **✅** |
| MCP server | ❌ | **✅ (6 tools)** |
| Dependencies | OpenAI API key | **ollama (free, local)** |

## How It Works

```
User message → AI processing → Response
      ↓ (before_agent_start)          ↓ (agent_end)
  cortex search → inject context    capture exchange → cortex import --extract
```

The plugin uses OpenClaw's lifecycle hooks:
- **`before_agent_start`**: Searches Cortex with the user's message, injects matching memories as `<cortex-memories>` context
- **`agent_end`**: Captures the conversation exchange, writes to temp file, runs `cortex import --extract` for fact extraction

All operations go through the local `cortex` binary — no HTTP APIs, no cloud calls, no API keys.

## Troubleshooting

### Gateway crashes on startup after installing

**Symptom:** Gateway exits with code 1 on every startup attempt, including heartbeats. Logs show config validation errors.

**Cause 1: Config keys at wrong level**
Plugin settings must be nested under `config`, not alongside `enabled`:

```json
// ❌ WRONG — will crash gateway
{
  "plugins": {
    "entries": {
      "openclaw-cortex": {
        "enabled": true,
        "autoRecall": true,
        "autoCapture": true
      }
    }
  }
}

// ✅ CORRECT
{
  "plugins": {
    "entries": {
      "openclaw-cortex": {
        "enabled": true,
        "config": {
          "autoRecall": true,
          "autoCapture": true
        }
      }
    }
  }
}
```

**Fix:** Edit `~/.openclaw/openclaw.json` manually and nest settings under `config`.

**Cause 1b: "Unrecognized keys" warnings even when config is correct**

You may still see warnings like:

```text
plugins.entries.openclaw-cortex: Unrecognized keys: "autoRecall", "autoCapture", ...
```

when your config is correctly nested under `config`. In current OpenClaw builds, this can happen because core config validation may not fully apply extension `configSchema` before warning.

- **Impact:** noisy logs only (plugin still works)
- **Safety:** non-blocking for runtime behavior
- **What to do:** keep config nested under `config` and continue

We'll track upstream validator behavior; until then, treat this as known log noise, not a plugin failure.

**Cause 2: Missing dependencies**
If you see `Cannot find module '@sinclair/typebox'`, run:
```bash
cd ~/.openclaw/extensions/openclaw-cortex && npm install
```

### Plugin loads but auto-recall/capture doesn't work

Check that ollama is running with nomic-embed-text:
```bash
ollama list  # Should show nomic-embed-text
```

Verify Cortex binary is accessible:
```bash
~/bin/cortex stats --json
```

### "Shell command execution detected" warning on install

This is expected — the plugin shells out to the local `cortex` binary. It's flagged as a safety warning, not an error. The plugin only runs commands against your local Cortex binary, never external services.

## License

MIT — same as Cortex.
