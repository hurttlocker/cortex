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
    allowed = {"PASS", "WARN", "FAIL", "NO_DATA"}
    if ops["overall_status"] not in allowed:
        fail("ops.overall_status not in PASS|WARN|FAIL|NO_DATA")

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
