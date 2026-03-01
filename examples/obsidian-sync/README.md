# Obsidian sync example

Export Cortex knowledge into an Obsidian vault and validate graph health.

## One-time dry run

```bash
cortex export obsidian --vault "$HOME/Documents/MyVault" --dry-run --validate --hub-stats
```

## Real export

```bash
cortex export obsidian --vault "$HOME/Documents/MyVault" --clean --validate
```

## Periodic sync (cron)

Use the provided script with launchd/cron/systemd.
