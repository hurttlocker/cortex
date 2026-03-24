#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import subprocess
import tempfile
from pathlib import Path


def run(cmd: list[str]) -> None:
    subprocess.check_call(cmd)


def run_capture(cmd: list[str]) -> str:
    return subprocess.check_output(cmd, text=True)


def import_corpus(binary: str, db: Path, corpus: str, gate: bool) -> dict:
    cmd = [
        binary,
        "--db",
        str(db),
        "import",
        corpus,
        "--recursive",
        "--extract",
        "--no-enrich",
        "--no-classify",
    ]
    if gate:
        cmd.append("--import-quality-gate")
    run(cmd)
    stats = json.loads(run_capture([binary, "--db", str(db), "stats"]))
    return stats


def run_retrieval_eval(binary: str, db: Path, fixture: str, mode: str, embed: str | None) -> dict:
    cmd = [
        "python3",
        "scripts/retrieval_precision_bench.py",
        "--binary",
        binary,
        "--db",
        str(db),
        "--fixture",
        fixture,
        "--mode",
        mode,
    ]
    if embed and mode not in {"keyword", "bm25"}:
        cmd.extend(["--embed", embed])
    return json.loads(run_capture(cmd))


def main() -> int:
    ap = argparse.ArgumentParser(description="Compare import-quality gate off vs on")
    ap.add_argument("--binary", required=True, help="Path to cortex binary")
    ap.add_argument("--corpus", required=True, help="Corpus directory to import")
    ap.add_argument("--fixture", help="Optional retrieval fixture JSON")
    ap.add_argument("--mode", default="bm25", help="Retrieval mode for optional eval")
    ap.add_argument("--embed", help="Optional embed provider/model for retrieval eval")
    ap.add_argument("--output", help="Optional report path")
    args = ap.parse_args()

    with tempfile.TemporaryDirectory(prefix="cortex-import-quality-eval-") as td:
        tmp = Path(td)
        baseline_db = tmp / "baseline.db"
        gated_db = tmp / "gated.db"

        baseline_stats = import_corpus(args.binary, baseline_db, args.corpus, gate=False)
        gated_stats = import_corpus(args.binary, gated_db, args.corpus, gate=True)

        report = {
            "baseline": {"stats": baseline_stats},
            "gated": {"stats": gated_stats},
            "delta": {
                "memories": int(gated_stats.get("memories", 0)) - int(baseline_stats.get("memories", 0)),
                "facts": int(gated_stats.get("facts", 0)) - int(baseline_stats.get("facts", 0)),
                "denied_at_import_count": int(gated_stats.get("denied_at_import_count", 0))
                - int(baseline_stats.get("denied_at_import_count", 0)),
            },
        }

        if args.fixture:
            baseline_eval = run_retrieval_eval(args.binary, baseline_db, args.fixture, args.mode, args.embed)
            gated_eval = run_retrieval_eval(args.binary, gated_db, args.fixture, args.mode, args.embed)
            report["baseline"]["retrieval_eval"] = baseline_eval
            report["gated"]["retrieval_eval"] = gated_eval
            report["delta"]["pass_rate"] = (
                float(gated_eval.get("summary", {}).get("passed", 0)) / max(1, int(gated_eval.get("summary", {}).get("queries", 0)))
                - float(baseline_eval.get("summary", {}).get("passed", 0)) / max(1, int(baseline_eval.get("summary", {}).get("queries", 0)))
            )
            report["delta"]["avg_precision_at_k"] = (
                float(gated_eval.get("summary", {}).get("avg_precision_at_k", 0.0))
                - float(baseline_eval.get("summary", {}).get("avg_precision_at_k", 0.0))
            )

        text = json.dumps(report, indent=2)
        print(text)
        if args.output:
            Path(args.output).write_text(text + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
