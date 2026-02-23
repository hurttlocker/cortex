# @cortex-ai/mcp

**Memory that forgets** — persistent agent memory with Ebbinghaus decay.

Zero-config MCP server for AI agents. No API keys. No cloud account. No setup wizard.

## Quickstart (60 seconds)

### 1. Add to your MCP config

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "cortex": {
      "command": "npx",
      "args": ["-y", "@cortex-ai/mcp"]
    }
  }
}
```

**Cursor** (`.cursor/mcp.json`):
```json
{
  "mcpServers": {
    "cortex": {
      "command": "npx",
      "args": ["-y", "@cortex-ai/mcp"]
    }
  }
}
```

**OpenClaw** (`openclaw.json`):
```json
{
  "mcpServers": {
    "cortex": {
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@cortex-ai/mcp"]
    }
  }
}
```

### 2. Done.

Your agent now has persistent memory with:
- **Fact extraction** — automatically extracts structured facts from conversations
- **Ebbinghaus decay** — knowledge fades over time unless reinforced
- **Hybrid search** — BM25 keyword + semantic vector search
- **Zero LLM dependency** — fact extraction uses rule-based NLP, not API calls

## MCP Tools

| Tool | Description |
|---|---|
| `cortex_search` | Search memories and facts (hybrid: BM25 + semantic) |
| `cortex_import` | Import text into memory with automatic fact extraction |
| `cortex_facts` | List extracted facts, optionally filtered |
| `cortex_reinforce` | Reinforce a fact (reset decay timer — "this is still true") |
| `cortex_stale` | Find facts that haven't been reinforced recently |
| `cortex_conflicts` | Detect contradictory facts |

## How It Works

```
Import text → Extract facts → Store with timestamp
                                    ↓
                              Confidence decays
                              (Ebbinghaus curve)
                                    ↓
                        Search ranks by relevance
                        × confidence × recency
                                    ↓
                          Reinforce what matters
                          (resets decay timer)
```

## What Makes Cortex Different

| Feature | Cortex | Vector DBs | Other Memory Tools |
|---|---|---|---|
| Knowledge decay | ✅ Ebbinghaus curve | ❌ | ❌ |
| Fact extraction | ✅ Rule-based (no LLM) | ❌ | ⚠️ Requires LLM |
| Hybrid search | ✅ BM25 + semantic | ⚠️ Semantic only | ⚠️ Varies |
| API keys needed | ❌ None | ⚠️ Usually | ⚠️ Usually |
| Self-hosted | ✅ SQLite (local) | ⚠️ Varies | ⚠️ Often cloud |
| Reinforcement | ✅ Explicit + implicit | ❌ | ❌ |

## Advanced Usage

### HTTP+SSE transport
```bash
npx @cortex-ai/mcp --port 8080
```

### Custom database path
```bash
CORTEX_DB=~/my-project/memory.db npx @cortex-ai/mcp
```

### With semantic search (requires Ollama)
```bash
# Install Ollama + embedding model
ollama pull nomic-embed-text

# Cortex auto-detects Ollama for semantic search
npx @cortex-ai/mcp
```

## Links

- [GitHub](https://github.com/hurttlocker/cortex)
- [Full Documentation](https://github.com/hurttlocker/cortex/blob/main/README.md)
- [Releases](https://github.com/hurttlocker/cortex/releases)

## License

MIT
