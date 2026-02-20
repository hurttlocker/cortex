#!/usr/bin/env python3
"""Generate visualizer exports and open Obsidian desktop to graph index."""

from __future__ import annotations

import argparse
import json
import pathlib
import subprocess
import sys


def run_export(args: argparse.Namespace) -> None:
    cmd = [
        "python3",
        str(args.exporter),
        "--output",
        str(args.canonical),
        "--obsidian-output",
        str(args.obsidian),
        "--obsidian-vault-dir",
        str(args.vault_dir),
        "--telemetry",
        str(args.telemetry),
    ]
    if args.cortex_bin:
        cmd.extend(["--cortex-bin", str(args.cortex_bin)])
    subprocess.check_call(cmd)


def open_obsidian(index_uri: str, index_path: pathlib.Path) -> int:
    uri_attempt = subprocess.run(["open", index_uri], check=False)
    if uri_attempt.returncode == 0:
        return 0

    file_attempt = subprocess.run(["open", "-a", "Obsidian", str(index_path)], check=False)
    return file_attempt.returncode


def main() -> int:
    parser = argparse.ArgumentParser(description="Open Obsidian to visualizer graph index")
    parser.add_argument("--exporter", type=pathlib.Path, default=pathlib.Path("scripts/visualizer_export.py"))
    parser.add_argument("--canonical", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/latest.json"))
    parser.add_argument("--obsidian", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/obsidian-graph.json"))
    parser.add_argument("--vault-dir", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/obsidian-vault"))
    parser.add_argument("--telemetry", type=pathlib.Path, default=pathlib.Path.home() / ".cortex/reason-telemetry.jsonl")
    parser.add_argument("--cortex-bin", type=pathlib.Path, default=pathlib.Path.home() / "bin/cortex")
    parser.add_argument("--no-refresh", action="store_true", help="skip export refresh before opening")
    args = parser.parse_args()

    if not args.no_refresh:
        run_export(args)

    obs = json.loads(args.obsidian.read_text(encoding="utf-8"))
    graph = obs.get("graph", {})
    index_uri = graph.get("obsidian_index_uri", "")
    index_path = pathlib.Path(graph.get("index_path", ""))

    if not index_uri or not index_path:
        print("obsidian adapter missing index metadata; run exporter with --obsidian-vault-dir", file=sys.stderr)
        return 1

    rc = open_obsidian(index_uri, index_path)
    if rc != 0:
        print("failed to open Obsidian desktop", file=sys.stderr)
    else:
        print(f"opened Obsidian index: {index_path}")
    return rc


if __name__ == "__main__":
    raise SystemExit(main())
