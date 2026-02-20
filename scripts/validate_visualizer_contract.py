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
    for section in ["overview", "ops", "quality", "reason", "retrieval", "graph", "stats"]:
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

    for idx, factor in enumerate(quality.get("factors_v2", [])):
        where = f"quality.factors_v2[{idx}]"
        for key in [
            "key",
            "label",
            "definition",
            "source",
            "direction",
            "weight",
            "value",
            "quality_value",
            "weighted_score",
            "status",
            "remediation",
        ]:
            require(factor, key, where)
        if factor["status"] not in allowed:
            fail(f"{where}.status not in PASS|WARN|FAIL|NO_DATA")

    retrieval = d["retrieval"]
    for k in ["query", "results", "deltas"]:
        require(retrieval, k, "retrieval")

    results = retrieval["results"]
    for mode in ["bm25", "semantic", "hybrid"]:
        require(results, mode, "retrieval.results")
        if not isinstance(results[mode], list):
            fail(f"retrieval.results.{mode} must be an array")
        for idx, row in enumerate(results[mode]):
            where = f"retrieval.results.{mode}[{idx}]"
            for key in ["id", "rank", "title", "score", "why"]:
                require(row, key, where)

    if not isinstance(retrieval["deltas"], list):
        fail("retrieval.deltas must be an array")
    for idx, row in enumerate(retrieval["deltas"]):
        where = f"retrieval.deltas[{idx}]"
        for key in [
            "id",
            "title",
            "bm25_rank",
            "semantic_rank",
            "hybrid_rank",
            "movement_vs_bm25",
            "movement_vs_semantic",
            "reason",
        ]:
            require(row, key, where)

    graph = d["graph"]
    for k in ["focus", "bounds", "nodes", "edges"]:
        require(graph, k, "graph")

    if not isinstance(graph["nodes"], list) or not isinstance(graph["edges"], list):
        fail("graph.nodes/edges must be arrays")

    bounds = graph.get("bounds", {})
    if not isinstance(bounds, dict):
        fail("graph.bounds must be an object")
    for k in ["max_hops", "max_nodes"]:
        require(bounds, k, "graph.bounds")

    print(f"OK canonical: {path}")


def validate_obsidian(path: pathlib.Path) -> None:
    data = json.loads(path.read_text(encoding="utf-8"))
    for k in ["schema_version", "generated_at", "source_snapshot", "graph"]:
        require(data, k, "obsidian root")

    graph = data["graph"]
    for k in ["focus", "bounds", "nodes", "edges"]:
        require(graph, k, "obsidian graph")

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
