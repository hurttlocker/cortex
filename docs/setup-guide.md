# Cortex Setup Guide — For AI Agents

> You just installed Cortex. This guide turns it into your persistent memory layer — facts survive restarts, searches find things you forgot you knew.

## 1. Import Your Existing Knowledge

Import every memory/knowledge file you have:

```bash
# Your main memory files (adjust paths to YOUR workspace)
cortex import ~/YOUR_WORKSPACE/MEMORY.md --extract
cortex import ~/YOUR_WORKSPACE/USER.md --extract
cortex import ~/YOUR_WORKSPACE/SOUL.md --extract

# If you have a memory directory with daily logs:
cortex import ~/YOUR_WORKSPACE/memory/ --recursive --extract

# Any other knowledge files (configs, notes, lessons):
cortex import ~/YOUR_WORKSPACE/tasks/ --recursive --extract

# Optional: class-tag critical docs for better retrieval priority
cortex import ~/YOUR_WORKSPACE/rules.md --class rule
cortex import ~/YOUR_WORKSPACE/decisions.md --class decision
```

Run `cortex stats` after — you should see memories, extracted facts, and sources.

## 2. Set Up Semantic Search (Recommended)

If you have [Ollama](https://ollama.com) installed:

```bash
ollama pull nomic-embed-text

# one-time bootstrap
cortex embed ollama/nomic-embed-text --batch-size 10

# recommended for 24/7 agents: refresh every 30 minutes
cortex embed ollama/nomic-embed-text --watch --interval 30m --batch-size 10
```

This generates vector embeddings for hybrid search (keyword + semantic) and keeps them fresh as new memories arrive.

Verify:
```bash
cortex search "test query" --mode hybrid --embed ollama/nomic-embed-text --limit 3
```

If you DON'T have Ollama, BM25 keyword search still works great:
```bash
cortex search "test query" --limit 5
```

## 3. Commands You'll Actually Use

### Search
Do this BEFORE answering questions about past work, people, or decisions:

```bash
# Hybrid (best results — needs Ollama)
cortex search "what do I know about X" --mode hybrid --embed ollama/nomic-embed-text --limit 5

# BM25 keyword (fast, no dependencies)
cortex search "what do I know about X" --limit 5

# Semantic only (conceptual matching)
cortex search "what do I know about X" --mode semantic --embed ollama/nomic-embed-text --limit 5

# Class-filtered retrieval (rules/decisions first)
cortex search "what's the deploy policy" --class rule,decision --limit 5

# Explainability (provenance + rank factors + confidence/decay)
cortex search "what's the deploy policy" --explain
cortex search "what's the deploy policy" --json --explain
```

### Store
When you learn something important — decisions, preferences, facts:

```bash
# Import a file
cortex import /path/to/file.md --extract

# Quick one-off storage
echo "Important fact: user prefers X over Y because Z" > /tmp/cortex-note.md
cortex import /tmp/cortex-note.md --extract
```

### Stats
Check your memory health:

```bash
cortex stats
```

Shows: total memories, facts extracted, sources, storage size, confidence distribution.

### Stale Facts
Find what's fading from memory (Ebbinghaus confidence decay):

```bash
cortex stale 7    # facts not reinforced in 7 days
cortex stale 30   # facts not reinforced in 30 days
```

### Reinforce
Reset decay timer on a fact you just used — keeps it fresh:

```bash
cortex reinforce <fact_id>
```

### Supersede (Tombstone)
Mark an outdated fact as superseded by a newer one (keeps audit history, hides stale truth by default):

```bash
cortex supersede <old_fact_id> --by <new_fact_id> --reason "policy updated"
cortex list --facts --include-superseded
```

### Cleanup
Purge garbage/duplicate memories:

```bash
cortex cleanup
```

## 4. Search Discipline

Before answering any question about past work, decisions, people, or preferences:

1. Search your built-in memory first (memory_search or equivalent)
2. **ALSO search Cortex** — it catches things built-in memory misses
3. Use whichever gives the better answer

The goal: Cortex finds cross-session knowledge that doesn't survive context window resets.

## 5. Verify Everything Works

Create a test file and import it:

```bash
cat > /tmp/cortex-setup.md << 'EOF'
# Cortex Setup Complete
- Cortex installed and configured
- Search discipline: always check Cortex alongside built-in memory
- Hybrid search enabled (if Ollama available)
- Fact extraction active on imports
- Store important decisions, preferences, and learnings immediately
- Run `cortex stale 7` periodically to find fading knowledge
EOF
cortex import /tmp/cortex-setup.md --extract
```

Then verify it's searchable:

```bash
cortex search "cortex setup" --limit 3
```

You should see your setup note in the results. If so, Cortex is fully operational. ✅

## 6. Recursive Reasoning (RLM)

Cortex can reason over your memories using LLMs — either single-pass or recursive (iterative search→reason loop inspired by the [RLM paper](https://arxiv.org/abs/2512.24601)).

### Quick Start

```bash
# Single-pass (fast, simple queries)
cortex reason "What happened today?" --preset daily-digest --embed ollama/nomic-embed-text

# Recursive (deep, complex queries — the LLM searches iteratively)
cortex reason "Analyze all my project risks" --recursive -v --embed ollama/nomic-embed-text
```

### LLM Setup

Cortex reason needs an LLM. Options:

**Cloud (recommended for interactive use):**
```bash
export OPENROUTER_API_KEY=sk-or-v1-...   # Get from https://openrouter.ai
cortex reason "query" --recursive -v --embed ollama/nomic-embed-text
# Auto-selects gemini-2.5-flash (interactive) or deepseek-v3.2 (deep analysis)
```

**Local (free, private — great for GPU users or scheduled cron):**
```bash
ollama pull phi4-mini   # 3.8B params, fits any machine
cortex reason "query" --recursive --model phi4-mini --embed ollama/nomic-embed-text
```

### Built-in Presets

```bash
cortex reason --list   # See all presets
```

| Preset | Use For | Default Model |
|--------|---------|---------------|
| `daily-digest` | Daily activity summary | gemini-2.5-flash |
| `fact-audit` | Find stale/bad facts | deepseek-v3.2 |
| `weekly-dive` | Deep analysis of a topic | deepseek-v3.2 |
| `conflict-check` | Find contradictions | gemini-2.5-flash |
| `agent-review` | Agent performance review | gemini-2.5-flash |

### Benchmark Models

When a new model drops, test it on your own memory:

```bash
cortex bench --models "google/gemini-2.5-flash,deepseek/deepseek-chat" \
  --embed ollama/nomic-embed-text --output report.md
```

## 7. Ongoing Habits

| When | Do |
|---|---|
| After important conversations | Import daily log or key decisions |
| Before answering recall questions | Search Cortex first |
| Weekly | Run `cortex stats` + `cortex stale 7` |
| After corrections/learnings | Store the lesson immediately |

## 8. OpenClaw Plugin (Optional)

If you're running [OpenClaw](https://github.com/openclaw/openclaw), the Cortex plugin adds automatic capture and recall:

```bash
# Install the plugin
openclaw plugins install /path/to/cortex/plugin

# Add to openclaw.json under plugins.entries:
"openclaw-cortex": {
  "enabled": true,
  "config": {
    "autoRecall": true,
    "autoCapture": true,
    "extractFacts": true,
    "searchMode": "hybrid",
    "embedProvider": "ollama/nomic-embed-text"
  }
}
```

- **autoRecall**: Injects relevant memories before each AI turn
- **autoCapture**: Stores conversation exchanges after each turn
- **extractFacts**: Pulls out key-value pairs, identities, temporal facts

Restart OpenClaw after config change.

---

## Quick Reference

```bash
cortex version                    # Check installed version
cortex stats                      # Memory health overview
cortex search "query" --limit 5   # BM25 keyword search
cortex search "query" --mode hybrid --embed ollama/nomic-embed-text --limit 5  # Hybrid
cortex import file.md --extract   # Import with fact extraction
cortex import dir/ --recursive --extract  # Bulk import
cortex stale 7                    # Fading facts (7 days)
cortex reinforce <id>             # Keep a fact fresh
cortex cleanup                    # Purge garbage
cortex list --limit 10            # Recent memories
cortex list --facts --limit 10    # Recent facts
```

**Repo:** [github.com/hurttlocker/cortex](https://github.com/hurttlocker/cortex)
