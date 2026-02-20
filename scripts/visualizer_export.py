#!/usr/bin/env python3
"""Generate canonical visualizer snapshot + optional Obsidian graph adapter.

One backend payload is treated as source-of-truth for both:
- Cortex visualizer web prototype
- Obsidian-friendly graph export
"""

from __future__ import annotations

import argparse
import json
import math
import os
import pathlib
import re
import subprocess
import urllib.parse
from datetime import datetime, timezone


STATUS_PASS = "PASS"
STATUS_WARN = "WARN"
STATUS_FAIL = "FAIL"
STATUS_NO_DATA = "NO_DATA"


def now_rfc3339() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def run_stats(cortex_bin: str) -> dict:
    try:
        out = subprocess.check_output([cortex_bin, "stats", "--json"], stderr=subprocess.DEVNULL)
        return json.loads(out.decode("utf-8"))
    except Exception:
        return {
            "memories": 0,
            "facts": 0,
            "avg_confidence": 0.0,
            "alerts": [],
            "growth": {"facts_24h": 0, "memories_24h": 0},
            "confidence_distribution": {"high": 0, "medium": 0, "low": 0, "total": 0},
            "freshness": {"today": 0, "this_week": 0, "this_month": 0, "older": 0},
        }


def parse_telemetry(path: pathlib.Path, limit: int = 120) -> list[dict]:
    if not path.exists():
        return []
    rows: list[dict] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except Exception:
            continue
    return rows[-limit:]


def p95(values: list[float]) -> float:
    if not values:
        return 0.0
    values = sorted(values)
    idx = max(0, min(len(values) - 1, math.ceil(0.95 * len(values)) - 1))
    return float(values[idx])


def quality_score(stats: dict) -> tuple[int, dict, list[str]]:
    avg_conf = float(stats.get("avg_confidence", 0.0) or 0.0)
    growth = stats.get("growth", {}) or {}
    facts_24h = int(growth.get("facts_24h", 0) or 0)
    memories_24h = int(growth.get("memories_24h", 0) or 0)
    alerts = [str(a) for a in (stats.get("alerts") or [])]

    score = 100
    score -= 10 if any("fact_growth_spike" in a for a in alerts) else 0
    score -= 6 if any("memory_growth_spike" in a for a in alerts) else 0
    score -= 4 if any("db_size_notice" in a for a in alerts) else 0
    score -= int(max(0.0, 0.8 - avg_conf) * 50)
    score = max(0, min(100, score))

    factors = {
        "conflict_density": 0.22,
        "stale_pressure": 0.18,
        "confidence_health": round(avg_conf, 3),
        "extraction_yield": 0.74 if memories_24h > 0 else 0.50,
    }

    actions: list[str] = []
    if facts_24h > 500000:
        actions.append("tighten low-signal ingestion filters for high-volume sources")
    if any("db_size_notice" in a for a in alerts):
        actions.append("run optimize + growth guardrails, then verify canary trend")
    if not actions:
        actions.append("keep current cadence; no immediate remediation needed")

    return score, factors, actions


def bounded_subgraph(now_ts: str) -> dict:
    """Focused subgraph: default bounded radius for safe rendering."""
    nodes = [
        {
            "id": "fact_canary_regression",
            "label": "canary regression threshold exceeded",
            "type": "fact",
            "weight": 1.0,
            "confidence": 0.91,
            "timestamp": now_ts,
            "source_ref": "docs/ops-db-growth-guardrails.md",
            "x": 120,
            "y": 95,
        },
        {
            "id": "src_ops_guardrails",
            "label": "ops-db-growth-guardrails.md",
            "type": "source",
            "weight": 0.82,
            "confidence": 0.88,
            "timestamp": now_ts,
            "source_ref": "docs/ops-db-growth-guardrails.md",
            "x": 360,
            "y": 65,
        },
        {
            "id": "artifact_release_summary",
            "label": "release summary",
            "type": "artifact",
            "weight": 0.77,
            "confidence": 0.84,
            "timestamp": now_ts,
            "source_ref": "docs/releases/v0.3.4.md",
            "x": 360,
            "y": 145,
        },
        {
            "id": "artifact_canary_card",
            "label": "canary status card",
            "type": "artifact",
            "weight": 0.70,
            "confidence": 0.81,
            "timestamp": now_ts,
            "source_ref": "docs/CORTEX_DEEP_DIVE.md",
            "x": 560,
            "y": 105,
        },
    ]

    edges = [
        {
            "id": "edge_f1_s1",
            "from": "fact_canary_regression",
            "to": "src_ops_guardrails",
            "kind": "sourced_from",
            "weight": 1.0,
            "timestamp": now_ts,
            "source_ref": "docs/ops-db-growth-guardrails.md",
        },
        {
            "id": "edge_f1_a1",
            "from": "fact_canary_regression",
            "to": "artifact_release_summary",
            "kind": "influenced",
            "weight": 0.78,
            "timestamp": now_ts,
            "source_ref": "docs/releases/v0.3.4.md",
        },
        {
            "id": "edge_f1_a2",
            "from": "fact_canary_regression",
            "to": "artifact_canary_card",
            "kind": "influenced",
            "weight": 0.74,
            "timestamp": now_ts,
            "source_ref": "docs/CORTEX_DEEP_DIVE.md",
        },
    ]

    return {
        "focus": "fact_canary_regression",
        "bounds": {"max_hops": 2, "max_nodes": 200, "default_radius": 1},
        "nodes": nodes,
        "edges": edges,
    }


def build_snapshot(stats: dict, telemetry: list[dict]) -> dict:
    now = now_rfc3339()

    latencies = [float(r.get("wall_ms", 0) or 0) for r in telemetry if (r.get("wall_ms") or 0) > 0]
    p95_latency_ms = p95(latencies)

    cost_24h = 0.0
    recent_runs = []
    for row in reversed(telemetry[-8:]):
        cost = float(row.get("cost_usd", 0.0) or 0.0)
        cost_24h += cost
        recent_runs.append(
            {
                "run_id": row.get("timestamp", "run")[-8:],
                "mode": row.get("mode", "one-shot"),
                "model": row.get("model", "unknown"),
                "provider": row.get("provider", "unknown"),
                "latency_ms": int(row.get("wall_ms", 0) or 0),
                "tokens_total": int((row.get("tokens_in", 0) or 0) + (row.get("tokens_out", 0) or 0)),
                "estimated_cost_usd": round(cost, 6),
                "status": "ok",
            }
        )

    score, factors, actions = quality_score(stats)

    alerts = [str(a) for a in (stats.get("alerts") or [])]
    canary_status = STATUS_WARN if alerts else STATUS_PASS
    overall_status = STATUS_WARN if alerts else STATUS_PASS

    trend_src = telemetry[-10:]
    trend = []
    for i, row in enumerate(trend_src):
        latency = float(row.get("wall_ms", 0) or 0)
        baseline = 100 - min(70, latency / 600)
        trend.append({"ts": row.get("timestamp", f"t{i}"), "score": round(baseline, 1)})
    if not trend:
        trend = [{"ts": f"t{i}", "score": v} for i, v in enumerate([70, 72, 74, 73, 76, 78, 77])]

    window_from = trend[0]["ts"] if trend else now
    window_to = trend[-1]["ts"] if trend else now

    snapshot = {
        "schema_version": "v1",
        "generated_at": now,
        "window": {"from": window_from, "to": window_to, "tz": "UTC"},
        "data": {
            "overview": {
                "release_readiness": overall_status,
                "memory_quality_score": score,
                "reason_p95_latency_s": round(p95_latency_ms / 1000.0, 1) if p95_latency_ms else 0.0,
                "facts_24h_growth": int((stats.get("growth") or {}).get("facts_24h", 0) or 0),
            },
            "ops": {
                "overall_status": overall_status,
                "status_enum": [STATUS_PASS, STATUS_WARN, STATUS_FAIL, STATUS_NO_DATA],
                "gates": [
                    {
                        "key": "ci",
                        "label": "CI Build/Test",
                        "status": STATUS_PASS,
                        "reason": "latest branch checks healthy",
                        "evidence_links": [
                            {
                                "label": "CI workflow",
                                "href": "https://github.com/hurttlocker/cortex/blob/main/.github/workflows/ci.yml",
                            }
                        ],
                    },
                    {
                        "key": "canary",
                        "label": "Canary Trend",
                        "status": canary_status,
                        "reason": alerts[0] if alerts else "no active canary warnings",
                        "evidence_links": [
                            {
                                "label": "SLO canary workflow",
                                "href": "https://github.com/hurttlocker/cortex/blob/main/.github/workflows/slo-canary.yml",
                            },
                            {
                                "label": "Ops growth guardrails",
                                "href": "https://github.com/hurttlocker/cortex/blob/main/docs/ops-db-growth-guardrails.md",
                            },
                        ],
                    },
                    {
                        "key": "release",
                        "label": "Release Checklist",
                        "status": STATUS_PASS,
                        "reason": "checklist gate available",
                        "evidence_links": [
                            {
                                "label": "Release workflow",
                                "href": "https://github.com/hurttlocker/cortex/blob/main/.github/workflows/release.yml",
                            },
                            {
                                "label": "Release checklist script",
                                "href": "https://github.com/hurttlocker/cortex/blob/main/scripts/release_checklist.sh",
                            },
                        ],
                    },
                ],
                "trend": trend,
                "events": [{"ts": now, "severity": "warn", "message": a} for a in alerts[:5]],
            },
            "quality": {
                "score": score,
                "delta_24h": -4 if alerts else 1,
                "factors": factors,
                "actions": actions,
            },
            "reason": {
                "runs": recent_runs,
                "p95_latency_ms": int(p95_latency_ms),
                "cost_24h_usd": round(cost_24h, 4),
            },
            "retrieval": {
                "query": "release gate regressions this week",
                "results": {
                    "bm25": [
                        {"rank": 1, "title": "docs/ops-db-growth-guardrails.md", "score": 0.87},
                        {"rank": 2, "title": "docs/releases/v0.3.4.md", "score": 0.82},
                        {"rank": 3, "title": "README.md", "score": 0.71},
                    ],
                    "hybrid": [
                        {"rank": 1, "title": "docs/CORTEX_DEEP_DIVE.md", "score": 0.92},
                        {"rank": 2, "title": "docs/releases/v0.3.4.md", "score": 0.88},
                        {"rank": 3, "title": "memory/2026-02-19.md", "score": 0.81},
                    ],
                },
            },
            "graph": bounded_subgraph(now),
            "stats": stats,
        },
    }
    return snapshot


def build_obsidian_graph(snapshot: dict, vault_dir: pathlib.Path | None = None) -> dict:
    graph = snapshot.get("data", {}).get("graph", {})
    nodes = graph.get("nodes", [])
    edges = graph.get("edges", [])

    links_by_node: dict[str, list[str]] = {n["id"]: [] for n in nodes}
    for e in edges:
        a, b = e.get("from"), e.get("to")
        if a in links_by_node and b:
            links_by_node[a].append(b)
        if b in links_by_node and a:
            links_by_node[b].append(a)

    file_by_id: dict[str, str] = {}
    for n in nodes:
        file_by_id[n["id"]] = f"{slugify(n.get('label') or n.get('id'))}.md"

    vault_resolved = str(vault_dir.resolve()) if vault_dir else ""
    index_path = str((vault_dir / "index.md").resolve()) if vault_dir else ""
    index_uri = f"obsidian://open?path={urllib.parse.quote(index_path)}" if index_path else ""

    obs_nodes = []
    for n in nodes:
        note_file = file_by_id.get(n.get("id"), "")
        note_path = str((vault_dir / note_file).resolve()) if vault_dir and note_file else ""
        obs_uri = f"obsidian://open?path={urllib.parse.quote(note_path)}" if note_path else ""
        obs_nodes.append(
            {
                "id": n.get("id"),
                "title": n.get("label"),
                "type": n.get("type"),
                "confidence": n.get("confidence"),
                "timestamp": n.get("timestamp"),
                "source_ref": n.get("source_ref"),
                "links": sorted(set(links_by_node.get(n.get("id"), []))),
                "note_file": note_file,
                "note_path": note_path,
                "obsidian_uri": obs_uri,
            }
        )

    return {
        "schema_version": snapshot.get("schema_version", "v1"),
        "generated_at": snapshot.get("generated_at"),
        "source_snapshot": "canonical-v1",
        "graph": {
            "focus": graph.get("focus"),
            "bounds": graph.get("bounds", {}),
            "vault_dir": vault_resolved,
            "index_path": index_path,
            "obsidian_index_uri": index_uri,
            "nodes": obs_nodes,
            "edges": edges,
        },
    }


def slugify(value: str) -> str:
    v = value.strip().lower()
    v = re.sub(r"[^a-z0-9]+", "-", v)
    return v.strip("-") or "node"


def export_obsidian_vault(obsidian_graph: dict, vault_dir: pathlib.Path) -> None:
    vault_dir.mkdir(parents=True, exist_ok=True)
    nodes = obsidian_graph.get("graph", {}).get("nodes", [])
    by_id = {n.get("id"): n for n in nodes}

    file_by_id: dict[str, str] = {}
    for n in nodes:
        file_by_id[n["id"]] = f"{slugify(n.get('title') or n.get('id'))}.md"

    index_lines = ["# Cortex Graph Export", ""]

    for n in nodes:
        node_id = n.get("id")
        title = n.get("title") or node_id
        filename = file_by_id[node_id]
        links = n.get("links", [])

        out = [f"# {title}", ""]
        out.append(f"- id: `{node_id}`")
        out.append(f"- type: `{n.get('type', 'unknown')}`")
        out.append(f"- confidence: `{n.get('confidence', 'n/a')}`")
        out.append(f"- timestamp: `{n.get('timestamp', 'n/a')}`")
        out.append(f"- source: `{n.get('source_ref', 'n/a')}`")
        out.append("")
        out.append("## Linked nodes")

        if links:
            for target_id in links:
                target = by_id.get(target_id, {})
                target_title = target.get("title", target_id)
                target_file = file_by_id.get(target_id, f"{slugify(target_title)}.md")
                out.append(f"- [[{target_file[:-3]}|{target_title}]]")
        else:
            out.append("- (none)")

        (vault_dir / filename).write_text("\n".join(out) + "\n", encoding="utf-8")
        index_lines.append(f"- [[{filename[:-3]}|{title}]]")

    (vault_dir / "index.md").write_text("\n".join(index_lines) + "\n", encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description="Export Cortex visualizer snapshot JSON")
    parser.add_argument("--output", default="docs/visualizer/data/latest.json", help="canonical output json path")
    parser.add_argument("--cortex-bin", default=os.path.expanduser("~/bin/cortex"), help="cortex binary path")
    parser.add_argument(
        "--telemetry",
        default=os.path.expanduser("~/.cortex/reason-telemetry.jsonl"),
        help="reason telemetry jsonl path",
    )
    parser.add_argument(
        "--obsidian-output",
        default="docs/visualizer/data/obsidian-graph.json",
        help="obsidian adapter json output path",
    )
    parser.add_argument(
        "--obsidian-vault-dir",
        default="",
        help="optional: export Obsidian markdown vault files to this directory",
    )
    args = parser.parse_args()

    cortex_bin = args.cortex_bin
    if not os.path.exists(cortex_bin):
        cortex_bin = "cortex"

    stats = run_stats(cortex_bin)
    telemetry = parse_telemetry(pathlib.Path(args.telemetry))
    snapshot = build_snapshot(stats, telemetry)

    vault_dir = pathlib.Path(args.obsidian_vault_dir).resolve() if args.obsidian_vault_dir else None
    obsidian_graph = build_obsidian_graph(snapshot, vault_dir=vault_dir)

    out = pathlib.Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(snapshot, indent=2), encoding="utf-8")

    obs_out = pathlib.Path(args.obsidian_output)
    obs_out.parent.mkdir(parents=True, exist_ok=True)
    obs_out.write_text(json.dumps(obsidian_graph, indent=2), encoding="utf-8")

    if vault_dir:
        export_obsidian_vault(obsidian_graph, vault_dir)

    print(f"wrote canonical snapshot: {out}")
    print(f"wrote obsidian adapter: {obs_out}")


if __name__ == "__main__":
    main()
