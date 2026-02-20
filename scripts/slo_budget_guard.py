#!/usr/bin/env python3
"""Budget-policy guard for SLO canary artifacts.

Evaluates canary/runtime budget constraints (total duration + trend status) and
emits PASS/WARN/FAIL with machine-readable + markdown outputs.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Evaluate SLO budget policy")
    p.add_argument("--snapshot", required=True, help="Path to slo snapshot JSON")
    p.add_argument("--trend", help="Path to slo trend JSON (optional)")
    p.add_argument("--warn-total-ms", type=int, default=4000)
    p.add_argument("--fail-total-ms", type=int, default=12000)
    p.add_argument("--warn-only-fail-thresholds", action="store_true")
    p.add_argument("--output-json", required=True)
    p.add_argument("--output-markdown")
    return p.parse_args()


def load_json(path: str) -> dict[str, Any]:
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def write_markdown(path: str, report: dict[str, Any]) -> None:
    lines: list[str] = []
    lines.append("# Cortex SLO Budget Guard")
    lines.append("")
    lines.append(f"- Overall status: **{report['overall_status']}**")
    lines.append(f"- Snapshot status: `{report['snapshot_status']}`")
    lines.append(f"- Trend status: `{report['trend_status']}`")
    lines.append("")
    lines.append("| Metric | Value |")
    lines.append("|---|---:|")
    lines.append(f"| Total duration (ms) | {report['total_duration_ms']} |")
    lines.append(f"| Warn total threshold (ms) | {report['warn_total_ms']} |")
    lines.append(f"| Fail total threshold (ms) | {report['fail_total_ms']} |")
    lines.append("")
    if report.get("reasons"):
        lines.append("## Reasons")
        for r in report["reasons"]:
            lines.append(f"- {r}")
        lines.append("")

    lines.append(f"- Warn-only fail thresholds: `{report['warn_only_fail_thresholds']}`")
    Path(path).write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    args = parse_args()

    if args.warn_total_ms < 0 or args.fail_total_ms < 0:
        raise SystemExit("thresholds must be >= 0")
    if args.fail_total_ms > 0 and args.warn_total_ms > args.fail_total_ms:
        raise SystemExit("warn threshold cannot exceed fail threshold")

    snapshot = load_json(args.snapshot)
    trend = None
    if args.trend and Path(args.trend).exists():
        trend = load_json(args.trend)

    checkpoints = snapshot.get("checkpoints", [])
    total_duration_ms = int(sum(int(cp.get("duration_ms", 0)) for cp in checkpoints))

    snapshot_status = str(snapshot.get("overall_status", "UNKNOWN"))
    trend_status = str(trend.get("overall_status", "NO_TREND")) if trend else "NO_TREND"

    overall_status = "PASS"
    reasons: list[str] = []

    if snapshot_status == "FAIL":
        overall_status = "FAIL"
        reasons.append("snapshot reported FAIL")
    elif snapshot_status == "WARN":
        if overall_status != "FAIL":
            overall_status = "WARN"
        reasons.append("snapshot reported WARN")

    if args.fail_total_ms > 0 and total_duration_ms > args.fail_total_ms:
        overall_status = "FAIL"
        reasons.append(
            f"total duration {total_duration_ms}ms exceeded fail threshold {args.fail_total_ms}ms"
        )
    elif args.warn_total_ms > 0 and total_duration_ms > args.warn_total_ms:
        if overall_status != "FAIL":
            overall_status = "WARN"
        reasons.append(
            f"total duration {total_duration_ms}ms exceeded warn threshold {args.warn_total_ms}ms"
        )

    if trend_status == "FAIL":
        overall_status = "FAIL"
        reasons.append("trend comparison reported FAIL")
    elif trend_status == "WARN":
        if overall_status != "FAIL":
            overall_status = "WARN"
        reasons.append("trend comparison reported WARN")

    if overall_status == "FAIL" and args.warn_only_fail_thresholds:
        overall_status = "WARN"
        reasons.append("FAIL downgraded to WARN by warn-only policy")

    report = {
        "snapshot_status": snapshot_status,
        "trend_status": trend_status,
        "total_duration_ms": total_duration_ms,
        "warn_total_ms": args.warn_total_ms,
        "fail_total_ms": args.fail_total_ms,
        "warn_only_fail_thresholds": args.warn_only_fail_thresholds,
        "overall_status": overall_status,
        "reasons": reasons,
    }

    Path(args.output_json).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    if args.output_markdown:
        write_markdown(args.output_markdown, report)

    print(f"SLO budget guard: {overall_status}")
    print(f"JSON: {args.output_json}")
    if args.output_markdown:
        print(f"Markdown: {args.output_markdown}")

    return 1 if overall_status == "FAIL" and not args.warn_only_fail_thresholds else 0


if __name__ == "__main__":
    raise SystemExit(main())
