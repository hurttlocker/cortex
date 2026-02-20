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

Optional: it can also write a **single visual dashboard markdown** (with Mermaid diagrams) into your Obsidian vault.

## Generate data
```bash
# Auto-detect active Obsidian vault, write dashboard to <vault>/_cortex_visualizer/
python3 scripts/visualizer_export.py \
  --output docs/visualizer/data/latest.json \
  --obsidian-output docs/visualizer/data/obsidian-graph.json

# Optional manual override
python3 scripts/visualizer_export.py \
  --output docs/visualizer/data/latest.json \
  --obsidian-output docs/visualizer/data/obsidian-graph.json \
  --obsidian-vault-dir ~/path/to/vault/_cortex_visualizer
```

Data sources currently used:
- `cortex stats --json`
- `~/.cortex/reason-telemetry.jsonl`

## Local preview (static)
```bash
python3 -m http.server 8787 --directory docs/visualizer
# open http://127.0.0.1:8787/prototype-v1.html
```

## Local preview (API + static, recommended)
```bash
python3 scripts/visualizer_api.py --bootstrap --port 8787
# open http://127.0.0.1:8787/prototype-v1.html
```

From the UI, click **Open in Obsidian** to launch desktop Obsidian at the visual dashboard note.

API routes:
- `GET /api/v1/canonical`
- `GET /api/v1/obsidian`
- `GET /api/v1/subgraph?focus=<node_id>&max_hops=2&max_nodes=200`
- `GET /api/v1/reason-runs?model=&provider=&preset=&mode=&since_hours=168&limit=80`
- `GET /api/v1/health`

## Open Obsidian directly from CLI
```bash
python3 scripts/visualizer_open_obsidian.py
```
This refreshes exports, auto-detects your active vault, and opens Obsidian desktop to:
`<active-vault>/_cortex_visualizer/cortex-visualizer-dashboard.md`

## Notes
- Black/white baseline for shadcn alignment.
- Provenance graph is a **focused clickable bounded subgraph**, not a full global graph render.
- Obsidian export defaults to a **single dashboard file** with Mermaid graph + pie chart + node directory.
- Dashboard now includes a **Memory Radar** section (recent memories, focus memory pack, timeline, stale/conflict watchlists).
- Default graph bounds are enforced in contract (`max_hops`, `max_nodes`).
- This is prototype-phase: producer path can move from script export to endpoint/read-model service later.
