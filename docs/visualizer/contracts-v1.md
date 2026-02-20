# Cortex Visualizer — Data Contracts v1 (Draft)

Status: draft scaffold for issue #100

## Contract Goals
- Stable read models for UI modules in issues #101-#104
- Backward-compatible evolution via explicit `schema_version`
- Cortex remains source-of-truth (no UI-owned state)
- One canonical backend payload can power both Cortex UI and Obsidian graph consumers

## Global Envelope
All visualizer payloads follow this shape:

```json
{
  "schema_version": "v1",
  "generated_at": "2026-02-20T15:50:00Z",
  "window": { "from": "...", "to": "..." },
  "data": {}
}
```

## 1) Ops Gate Board Contract

```json
{
  "schema_version": "v1",
  "data": {
    "overall_status": "PASS|WARN|FAIL|NO_DATA",
    "gates": [
      { "key": "ci", "label": "CI Build/Test", "status": "PASS", "reason": "..." },
      { "key": "canary", "label": "Canary Trend", "status": "WARN", "reason": "..." },
      { "key": "release", "label": "Release Checklist", "status": "PASS", "reason": "..." }
    ],
    "trend": [
      { "ts": "2026-02-20T00:00:00Z", "score": 72.1 },
      { "ts": "2026-02-20T04:00:00Z", "score": 74.5 }
    ],
    "events": [
      { "ts": "...", "severity": "warn", "message": "canary regression threshold exceeded" }
    ]
  }
}
```

## 2) Memory Quality Engine Contract

```json
{
  "schema_version": "v1",
  "data": {
    "score": 78,
    "delta_24h": -4,
    "factors": [
      { "key": "conflict_density", "value": 0.32, "weight": 0.25, "impact": -7 },
      { "key": "stale_pressure", "value": 0.44, "weight": 0.20, "impact": -5 },
      { "key": "confidence_health", "value": 0.79, "weight": 0.30, "impact": 8 },
      { "key": "extraction_yield", "value": 0.69, "weight": 0.25, "impact": 2 }
    ],
    "actions": [
      "tighten low-signal ingestion filter window"
    ]
  }
}
```

## 3) Reasoning Run Inspector Contract

```json
{
  "schema_version": "v1",
  "data": {
    "runs": [
      {
        "run_id": "2201",
        "mode": "recursive|one-shot",
        "model": "...",
        "provider": "...",
        "latency_ms": 18700,
        "tokens_total": 19300,
        "estimated_cost_usd": 0.021,
        "status": "ok|error",
        "steps": [
          { "name": "search", "latency_ms": 1200, "status": "ok" },
          { "name": "reason", "latency_ms": 13000, "status": "ok" }
        ]
      }
    ],
    "p95_latency_ms": 14200,
    "cost_24h_usd": 0.42
  }
}
```

## 4) Retrieval Debug Contract

```json
{
  "schema_version": "v1",
  "data": {
    "query": "...",
    "results": {
      "bm25": [ { "rank": 1, "id": "...", "score": 0.88, "title": "..." } ],
      "semantic": [ { "rank": 1, "id": "...", "score": 0.76, "title": "..." } ],
      "hybrid": [ { "rank": 1, "id": "...", "score": 0.91, "title": "..." } ]
    },
    "deltas": [
      { "id": "...", "bm25_rank": 9, "hybrid_rank": 2, "reason": "semantic boost" }
    ]
  }
}
```

## 5) Provenance Explorer / Canonical Graph Contract

```json
{
  "schema_version": "v1",
  "data": {
    "focus": "fact_123",
    "bounds": { "max_hops": 2, "max_nodes": 200, "default_radius": 1 },
    "nodes": [
      {
        "id": "fact_123",
        "type": "fact",
        "label": "canary regression threshold exceeded",
        "weight": 1.0,
        "confidence": 0.91,
        "timestamp": "2026-02-20T15:50:00Z",
        "source_ref": "docs/ops-db-growth-guardrails.md"
      }
    ],
    "edges": [
      {
        "id": "edge_1",
        "from": "fact_123",
        "to": "mem_88",
        "kind": "sourced_from",
        "weight": 1.0,
        "timestamp": "2026-02-20T15:50:00Z",
        "source_ref": "docs/ops-db-growth-guardrails.md"
      }
    ]
  }
}
```

## 6) Obsidian Adapter Contract (derived from canonical graph)

```json
{
  "schema_version": "v1",
  "generated_at": "2026-02-20T15:50:00Z",
  "source_snapshot": "canonical-v1",
  "graph": {
    "focus": "fact_123",
    "bounds": { "max_hops": 2, "max_nodes": 200, "default_radius": 1 },
    "nodes": [
      {
        "id": "fact_123",
        "title": "canary regression threshold exceeded",
        "type": "fact",
        "confidence": 0.91,
        "timestamp": "2026-02-20T15:50:00Z",
        "source_ref": "docs/ops-db-growth-guardrails.md",
        "links": ["mem_88", "out_12"]
      }
    ],
    "edges": [
      { "id": "edge_1", "from": "fact_123", "to": "mem_88", "kind": "sourced_from" }
    ]
  }
}
```

## Versioning Rules
- Non-breaking additions: add nullable fields only
- Breaking changes: bump `schema_version` (v2, v3...)
- Keep one previous version adapter for smooth UI migration

## Next Steps
1. Define exact producers for each contract (command adapter vs endpoint)
2. Add golden fixtures under `tests/fixtures/visualizer/`
3. Bind #101-#104 UI modules to these contracts
4. Keep Obsidian adapter derived-only (never as source-of-truth)
