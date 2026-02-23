# Cortex Platform Features Guide

These features were introduced in Cortex v0.5 (Epic #158). They extend Cortex
from a memory tool to a multi-agent knowledge platform with proactive alerts,
agent attribution, and an emergent knowledge graph.

## Agent Namespaces

Facts are attributed to the agent that created them. This enables scoped search,
agent-specific reinforcement, and cross-agent conflict detection.

### How It Works

Every fact has an optional `agent_id` field:
- `agent_id=""` → Global fact (visible to all, created by CLI/import)
- `agent_id="mister"` → Mister's fact (prioritized in Mister's searches)

### CLI Usage

```bash
# Import with agent attribution
cortex import notes.md --agent mister

# Search scoped to an agent
cortex search "deployment config" --agent mister

# Facts for a specific agent
cortex facts --agent hawk --limit 20
```

### MCP Usage

Most MCP tools accept an `agent_id` parameter:
- `cortex_search` → `agent_id` prioritizes agent's own facts
- `cortex_import` → `agent_id` attributes imported facts
- `cortex_facts` → `agent_id` filters fact listing

### Search Behavior

When searching with `--agent mister`:
1. Mister's facts are **boosted** in ranking (not exclusive filter)
2. Global facts are still visible
3. Other agents' facts are still visible but ranked lower

This means agents benefit from the full knowledge base while getting
priority on their own contributions.

---

## Proactive Alerts

Cortex creates alerts automatically when it detects important changes in your
knowledge base. Three alert types:

### 1. Conflict Detection

When a new fact contradicts an existing fact (same subject, different value):

```bash
# Check a specific fact for conflicts
# (happens automatically during import)
cortex alerts

# Example output:
# 🔴 [conflict] #42 (critical) — Conflicting facts: "Deploy target: staging" vs "Deploy target: production"
```

**Cross-agent conflicts** are escalated: if Mister says "deploy to staging" and
Hawk says "deploy to production", severity is bumped (info→warning, warning→critical).

### 2. Decay Notifications

Facts lose confidence over time (Ebbinghaus decay). When facts drop below
configurable thresholds:

```bash
# Run decay scan
cortex alerts check-decay

# Custom thresholds
cortex alerts check-decay --warning 0.6 --critical 0.4

# Get a grouped digest
cortex alerts digest
```

Default thresholds: Warning < 0.5, Critical < 0.3.

### 3. Watch Queries

Persistent search queries that trigger alerts when new matching content arrives:

```bash
# Create a watch
cortex watch add "deployment failures" --threshold 0.7

# List active watches
cortex watch list

# Watches fire automatically during import — no polling needed
```

### Alert Management

```bash
# List unacknowledged alerts
cortex alerts

# JSON output
cortex alerts --json

# Acknowledge specific alert
cortex alerts --ack 42

# Acknowledge all
cortex alerts --ack-all

# Filter by type
cortex alerts --type conflict
cortex alerts --type decay
cortex alerts --type match
```

### Webhook Delivery

Alerts can be POSTed to a webhook URL:

```bash
# Configure via environment
export CORTEX_ALERT_WEBHOOK_URL="https://your-server.com/cortex-alerts"
export CORTEX_ALERT_WEBHOOK_HEADERS='{"Authorization": "Bearer token123"}'
```

Webhook behavior:
- **Non-blocking**: Alert creation never waits for webhook delivery
- **Batched**: Alerts within 5 seconds are grouped into a single POST
- **Retry**: One retry after 5s on 5xx errors
- **Single alert payload**: `{"type": "conflict", "severity": "warning", ...}`
- **Batch payload**: `{"alerts": [...], "count": 3}`

---

## Knowledge Graph

Facts can be connected by typed relationship edges. The graph emerges from
usage patterns rather than manual curation.

### Edge Types

| Type | Meaning | Example |
|------|---------|---------|
| `supports` | Fact A provides evidence for Fact B | Test result supports hypothesis |
| `contradicts` | Fact A conflicts with Fact B | Old config contradicts new config |
| `supersedes` | Fact A replaces Fact B | New policy supersedes old one |
| `relates_to` | Fact A is relevant to Fact B | General association |
| `derived_from` | Fact A was computed from Fact B | Summary derived from source |

### Manual Edges

```bash
# Add an edge
cortex edge add 123 456 --type supports --confidence 0.9

# List edges for a fact
cortex edge list 123

# Remove an edge
cortex edge remove 123 456 --type supports
```

### Graph Traversal

```bash
# Traverse from a fact (BFS, max depth 3)
cortex graph 123 --depth 3 --min-confidence 0.5

# JSON output
cortex graph 123 --json
```

### Co-occurrence Tracking

Every search automatically records which facts appear together in results.
After enough co-occurrences, the inference engine can suggest relationships:

```bash
# Run inference (dry-run first)
cortex infer --dry-run

# Create inferred edges
cortex infer

# Example output:
# Proposed: 123 → 456 (relates_to, confidence: 0.7, reason: co-occurrence count 12)
```

### Inference Rules

1. **Co-occurrence → relates_to**: Facts that frequently appear in the same search
   results likely relate to each other. Confidence scales with count (5→0.5, 10→0.7, 20+→0.9).

2. **Subject clustering → relates_to**: Facts with the same subject (e.g., both about
   "deployment") get a weak relates_to edge (confidence: 0.4).

3. **Supersession → supersedes**: When a newer fact has the same subject and predicate
   as an older fact but different value, it likely supersedes it (confidence: 0.6).

### Graph Decay

Inferred edges that go unused (not traversed or reinforced) for 90 days are
automatically pruned. This keeps the graph from accumulating stale relationships.

---

## Shared Reinforcement

In multi-agent setups, cross-agent activity reinforces facts — keeping important
shared knowledge fresh.

### Reinforcement Weights

| Action | Weight | Effect |
|--------|--------|--------|
| Explicit reinforce (`cortex reinforce`) | 1.0 | Full confidence reset |
| Import/update | 0.8 | Strong refresh |
| Cross-agent access | 0.5 | Moderate refresh |
| Search hit | 0.3 | Light refresh |

### How It Works

When Agent A searches and finds a fact originally from Agent B:
1. The fact gets a weighted reinforcement (0.3 for search, 0.5 for explicit access)
2. The access is logged in `fact_accesses_v1` with source agent attribution
3. The fact's `last_reinforced` timestamp moves proportionally toward now

This means important facts that multiple agents rely on naturally stay fresh,
while unused facts decay normally.

### Viewing Access History

```bash
# See which agents have accessed a fact
cortex facts --id 123 --verbose
```

---

## Putting It All Together

A typical multi-agent setup:

```bash
# Agent setup
cortex import mister-notes/ --agent mister --extract
cortex import hawk-reports/ --agent hawk --extract

# Set up watches for each agent's domain
cortex watch add "deployment failures" --agent hawk
cortex watch add "customer complaints" --agent mister

# Configure webhook for team alerting
export CORTEX_ALERT_WEBHOOK_URL="https://slack.webhook.url"

# Connect external sources
cortex connect add github --config '{"token": "...", "repos": ["org/repo"]}'
cortex connect add slack --config '{"token": "xoxb-...", "channels": ["C01234"]}'
cortex connect sync --all

# Let inference discover relationships
cortex infer --dry-run  # preview
cortex infer            # create edges

# Visualize the graph
cortex graph 123 --depth 3
```

The system then runs autonomously:
- Imports trigger watch alerts and fact extraction
- Conflicts are detected and alerted in real-time
- Cross-agent searches reinforce shared knowledge
- The inference engine periodically discovers new relationships
- Stale facts and edges decay naturally
