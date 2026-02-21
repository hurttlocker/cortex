#!/usr/bin/env python3
"""First-pass reason quality evaluation pack for `cortex reason`.

Runs a fixture of prompts against the local cortex CLI, scores each answer on:
- actionability
- factual grounding / citation behavior
- contradiction handling
- usefulness

Outputs a JSON report with per-case pass/fail and suite summary metrics.
"""

from __future__ import annotations

import argparse
import json
import re
import statistics
import subprocess
import sys
import time
from pathlib import Path
from typing import Any, Dict, List, Tuple

DIMENSIONS = [
    "actionability",
    "factual_grounding",
    "contradiction_handling",
    "usefulness",
]

METRIC_ALIASES = {
    "actionability": "actionability",
    "actionability_score": "actionability",
    "factual_grounding": "factual_grounding",
    "grounding": "factual_grounding",
    "grounding_score": "factual_grounding",
    "contradiction_handling": "contradiction_handling",
    "contradiction_handling_score": "contradiction_handling",
    "usefulness": "usefulness",
    "concise_clarity": "usefulness",
    "concise_clarity_score": "usefulness",
}

DEFAULT_WEIGHTS = {
    "actionability": 0.30,
    "factual_grounding": 0.30,
    "contradiction_handling": 0.15,
    "usefulness": 0.25,
}

DEFAULT_MIN_SCORES = {
    "actionability": 0.55,
    "factual_grounding": 0.50,
    "contradiction_handling": 0.50,
    "usefulness": 0.60,
}

ACTION_TERMS = [
    "next step",
    "next steps",
    "recommend",
    "recommendation",
    "do this",
    "ship",
    "implement",
    "fix",
    "owner",
    "timeline",
    "priority",
    "follow-up",
    "mitigation",
]

GROUNDING_TERMS = [
    "source",
    "sources",
    "fact",
    "facts",
    "memory",
    "memories",
    "confidence",
    "evidence",
    "according",
    "from",
    "based on",
    "provenance",
]

CONTRADICTION_TERMS = [
    "contradiction",
    "contradict",
    "conflict",
    "inconsistent",
    "mismatch",
    "however",
    "on the other hand",
    "trade-off",
    "uncertain",
    "cannot verify",
]

RESOLUTION_TERMS = [
    "resolve",
    "reconcile",
    "supersede",
    "verify",
    "confirm",
    "deprecate",
    "follow up",
]

USEFULNESS_TERMS = [
    "summary",
    "impact",
    "risk",
    "recommendation",
    "decision",
    "priority",
    "why",
    "because",
]

CONTRADICTION_PROMPT_HINT = re.compile(
    r"\b(conflict|contradict|inconsistent|mismatch|trade[- ]off|disagree)\b",
    re.IGNORECASE,
)

WORD_RE = re.compile(r"\b\w+\b")
BULLET_RE = re.compile(r"(?m)^\s*(?:[-*]|\d+\.)\s+")
HEADING_RE = re.compile(r"(?m)^\s*(?:#{1,3}\s+|\*\*[^*]+\*\*)")
DATE_OWNER_RE = re.compile(
    r"\b(owner|who|by\s+\w+|today|tomorrow|this week|next week|next sprint|due)\b",
    re.IGNORECASE,
)
CONFIDENCE_RE = re.compile(r"\[[01]\.\d{2}\]")
FILE_LINE_RE = re.compile(r"\b[\w./-]+\.(?:md|txt|json|yaml|yml|go|py):\d+\b", re.IGNORECASE)
NUMERIC_SPEC_RE = re.compile(r"\b\d+(?:\.\d+)?%?\b")


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Reason quality evaluation harness")
    ap.add_argument("--binary", default="./cortex", help="Path to cortex binary")
    ap.add_argument(
        "--fixture",
        default="tests/fixtures/reason/eval-set-v1.json",
        help="Reason eval fixture JSON",
    )
    ap.add_argument("--db", help="Optional cortex DB path")
    ap.add_argument("--model", help="Override model (provider/model or local model)")
    ap.add_argument("--embed", help="Optional embed provider/model")
    ap.add_argument("--preset", help="Override preset for all cases")
    ap.add_argument("--project", help="Override project for all cases")
    ap.add_argument("--max-cases", type=int, default=0, help="Run only first N cases")
    ap.add_argument("--max-prompts", type=int, default=0, help="Alias for --max-cases")
    ap.add_argument("--timeout-sec", type=int, default=240, help="Per-case timeout")
    ap.add_argument("--output", help="Optional report output path")
    ap.add_argument("--output-dir", help="Optional directory to write timestamped JSON+Markdown reports")
    ap.add_argument("--dry-run", action="store_true", help="Validate fixture and print plan only")
    ap.add_argument(
        "--case-min-overall",
        type=float,
        help="Override per-case minimum overall score threshold",
    )
    ap.add_argument(
        "--suite-min-pass-rate",
        type=float,
        help="Override suite minimum pass rate threshold",
    )
    ap.add_argument(
        "--fail-on-errors",
        action="store_true",
        help="Fail suite if any command/runtime errors occur",
    )
    ap.add_argument("--verbose", action="store_true", help="Progress logging to stderr")
    return ap.parse_args()


def normalize_metric_key(key: str) -> str:
    return METRIC_ALIASES.get(key, key)


def normalize_metric_map(values: Dict[str, Any]) -> Dict[str, Any]:
    out: Dict[str, Any] = {}
    for k, v in (values or {}).items():
        out[normalize_metric_key(str(k))] = v
    return out


def load_fixture(path: Path) -> Dict[str, Any]:
    data = json.loads(path.read_text())

    if "cases" not in data and "prompts" in data:
        data["cases"] = data["prompts"]

    if "cases" not in data or not isinstance(data["cases"], list):
        raise ValueError("fixture must contain a 'cases' array (or legacy 'prompts' array)")
    if len(data["cases"]) == 0:
        raise ValueError("fixture has no cases")

    return data


def normalize_text(s: str) -> str:
    return s.lower()


def unique_hits(text: str, terms: List[str]) -> List[str]:
    seen = []
    t = normalize_text(text)
    for term in terms:
        if term.lower() in t:
            seen.append(term)
    return sorted(set(seen))


def keyword_score(text: str, cfg: Dict[str, Any], fallback_terms: List[str]) -> Tuple[float, Dict[str, Any]]:
    terms = cfg.get("must_include_any", fallback_terms)
    min_hits = max(1, int(cfg.get("min_hits", 1)))
    hits = unique_hits(text, terms)
    score = min(1.0, len(hits) / float(min_hits))
    return score, {
        "hits": hits,
        "hit_count": len(hits),
        "min_hits": min_hits,
        "terms": terms,
    }


def to_rubric(score: float) -> int:
    if score >= 0.85:
        return 3
    if score >= 0.65:
        return 2
    if score >= 0.40:
        return 1
    return 0


def score_actionability(text: str, cfg: Dict[str, Any]) -> Dict[str, Any]:
    kw, kw_detail = keyword_score(text, cfg, ACTION_TERMS)
    t = normalize_text(text)

    bullets = len(BULLET_RE.findall(text))
    action_hits = len(unique_hits(text, ACTION_TERMS))
    has_owner_or_time = bool(DATE_OWNER_RE.search(t))

    heur = 0.0
    heur += 0.35 if bullets >= 2 else 0.20 if bullets == 1 else 0.0
    heur += 0.35 if action_hits >= 3 else 0.20 if action_hits > 0 else 0.0
    heur += 0.30 if has_owner_or_time else 0.0
    heur = min(1.0, heur)

    score = 0.60 * kw + 0.40 * heur
    return {
        "score": round(score, 4),
        "rubric_score": to_rubric(score),
        "details": {
            **kw_detail,
            "keyword_score": round(kw, 4),
            "heuristic_score": round(heur, 4),
            "bullet_lines": bullets,
            "action_term_hits": action_hits,
            "owner_or_time_detected": has_owner_or_time,
        },
    }


def score_factual_grounding(text: str, cfg: Dict[str, Any]) -> Dict[str, Any]:
    kw, kw_detail = keyword_score(text, cfg, GROUNDING_TERMS)
    t = normalize_text(text)

    confidence_tags = len(CONFIDENCE_RE.findall(text))
    file_line_mentions = len(FILE_LINE_RE.findall(text))
    provenance_hits = len(unique_hits(text, ["source", "fact", "memory", "evidence", "provenance"]))
    uncertainty_hits = len(unique_hits(text, ["confidence", "likely", "uncertain", "unknown", "cannot verify"]))

    heur = 0.0
    heur += 0.50 if (confidence_tags + file_line_mentions) >= 2 else 0.30 if (confidence_tags + file_line_mentions) >= 1 else 0.0
    heur += 0.30 if provenance_hits >= 2 else 0.15 if provenance_hits == 1 else 0.0
    heur += 0.20 if uncertainty_hits >= 1 else 0.0
    heur = min(1.0, heur)

    score = 0.65 * kw + 0.35 * heur
    return {
        "score": round(score, 4),
        "rubric_score": to_rubric(score),
        "details": {
            **kw_detail,
            "keyword_score": round(kw, 4),
            "heuristic_score": round(heur, 4),
            "confidence_tags": confidence_tags,
            "file_line_mentions": file_line_mentions,
            "provenance_hits": provenance_hits,
            "uncertainty_hits": uncertainty_hits,
            "contains_from_phrase": "from" in t,
        },
    }


def contradiction_required(prompt: str, cfg: Dict[str, Any]) -> bool:
    if "required" in cfg:
        return bool(cfg["required"])
    return bool(CONTRADICTION_PROMPT_HINT.search(prompt))


def score_contradiction_handling(prompt: str, text: str, cfg: Dict[str, Any]) -> Dict[str, Any]:
    required = contradiction_required(prompt, cfg)
    if not required:
        return {
            "score": 1.0,
            "rubric_score": 3,
            "required": False,
            "details": {"mode": "not-required"},
        }

    kw, kw_detail = keyword_score(text, cfg, CONTRADICTION_TERMS)
    contradiction_hits = len(unique_hits(text, CONTRADICTION_TERMS))
    resolution_hits = len(unique_hits(text, RESOLUTION_TERMS))
    uncertainty_hits = len(unique_hits(text, ["uncertain", "cannot verify", "unknown", "confidence"]))

    heur = 0.0
    heur += 0.50 if resolution_hits >= 1 else 0.20
    heur += 0.30 if uncertainty_hits >= 1 else 0.0
    heur += 0.20 if contradiction_hits >= 2 else 0.10 if contradiction_hits == 1 else 0.0
    heur = min(1.0, heur)

    score = 0.70 * kw + 0.30 * heur
    return {
        "score": round(score, 4),
        "rubric_score": to_rubric(score),
        "required": True,
        "details": {
            **kw_detail,
            "keyword_score": round(kw, 4),
            "heuristic_score": round(heur, 4),
            "contradiction_hits": contradiction_hits,
            "resolution_hits": resolution_hits,
            "uncertainty_hits": uncertainty_hits,
        },
    }


def score_usefulness(text: str, cfg: Dict[str, Any]) -> Dict[str, Any]:
    kw, kw_detail = keyword_score(text, cfg, USEFULNESS_TERMS)

    words = len(WORD_RE.findall(text))
    min_words = max(40, int(cfg.get("min_words", 100)))
    word_score = min(1.0, words / float(min_words))

    headings = len(HEADING_RE.findall(text))
    bullets = len(BULLET_RE.findall(text))
    specificity = len(NUMERIC_SPEC_RE.findall(text))
    summary_hits = len(unique_hits(text, ["summary", "recommend", "risk", "impact", "decision"]))

    heur = 0.0
    heur += 0.25 if headings >= 1 else 0.0
    heur += 0.25 if bullets >= 3 else 0.10 if bullets >= 1 else 0.0
    heur += 0.25 if specificity >= 2 else 0.10 if specificity >= 1 else 0.0
    heur += 0.25 if summary_hits >= 2 else 0.10 if summary_hits == 1 else 0.0
    heur = min(1.0, heur)

    score = 0.40 * word_score + 0.35 * kw + 0.25 * heur
    return {
        "score": round(score, 4),
        "rubric_score": to_rubric(score),
        "details": {
            **kw_detail,
            "keyword_score": round(kw, 4),
            "word_score": round(word_score, 4),
            "heuristic_score": round(heur, 4),
            "word_count": words,
            "min_words": min_words,
            "headings": headings,
            "bullet_lines": bullets,
            "specificity_tokens": specificity,
        },
    }


def signal_cfg(signals: Dict[str, Any], dim: str) -> Dict[str, Any]:
    candidates = [dim]
    if dim == "factual_grounding":
        candidates.extend(["grounding", "grounding_score", "factual_grounding_score"])
    elif dim == "usefulness":
        candidates.extend(["concise_clarity", "concise_clarity_score", "usefulness_score"])
    elif dim == "actionability":
        candidates.append("actionability_score")
    elif dim == "contradiction_handling":
        candidates.append("contradiction_handling_score")

    for key in candidates:
        if key in signals and isinstance(signals[key], dict):
            return signals[key]
    return {}


def evaluate_case(
    case: Dict[str, Any],
    content: str,
    weights: Dict[str, float],
    case_min_overall: float,
) -> Dict[str, Any]:
    signals = case.get("expected_signals", {})

    dims: Dict[str, Dict[str, Any]] = {}
    dims["actionability"] = score_actionability(content, signal_cfg(signals, "actionability"))
    dims["factual_grounding"] = score_factual_grounding(content, signal_cfg(signals, "factual_grounding"))
    dims["contradiction_handling"] = score_contradiction_handling(
        case.get("prompt", ""), content, signal_cfg(signals, "contradiction_handling")
    )
    dims["usefulness"] = score_usefulness(content, signal_cfg(signals, "usefulness"))

    weighted_total = 0.0
    weight_sum = 0.0
    hard_failures = []

    for dim in DIMENSIONS:
        w = float(weights.get(dim, DEFAULT_WEIGHTS[dim]))
        weight_sum += w
        weighted_total += dims[dim]["score"] * w

        cfg = signal_cfg(signals, dim)
        required = dims[dim].get("required", True)
        min_score = float(cfg.get("min_score", DEFAULT_MIN_SCORES[dim]))
        passed_dim = (not required) or (dims[dim]["score"] >= min_score)
        dims[dim]["min_score"] = min_score
        dims[dim]["passed"] = bool(passed_dim)
        if required and not passed_dim:
            hard_failures.append({"dimension": dim, "score": dims[dim]["score"], "min": min_score})

    overall = weighted_total / weight_sum if weight_sum > 0 else 0.0
    passed = overall >= case_min_overall and len(hard_failures) == 0

    return {
        "overall_score": round(overall, 4),
        "overall_rubric_score": to_rubric(overall),
        "case_min_overall": case_min_overall,
        "passed": passed,
        "hard_failures": hard_failures,
        "dimension_scores": dims,
    }


def merge_reason_args(defaults: Dict[str, Any], case: Dict[str, Any]) -> Dict[str, Any]:
    out = dict(defaults)
    out.update(case)
    return out


def render_markdown_report(report: Dict[str, Any]) -> str:
    summary = report.get("summary", {})
    lines = []
    lines.append(f"# Reason Quality Eval Report — {report.get('fixture', 'unknown')}")
    lines.append("")
    lines.append(f"- Started: `{report.get('started_at')}`")
    lines.append(f"- Finished: `{report.get('finished_at')}`")
    lines.append(f"- Suite passed: **{summary.get('passed')}**")
    lines.append(f"- Pass rate: **{summary.get('pass_rate')}**")
    lines.append(f"- Avg overall score: **{summary.get('average_overall_score')}**")
    lines.append("")
    lines.append("## Dimension averages")
    for dim, value in (summary.get("dimension_averages") or {}).items():
        lines.append(f"- `{dim}`: **{value}**")
    lines.append("")

    failures = [r for r in report.get("results", []) if not r.get("passed")]
    lines.append(f"## Failures ({len(failures)})")
    if not failures:
        lines.append("- None")
    else:
        for r in failures[:20]:
            rid = r.get("id", "unknown")
            if "error" in r:
                lines.append(f"- `{rid}` error: {r.get('error')}")
            else:
                lines.append(
                    f"- `{rid}` overall={r.get('overall_score')} hard_failures={len(r.get('hard_failures', []))}"
                )
    lines.append("")
    lines.append("## Top sample results")
    for r in report.get("results", [])[:10]:
        rid = r.get("id", "unknown")
        lines.append(f"- `{rid}` passed={r.get('passed')} overall={r.get('overall_score', 'n/a')}")

    return "\n".join(lines) + "\n"


def run_reason(
    binary: str,
    db: str | None,
    prompt: str,
    preset: str | None,
    project: str | None,
    model: str | None,
    embed: str | None,
    reason_args: Dict[str, Any],
    timeout_sec: int,
) -> Dict[str, Any]:
    cmd = [binary]
    if db:
        cmd.extend(["--db", db])

    cmd.extend(["reason", prompt, "--json"])

    if preset:
        cmd.extend(["--preset", preset])
    if project:
        cmd.extend(["--project", project])
    if model:
        cmd.extend(["--model", model])
    if embed:
        cmd.extend(["--embed", embed])

    if reason_args.get("recursive"):
        cmd.append("--recursive")
    if "max_iterations" in reason_args:
        cmd.extend(["--max-iterations", str(reason_args["max_iterations"])])
    if "max_depth" in reason_args:
        cmd.extend(["--max-depth", str(reason_args["max_depth"])])
    if "max_context" in reason_args:
        cmd.extend(["--max-context", str(reason_args["max_context"])])
    if "max_tokens" in reason_args:
        cmd.extend(["--max-tokens", str(reason_args["max_tokens"])])

    started = time.time()
    cp = subprocess.run(cmd, text=True, capture_output=True, timeout=timeout_sec)
    elapsed_ms = int((time.time() - started) * 1000)

    result: Dict[str, Any] = {
        "cmd": cmd,
        "elapsed_ms": elapsed_ms,
        "returncode": cp.returncode,
        "stdout": cp.stdout,
        "stderr": cp.stderr,
    }

    if cp.returncode != 0:
        return result

    try:
        payload = json.loads(cp.stdout)
    except json.JSONDecodeError as exc:
        result["returncode"] = 2
        result["stderr"] = f"failed to parse JSON output: {exc}"
        return result

    result["payload"] = payload
    return result


def main() -> int:
    args = parse_args()
    fixture_path = Path(args.fixture)

    fixture = load_fixture(fixture_path)
    defaults = fixture.get("defaults", {})
    fixture_reason_defaults = defaults.get("reason_args", {})

    weights = dict(DEFAULT_WEIGHTS)
    fixture_weights = fixture.get("dimension_weights", fixture.get("weights", {}))
    weights.update(normalize_metric_map(fixture_weights))

    thresholds = fixture.get("thresholds", {})
    case_floor_default = thresholds.get("case_min_overall", thresholds.get("overall_pass_score", 0.65))
    pass_rate_default = thresholds.get("suite_min_pass_rate", thresholds.get("pass_rate_min", 0.70))

    case_min_overall = (
        args.case_min_overall
        if args.case_min_overall is not None
        else float(case_floor_default)
    )
    suite_min_pass_rate = (
        args.suite_min_pass_rate
        if args.suite_min_pass_rate is not None
        else float(pass_rate_default)
    )
    suite_dim_mins_raw = thresholds.get("dimension_min_average", thresholds.get("metric_minimums", {}))
    suite_dim_mins = normalize_metric_map(dict(suite_dim_mins_raw))

    all_cases = fixture["cases"]
    max_cases = args.max_cases if args.max_cases and args.max_cases > 0 else args.max_prompts
    cases = all_cases[: max_cases] if max_cases and max_cases > 0 else all_cases

    started_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    if args.dry_run:
        report = {
            "fixture": fixture.get("name", fixture_path.name),
            "dry_run": True,
            "total_cases": len(cases),
            "sample_cases": [
                {
                    "id": c.get("id"),
                    "preset": args.preset or c.get("preset") or defaults.get("preset"),
                    "prompt": c.get("prompt") or c.get("query"),
                    "reason_args": merge_reason_args(fixture_reason_defaults, c.get("reason_args", {})),
                }
                for c in cases[:5]
            ],
            "thresholds": {
                "case_min_overall": case_min_overall,
                "suite_min_pass_rate": suite_min_pass_rate,
                "dimension_min_average": suite_dim_mins,
            },
        }
        text = json.dumps(report, indent=2)
        print(text)
        if args.output:
            Path(args.output).write_text(text)
        if args.output_dir:
            out_dir = Path(args.output_dir)
            out_dir.mkdir(parents=True, exist_ok=True)
            ts = time.strftime("%Y%m%d-%H%M%S", time.gmtime())
            (out_dir / f"reason-quality-eval-dry-run-{ts}.json").write_text(text)
        return 0

    results: List[Dict[str, Any]] = []
    dimension_rollup = {d: [] for d in DIMENSIONS}

    for idx, case in enumerate(cases, start=1):
        case_id = case.get("id", f"case-{idx:03d}")
        prompt = case.get("prompt") or case.get("query")
        if not prompt:
            results.append(
                {
                    "id": case_id,
                    "passed": False,
                    "error": "missing prompt/query",
                }
            )
            continue

        preset = args.preset or case.get("preset") or defaults.get("preset")
        project = args.project or case.get("project") or defaults.get("project")
        model = args.model or case.get("model")
        embed = args.embed or case.get("embed") or defaults.get("embed")
        reason_args = merge_reason_args(fixture_reason_defaults, case.get("reason_args", {}))

        if args.verbose:
            print(f"[{idx}/{len(cases)}] {case_id}", file=sys.stderr)

        run = run_reason(
            binary=args.binary,
            db=args.db,
            prompt=prompt,
            preset=preset,
            project=project,
            model=model,
            embed=embed,
            reason_args=reason_args,
            timeout_sec=args.timeout_sec,
        )

        if run.get("returncode") != 0:
            results.append(
                {
                    "id": case_id,
                    "prompt": prompt,
                    "passed": False,
                    "error": run.get("stderr", "reason command failed").strip(),
                    "returncode": run.get("returncode"),
                    "elapsed_ms": run.get("elapsed_ms"),
                    "cmd": run.get("cmd"),
                }
            )
            continue

        payload = run["payload"]
        content = payload.get("content") or payload.get("answer") or ""
        empty_content = isinstance(content, str) and content.strip() == "" and int(payload.get("tokens_out") or 0) > 0
        scored = evaluate_case(case, content, weights, case_min_overall)

        for dim in DIMENSIONS:
            dimension_rollup[dim].append(scored["dimension_scores"][dim]["score"])

        results.append(
            {
                "id": case_id,
                "prompt": prompt,
                "preset": preset,
                "project": project,
                "passed": scored["passed"],
                "overall_score": scored["overall_score"],
                "overall_rubric_score": scored["overall_rubric_score"],
                "case_min_overall": scored["case_min_overall"],
                "hard_failures": scored["hard_failures"],
                "dimension_scores": scored["dimension_scores"],
                "provider": payload.get("provider"),
                "model": payload.get("model"),
                "tokens_in": payload.get("tokens_in"),
                "tokens_out": payload.get("tokens_out"),
                "duration_ns": payload.get("duration"),
                "elapsed_ms": run.get("elapsed_ms"),
                "response_chars": len(content),
                "empty_content": empty_content,
            }
        )

    total = len(results)
    passed = sum(1 for r in results if r.get("passed"))
    errors = sum(1 for r in results if "error" in r)
    failed = total - passed
    pass_rate = (passed / float(total)) if total else 0.0

    avg_overall = 0.0
    scored_overall = [float(r["overall_score"]) for r in results if "overall_score" in r]
    if scored_overall:
        avg_overall = statistics.fmean(scored_overall)

    dim_avgs: Dict[str, float] = {}
    dim_threshold_failures = []
    for dim in DIMENSIONS:
        vals = dimension_rollup[dim]
        dim_avgs[dim] = statistics.fmean(vals) if vals else 0.0
        min_avg = float(suite_dim_mins.get(dim, 0.0))
        if min_avg > 0 and dim_avgs[dim] < min_avg:
            dim_threshold_failures.append(
                {
                    "dimension": dim,
                    "avg": round(dim_avgs[dim], 4),
                    "min_avg": min_avg,
                }
            )

    suite_passed = pass_rate >= suite_min_pass_rate and len(dim_threshold_failures) == 0
    if args.fail_on_errors and errors > 0:
        suite_passed = False

    finished_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    report = {
        "fixture": fixture.get("name", fixture_path.name),
        "fixture_version": fixture.get("version"),
        "started_at": started_at,
        "finished_at": finished_at,
        "summary": {
            "total_cases": total,
            "passed_cases": passed,
            "failed_cases": failed,
            "error_cases": errors,
            "pass_rate": round(pass_rate, 4),
            "average_overall_score": round(avg_overall, 4),
            "dimension_averages": {k: round(v, 4) for k, v in dim_avgs.items()},
            "thresholds": {
                "case_min_overall": case_min_overall,
                "suite_min_pass_rate": suite_min_pass_rate,
                "dimension_min_average": suite_dim_mins,
                "fail_on_errors": args.fail_on_errors,
            },
            "dimension_threshold_failures": dim_threshold_failures,
            "passed": suite_passed,
        },
        "results": results,
    }

    out_text = json.dumps(report, indent=2)
    print(out_text)

    if args.output:
        Path(args.output).write_text(out_text)

    if args.output_dir:
        out_dir = Path(args.output_dir)
        out_dir.mkdir(parents=True, exist_ok=True)
        ts = time.strftime("%Y%m%d-%H%M%S", time.gmtime())
        json_path = out_dir / f"reason-quality-eval-{ts}.json"
        md_path = out_dir / f"reason-quality-eval-{ts}.md"
        json_path.write_text(out_text)
        md_path.write_text(render_markdown_report(report))

    return 0 if suite_passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
