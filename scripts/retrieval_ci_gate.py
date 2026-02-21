#!/usr/bin/env python3
import argparse
import json
import subprocess
import tempfile
from pathlib import Path


def run(cmd: list[str]):
    subprocess.check_call(cmd)


def run_capture(cmd: list[str]) -> str:
    return subprocess.check_output(cmd, text=True)


def main():
    ap = argparse.ArgumentParser(description="Deterministic retrieval CI gate")
    ap.add_argument("--binary", required=True, help="Path to cortex binary")
    ap.add_argument("--fixture", required=True, help="Retrieval fixture JSON")
    ap.add_argument("--corpus", required=True, help="Corpus directory to import")
    ap.add_argument("--mode", default="keyword", choices=["keyword", "semantic", "hybrid", "bm25"])
    ap.add_argument("--embed", default="ollama/nomic-embed-text")
    ap.add_argument("--min-pass-rate", type=float, default=0.95)
    ap.add_argument("--min-avg-precision", type=float, default=0.60)
    ap.add_argument("--max-total-noisy-top3", type=int, default=2)
    ap.add_argument("--output", help="Optional report path")
    args = ap.parse_args()

    with tempfile.TemporaryDirectory(prefix="cortex-retrieval-gate-") as td:
        tmp = Path(td)
        db = tmp / "cortex.db"

        # Seed deterministic corpus.
        run([
            args.binary,
            "--db",
            str(db),
            "reimport",
            args.corpus,
            "--recursive",
            "--force",
        ])

        bench_cmd = [
            "python3",
            "scripts/retrieval_precision_bench.py",
            "--binary",
            args.binary,
            "--db",
            str(db),
            "--fixture",
            args.fixture,
            "--mode",
            args.mode,
        ]
        if args.mode not in ("keyword", "bm25"):
            bench_cmd.extend(["--embed", args.embed])

        bench_json = run_capture(bench_cmd)
        report = json.loads(bench_json)

        summary = report.get("summary", {})
        total = max(1, int(summary.get("queries", 0)))
        passed = int(summary.get("passed", 0))
        pass_rate = passed / total
        avg_precision = float(summary.get("avg_precision_at_k", 0.0))

        total_noisy_top3 = 0
        for item in report.get("results", []):
            total_noisy_top3 += int(item.get("noisy_top3", 0))

        gate = {
            "pass_rate": pass_rate,
            "avg_precision_at_k": avg_precision,
            "total_noisy_top3": total_noisy_top3,
            "thresholds": {
                "min_pass_rate": args.min_pass_rate,
                "min_avg_precision": args.min_avg_precision,
                "max_total_noisy_top3": args.max_total_noisy_top3,
            },
            "passed": (
                pass_rate >= args.min_pass_rate
                and avg_precision >= args.min_avg_precision
                and total_noisy_top3 <= args.max_total_noisy_top3
            ),
            "bench": report,
        }

        out_text = json.dumps(gate, indent=2)
        print(out_text)
        if args.output:
            Path(args.output).write_text(out_text)

        if not gate["passed"]:
            raise SystemExit(1)


if __name__ == "__main__":
    main()
