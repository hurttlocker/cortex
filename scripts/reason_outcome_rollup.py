#!/usr/bin/env python3
"""Outcome loop rollup for reason-quality human feedback.

Input: JSONL log where each line includes at minimum:
- timestamp
- prompt_id
- accepted_without_edits (bool)
- action_taken (bool)
Optional:
- edited (bool)
- useful (bool)
- model
- notes

This script computes high-level product metrics for issue #31 closure:
1) accepted-without-edits rate
2) action-taken rate
3) usefulness rate (if provided)
"""

from __future__ import annotations

import argparse
import json
from collections import Counter
from pathlib import Path


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Roll up reason outcome loop metrics")
    ap.add_argument("--input", required=True, help="Path to JSONL feedback log")
    ap.add_argument("--output", help="Optional JSON output path")
    ap.add_argument("--min-samples", type=int, default=20)
    ap.add_argument("--min-accept-rate", type=float, default=0.70)
    ap.add_argument("--min-action-rate", type=float, default=0.55)
    ap.add_argument("--min-useful-rate", type=float, default=0.65)
    return ap.parse_args()


def as_bool(v) -> bool:
    if isinstance(v, bool):
        return v
    if isinstance(v, str):
        return v.strip().lower() in {"1", "true", "yes", "y"}
    if isinstance(v, (int, float)):
        return bool(v)
    return False


def ratio(n: int, d: int) -> float:
    return float(n) / float(d) if d > 0 else 0.0


def main() -> int:
    args = parse_args()
    path = Path(args.input)

    rows = []
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError:
            continue

    total = len(rows)
    accepted = sum(1 for r in rows if as_bool(r.get("accepted_without_edits")))
    actioned = sum(1 for r in rows if as_bool(r.get("action_taken")))

    useful_rows = [r for r in rows if "useful" in r]
    useful_yes = sum(1 for r in useful_rows if as_bool(r.get("useful")))

    models = Counter(str(r.get("model", "unknown")) for r in rows)

    accept_rate = ratio(accepted, total)
    action_rate = ratio(actioned, total)
    useful_rate = ratio(useful_yes, len(useful_rows)) if useful_rows else None

    checks = [
        {"name": "sample_size", "ok": total >= args.min_samples, "value": total, "min": args.min_samples},
        {"name": "accept_rate", "ok": accept_rate >= args.min_accept_rate, "value": round(accept_rate, 4), "min": args.min_accept_rate},
        {"name": "action_rate", "ok": action_rate >= args.min_action_rate, "value": round(action_rate, 4), "min": args.min_action_rate},
    ]

    if useful_rate is not None:
        checks.append(
            {
                "name": "useful_rate",
                "ok": useful_rate >= args.min_useful_rate,
                "value": round(useful_rate, 4),
                "min": args.min_useful_rate,
            }
        )

    failed = [c for c in checks if not c["ok"]]

    out = {
        "input": args.input,
        "passed": len(failed) == 0,
        "summary": {
            "samples": total,
            "accepted_without_edits": accepted,
            "accepted_without_edits_rate": round(accept_rate, 4),
            "action_taken": actioned,
            "action_taken_rate": round(action_rate, 4),
            "useful_count": useful_yes,
            "useful_rate": None if useful_rate is None else round(useful_rate, 4),
            "models": models.most_common(10),
        },
        "checks": checks,
        "failed_checks": failed,
    }

    text = json.dumps(out, indent=2)
    print(text)
    if args.output:
        Path(args.output).write_text(text)

    return 0 if out["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
