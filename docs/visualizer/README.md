# Cortex Visualizer Prototype (v1)

This folder contains an in-repo prototype for the custom visualizer workstream.

## What this is
- `mock-v1.html` — static layout mock (visual direction only)
- `prototype-v1.html` — data-bound prototype using exported JSON snapshot
- `data/latest.json` — latest exported snapshot payload

## Data source
The prototype currently reads `docs/visualizer/data/latest.json`.
Generate/refresh that file with:

```bash
python3 scripts/visualizer_export.py --output docs/visualizer/data/latest.json
```

The exporter currently pulls from:
- `cortex stats --json`
- `~/.cortex/reason-telemetry.jsonl`

## Local preview
```bash
python3 -m http.server 8787 --directory docs/visualizer
# then open http://127.0.0.1:8787/prototype-v1.html
```

## Notes
- This is intentionally black/white baseline for shadcn alignment.
- Provenance graph is a **focused clickable subgraph prototype**, not global graph rendering.
- Next step is wiring these views to versioned contracts in `docs/visualizer/contracts-v1.md`.
