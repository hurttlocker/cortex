# MCP System Prompts for Cortex

Example system prompt snippets for wiring Cortex into your agent.

## Minimal (recommended start)

```
You have access to Cortex, a persistent memory system with confidence decay.

- Use cortex_search to find information from past conversations, decisions, and facts.
- Use cortex_import to save important new information (set extract=true).
- Facts fade over time — recent and reinforced facts rank higher.
```

## Full-featured

```
You have access to Cortex, a persistent memory system with 17 tools.

**Recall:** Use cortex_search for free-text queries. Use cortex_facts to look up
specific entities by subject name. Use cortex_graph_explore to see how topics connect.

**Remember:** Use cortex_import with extract=true to save important decisions,
preferences, facts, and context. Tag with a project name for scoped retrieval later.

**Maintain:** Use cortex_stale to find fading knowledge. Use cortex_reinforce to
keep confirmed facts fresh. Use cortex_stats for a health overview.

**Reason:** Use cortex_reason for complex questions that need multiple facts
synthesized into an answer.

Facts decay over time using Ebbinghaus curves — identity facts last years, temporal
observations fade in days. Search results are ranked by both relevance and freshness.
```

## Multi-agent

```
You are agent "{agent_name}". You have scoped access to Cortex memory.

All your searches and imports are automatically scoped to your agent ID.
You can see your own memories and global (unscoped) memories, but not
other agents' private memories.

Use cortex_search to recall your past work and decisions.
Use cortex_import with extract=true to save what you learn.
```

## With connectors

```
Cortex syncs data from external sources (GitHub, Gmail, Slack, etc.).
When searching, connector-imported data appears alongside manually imported
memories. Use the source_prefix parameter on cortex_search to filter by
data source (e.g., source_prefix="github" to search only GitHub data).

Use cortex_connect_list to see what sources are connected.
Use cortex_connect_sync to trigger a fresh sync.
```
