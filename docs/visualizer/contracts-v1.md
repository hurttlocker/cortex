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
      {
        "key": "ci",
        "label": "CI Build/Test",
        "status": "PASS",
        "reason": "...",
        "evidence_links": [
          { "label": "CI workflow", "href": "https://..." }
        ]
      },
      {
        "key": "canary",
        "label": "Canary Trend",
        "status": "WARN",
        "reason": "...",
        "evidence_links": [
          { "label": "SLO canary workflow", "href": "https://..." }
        ]
      },
      {
        "key": "release",
        "label": "Release Checklist",
        "status": "PASS",
        "reason": "...",
        "evidence_links": [
          { "label": "Release workflow", "href": "https://..." }
        ]
      }
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

Contract notes:
- `status` uses enum: `PASS|WARN|FAIL|NO_DATA`
- `evidence_links` should be non-empty for operational gates when status is not `NO_DATA`

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
  "generated_at": "2026-02-20T16:40:00Z",
  "filters_applied": {
    "model": "google/gemini-3-flash-preview",
    "provider": "openrouter",
    "preset": "daily-digest",
    "mode": "recursive",
    "since_hours": 168,
    "limit": 80
  },
  "filter_options": {
    "model": ["..."],
    "provider": ["..."],
    "preset": ["..."],
    "mode": ["recursive", "one-shot"],
    "since_hours_default": 168
  },
  "summary": {
    "run_count": 12,
    "error_count": 1,
    "recursive_count": 10,
    "one_shot_count": 2,
    "p95_latency_ms": 14200,
    "cost_total_usd": 0.42,
    "tokens_total": 19300
  },
  "runs": [
    {
      "run_id": "2026-02-20T16:24:58Z",
      "timestamp": "2026-02-20T16:24:58Z",
      "mode": "recursive",
      "model": "google/gemini-3-flash-preview",
      "provider": "openrouter",
      "preset": "daily-digest",
      "query": "...",
      "latency_ms": 121872,
      "search_ms": 1024,
      "llm_ms": 119001,
      "tokens_in": 4103,
      "tokens_out": 508,
      "tokens_total": 4611,
      "estimated_cost_usd": 0.000920,
      "cost_known": true,
      "iterations": 7,
      "recursive_depth": 2,
      "facts_used": 48,
      "memories_used": 20,
      "status": "ok|error",
      "step_outcomes": [
        { "name": "search", "latency_ms": 1024, "status": "ok|no-data|error" },
        { "name": "reason", "latency_ms": 119001, "status": "ok|no-data|error" },
        { "name": "recursive-loop", "count": 7, "status": "ok|no-data|error" }
      ]
    }
  ]
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
    "vault_dir": "/abs/path/to/obsidian-vault",
    "index_path": "/abs/path/to/obsidian-vault/index.md",
    "obsidian_index_uri": "obsidian://open?path=/abs/path/to/obsidian-vault/index.md",
    "nodes": [
      {
        "id": "fact_123",
        "title": "canary regression threshold exceeded",
        "type": "fact",
        "confidence": 0.91,
        "timestamp": "2026-02-20T15:50:00Z",
        "source_ref": "docs/ops-db-growth-guardrails.md",
        "links": ["mem_88", "out_12"],
        "note_file": "canary-regression-threshold-exceeded.md",
        "note_path": "/abs/path/to/obsidian-vault/canary-regression-threshold-exceeded.md",
        "obsidian_uri": "obsidian://open?path=/abs/path/to/obsidian-vault/canary-regression-threshold-exceeded.md"
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

## Current Producer Path (prototype)
- Exporter: `scripts/visualizer_export.py`
- API shim: `scripts/visualizer_api.py`
  - `/api/v1/canonical` → canonical read-model
  - `/api/v1/obsidian` → derived Obsidian adapter
  - `/api/v1/subgraph` → bounded neighborhood extraction from canonical graph
  - `/api/v1/reason-runs` → filterable run timeline + step outcomes

## Next Steps
1. Migrate producer path from script shim to first-class Cortex read-model endpoint/command
2. Add richer evidence links and source line anchors for graph nodes/edges
3. Bind #101-#104 UI modules to these contracts
4. Keep Obsidian adapter derived-only (never as source-of-truth)
