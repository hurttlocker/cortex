#!/usr/bin/env python3
import argparse
import json
import subprocess
from pathlib import Path
from typing import Optional


def run_search(binary: str, db: str, query: str, limit: int, mode: str, embed: Optional[str]):
    cmd = [
        binary,
        "--db",
        db,
        "search",
        query,
        "--mode",
        mode,
        "--limit",
        str(limit),
        "--json",
    ]
    if embed:
        cmd.extend(["--embed", embed])
    out = subprocess.check_output(cmd, text=True)
    return json.loads(out)


def contains_any(text: str, needles):
    t = (text or "").lower()
    for n in needles or []:
        if n.lower() in t:
            return True
    return False


def main():
    ap = argparse.ArgumentParser(description="Run retrieval precision/noise benchmark fixture")
    ap.add_argument("--binary", required=True, help="Path to cortex binary")
    ap.add_argument("--db", required=True, help="Path to cortex.db")
    ap.add_argument("--fixture", required=True, help="Fixture JSON path")
    ap.add_argument("--mode", default="hybrid", choices=["keyword", "semantic", "hybrid", "bm25"], help="Search mode")
    ap.add_argument("--embed", default="ollama/nomic-embed-text", help="Embedding provider/model (required for semantic/hybrid)")
    ap.add_argument("--output", help="Optional output JSON report")
    args = ap.parse_args()

    fixture = json.loads(Path(args.fixture).read_text())
    markers = fixture.get("noise_markers", [])

    mode = "keyword" if args.mode == "bm25" else args.mode
    embed = args.embed
    if mode == "keyword":
        embed = None

    report = {
        "fixture": fixture.get("name"),
        "binary": args.binary,
        "db": args.db,
        "mode": mode,
        "embed": embed,
        "summary": {
            "queries": 0,
            "passed": 0,
            "failed": 0,
            "avg_precision_at_k": 0.0,
        },
        "results": [],
    }

    precision_values = []

    for spec in fixture.get("queries", []):
        q = spec["query"]
        limit = int(spec.get("limit", 8))
        min_hits = int(spec.get("min_hits", 1))
        max_noisy_top3 = int(spec.get("max_noisy_top3", 99))

        # Precision@k fields (optional)
        k = int(spec.get("k", 5))
        expected_contains_any = spec.get("expected_contains_any", [])
        min_precision_at_k = float(spec.get("min_precision_at_k", 0.0))

        hits = run_search(args.binary, args.db, q, limit, mode, embed)

        noisy_positions = []
        for i, r in enumerate(hits, start=1):
            content = (r.get("content") or "")
            if contains_any(content, markers):
                noisy_positions.append(i)

        noisy_top3 = sum(1 for p in noisy_positions if p <= 3)

        topk = hits[: max(0, k)]
        relevant = 0
        for r in topk:
            c = (r.get("content") or "")
            source = (r.get("source_file") or "")
            if contains_any(c, expected_contains_any) or contains_any(source, expected_contains_any):
                relevant += 1
        precision_at_k = (relevant / max(1, k)) if k > 0 else 0.0
        precision_values.append(precision_at_k)

        passed = (
            len(hits) >= min_hits
            and noisy_top3 <= max_noisy_top3
            and precision_at_k >= min_precision_at_k
        )

        report["results"].append(
            {
                "query": q,
                "hits": len(hits),
                "noisy_positions": noisy_positions,
                "noisy_top3": noisy_top3,
                "max_noisy_top3": max_noisy_top3,
                "k": k,
                "expected_contains_any": expected_contains_any,
                "relevant_in_top_k": relevant,
                "precision_at_k": precision_at_k,
                "min_precision_at_k": min_precision_at_k,
                "passed": passed,
                "top_memory_id": hits[0].get("memory_id") if hits else None,
            }
        )

        report["summary"]["queries"] += 1
        if passed:
            report["summary"]["passed"] += 1
        else:
            report["summary"]["failed"] += 1

    if precision_values:
        report["summary"]["avg_precision_at_k"] = sum(precision_values) / len(precision_values)

    print(json.dumps(report, indent=2))
    if args.output:
        Path(args.output).write_text(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
