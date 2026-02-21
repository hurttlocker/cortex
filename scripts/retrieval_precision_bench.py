#!/usr/bin/env python3
import argparse
import json
import subprocess
from pathlib import Path


def run_search(binary: str, db: str, query: str, limit: int):
    cmd = [binary, "--db", db, "search", query, "--mode", "hybrid", "--embed", "ollama/nomic-embed-text", "--limit", str(limit), "--json"]
    out = subprocess.check_output(cmd, text=True)
    return json.loads(out)


def main():
    ap = argparse.ArgumentParser(description="Run retrieval precision/noise benchmark fixture")
    ap.add_argument("--binary", required=True, help="Path to cortex binary")
    ap.add_argument("--db", required=True, help="Path to cortex.db")
    ap.add_argument("--fixture", required=True, help="Fixture JSON path")
    ap.add_argument("--output", help="Optional output JSON report")
    args = ap.parse_args()

    fixture = json.loads(Path(args.fixture).read_text())
    markers = fixture.get("noise_markers", [])

    report = {
        "fixture": fixture.get("name"),
        "binary": args.binary,
        "db": args.db,
        "summary": {"queries": 0, "passed": 0, "failed": 0},
        "results": [],
    }

    for spec in fixture.get("queries", []):
        q = spec["query"]
        limit = int(spec.get("limit", 8))
        min_hits = int(spec.get("min_hits", 1))
        max_noisy_top3 = int(spec.get("max_noisy_top3", 99))

        hits = run_search(args.binary, args.db, q, limit)
        noisy_positions = []
        for i, r in enumerate(hits, start=1):
            content = (r.get("content") or "")
            if any(m in content for m in markers):
                noisy_positions.append(i)

        noisy_top3 = sum(1 for p in noisy_positions if p <= 3)
        passed = len(hits) >= min_hits and noisy_top3 <= max_noisy_top3

        report["results"].append({
            "query": q,
            "hits": len(hits),
            "noisy_positions": noisy_positions,
            "noisy_top3": noisy_top3,
            "max_noisy_top3": max_noisy_top3,
            "passed": passed,
            "top_memory_id": hits[0].get("memory_id") if hits else None,
        })

        report["summary"]["queries"] += 1
        if passed:
            report["summary"]["passed"] += 1
        else:
            report["summary"]["failed"] += 1

    print(json.dumps(report, indent=2))
    if args.output:
        Path(args.output).write_text(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
