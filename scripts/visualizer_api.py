#!/usr/bin/env python3
"""Minimal visualizer API server.

Serves one canonical backend payload and one Obsidian adapter payload
from the same exporter pipeline.
"""

from __future__ import annotations

import argparse
import json
import math
import pathlib
import subprocess
import urllib.parse
from datetime import datetime, timedelta, timezone
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


def now_rfc3339() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def parse_rfc3339(value: str) -> datetime | None:
    if not value:
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except Exception:
        return None


def p95(values: list[int]) -> int:
    if not values:
        return 0
    ordered = sorted(values)
    idx = max(0, min(len(ordered) - 1, math.ceil(0.95 * len(ordered)) - 1))
    return int(ordered[idx])


def normalize_reason_run(row: dict, idx: int) -> dict:
    ts = str(row.get("timestamp", ""))
    mode = str(row.get("mode", "one-shot"))
    wall_ms = int(row.get("wall_ms", 0) or 0)
    search_ms = int(row.get("search_ms", 0) or 0)
    llm_ms = int(row.get("llm_ms", 0) or 0)
    tokens_in = int(row.get("tokens_in", 0) or 0)
    tokens_out = int(row.get("tokens_out", 0) or 0)
    cost = float(row.get("cost_usd", 0.0) or 0.0)
    iterations = int(row.get("iterations", 0) or 0)
    reason_status = str(row.get("reason_status", "")).lower()

    run_status = "ok"
    if reason_status in {"error", "failed", "fail"}:
        run_status = "error"
    elif wall_ms <= 0:
        run_status = "error"

    step_outcomes: list[dict] = []
    if mode == "recursive":
        step_outcomes.append(
            {
                "name": "search",
                "latency_ms": search_ms,
                "status": "ok" if search_ms > 0 else "no-data",
            }
        )
        step_outcomes.append(
            {
                "name": "reason",
                "latency_ms": llm_ms,
                "status": "ok" if llm_ms > 0 else "no-data",
            }
        )
        step_outcomes.append(
            {
                "name": "recursive-loop",
                "count": iterations,
                "status": "ok" if iterations > 0 else "no-data",
            }
        )
    else:
        step_outcomes.append(
            {
                "name": "reason",
                "latency_ms": llm_ms if llm_ms > 0 else wall_ms,
                "status": "ok" if (llm_ms > 0 or wall_ms > 0) else "no-data",
            }
        )

    return {
        "run_id": ts if ts else f"run-{idx}",
        "timestamp": ts,
        "mode": mode,
        "model": str(row.get("model", "unknown")),
        "provider": str(row.get("provider", "unknown")),
        "preset": str(row.get("preset", "")),
        "query": str(row.get("query", "")),
        "latency_ms": wall_ms,
        "search_ms": search_ms,
        "llm_ms": llm_ms,
        "tokens_in": tokens_in,
        "tokens_out": tokens_out,
        "tokens_total": tokens_in + tokens_out,
        "estimated_cost_usd": round(cost, 6),
        "cost_known": bool(row.get("cost_known", True)),
        "iterations": iterations,
        "recursive_depth": int(row.get("recursive_depth", 0) or 0),
        "facts_used": int(row.get("facts_used", 0) or 0),
        "memories_used": int(row.get("memories_used", 0) or 0),
        "status": run_status,
        "step_outcomes": step_outcomes,
    }


def load_reason_runs(telemetry_path: pathlib.Path, limit: int = 800) -> list[dict]:
    if not telemetry_path.exists():
        return []

    rows: list[dict] = []
    try:
        for line in telemetry_path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                rows.append(json.loads(line))
            except Exception:
                continue
    except Exception:
        return []

    rows = rows[-limit:]
    out = [normalize_reason_run(row, idx) for idx, row in enumerate(rows)]
    out.sort(key=lambda r: parse_rfc3339(r.get("timestamp", "")) or datetime.min.replace(tzinfo=timezone.utc), reverse=True)
    return out


def filter_reason_runs(runs: list[dict], qs: dict) -> tuple[list[dict], dict]:
    model = qs.get("model", [""])[0].strip()
    provider = qs.get("provider", [""])[0].strip()
    preset = qs.get("preset", [""])[0].strip()
    mode = qs.get("mode", [""])[0].strip()

    try:
        since_hours = max(1, min(24 * 30, int(qs.get("since_hours", ["168"])[0])))
    except Exception:
        since_hours = 168

    try:
        limit = max(1, min(300, int(qs.get("limit", ["80"])[0])))
    except Exception:
        limit = 80

    cutoff = datetime.now(timezone.utc) - timedelta(hours=since_hours)

    filtered = []
    for run in runs:
        ts = parse_rfc3339(run.get("timestamp", ""))
        if ts is not None and ts < cutoff:
            continue
        if model and run.get("model") != model:
            continue
        if provider and run.get("provider") != provider:
            continue
        if preset and run.get("preset") != preset:
            continue
        if mode and run.get("mode") != mode:
            continue
        filtered.append(run)

    return filtered[:limit], {
        "model": model,
        "provider": provider,
        "preset": preset,
        "mode": mode,
        "since_hours": since_hours,
        "limit": limit,
    }


def summarize_reason_runs(runs: list[dict]) -> dict:
    latencies = [int(r.get("latency_ms", 0) or 0) for r in runs if int(r.get("latency_ms", 0) or 0) > 0]
    total_cost = sum(float(r.get("estimated_cost_usd", 0.0) or 0.0) for r in runs)
    total_tokens = sum(int(r.get("tokens_total", 0) or 0) for r in runs)
    errors = [r for r in runs if r.get("status") == "error"]
    recursive = [r for r in runs if r.get("mode") == "recursive"]
    one_shot = [r for r in runs if r.get("mode") != "recursive"]

    return {
        "run_count": len(runs),
        "error_count": len(errors),
        "recursive_count": len(recursive),
        "one_shot_count": len(one_shot),
        "p95_latency_ms": p95(latencies),
        "cost_total_usd": round(total_cost, 6),
        "tokens_total": total_tokens,
    }


def reason_filter_options(runs: list[dict]) -> dict:
    def unique(field: str) -> list[str]:
        vals = sorted({str(r.get(field, "")).strip() for r in runs if str(r.get(field, "")).strip()})
        return vals

    return {
        "model": unique("model"),
        "provider": unique("provider"),
        "preset": unique("preset"),
        "mode": unique("mode"),
        "since_hours_default": 168,
    }


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

            if parsed.path in ("/api/v1/canonical", "/api/v1/obsidian", "/api/v1/subgraph", "/api/v1/reason-runs"):
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

                if parsed.path == "/api/v1/reason-runs":
                    runs = load_reason_runs(args.telemetry)
                    filtered, filters_applied = filter_reason_runs(runs, qs)
                    payload = {
                        "schema_version": "v1",
                        "generated_at": now_rfc3339(),
                        "filters_applied": filters_applied,
                        "filter_options": reason_filter_options(runs),
                        "summary": summarize_reason_runs(filtered),
                        "runs": filtered,
                    }
                    response_json(self, payload)
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
