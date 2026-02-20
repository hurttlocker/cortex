#!/usr/bin/env python3
"""Refresh visualizer exports and open Obsidian dashboard note."""

from __future__ import annotations

import argparse
import json
import pathlib
import subprocess
import sys


OBSIDIAN_CONFIG_PATH = pathlib.Path.home() / "Library/Application Support/obsidian/obsidian.json"
DEFAULT_SUBDIR = "_cortex_visualizer"
DEFAULT_DASHBOARD = "cortex-visualizer-dashboard.md"


def detect_obsidian_vault(config_path: pathlib.Path = OBSIDIAN_CONFIG_PATH) -> pathlib.Path | None:
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


def run_export(
    exporter: pathlib.Path,
    canonical: pathlib.Path,
    obsidian: pathlib.Path,
    vault_dir: pathlib.Path,
    telemetry: pathlib.Path,
    cortex_bin: pathlib.Path,
    dashboard_file: str,
) -> None:
    cmd = [
        "python3",
        str(exporter),
        "--output",
        str(canonical),
        "--obsidian-output",
        str(obsidian),
        "--obsidian-vault-dir",
        str(vault_dir),
        "--obsidian-dashboard-file",
        dashboard_file,
        "--telemetry",
        str(telemetry),
    ]
    if cortex_bin:
        cmd.extend(["--cortex-bin", str(cortex_bin)])
    subprocess.check_call(cmd)


def main() -> int:
    parser = argparse.ArgumentParser(description="Open Obsidian dashboard for visualizer")
    parser.add_argument("--exporter", type=pathlib.Path, default=pathlib.Path("scripts/visualizer_export.py"))
    parser.add_argument("--canonical", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/latest.json"))
    parser.add_argument("--obsidian", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/obsidian-graph.json"))
    parser.add_argument("--vault-dir", type=pathlib.Path, default=None)
    parser.add_argument("--subdir", type=str, default=DEFAULT_SUBDIR)
    parser.add_argument("--dashboard-file", type=str, default=DEFAULT_DASHBOARD)
    parser.add_argument("--telemetry", type=pathlib.Path, default=pathlib.Path.home() / ".cortex/reason-telemetry.jsonl")
    parser.add_argument("--cortex-bin", type=pathlib.Path, default=pathlib.Path.home() / "bin/cortex")
    parser.add_argument("--no-refresh", action="store_true", help="skip export refresh before opening")
    args = parser.parse_args()

    vault_dir = args.vault_dir
    if vault_dir is None:
        detected = detect_obsidian_vault()
        if not detected:
            print("could not detect active Obsidian vault; pass --vault-dir", file=sys.stderr)
            return 1
        vault_dir = (detected / args.subdir).resolve()

    if not args.no_refresh:
        run_export(
            exporter=args.exporter,
            canonical=args.canonical,
            obsidian=args.obsidian,
            vault_dir=vault_dir,
            telemetry=args.telemetry,
            cortex_bin=args.cortex_bin,
            dashboard_file=args.dashboard_file,
        )

    payload = json.loads(args.obsidian.read_text(encoding="utf-8"))
    graph = payload.get("graph", {})
    uri = graph.get("obsidian_dashboard_uri") or graph.get("obsidian_index_uri")
    path = graph.get("dashboard_path") or graph.get("index_path")
    if not uri or not path:
        print("obsidian adapter missing dashboard metadata", file=sys.stderr)
        return 1

    rc = subprocess.run(["open", uri], check=False).returncode
    if rc != 0:
        rc = subprocess.run(["open", "-a", "Obsidian", str(path)], check=False).returncode
    if rc != 0:
        print("failed to open Obsidian", file=sys.stderr)
        return rc

    print(f"opened Obsidian dashboard: {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
