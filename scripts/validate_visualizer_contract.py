#!/usr/bin/env python3
"""Lightweight validator for visualizer fixture contracts."""

from __future__ import annotations

import json
import pathlib
import sys


def fail(msg: str) -> None:
    print(f"ERROR: {msg}")
    sys.exit(1)


def require(obj: dict, key: str, where: str) -> None:
    if key not in obj:
        fail(f"missing `{key}` in {where}")


def validate_canonical(path: pathlib.Path) -> None:
    data = json.loads(path.read_text(encoding="utf-8"))
    require(data, "schema_version", "canonical root")
    require(data, "generated_at", "canonical root")
    require(data, "window", "canonical root")
    require(data, "data", "canonical root")

    d = data["data"]
    for section in ["overview", "ops", "quality", "reason", "retrieval", "graph", "memory", "stats"]:
        require(d, section, "canonical data")

    ops = d["ops"]
    require(ops, "overall_status", "ops")
    require(ops, "gates", "ops")
    allowed = {"PASS", "WARN", "FAIL", "NO_DATA"}
    if ops["overall_status"] not in allowed:
        fail("ops.overall_status not in PASS|WARN|FAIL|NO_DATA")

    for idx, gate in enumerate(ops.get("gates", [])):
        where = f"ops.gates[{idx}]"
        for key in ["key", "label", "status", "reason"]:
            require(gate, key, where)
        if gate["status"] not in allowed:
            fail(f"{where}.status not in PASS|WARN|FAIL|NO_DATA")
        links = gate.get("evidence_links", [])
        if links is None:
            links = []
        if not isinstance(links, list):
            fail(f"{where}.evidence_links must be an array")
        for j, link in enumerate(links):
            if not isinstance(link, dict):
                fail(f"{where}.evidence_links[{j}] must be an object")
            for link_key in ["label", "href"]:
                require(link, link_key, f"{where}.evidence_links[{j}]")

    quality = d["quality"]
    for k in [
        "formula_version",
        "score",
        "score_status",
        "delta_24h",
        "trend_24h",
        "factors",
        "factors_v2",
        "top_drivers",
        "actions",
        "reproducibility",
    ]:
        require(quality, k, "quality")

    if quality.get("score_status") not in allowed:
        fail("quality.score_status not in PASS|WARN|FAIL|NO_DATA")

    if not isinstance(quality.get("factors_v2"), list):
        fail("quality.factors_v2 must be an array")

    memory = d["memory"]
    for k in [
        "focus_query",
        "recent_total",
        "recent_memories",
        "focus_memories",
        "class_distribution",
        "source_distribution",
        "timeline",
        "health",
    ]:
        require(memory, k, "memory")

    health = memory.get("health", {})
    for k in ["stale_count", "conflict_count", "alerts", "stale_examples", "conflict_examples"]:
        require(health, k, "memory.health")

    graph = d["graph"]
    for k in ["focus", "bounds", "nodes", "edges"]:
        require(graph, k, "graph")

    if not isinstance(graph["nodes"], list) or not isinstance(graph["edges"], list):
        fail("graph.nodes/edges must be arrays")

    print(f"OK canonical: {path}")


def validate_obsidian(path: pathlib.Path) -> None:
    data = json.loads(path.read_text(encoding="utf-8"))
    for k in ["schema_version", "generated_at", "source_snapshot", "graph"]:
        require(data, k, "obsidian root")

    graph = data["graph"]
    for k in [
        "focus",
        "bounds",
        "nodes",
        "edges",
        "vault_dir",
        "index_path",
        "obsidian_index_uri",
        "dashboard_file",
        "dashboard_path",
        "obsidian_dashboard_uri",
    ]:
        require(graph, k, "obsidian graph")

    for uri_key in ["obsidian_index_uri", "obsidian_dashboard_uri"]:
        uri = str(graph.get(uri_key, ""))
        if uri and not uri.startswith("obsidian://open?path="):
            fail(f"obsidian graph {uri_key} must start with obsidian://open?path=")

    node_ids = set()
    for idx, node in enumerate(graph.get("nodes", [])):
        where = f"obsidian.graph.nodes[{idx}]"
        for k in [
            "id",
            "title",
            "type",
            "confidence",
            "timestamp",
            "source_ref",
            "links",
            "note_file",
            "note_path",
            "obsidian_uri",
        ]:
            require(node, k, where)

        if node.get("obsidian_uri") and not str(node.get("obsidian_uri")).startswith("obsidian://open?path="):
            fail(f"{where}.obsidian_uri must start with obsidian://open?path=")
        node_ids.add(node.get("id"))

    for idx, node in enumerate(graph.get("nodes", [])):
        for target in node.get("links", []):
            if target not in node_ids:
                fail(f"obsidian.graph.nodes[{idx}] link target missing node `{target}`")

    print(f"OK obsidian: {path}")


def main() -> None:
    base = pathlib.Path("tests/fixtures/visualizer")
    canonical = base / "canonical-v1.json"
    obsidian = base / "obsidian-adapter-v1.json"

    if not canonical.exists() or not obsidian.exists():
        fail("fixtures not found in tests/fixtures/visualizer")

    validate_canonical(canonical)
    validate_obsidian(obsidian)
    print("Visualizer contract validation passed.")


if __name__ == "__main__":
    main()
