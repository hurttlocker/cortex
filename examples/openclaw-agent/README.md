# OpenClaw agent integration (MCP)

Expose Cortex tools to your agent through MCP.

## Add MCP server to your coding/chat agent

```bash
# Claude Code style
claude mcp add cortex -- cortex mcp

# Generic stdio MCP run
cortex mcp
```

## Typical agent flow

```bash
cortex init
cortex import ~/workspace --recursive --extract
cortex doctor
```

## Notes

- No API keys required for baseline import/search.
- Add LLM keys later for enrichment/semantic quality.
