# Cortex Visualizer — Data Contracts v1 (Draft)

Status: draft scaffold for issue #100

## Contract Goals
- Stable read models for UI modules in issues #101-#104
- Backward-compatible evolution via explicit `schema_version`
- Cortex remains source-of-truth (no UI-owned state)

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
    "overall_status": "PASS|WARN|FAIL",
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

## 5) Provenance Explorer Contract

```json
{
  "schema_version": "v1",
  "data": {
    "focus_fact_id": "fact_123",
    "nodes": [
      { "id": "fact_123", "type": "fact", "label": "canary regression threshold exceeded" },
      { "id": "mem_88", "type": "source", "label": "docs/ops-db-growth-guardrails.md" },
      { "id": "out_12", "type": "artifact", "label": "release summary" }
    ],
    "edges": [
      { "from": "fact_123", "to": "mem_88", "kind": "sourced_from" },
      { "from": "fact_123", "to": "out_12", "kind": "influenced" }
    ],
    "bounds": { "max_hops": 2, "max_nodes": 200 }
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
