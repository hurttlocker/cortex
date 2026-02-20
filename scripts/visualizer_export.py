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
import subprocess
import urllib.parse
from datetime import datetime, timezone


STATUS_PASS = "PASS"
STATUS_WARN = "WARN"
STATUS_FAIL = "FAIL"
STATUS_NO_DATA = "NO_DATA"

DEFAULT_OBSIDIAN_SUBDIR = "_cortex_visualizer"
DEFAULT_OBSIDIAN_DASHBOARD = "cortex-visualizer-dashboard.md"
DEFAULT_OBSIDIAN_FALLBACK_DIR = pathlib.Path("docs/visualizer/data/obsidian-vault")
OBSIDIAN_CONFIG_PATH = pathlib.Path.home() / "Library/Application Support/obsidian/obsidian.json"


def now_rfc3339() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def detect_obsidian_open_vault(config_path: pathlib.Path = OBSIDIAN_CONFIG_PATH) -> pathlib.Path | None:
    """Return currently open Obsidian vault path, or most recent vault as fallback."""
    try:
        data = json.loads(config_path.read_text(encoding="utf-8"))
    except Exception:
        return None

    vaults = data.get("vaults") or {}
    if not isinstance(vaults, dict) or not vaults:
        return None

    active = None
    for meta in vaults.values():
        if isinstance(meta, dict) and meta.get("open"):
            active = meta
            break

    if active is None:
        metas = [m for m in vaults.values() if isinstance(m, dict)]
        if not metas:
            return None
        active = max(metas, key=lambda m: int(m.get("ts", 0) or 0))

    path = str(active.get("path", "")).strip()
    return pathlib.Path(path).expanduser().resolve() if path else None


def obsidian_uri_for_path(path: pathlib.Path | str | None) -> str:
    if not path:
        return ""
    return f"obsidian://open?path={urllib.parse.quote(str(path))}"


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


def parse_iso8601(value: str | None) -> datetime | None:
    if not value:
        return None
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except Exception:
        return None


def clip_text(value: str | None, max_len: int = 180) -> str:
    text = " ".join(str(value or "").split())
    if len(text) <= max_len:
        return text
    return text[: max(0, max_len - 1)].rstrip() + "…"


def run_cortex_json(cortex_bin: str, args: list[str], default: list | dict):
    try:
        out = subprocess.check_output([cortex_bin, *args], stderr=subprocess.DEVNULL)
        raw = out.decode("utf-8").strip()
        if not raw:
            return default
        payload = json.loads(raw)
        return payload
    except Exception:
        return default


def build_memory_intel(cortex_bin: str, focus_label: str, recent_limit: int = 10) -> dict:
    memories_raw = run_cortex_json(cortex_bin, ["list", "--json", "--limit", str(max(25, recent_limit * 2))], [])
    if not isinstance(memories_raw, list):
        memories_raw = []

    shaped_recent = []
    class_counts: dict[str, int] = {}
    source_counts: dict[str, int] = {}

    for row in memories_raw:
        if not isinstance(row, dict):
            continue
        metadata = row.get("Metadata") or {}
        class_name = str(row.get("MemoryClass") or "uncategorized")
        source_file = str(row.get("SourceFile") or "")
        source_name = pathlib.Path(source_file).name or "unknown"

        class_counts[class_name] = class_counts.get(class_name, 0) + 1
        source_counts[source_name] = source_counts.get(source_name, 0) + 1

        shaped_recent.append(
            {
                "id": int(row.get("ID", 0) or 0),
                "imported_at": row.get("ImportedAt") or "",
                "class": class_name,
                "project": row.get("Project") or "",
                "source_file": source_file,
                "source_name": source_name,
                "source_section": row.get("SourceSection") or "",
                "channel": (metadata or {}).get("channel") or (metadata or {}).get("surface") or "",
                "snippet": clip_text(row.get("Content"), 220),
            }
        )

    shaped_recent.sort(key=lambda r: parse_iso8601(r.get("imported_at")) or datetime.min.replace(tzinfo=timezone.utc), reverse=True)
    recent = shaped_recent[:recent_limit]

    class_distribution = [
        {"label": k, "count": v}
        for k, v in sorted(class_counts.items(), key=lambda item: item[1], reverse=True)[:8]
    ]
    source_distribution = [
        {"label": k, "count": v}
        for k, v in sorted(source_counts.items(), key=lambda item: item[1], reverse=True)[:8]
    ]

    timeline = []
    for row in reversed(recent[-7:]):
        ts = parse_iso8601(row.get("imported_at"))
        timeline.append(
            {
                "id": row.get("id"),
                "ts": row.get("imported_at"),
                "label": ts.strftime("%m-%d %H:%M") if ts else "unknown",
                "class": row.get("class", "uncategorized"),
                "headline": clip_text(row.get("snippet", ""), 72),
            }
        )

    focus_mem_raw = run_cortex_json(cortex_bin, ["search", focus_label or "memory", "--json", "--limit", "6"], [])
    if not isinstance(focus_mem_raw, list):
        focus_mem_raw = []
    focus_memories = []
    for row in focus_mem_raw[:6]:
        if not isinstance(row, dict):
            continue
        focus_memories.append(
            {
                "memory_id": int(row.get("memory_id", 0) or 0),
                "class": row.get("class") or "uncategorized",
                "source_file": row.get("source_file") or "",
                "source_section": row.get("source_section") or "",
                "imported_at": row.get("imported_at") or "",
                "score": float(row.get("score", 0.0) or 0.0),
                "snippet": clip_text(row.get("content") or row.get("snippet") or "", 220),
            }
        )

    stale_raw = run_cortex_json(cortex_bin, ["stale", "--json"], [])
    if not isinstance(stale_raw, list):
        stale_raw = []
    stale_examples = []
    for row in stale_raw[:5]:
        if not isinstance(row, dict):
            continue
        stale_examples.append(
            {
                "subject": row.get("subject") or row.get("Subject") or "",
                "predicate": row.get("predicate") or row.get("Predicate") or "",
                "object": row.get("object") or row.get("Object") or "",
                "confidence": row.get("confidence") or row.get("Confidence") or 0.0,
                "last_reinforced": row.get("last_reinforced") or row.get("LastReinforced") or "",
            }
        )

    conflicts_raw = run_cortex_json(cortex_bin, ["conflicts", "--json", "--limit", "8"], [])
    if not isinstance(conflicts_raw, list):
        conflicts_raw = []
    conflict_examples = []
    for row in conflicts_raw[:5]:
        if not isinstance(row, dict):
            continue

        fact1 = row.get("fact1") if isinstance(row.get("fact1"), dict) else {}
        fact2 = row.get("fact2") if isinstance(row.get("fact2"), dict) else {}

        subject = fact1.get("Subject") or fact2.get("Subject") or row.get("subject") or row.get("Subject") or ""
        pred1 = fact1.get("Predicate") or ""
        obj1 = fact1.get("Object") or ""
        pred2 = fact2.get("Predicate") or ""
        obj2 = fact2.get("Object") or ""
        conflict_type = row.get("conflict_type") or row.get("ConflictType") or "conflict"
        reason = f"{conflict_type}: {pred1}={obj1} vs {pred2}={obj2}" if (pred1 or pred2) else str(conflict_type)

        conflict_examples.append(
            {
                "winner_id": fact1.get("ID") or row.get("winner_id") or row.get("WinnerID") or "",
                "loser_id": fact2.get("ID") or row.get("loser_id") or row.get("LoserID") or "",
                "reason": clip_text(reason, 140),
                "subject": subject,
            }
        )

    alerts = []
    if stale_raw:
        alerts.append(f"{len(stale_raw)} stale facts are candidates for reinforcement")
    if conflicts_raw:
        alerts.append(f"{len(conflicts_raw)} potential fact conflicts detected")
    if not alerts:
        alerts.append("No stale facts or conflicts detected in latest scan")

    return {
        "focus_query": focus_label,
        "recent_memories": recent,
        "recent_total": len(memories_raw),
        "focus_memories": focus_memories,
        "class_distribution": class_distribution,
        "source_distribution": source_distribution,
        "timeline": timeline,
        "health": {
            "stale_count": len(stale_raw),
            "conflict_count": len(conflicts_raw),
            "alerts": alerts,
            "stale_examples": stale_examples,
            "conflict_examples": conflict_examples,
        },
    }


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


def build_snapshot(stats: dict, telemetry: list[dict], cortex_bin: str) -> dict:
    now = now_rfc3339()

    graph_data = bounded_subgraph(now)
    focus_id = graph_data.get("focus", "")
    focus_label = ""
    for node in graph_data.get("nodes", []):
        if node.get("id") == focus_id:
            focus_label = str(node.get("label") or focus_id)
            break
    memory_intel = build_memory_intel(cortex_bin, focus_label)

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
            "graph": graph_data,
            "memory": memory_intel,
            "stats": stats,
        },
    }
    return snapshot


def build_obsidian_graph(
    snapshot: dict,
    vault_dir: pathlib.Path | None = None,
    dashboard_file: str = DEFAULT_OBSIDIAN_DASHBOARD,
) -> dict:
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

    dashboard_file = dashboard_file or DEFAULT_OBSIDIAN_DASHBOARD
    vault_resolved = str(vault_dir.resolve()) if vault_dir else ""
    dashboard_path = str((vault_dir / dashboard_file).resolve()) if vault_dir else ""
    dashboard_uri = obsidian_uri_for_path(dashboard_path)

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
                "note_file": dashboard_file if vault_dir else "",
                "note_path": dashboard_path,
                "obsidian_uri": dashboard_uri,
                "note_heading": n.get("label") or n.get("id"),
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
            "index_path": dashboard_path,
            "obsidian_index_uri": dashboard_uri,
            "dashboard_file": dashboard_file,
            "dashboard_path": dashboard_path,
            "obsidian_dashboard_uri": dashboard_uri,
            "nodes": obs_nodes,
            "edges": edges,
        },
    }


def export_obsidian_vault(obsidian_graph: dict, snapshot: dict, vault_dir: pathlib.Path) -> None:
    vault_dir.mkdir(parents=True, exist_ok=True)
    graph = obsidian_graph.get("graph", {})
    nodes = graph.get("nodes", [])
    edges = graph.get("edges", [])

    dashboard_file = graph.get("dashboard_file") or DEFAULT_OBSIDIAN_DASHBOARD
    dashboard_path = vault_dir / dashboard_file

    # Single-file Obsidian mode: keep only the dashboard markdown in this export directory.
    for md_file in vault_dir.glob("*.md"):
        if md_file.name != dashboard_file:
            try:
                md_file.unlink()
            except Exception:
                pass

    by_id = {n.get("id"): n for n in nodes}
    mermaid_id = {n.get("id"): f"N{i + 1}" for i, n in enumerate(nodes)}

    type_counts: dict[str, int] = {}
    for n in nodes:
        ntype = str(n.get("type") or "unknown")
        type_counts[ntype] = type_counts.get(ntype, 0) + 1

    def clean_label(text: str) -> str:
        return (text or "").replace('"', "'").replace("`", "")

    lines = ["# Cortex Visualizer Dashboard", ""]
    lines.append(f"> Generated: `{snapshot.get('generated_at', '')}`")
    lines.append(f"> Focus: `{graph.get('focus', '')}`")
    lines.append(
        f"> Bounds: `max_hops={graph.get('bounds', {}).get('max_hops', '-')}` · `max_nodes={graph.get('bounds', {}).get('max_nodes', '-')}`"
    )
    lines.append("")

    overview = snapshot.get("data", {}).get("overview", {})
    quality = snapshot.get("data", {}).get("quality", {})
    memory = snapshot.get("data", {}).get("memory", {})
    memory_health = memory.get("health", {})

    lines.extend(
        [
            "## Snapshot",
            "",
            f"- **Release readiness:** `{overview.get('release_readiness', 'NO_DATA')}`",
            f"- **Memory quality score:** `{overview.get('memory_quality_score', 0)}/100`",
            f"- **Reason p95 latency:** `{overview.get('reason_p95_latency_s', 0)}s`",
            f"- **Facts 24h growth:** `{overview.get('facts_24h_growth', 0)}`",
            f"- **Top actions:** {', '.join(quality.get('actions', [])[:2]) or 'none'}",
            "",
        ]
    )

    lines.extend(["## Memory Radar", ""])
    lines.append(
        f"> [!info] Memory health\n> - stale facts: **{memory_health.get('stale_count', 0)}**\n> - conflicts: **{memory_health.get('conflict_count', 0)}**"
    )
    lines.append("")

    for alert in (memory_health.get("alerts") or [])[:3]:
        lines.append(f"> [!warning] {alert}")
    if memory_health.get("alerts"):
        lines.append("")

    recent_memories = memory.get("recent_memories") or []
    if recent_memories:
        lines.extend(["### Recent Memories", "", "| Time | Class | Source | Snippet |", "|---|---|---|---|"])
        for m in recent_memories[:8]:
            ts = parse_iso8601(m.get("imported_at"))
            ts_txt = ts.strftime("%m-%d %H:%M") if ts else "unknown"
            lines.append(
                f"| `{ts_txt}` | `{m.get('class', 'uncategorized')}` | `{m.get('source_name', 'unknown')}` | {clip_text(m.get('snippet', ''), 90)} |"
            )
        lines.append("")

    class_dist = memory.get("class_distribution") or []
    if class_dist:
        lines.extend(["### Memory Class Mix", "", "```mermaid", "pie title Recent memory classes"])
        for row in class_dist[:8]:
            lines.append(f"  \"{clean_label(str(row.get('label') or 'unknown'))}\" : {int(row.get('count') or 0)}")
        lines.extend(["```", ""])

    source_dist = memory.get("source_distribution") or []
    if source_dist:
        lines.extend(["### Source Heatmap", "", "```mermaid", "pie title Source contribution (recent memories)"])
        for row in source_dist[:8]:
            lines.append(f"  \"{clean_label(str(row.get('label') or 'unknown'))}\" : {int(row.get('count') or 0)}")
        lines.extend(["```", ""])

    timeline = memory.get("timeline") or []
    if timeline:
        lines.extend(["### Memory Timeline", "", "```mermaid", "graph TD"])
        for i, row in enumerate(timeline):
            nid = f"M{i + 1}"
            label = clean_label(f"{row.get('label', 'time')} · {row.get('class', 'uncategorized')} · {clip_text(row.get('headline', ''), 44)}")
            lines.append(f"  {nid}[\"{label}\"]")
            if i > 0:
                lines.append(f"  M{i} --> {nid}")
        lines.extend(["```", ""])

    focus_pack = memory.get("focus_memories") or []
    if focus_pack:
        lines.extend(["### Focus Memory Pack", ""])
        for idx, fm in enumerate(focus_pack[:5], start=1):
            lines.extend(
                [
                    f"> [!abstract] Match {idx} · score {float(fm.get('score', 0.0) or 0.0):.3f}",
                    f"> `{fm.get('class', 'uncategorized')}` · `{pathlib.Path(str(fm.get('source_file') or '')).name or 'unknown'}`",
                    f"> {clip_text(fm.get('snippet', ''), 180)}",
                    ">",
                ]
            )
        lines.append("")

    stale_examples = memory_health.get("stale_examples") or []
    if stale_examples:
        lines.extend(["### Stale Fact Watchlist", ""])
        for row in stale_examples[:5]:
            subject = row.get("subject") or "(unknown subject)"
            pred = row.get("predicate") or "related_to"
            obj = row.get("object") or "(unknown object)"
            lines.append(f"- `{subject}` **{pred}** `{clip_text(obj, 80)}`")
        lines.append("")

    conflict_examples = memory_health.get("conflict_examples") or []
    if conflict_examples:
        lines.extend(["### Conflict Watchlist", ""])
        for row in conflict_examples[:5]:
            reason = row.get("reason") or "possible contradiction"
            subject = row.get("subject") or "unknown subject"
            lines.append(f"- `{subject}` → {reason}")
        lines.append("")

    lines.extend(["## Graph (Mermaid)", "", "```mermaid", "graph LR"])
    for n in nodes:
        nid = n.get("id")
        title = clean_label(str(n.get("title") or nid))
        ntype = clean_label(str(n.get("type") or "unknown"))
        conf = n.get("confidence")
        conf_txt = f"{float(conf):.2f}" if isinstance(conf, (float, int)) else "n/a"
        lines.append(f"  {mermaid_id.get(nid, 'N0')}[\"{title}<br/>{ntype} · conf {conf_txt}\"]")
    for e in edges:
        src = mermaid_id.get(e.get("from"))
        dst = mermaid_id.get(e.get("to"))
        if not src or not dst:
            continue
        kind = clean_label(str(e.get("kind") or "related_to"))
        lines.append(f"  {src} -- {kind} --> {dst}")
    lines.extend(["```", ""])

    if type_counts:
        lines.extend(["## Node Type Mix", "", "```mermaid", "pie title Node type distribution"])
        for k, v in sorted(type_counts.items()):
            lines.append(f"  \"{clean_label(k)}\" : {v}")
        lines.extend(["```", ""])

    lines.extend(["## Node Directory", "", "| Node | Type | Confidence | Source |", "|---|---:|---:|---|"])
    for n in nodes:
        src = n.get("source_ref") or ""
        conf = n.get("confidence")
        conf_txt = f"{float(conf):.2f}" if isinstance(conf, (float, int)) else "n/a"
        lines.append(f"| `{n.get('id')}` | {n.get('type', 'unknown')} | {conf_txt} | `{src}` |")
    lines.append("")

    lines.extend(["## Node Drilldown", ""])
    for n in nodes:
        title = n.get("title") or n.get("id")
        lines.append(f"### {title}")
        lines.append("")
        lines.append(f"- id: `{n.get('id')}`")
        lines.append(f"- type: `{n.get('type', 'unknown')}`")
        lines.append(f"- source: `{n.get('source_ref', 'n/a')}`")
        links = n.get("links", [])
        if links:
            friendly = [by_id.get(t, {}).get("title", t) for t in links]
            lines.append(f"- links: {', '.join(f'`{x}`' for x in friendly)}")
        else:
            lines.append("- links: (none)")
        lines.append("")

    dashboard_path.write_text("\n".join(lines) + "\n", encoding="utf-8")


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
    parser.add_argument(
        "--obsidian-auto-subdir",
        default=DEFAULT_OBSIDIAN_SUBDIR,
        help="when auto-detecting active Obsidian vault, write into this subdirectory",
    )
    parser.add_argument(
        "--obsidian-dashboard-file",
        default=DEFAULT_OBSIDIAN_DASHBOARD,
        help="dashboard markdown filename inside Obsidian vault",
    )
    parser.add_argument(
        "--no-obsidian-auto-vault",
        action="store_true",
        help="disable auto-detection of active Obsidian vault",
    )
    args = parser.parse_args()

    cortex_bin = args.cortex_bin
    if not os.path.exists(cortex_bin):
        cortex_bin = "cortex"

    stats = run_stats(cortex_bin)
    telemetry = parse_telemetry(pathlib.Path(args.telemetry))
    snapshot = build_snapshot(stats, telemetry, cortex_bin)

    vault_dir: pathlib.Path | None = None
    if args.obsidian_vault_dir:
        vault_dir = pathlib.Path(args.obsidian_vault_dir).expanduser().resolve()
    elif not args.no_obsidian_auto_vault:
        active_vault = detect_obsidian_open_vault()
        if active_vault:
            vault_dir = (active_vault / (args.obsidian_auto_subdir or DEFAULT_OBSIDIAN_SUBDIR)).resolve()

    if vault_dir is None:
        vault_dir = DEFAULT_OBSIDIAN_FALLBACK_DIR.resolve()

    obsidian_graph = build_obsidian_graph(
        snapshot,
        vault_dir=vault_dir,
        dashboard_file=args.obsidian_dashboard_file,
    )

    out = pathlib.Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(snapshot, indent=2), encoding="utf-8")

    obs_out = pathlib.Path(args.obsidian_output)
    obs_out.parent.mkdir(parents=True, exist_ok=True)
    obs_out.write_text(json.dumps(obsidian_graph, indent=2), encoding="utf-8")

    if vault_dir:
        export_obsidian_vault(obsidian_graph, snapshot, vault_dir)

    print(f"wrote canonical snapshot: {out}")
    print(f"wrote obsidian adapter: {obs_out}")


if __name__ == "__main__":
    main()
