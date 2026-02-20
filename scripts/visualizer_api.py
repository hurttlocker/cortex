#!/usr/bin/env python3
"""Minimal visualizer API server.

Serves one canonical backend payload and one Obsidian adapter payload
from the same exporter pipeline.
"""

from __future__ import annotations

import argparse
import json
import pathlib
import subprocess
import urllib.parse
from http import HTTPStatus
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer


def run_export(exporter: pathlib.Path, args: argparse.Namespace) -> None:
    cmd = [
        "python3",
        str(exporter),
        "--output",
        str(args.canonical),
        "--obsidian-output",
        str(args.obsidian),
        "--telemetry",
        str(args.telemetry),
    ]
    if args.obsidian_vault_dir:
        cmd.extend(["--obsidian-vault-dir", str(args.obsidian_vault_dir)])
    if args.cortex_bin:
        cmd.extend(["--cortex-bin", str(args.cortex_bin)])
    subprocess.run(cmd, check=False)


def load_json(path: pathlib.Path) -> dict:
    if not path.exists():
        return {}
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return {}


def response_json(h: SimpleHTTPRequestHandler, payload: dict, status: int = 200) -> None:
    body = json.dumps(payload).encode("utf-8")
    h.send_response(status)
    h.send_header("Content-Type", "application/json")
    h.send_header("Content-Length", str(len(body)))
    h.end_headers()
    h.wfile.write(body)


def bounded_subgraph(canonical: dict, focus: str, max_hops: int, max_nodes: int) -> dict:
    graph = canonical.get("data", {}).get("graph", {})
    nodes = graph.get("nodes", [])
    edges = graph.get("edges", [])
    by_id = {n.get("id"): n for n in nodes}

    if focus not in by_id:
        focus = graph.get("focus") or (nodes[0].get("id") if nodes else "")
    if not focus:
        return {"focus": "", "bounds": {"max_hops": max_hops, "max_nodes": max_nodes}, "nodes": [], "edges": []}

    adjacency: dict[str, set[str]] = {}
    for e in edges:
        a = e.get("from")
        b = e.get("to")
        if not a or not b:
            continue
        adjacency.setdefault(a, set()).add(b)
        adjacency.setdefault(b, set()).add(a)

    visited = {focus}
    frontier = {focus}
    for _ in range(max_hops):
        nxt = set()
        for n in frontier:
            nxt |= adjacency.get(n, set())
        nxt -= visited
        if not nxt:
            break
        visited |= nxt
        frontier = nxt
        if len(visited) >= max_nodes:
            break

    keep = list(visited)[:max_nodes]
    keep_set = set(keep)
    out_nodes = [by_id[nid] for nid in keep if nid in by_id]
    out_edges = [e for e in edges if e.get("from") in keep_set and e.get("to") in keep_set]

    return {
        "focus": focus,
        "bounds": {"max_hops": max_hops, "max_nodes": max_nodes},
        "nodes": out_nodes,
        "edges": out_edges,
    }


def make_handler(args: argparse.Namespace):
    root = args.static_root.resolve()
    exporter = args.exporter.resolve()

    class Handler(SimpleHTTPRequestHandler):
        def __init__(self, *a, **kw):
            super().__init__(*a, directory=str(root), **kw)

        def do_GET(self):
            parsed = urllib.parse.urlparse(self.path)
            qs = urllib.parse.parse_qs(parsed.query)

            if parsed.path == "/api/v1/health":
                response_json(self, {"ok": True})
                return

            if parsed.path in ("/api/v1/canonical", "/api/v1/obsidian", "/api/v1/subgraph"):
                refresh = qs.get("refresh", ["0"])[0] in ("1", "true", "yes")
                if refresh:
                    run_export(exporter, args)

                canonical = load_json(args.canonical)
                obsidian = load_json(args.obsidian)

                if parsed.path == "/api/v1/canonical":
                    if not canonical:
                        response_json(self, {"error": "canonical snapshot unavailable"}, status=HTTPStatus.SERVICE_UNAVAILABLE)
                        return
                    response_json(self, canonical)
                    return

                if parsed.path == "/api/v1/obsidian":
                    if not obsidian:
                        response_json(self, {"error": "obsidian adapter unavailable"}, status=HTTPStatus.SERVICE_UNAVAILABLE)
                        return
                    response_json(self, obsidian)
                    return

                focus = qs.get("focus", [""])[0]
                try:
                    max_hops = max(1, min(4, int(qs.get("max_hops", ["2"])[0])))
                except Exception:
                    max_hops = 2
                try:
                    max_nodes = max(1, min(500, int(qs.get("max_nodes", ["200"])[0])))
                except Exception:
                    max_nodes = 200

                if not canonical:
                    response_json(self, {"error": "canonical snapshot unavailable"}, status=HTTPStatus.SERVICE_UNAVAILABLE)
                    return

                response_json(self, bounded_subgraph(canonical, focus, max_hops, max_nodes))
                return

            return super().do_GET()

    return Handler


def main() -> None:
    parser = argparse.ArgumentParser(description="Serve visualizer static UI + minimal API")
    parser.add_argument("--port", type=int, default=8787)
    parser.add_argument("--static-root", type=pathlib.Path, default=pathlib.Path("docs/visualizer"))
    parser.add_argument("--canonical", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/latest.json"))
    parser.add_argument("--obsidian", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/obsidian-graph.json"))
    parser.add_argument("--telemetry", type=pathlib.Path, default=pathlib.Path.home() / ".cortex/reason-telemetry.jsonl")
    parser.add_argument("--obsidian-vault-dir", type=pathlib.Path, default=pathlib.Path("docs/visualizer/data/obsidian-vault"))
    parser.add_argument("--cortex-bin", type=pathlib.Path, default=pathlib.Path.home() / "bin/cortex")
    parser.add_argument("--exporter", type=pathlib.Path, default=pathlib.Path("scripts/visualizer_export.py"))
    parser.add_argument("--bootstrap", action="store_true", help="generate snapshots on startup")
    args = parser.parse_args()

    if args.bootstrap:
        run_export(args.exporter, args)

    handler = make_handler(args)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), handler)
    print(f"visualizer api listening on http://127.0.0.1:{args.port}")
    server.serve_forever()


if __name__ == "__main__":
    main()
