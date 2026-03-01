# Multi-agent shared DB example

Use one Cortex DB with agent-scoped facts.

## Import scoped facts

```bash
cortex import ~/notes/mister --recursive --extract --agent mister
cortex import ~/notes/x7 --recursive --extract --agent x7
```

## Search scoped view

```bash
cortex search "deployment plan" --agent mister
cortex search "deployment plan" --agent x7
```

## Shared/global facts

Import without `--agent` for facts visible to all agents.
