#!/usr/bin/env python3
"""Compare current SLO snapshot against a baseline snapshot.

Outputs PASS/WARN/FAIL/NO_BASELINE and exits non-zero only on FAIL
(unless --warn-only-fail-thresholds is set).
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Compare SLO snapshot trend vs baseline")
    p.add_argument("--current", required=True, help="Path to current slo snapshot JSON")
    p.add_argument("--baseline", help="Path to baseline slo snapshot JSON")
    p.add_argument("--output-json", required=True, help="Path to write trend JSON")
    p.add_argument("--output-markdown", help="Optional path to write markdown summary")
    p.add_argument("--warn-regression-pct", type=float, default=30.0)
    p.add_argument("--fail-regression-pct", type=float, default=80.0)
    p.add_argument("--warn-regression-ms", type=int, default=150)
    p.add_argument("--fail-regression-ms", type=int, default=500)
    p.add_argument("--warn-only-fail-thresholds", action="store_true")
    return p.parse_args()


def load_json(path: str) -> dict[str, Any]:
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def checkpoint_map(report: dict[str, Any]) -> dict[str, dict[str, Any]]:
    out: dict[str, dict[str, Any]] = {}
    for cp in report.get("checkpoints", []):
        name = str(cp.get("name", "")).strip()
        if name:
            out[name] = cp
    return out


def classify_regression(
    delta_ms: int,
    delta_pct: float | None,
    warn_ms: int,
    fail_ms: int,
    warn_pct: float,
    fail_pct: float,
) -> str:
    if delta_ms <= 0:
        return "improved" if delta_ms < 0 else "stable"

    # Guard against tiny baselines causing noisy huge percentages: require both ms and pct.
    meets_warn = delta_ms >= warn_ms and (delta_pct is not None and delta_pct >= warn_pct)
    meets_fail = delta_ms >= fail_ms and (delta_pct is not None and delta_pct >= fail_pct)

    if meets_fail:
        return "fail"
    if meets_warn:
        return "warn"
    return "regression-minor"


def write_markdown(path: str, report: dict[str, Any]) -> None:
    lines: list[str] = []
    lines.append("# Cortex SLO Trend Compare")
    lines.append("")
    lines.append(f"- Current generated: `{report['current_generated_at']}`")
    lines.append(f"- Baseline generated: `{report.get('baseline_generated_at', '(none)')}`")
    lines.append(f"- Overall status: **{report['overall_status']}**")
    lines.append("")
    lines.append("| Checkpoint | Current (ms) | Baseline (ms) | Delta (ms) | Delta (%) | Status |")
    lines.append("|---|---:|---:|---:|---:|---:|")
    for row in report.get("comparisons", []):
        base = row["baseline_ms"] if row["baseline_ms"] is not None else "-"
        pct = "-" if row["delta_pct"] is None else f"{row['delta_pct']:.1f}%"
        lines.append(
            f"| {row['name']} | {row['current_ms']} | {base} | {row['delta_ms']} | {pct} | {row['status']} |"
        )
    lines.append("")
    lines.append(
        f"- Fail threshold breaches: `{report.get('fail_count', 0)}`"
    )
    lines.append(
        f"- Warn threshold breaches: `{report.get('warn_count', 0)}`"
    )
    lines.append(
        f"- Warn-only fail thresholds: `{report.get('warn_only_fail_thresholds', False)}`"
    )

    Path(path).write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    args = parse_args()

    current = load_json(args.current)
    baseline = None
    if args.baseline and Path(args.baseline).exists():
        baseline = load_json(args.baseline)

    cur_map = checkpoint_map(current)

    comparisons: list[dict[str, Any]] = []
    warn_count = 0
    fail_count = 0

    if baseline is None:
        for name, cur_cp in sorted(cur_map.items()):
            comparisons.append(
                {
                    "name": name,
                    "current_ms": int(cur_cp.get("duration_ms", 0)),
                    "baseline_ms": None,
                    "delta_ms": 0,
                    "delta_pct": None,
                    "status": "no-baseline",
                }
            )
        overall_status = "NO_BASELINE"
    else:
        base_map = checkpoint_map(baseline)
        all_names = sorted(set(cur_map.keys()) | set(base_map.keys()))
        for name in all_names:
            cur_cp = cur_map.get(name)
            base_cp = base_map.get(name)

            cur_ms = int(cur_cp.get("duration_ms", 0)) if cur_cp else 0
            base_ms = int(base_cp.get("duration_ms", 0)) if base_cp else None

            if base_ms is None:
                status = "no-baseline-checkpoint"
                delta_ms = 0
                delta_pct = None
            else:
                delta_ms = cur_ms - base_ms
                delta_pct = None if base_ms <= 0 else (delta_ms / base_ms) * 100.0
                status = classify_regression(
                    delta_ms=delta_ms,
                    delta_pct=delta_pct,
                    warn_ms=args.warn_regression_ms,
                    fail_ms=args.fail_regression_ms,
                    warn_pct=args.warn_regression_pct,
                    fail_pct=args.fail_regression_pct,
                )
                if status == "warn":
                    warn_count += 1
                elif status == "fail":
                    fail_count += 1

            comparisons.append(
                {
                    "name": name,
                    "current_ms": cur_ms,
                    "baseline_ms": base_ms,
                    "delta_ms": delta_ms,
                    "delta_pct": delta_pct,
                    "status": status,
                }
            )

        if fail_count > 0 and args.warn_only_fail_thresholds:
            overall_status = "WARN"
        elif fail_count > 0:
            overall_status = "FAIL"
        elif warn_count > 0:
            overall_status = "WARN"
        else:
            overall_status = "PASS"

    report = {
        "current_generated_at": current.get("generated_at"),
        "baseline_generated_at": baseline.get("generated_at") if baseline else None,
        "warn_regression_pct": args.warn_regression_pct,
        "fail_regression_pct": args.fail_regression_pct,
        "warn_regression_ms": args.warn_regression_ms,
        "fail_regression_ms": args.fail_regression_ms,
        "warn_only_fail_thresholds": args.warn_only_fail_thresholds,
        "comparisons": comparisons,
        "warn_count": warn_count,
        "fail_count": fail_count,
        "overall_status": overall_status,
    }

    Path(args.output_json).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")

    if args.output_markdown:
        write_markdown(args.output_markdown, report)

    print(f"SLO trend compare: {overall_status}")
    print(f"JSON: {args.output_json}")
    if args.output_markdown:
        print(f"Markdown: {args.output_markdown}")

    if overall_status == "FAIL" and not args.warn_only_fail_thresholds:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
