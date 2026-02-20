#!/usr/bin/env python3
"""Generate a local JSON snapshot for the Cortex visualizer prototype."""

from __future__ import annotations

import argparse
import json
import math
import os
import pathlib
import statistics
import subprocess
from datetime import datetime, timezone


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


def parse_telemetry(path: pathlib.Path, limit: int = 80) -> list[dict]:
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


def build_snapshot(stats: dict, telemetry: list[dict]) -> dict:
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

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
                "latency_ms": int(row.get("wall_ms", 0) or 0),
                "tokens_total": int((row.get("tokens_in", 0) or 0) + (row.get("tokens_out", 0) or 0)),
                "estimated_cost_usd": round(cost, 6),
                "status": "ok",
            }
        )

    score, factors, actions = quality_score(stats)

    alerts = [str(a) for a in (stats.get("alerts") or [])]
    canary_status = "WARN" if alerts else "PASS"
    overall_status = "WARN" if alerts else "PASS"

    trend_src = telemetry[-10:]
    trend = []
    for i, row in enumerate(trend_src):
        latency = float(row.get("wall_ms", 0) or 0)
        baseline = 100 - min(70, latency / 600)
        trend.append({"ts": row.get("timestamp", f"t{i}"), "score": round(baseline, 1)})
    if not trend:
        trend = [{"ts": f"t{i}", "score": v} for i, v in enumerate([70, 72, 74, 73, 76, 78, 77])]

    snapshot = {
        "schema_version": "v1",
        "generated_at": now,
        "data": {
            "overview": {
                "release_readiness": overall_status,
                "memory_quality_score": score,
                "reason_p95_latency_s": round(p95_latency_ms / 1000.0, 1) if p95_latency_ms else 0.0,
                "facts_24h_growth": int((stats.get("growth") or {}).get("facts_24h", 0) or 0),
            },
            "ops": {
                "overall_status": overall_status,
                "gates": [
                    {"key": "ci", "label": "CI Build/Test", "status": "PASS", "reason": "latest branch checks healthy"},
                    {
                        "key": "canary",
                        "label": "Canary Trend",
                        "status": canary_status,
                        "reason": alerts[0] if alerts else "no active canary warnings",
                    },
                    {"key": "release", "label": "Release Checklist", "status": "PASS", "reason": "checklist gate available"},
                ],
                "trend": trend,
                "events": [
                    {"ts": now, "severity": "warn", "message": a} for a in alerts[:5]
                ],
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
            "graph": {
                "focus": "fact_1",
                "nodes": [
                    {"id": "fact_1", "label": "canary regression threshold exceeded", "type": "fact", "x": 120, "y": 95},
                    {"id": "src_1", "label": "ops-db-growth-guardrails.md", "type": "source", "x": 360, "y": 65},
                    {"id": "out_1", "label": "release summary", "type": "artifact", "x": 360, "y": 145},
                    {"id": "out_2", "label": "canary status card", "type": "artifact", "x": 560, "y": 105},
                ],
                "edges": [
                    {"from": "fact_1", "to": "src_1", "kind": "sourced_from"},
                    {"from": "fact_1", "to": "out_1", "kind": "influenced"},
                    {"from": "fact_1", "to": "out_2", "kind": "influenced"},
                ],
            },
            "stats": stats,
        },
    }
    return snapshot


def main() -> None:
    parser = argparse.ArgumentParser(description="Export Cortex visualizer snapshot JSON")
    parser.add_argument("--output", default="docs/visualizer/data/latest.json", help="output json path")
    parser.add_argument("--cortex-bin", default=os.path.expanduser("~/bin/cortex"), help="cortex binary path")
    parser.add_argument(
        "--telemetry",
        default=os.path.expanduser("~/.cortex/reason-telemetry.jsonl"),
        help="reason telemetry jsonl path",
    )
    args = parser.parse_args()

    cortex_bin = args.cortex_bin
    if not os.path.exists(cortex_bin):
        cortex_bin = "cortex"

    stats = run_stats(cortex_bin)
    telemetry = parse_telemetry(pathlib.Path(args.telemetry))
    payload = build_snapshot(stats, telemetry)

    out = pathlib.Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(payload, indent=2), encoding="utf-8")
    print(f"wrote {out}")


if __name__ == "__main__":
    main()
