# Cortex Visualizer Prototype (v1)

This folder contains the in-repo prototype for the custom visualizer workstream.

## What this is
- `mock-v1.html` — static layout mock (visual direction only)
- `prototype-v1.html` — data-bound prototype using canonical snapshot JSON
- `contracts-v1.md` — draft contract spec for #100
- `data/latest.json` — canonical visualizer snapshot (source-of-truth payload)
- `data/obsidian-graph.json` — adapter payload for Obsidian-style graph consumers

## One backend, two consumers
The exporter builds **one canonical graph/read-model** and then adapts it for Obsidian.

- Canonical: `data/latest.json` (Cortex visualizer)
- Adapter: `data/obsidian-graph.json` (Obsidian graph-friendly)

Optional: it can also write markdown files with wikilinks for vault graph browsing.

## Generate data
```bash
python3 scripts/visualizer_export.py \
  --output docs/visualizer/data/latest.json \
  --obsidian-output docs/visualizer/data/obsidian-graph.json \
  --obsidian-vault-dir docs/visualizer/data/obsidian-vault
```

Data sources currently used:
- `cortex stats --json`
- `~/.cortex/reason-telemetry.jsonl`

## Local preview
```bash
python3 -m http.server 8787 --directory docs/visualizer
# open http://127.0.0.1:8787/prototype-v1.html
```

## Notes
- Black/white baseline for shadcn alignment.
- Provenance graph is a **focused clickable bounded subgraph**, not a full global graph render.
- Default graph bounds are enforced in contract (`max_hops`, `max_nodes`).
- This is prototype-phase: producer path can move from script export to endpoint/read-model service later.
