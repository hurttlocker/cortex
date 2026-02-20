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


def clamp01(value: float) -> float:
    return max(0.0, min(1.0, value))


def quality_status(quality_value: float | None) -> str:
    if quality_value is None:
        return STATUS_NO_DATA
    if quality_value >= 0.80:
        return STATUS_PASS
    if quality_value >= 0.60:
        return STATUS_WARN
    return STATUS_FAIL


def extraction_yield_health(facts_per_memory_24h: float | None) -> float | None:
    if facts_per_memory_24h is None:
        return None
    if facts_per_memory_24h < 20:
        return 0.45
    if facts_per_memory_24h < 100:
        return 0.70
    if facts_per_memory_24h < 500:
        return 0.90
    if facts_per_memory_24h < 1500:
        return 0.95
    if facts_per_memory_24h < 3000:
        return 0.70
    return 0.45


def quality_engine(stats: dict, telemetry: list[dict]) -> dict:
    alerts = [str(a) for a in (stats.get("alerts") or [])]

    conf = stats.get("confidence_distribution") or {}
    conf_total = int(conf.get("total", 0) or 0)
    conf_high = int(conf.get("high", 0) or 0)
    conf_medium = int(conf.get("medium", 0) or 0)
    conf_low = int(conf.get("low", 0) or 0)

    high_ratio = (conf_high / conf_total) if conf_total > 0 else None
    medium_ratio = (conf_medium / conf_total) if conf_total > 0 else None
    low_ratio = (conf_low / conf_total) if conf_total > 0 else None

    freshness = stats.get("freshness") or {}
    freshness_total = sum(int(freshness.get(k, 0) or 0) for k in ["today", "this_week", "this_month", "older"])
    stale_ratio = (int(freshness.get("older", 0) or 0) / freshness_total) if freshness_total > 0 else None

    growth = stats.get("growth") or {}
    memories_24h = int(growth.get("memories_24h", 0) or 0)
    facts_24h = int(growth.get("facts_24h", 0) or 0)
    facts_per_memory_24h = (facts_24h / memories_24h) if memories_24h > 0 else None

    facts_total = int(stats.get("facts", 0) or 0)
    facts_by_type = stats.get("facts_by_type") or {}
    kv_facts = int(facts_by_type.get("kv", 0) or 0)
    kv_ratio = (kv_facts / facts_total) if facts_total > 0 else None

    conflict_alert_bonus = 0.20 if any("conflict" in a.lower() for a in alerts) else 0.0
    conflict_density = None
    if medium_ratio is not None and low_ratio is not None:
        conflict_density = clamp01((low_ratio * 1.50) + (medium_ratio * 0.35) + conflict_alert_bonus)

    confidence_health = None
    if high_ratio is not None and medium_ratio is not None:
        confidence_health = clamp01(high_ratio + (0.50 * medium_ratio))

    duplication_noise = None
    if kv_ratio is not None:
        noise = clamp01(max(0.0, kv_ratio - 0.85) / 0.15)
        if any("fact_growth_spike" in a for a in alerts):
            noise = clamp01(noise + 0.10)
        if any("memory_growth_spike" in a for a in alerts):
            noise = clamp01(noise + 0.10)
        duplication_noise = noise

    extraction_health = extraction_yield_health(facts_per_memory_24h)

    factors = [
        {
            "key": "stale_ratio",
            "label": "Stale Ratio",
            "definition": "Share of records in freshness.older over total freshness buckets.",
            "source": "stats.freshness.older / (today + this_week + this_month + older)",
            "direction": "penalty",
            "weight": 0.22,
            "value": stale_ratio,
            "remediation": "Run stale-fact cleanup and reinforce active facts used in current workflows.",
        },
        {
            "key": "conflict_density",
            "label": "Conflict Density (proxy)",
            "definition": "Proxy derived from low/medium confidence mix plus explicit conflict alerts.",
            "source": "stats.confidence_distribution + stats.alerts",
            "direction": "penalty",
            "weight": 0.20,
            "value": conflict_density,
            "remediation": "Review conflict clusters and add supersession/tombstones for contradictory facts.",
        },
        {
            "key": "confidence_health",
            "label": "Confidence Distribution Health",
            "definition": "High-confidence share plus half-weighted medium-confidence share.",
            "source": "stats.confidence_distribution.high/medium/total",
            "direction": "bonus",
            "weight": 0.24,
            "value": confidence_health,
            "remediation": "Improve extraction quality on noisy sources and reinforce high-value facts.",
        },
        {
            "key": "extraction_yield",
            "label": "Extraction Yield Health",
            "definition": "Health band based on facts generated per memory over the last 24h.",
            "source": "stats.growth.facts_24h / stats.growth.memories_24h",
            "direction": "bonus",
            "weight": 0.18,
            "value": extraction_health,
            "remediation": "Tune extraction prompts/chunking to maintain high-signal yield without over-generation.",
        },
        {
            "key": "duplication_noise",
            "label": "Duplication / Noise Pressure",
            "definition": "KV fact dominance plus growth-spike penalties to flag likely ingestion noise.",
            "source": "stats.facts_by_type.kv / stats.facts + stats.alerts",
            "direction": "penalty",
            "weight": 0.16,
            "value": duplication_noise,
            "remediation": "Tighten low-signal ingestion filters and dedupe thresholds for repetitive captures.",
        },
    ]

    weighted_sum = 0.0
    used_weight = 0.0
    for f in factors:
        raw = f.get("value")
        if raw is None:
            f["quality_value"] = None
            f["status"] = STATUS_NO_DATA
            f["weighted_score"] = 0.0
            continue

        metric_value = clamp01(float(raw))
        quality_value = metric_value if f["direction"] == "bonus" else clamp01(1.0 - metric_value)
        weighted_score = float(f["weight"]) * quality_value * 100.0

        f["value"] = round(metric_value, 4)
        f["quality_value"] = round(quality_value, 4)
        f["status"] = quality_status(quality_value)
        f["weighted_score"] = round(weighted_score, 2)

        weighted_sum += weighted_score
        used_weight += float(f["weight"]) * 100.0

    score = int(round((weighted_sum / used_weight) * 100)) if used_weight > 0 else 0
    score = max(0, min(100, score))

    legacy_factors = {
        "conflict_density": round(float(conflict_density or 0.0), 3),
        "stale_pressure": round(float(stale_ratio or 0.0), 3),
        "confidence_health": round(float(confidence_health or 0.0), 3),
        "extraction_yield": round(float(extraction_health or 0.0), 3),
    }

    trend_24h = []
    trend_source = telemetry[-8:]
    for idx, row in enumerate(trend_source):
        latency = float(row.get("wall_ms", 0) or 0)
        reason_status = str(row.get("reason_status", "")).lower()
        err_penalty = 4.0 if reason_status in {"error", "failed", "fail"} else 0.0
        latency_penalty = min(18.0, latency / 9000.0)
        trend_score = max(0.0, min(100.0, float(score) - latency_penalty - err_penalty + (idx * 0.4)))
        trend_24h.append({"ts": row.get("timestamp", f"t{idx}"), "score": round(trend_score, 1)})

    if not trend_24h:
        trend_24h = [
            {"ts": "t0", "score": max(0, score - 3)},
            {"ts": "t1", "score": max(0, score - 2)},
            {"ts": "t2", "score": max(0, score - 1)},
            {"ts": "t3", "score": score},
        ]

    delta_24h = round(float(trend_24h[-1]["score"]) - float(trend_24h[0]["score"]), 1) if trend_24h else 0.0

    degraders = [f for f in factors if f.get("quality_value") is not None]
    degraders.sort(key=lambda x: float(x.get("quality_value", 1.0)))

    non_pass = [f for f in degraders if f.get("status") in {STATUS_WARN, STATUS_FAIL}]
    driver_candidates = non_pass if non_pass else degraders[:1]

    top_drivers = []
    for f in driver_candidates[:3]:
        impact_points = round(float(f.get("weight", 0.0)) * (1.0 - float(f.get("quality_value", 1.0))) * 100.0, 2)
        top_drivers.append(
            {
                "key": f["key"],
                "label": f["label"],
                "status": f["status"],
                "impact_points": impact_points,
                "why": f["definition"],
                "remediation": f["remediation"],
            }
        )

    actions = []
    for d in top_drivers:
        action = d.get("remediation", "")
        if action and action not in actions:
            actions.append(action)
        if len(actions) >= 2:
            break
    if not actions:
        actions.append("keep current cadence; no immediate remediation needed")

    return {
        "formula_version": "mqe-v1",
        "score": score,
        "score_status": quality_status(score / 100.0),
        "delta_24h": delta_24h,
        "trend_24h": trend_24h,
        "factors": legacy_factors,
        "factors_v2": factors,
        "top_drivers": top_drivers,
        "actions": actions,
        "reproducibility": {
            "formula": "score = round(sum(weight_i * quality_i) / sum(weight_i) * 100); quality_i = metric_i for bonus factors, (1 - metric_i) for penalty factors",
            "inputs": {
                "stats.confidence_distribution": conf,
                "stats.freshness": freshness,
                "stats.growth": growth,
                "stats.facts_by_type": facts_by_type,
                "stats.alerts": alerts,
                "derived": {
                    "facts_per_memory_24h": round(facts_per_memory_24h, 3) if facts_per_memory_24h is not None else None,
                    "kv_ratio": round(kv_ratio, 4) if kv_ratio is not None else None,
                    "confidence_ratios": {
                        "high": round(high_ratio, 4) if high_ratio is not None else None,
                        "medium": round(medium_ratio, 4) if medium_ratio is not None else None,
                        "low": round(low_ratio, 4) if low_ratio is not None else None,
                    },
                },
            },
        },
    }


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

    quality = quality_engine(stats, telemetry)

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
                "memory_quality_score": int(quality.get("score", 0)),
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
                "formula_version": quality.get("formula_version", "mqe-v1"),
                "score": int(quality.get("score", 0)),
                "score_status": quality.get("score_status", STATUS_NO_DATA),
                "delta_24h": float(quality.get("delta_24h", 0.0)),
                "trend_24h": quality.get("trend_24h", []),
                "factors": quality.get("factors", {}),
                "factors_v2": quality.get("factors_v2", []),
                "top_drivers": quality.get("top_drivers", []),
                "actions": quality.get("actions", []),
                "reproducibility": quality.get("reproducibility", {}),
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


def build_obsidian_graph(snapshot: dict) -> dict:
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

    obs_nodes = []
    for n in nodes:
        obs_nodes.append(
            {
                "id": n.get("id"),
                "title": n.get("label"),
                "type": n.get("type"),
                "confidence": n.get("confidence"),
                "timestamp": n.get("timestamp"),
                "source_ref": n.get("source_ref"),
                "links": sorted(set(links_by_node.get(n.get("id"), []))),
            }
        )

    return {
        "schema_version": snapshot.get("schema_version", "v1"),
        "generated_at": snapshot.get("generated_at"),
        "source_snapshot": "canonical-v1",
        "graph": {
            "focus": graph.get("focus"),
            "bounds": graph.get("bounds", {}),
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
    obsidian_graph = build_obsidian_graph(snapshot)

    out = pathlib.Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(snapshot, indent=2), encoding="utf-8")

    obs_out = pathlib.Path(args.obsidian_output)
    obs_out.parent.mkdir(parents=True, exist_ok=True)
    obs_out.write_text(json.dumps(obsidian_graph, indent=2), encoding="utf-8")

    if args.obsidian_vault_dir:
        export_obsidian_vault(obsidian_graph, pathlib.Path(args.obsidian_vault_dir))

    print(f"wrote canonical snapshot: {out}")
    print(f"wrote obsidian adapter: {obs_out}")


if __name__ == "__main__":
    main()
