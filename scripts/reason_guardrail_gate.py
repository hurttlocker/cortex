#!/usr/bin/env python3
"""Guardrail gate for reason quality reports.

Consumes JSON output from scripts/reason_quality_eval.py and enforces reliability
thresholds that are stricter than simple pass-rate checks.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Reason reliability guardrail gate")
    ap.add_argument("--report", required=True, help="Path to reason-quality-eval JSON report")
    ap.add_argument("--output", help="Optional JSON output path")
    ap.add_argument("--min-pass-rate", type=float, default=0.80)
    ap.add_argument("--min-overall", type=float, default=0.72)
    ap.add_argument("--min-grounding", type=float, default=0.62)
    ap.add_argument("--min-actionability", type=float, default=0.65)
    ap.add_argument("--min-usefulness", type=float, default=0.68)
    ap.add_argument("--max-error-rate", type=float, default=0.05)
    ap.add_argument("--max-empty-content-rate", type=float, default=0.02)
    ap.add_argument("--max-hard-failure-rate", type=float, default=0.10)
    return ap.parse_args()


def safe_float(v, default=0.0) -> float:
    try:
        return float(v)
    except Exception:
        return float(default)


def main() -> int:
    args = parse_args()
    report = json.loads(Path(args.report).read_text())

    summary = report.get("summary", {})
    results = report.get("results", [])
    total = max(1, int(summary.get("total_cases", len(results) or 1)))

    pass_rate = safe_float(summary.get("pass_rate"), 0.0)
    avg_overall = safe_float(summary.get("average_overall_score"), 0.0)
    dim_avgs = summary.get("dimension_averages", {}) or {}

    grounding = safe_float(dim_avgs.get("factual_grounding"), 0.0)
    actionability = safe_float(dim_avgs.get("actionability"), 0.0)
    usefulness = safe_float(dim_avgs.get("usefulness"), 0.0)

    error_cases = int(summary.get("error_cases", 0))
    error_rate = error_cases / float(total)

    empty_content_cases = 0
    hard_failure_cases = 0
    for r in results:
        if r.get("empty_content"):
            empty_content_cases += 1
        if r.get("hard_failures"):
            hard_failure_cases += 1

    empty_content_rate = empty_content_cases / float(total)
    hard_failure_rate = hard_failure_cases / float(total)

    checks = [
        {"name": "pass_rate", "value": pass_rate, "min": args.min_pass_rate, "ok": pass_rate >= args.min_pass_rate},
        {"name": "overall_score", "value": avg_overall, "min": args.min_overall, "ok": avg_overall >= args.min_overall},
        {"name": "grounding", "value": grounding, "min": args.min_grounding, "ok": grounding >= args.min_grounding},
        {"name": "actionability", "value": actionability, "min": args.min_actionability, "ok": actionability >= args.min_actionability},
        {"name": "usefulness", "value": usefulness, "min": args.min_usefulness, "ok": usefulness >= args.min_usefulness},
        {"name": "error_rate", "value": error_rate, "max": args.max_error_rate, "ok": error_rate <= args.max_error_rate},
        {
            "name": "empty_content_rate",
            "value": empty_content_rate,
            "max": args.max_empty_content_rate,
            "ok": empty_content_rate <= args.max_empty_content_rate,
        },
        {
            "name": "hard_failure_rate",
            "value": hard_failure_rate,
            "max": args.max_hard_failure_rate,
            "ok": hard_failure_rate <= args.max_hard_failure_rate,
        },
    ]

    failed = [c for c in checks if not c["ok"]]
    gate = {
        "report": args.report,
        "passed": len(failed) == 0,
        "summary": {
            "total_cases": total,
            "pass_rate": round(pass_rate, 4),
            "average_overall_score": round(avg_overall, 4),
            "factual_grounding": round(grounding, 4),
            "actionability": round(actionability, 4),
            "usefulness": round(usefulness, 4),
            "error_rate": round(error_rate, 4),
            "empty_content_rate": round(empty_content_rate, 4),
            "hard_failure_rate": round(hard_failure_rate, 4),
        },
        "checks": checks,
        "failed_checks": failed,
    }

    text = json.dumps(gate, indent=2)
    print(text)
    if args.output:
        Path(args.output).write_text(text)

    return 0 if gate["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
