#!/usr/bin/env python3
"""Reason Quality Eval Pack (v1).

Runs `cortex reason` across fixture prompts and computes deterministic heuristic metrics:
- grounding_score (evidence presence + relevance)
- actionability_score (clear next steps)
- contradiction_handling_score
- concise_clarity_score

Outputs both JSON and Markdown reports with pass/fail summary.
"""

from __future__ import annotations

import argparse
import json
import re
import statistics
import subprocess
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Tuple

METRICS = [
    "grounding_score",
    "actionability_score",
    "contradiction_handling_score",
    "concise_clarity_score",
]

DEFAULT_WEIGHTS: Dict[str, float] = {
    "grounding_score": 0.30,
    "actionability_score": 0.30,
    "contradiction_handling_score": 0.20,
    "concise_clarity_score": 0.20,
}

DEFAULT_MINIMUMS: Dict[str, float] = {
    "grounding_score": 0.60,
    "actionability_score": 0.60,
    "contradiction_handling_score": 0.55,
    "concise_clarity_score": 0.60,
}

DEFAULT_OVERALL_PASS = 0.70
DEFAULT_PASS_RATE = 0.75

DEFAULT_GROUNDING_TERMS = ["source", "memory", "fact", "evidence", "confidence", "provenance"]
DEFAULT_ACTION_TERMS = ["next step", "priority", "owner", "timeline", "recommend", "plan"]
DEFAULT_CONTRADICTION_TERMS = ["conflict", "contradiction", "inconsistent", "trade-off", "verify", "uncertain"]
DEFAULT_CLARITY_TERMS = ["summary", "clear", "concise", "key points", "bullets"]

STOPWORDS = {
    "the",
    "and",
    "for",
    "that",
    "with",
    "this",
    "from",
    "into",
    "what",
    "when",
    "where",
    "which",
    "should",
    "have",
    "has",
    "are",
    "was",
    "were",
    "will",
    "would",
    "your",
    "about",
    "across",
    "today",
    "week",
    "than",
    "then",
    "them",
    "they",
    "them",
    "over",
    "only",
    "also",
}

WORD_RE = re.compile(r"\b[a-zA-Z0-9][a-zA-Z0-9_\-'/]*\b")
BULLET_RE = re.compile(r"(?m)^\s*(?:[-*]|\d+\.)\s+")
HEADING_RE = re.compile(r"(?m)^\s*(?:#{1,4}\s+|\*\*[^*]+\*\*)")
SOURCE_RE = re.compile(r"(?i)(source\s*:|memory/[^\s#]+#L\d+|[\w./-]+\.(?:md|json|go|py):\d+)")
SENTENCE_SPLIT_RE = re.compile(r"[.!?]+\s+")
CONTRADICTION_HINT_RE = re.compile(r"\b(conflict|contradict|mismatch|inconsisten|trade-?off|disagree|uncertain)\b", re.IGNORECASE)
OWNER_TIME_RE = re.compile(r"\b(owner|by\s+\w+|today|tomorrow|this\s+week|next\s+week|deadline|due)\b", re.IGNORECASE)


@dataclass
class RunResult:
    returncode: int
    elapsed_ms: int
    cmd: List[str]
    stdout: str
    stderr: str
    payload: Dict[str, Any] | None = None


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run reason quality eval pack and emit JSON + Markdown reports")
    parser.add_argument("--binary", default="./cortex", help="Path to cortex binary")
    parser.add_argument("--fixture", default="tests/fixtures/reason/eval-set-v1.json", help="Fixture JSON path")
    parser.add_argument("--output-dir", default="tests/output/reason-eval", help="Directory for timestamped reports")
    parser.add_argument("--json-output", help="Optional explicit JSON output path")
    parser.add_argument("--markdown-output", help="Optional explicit Markdown output path")
    parser.add_argument("--db", help="Optional cortex DB path")
    parser.add_argument("--model", help="Model override passthrough to cortex reason")
    parser.add_argument("--embed", help="Embedding provider/model passthrough")
    parser.add_argument("--preset", help="Preset override for all prompts")
    parser.add_argument("--project", help="Project override for all prompts")
    parser.add_argument("--timeout-sec", type=int, default=300, help="Per-prompt timeout")
    parser.add_argument("--max-prompts", type=int, default=0, help="Run only first N prompts")
    parser.add_argument("--fail-on-errors", action="store_true", help="Mark suite failed when any prompt command errors")
    parser.add_argument("--print-json", action="store_true", help="Print full JSON report to stdout")
    parser.add_argument("--dry-run", action="store_true", help="Validate fixture and print execution plan without running")
    parser.add_argument("--verbose", action="store_true", help="Print progress to stderr")
    return parser.parse_args()


def clamp(value: float, lo: float = 0.0, hi: float = 1.0) -> float:
    return max(lo, min(hi, value))


def to_words(text: str) -> List[str]:
    return [w.lower() for w in WORD_RE.findall(text)]


def unique_hits(text: str, terms: Iterable[str]) -> List[str]:
    lower = text.lower()
    found = []
    for term in terms:
        t = term.lower()
        if t and t in lower:
            found.append(term)
    return sorted(set(found))


def relevance_overlap(prompt: str, response: str) -> float:
    p_tokens = [t for t in to_words(prompt) if len(t) > 3 and t not in STOPWORDS]
    if not p_tokens:
        return 0.0
    r_set = set(to_words(response))
    overlap = len({t for t in p_tokens if t in r_set})
    return clamp(overlap / float(len(set(p_tokens))))


def get_metric_signal(case: Dict[str, Any], key: str, default_terms: List[str]) -> Dict[str, Any]:
    expected = case.get("expected_signals", {})
    signal = expected.get(key, {})
    out = dict(signal)
    out.setdefault("must_include_any", default_terms)
    out.setdefault("min_hits", 1)
    return out


def metric_minimum(case: Dict[str, Any], metric: str, fixture_mins: Dict[str, float]) -> float:
    expected = case.get("expected_signals", {})
    by_metric = expected.get(metric.replace("_score", ""), {})
    if "min_score" in by_metric:
        return float(by_metric["min_score"])
    return float(fixture_mins.get(metric, DEFAULT_MINIMUMS[metric]))


def contradiction_required(case: Dict[str, Any], signal: Dict[str, Any]) -> bool:
    if "required" in signal:
        return bool(signal["required"])
    return bool(CONTRADICTION_HINT_RE.search(case.get("prompt", "")))


def score_grounding(case: Dict[str, Any], text: str, payload: Dict[str, Any]) -> Tuple[float, Dict[str, Any]]:
    signal = get_metric_signal(case, "grounding", DEFAULT_GROUNDING_TERMS)
    terms = signal["must_include_any"]
    min_hits = max(1, int(signal.get("min_hits", 1)))

    hits = unique_hits(text, terms)
    citation_hits = len(SOURCE_RE.findall(text))
    memories_used = int(payload.get("memories_used", 0) or 0)
    facts_used = int(payload.get("facts_used", 0) or 0)

    evidence_presence = 0.0
    evidence_presence += 0.50 * clamp(len(hits) / float(min_hits))
    evidence_presence += 0.25 * clamp(citation_hits / 1.0)
    evidence_presence += 0.15 * (1.0 if memories_used > 0 else 0.0)
    evidence_presence += 0.10 * (1.0 if facts_used > 0 else 0.0)

    relevance = relevance_overlap(case.get("prompt", ""), text)
    score = clamp(0.60 * evidence_presence + 0.40 * relevance)

    return score, {
        "signal_hits": hits,
        "signal_hit_count": len(hits),
        "signal_min_hits": min_hits,
        "citation_hits": citation_hits,
        "memories_used": memories_used,
        "facts_used": facts_used,
        "evidence_presence": round(evidence_presence, 4),
        "relevance": round(relevance, 4),
    }


def score_actionability(case: Dict[str, Any], text: str) -> Tuple[float, Dict[str, Any]]:
    signal = get_metric_signal(case, "actionability", DEFAULT_ACTION_TERMS)
    terms = signal["must_include_any"]
    min_hits = max(1, int(signal.get("min_hits", 1)))

    hits = unique_hits(text, terms)
    bullets = len(BULLET_RE.findall(text))
    owner_time = bool(OWNER_TIME_RE.search(text))

    term_score = clamp(len(hits) / float(min_hits))
    structure_score = 1.0 if bullets >= 3 else 0.65 if bullets >= 1 else 0.20
    ownership_score = 1.0 if owner_time else 0.0

    score = clamp(0.55 * term_score + 0.30 * structure_score + 0.15 * ownership_score)

    return score, {
        "signal_hits": hits,
        "signal_hit_count": len(hits),
        "signal_min_hits": min_hits,
        "bullet_lines": bullets,
        "owner_or_timeline_detected": owner_time,
    }


def score_contradiction(case: Dict[str, Any], text: str) -> Tuple[float, bool, Dict[str, Any]]:
    signal = get_metric_signal(case, "contradiction_handling", DEFAULT_CONTRADICTION_TERMS)
    terms = signal["must_include_any"]
    min_hits = max(1, int(signal.get("min_hits", 1)))
    required = contradiction_required(case, signal)

    if not required:
        return 1.0, False, {"required": False, "note": "not required for this prompt"}

    hits = unique_hits(text, terms)
    uncertainty_hits = unique_hits(text, ["uncertain", "cannot verify", "needs verification", "confidence"])
    resolution_hits = unique_hits(text, ["resolve", "reconcile", "verify", "supersede", "mitigation"])

    term_score = clamp(len(hits) / float(min_hits))
    uncertainty_score = clamp(len(uncertainty_hits) / 1.0)
    resolution_score = clamp(len(resolution_hits) / 1.0)

    score = clamp(0.50 * term_score + 0.25 * uncertainty_score + 0.25 * resolution_score)
    return score, True, {
        "required": True,
        "signal_hits": hits,
        "signal_hit_count": len(hits),
        "signal_min_hits": min_hits,
        "uncertainty_hits": uncertainty_hits,
        "resolution_hits": resolution_hits,
    }


def score_concise_clarity(case: Dict[str, Any], text: str) -> Tuple[float, Dict[str, Any]]:
    signal = get_metric_signal(case, "concise_clarity", DEFAULT_CLARITY_TERMS)
    terms = signal["must_include_any"]
    min_hits = max(1, int(signal.get("min_hits", 1)))
    min_words = int(signal.get("min_words", 80))
    max_words = int(signal.get("max_words", 280))

    words = to_words(text)
    word_count = len(words)
    hits = unique_hits(text, terms)

    if min_words <= word_count <= max_words:
        length_score = 1.0
    elif word_count < min_words:
        length_score = clamp(word_count / float(max(1, min_words)))
    else:
        length_score = clamp(max_words / float(max(1, word_count)))

    headings = len(HEADING_RE.findall(text))
    bullets = len(BULLET_RE.findall(text))
    structure_score = 1.0 if headings >= 1 and bullets >= 1 else 0.75 if bullets >= 1 else 0.45

    sentences = [s.strip() for s in SENTENCE_SPLIT_RE.split(text.strip()) if s.strip()]
    avg_sentence_words = statistics.fmean([len(to_words(s)) for s in sentences]) if sentences else 0.0
    sentence_score = 1.0 if avg_sentence_words and avg_sentence_words <= 22 else 0.75 if avg_sentence_words <= 30 else 0.45

    term_score = clamp(len(hits) / float(min_hits))

    score = clamp(0.35 * length_score + 0.30 * structure_score + 0.20 * sentence_score + 0.15 * term_score)
    return score, {
        "signal_hits": hits,
        "signal_hit_count": len(hits),
        "signal_min_hits": min_hits,
        "word_count": word_count,
        "min_words": min_words,
        "max_words": max_words,
        "headings": headings,
        "bullet_lines": bullets,
        "avg_sentence_words": round(avg_sentence_words, 2),
        "length_score": round(length_score, 4),
        "structure_score": round(structure_score, 4),
        "sentence_score": round(sentence_score, 4),
    }


def parse_reason_payload(stdout: str) -> Dict[str, Any]:
    try:
        return json.loads(stdout)
    except json.JSONDecodeError as exc:
        raise ValueError(f"invalid JSON from cortex reason: {exc}") from exc


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
) -> RunResult:
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

    started = datetime.now(tz=timezone.utc)
    try:
        cp = subprocess.run(cmd, text=True, capture_output=True, timeout=timeout_sec)
    except subprocess.TimeoutExpired as exc:
        elapsed = int((datetime.now(tz=timezone.utc) - started).total_seconds() * 1000)
        return RunResult(
            returncode=124,
            elapsed_ms=elapsed,
            cmd=cmd,
            stdout=exc.stdout or "",
            stderr=f"timeout after {timeout_sec}s",
            payload=None,
        )

    elapsed = int((datetime.now(tz=timezone.utc) - started).total_seconds() * 1000)
    payload = None
    if cp.returncode == 0:
        try:
            payload = parse_reason_payload(cp.stdout)
        except ValueError as exc:
            return RunResult(cp.returncode, elapsed, cmd, cp.stdout, str(exc), None)

    return RunResult(cp.returncode, elapsed, cmd, cp.stdout, cp.stderr, payload)


def evaluate_case(
    case: Dict[str, Any],
    payload: Dict[str, Any],
    mins: Dict[str, float],
    weights: Dict[str, float],
    overall_pass_score: float,
) -> Dict[str, Any]:
    content = str(payload.get("content", ""))

    grounding_score, grounding_detail = score_grounding(case, content, payload)
    actionability_score, actionability_detail = score_actionability(case, content)
    contradiction_score, contradiction_required_flag, contradiction_detail = score_contradiction(case, content)
    concise_score, concise_detail = score_concise_clarity(case, content)

    metrics = {
        "grounding_score": grounding_score,
        "actionability_score": actionability_score,
        "contradiction_handling_score": contradiction_score,
        "concise_clarity_score": concise_score,
    }

    weighted = 0.0
    weight_sum = 0.0
    for m in METRICS:
        w = float(weights.get(m, DEFAULT_WEIGHTS[m]))
        weight_sum += w
        weighted += metrics[m] * w
    overall = weighted / weight_sum if weight_sum else 0.0

    metric_failures = []
    for metric in METRICS:
        if metric == "contradiction_handling_score" and not contradiction_required_flag:
            continue
        min_score = float(mins.get(metric, DEFAULT_MINIMUMS[metric]))
        if metrics[metric] < min_score:
            metric_failures.append({"metric": metric, "score": round(metrics[metric], 4), "min": min_score})

    passed = (overall >= overall_pass_score) and not metric_failures

    return {
        "passed": passed,
        "overall_score": round(overall, 4),
        "metric_scores": {k: round(v, 4) for k, v in metrics.items()},
        "metric_failures": metric_failures,
        "details": {
            "grounding": grounding_detail,
            "actionability": actionability_detail,
            "contradiction_handling": contradiction_detail,
            "concise_clarity": concise_detail,
        },
        "reason_output_meta": {
            "provider": payload.get("provider"),
            "model": payload.get("model"),
            "tokens_in": payload.get("tokens_in"),
            "tokens_out": payload.get("tokens_out"),
            "memories_used": payload.get("memories_used"),
            "facts_used": payload.get("facts_used"),
        },
    }


def render_markdown(report: Dict[str, Any], json_path: Path, md_path: Path) -> str:
    s = report["summary"]
    lines = []
    lines.append("# Reason Quality Eval Report (v1)")
    lines.append("")
    lines.append(f"- **Generated:** {report['finished_at']}")
    lines.append(f"- **Fixture:** `{report['fixture']}`")
    lines.append(f"- **Prompts run:** {s['total_prompts']}")
    lines.append(f"- **Suite passed:** {'✅ yes' if s['passed'] else '❌ no'}")
    lines.append(f"- **Overall pass rate:** {s['pass_rate']:.2%} (threshold {s['thresholds']['pass_rate_min']:.2%})")
    lines.append(f"- **Average overall score:** {s['average_overall_score']:.4f} (threshold {s['thresholds']['overall_pass_score']:.2f})")
    lines.append("")
    lines.append("## Metric Averages")
    lines.append("")
    lines.append("| Metric | Average | Minimum | Status |")
    lines.append("|---|---:|---:|---|")
    for metric in METRICS:
        avg = s["metric_averages"].get(metric, 0.0)
        minv = s["thresholds"]["metric_minimums"].get(metric, DEFAULT_MINIMUMS[metric])
        status = "✅" if avg >= minv else "❌"
        lines.append(f"| {metric} | {avg:.4f} | {minv:.4f} | {status} |")

    lines.append("")
    lines.append("## Prompt Outcomes")
    lines.append("")
    lines.append("| ID | Category | Pass | Overall | Grounding | Actionability | Contradiction | Concise/Clarity |")
    lines.append("|---|---|---|---:|---:|---:|---:|---:|")
    for item in report["results"]:
        if "error" in item:
            lines.append(
                f"| {item['id']} | {item.get('category','-')} | ❌ | 0.0000 | 0.0000 | 0.0000 | 0.0000 | 0.0000 |"
            )
            continue
        m = item["metric_scores"]
        status = "✅" if item.get("passed") else "❌"
        lines.append(
            f"| {item['id']} | {item.get('category','-')} | {status} | {item['overall_score']:.4f} | "
            f"{m['grounding_score']:.4f} | {m['actionability_score']:.4f} | "
            f"{m['contradiction_handling_score']:.4f} | {m['concise_clarity_score']:.4f} |"
        )

    lines.append("")
    lines.append("## Artifacts")
    lines.append("")
    lines.append(f"- JSON: `{json_path}`")
    lines.append(f"- Markdown: `{md_path}`")
    lines.append("")
    lines.append("## Notes")
    lines.append("")
    lines.append("- Scores are deterministic heuristics; no additional model grading is used.")
    lines.append("- Prompt-level errors/timeouts are recorded and execution continues.")
    return "\n".join(lines) + "\n"


def load_fixture(path: Path) -> Dict[str, Any]:
    data = json.loads(path.read_text())
    prompts = data.get("prompts")
    if not isinstance(prompts, list) or not prompts:
        raise ValueError("fixture must contain a non-empty 'prompts' list")
    for idx, p in enumerate(prompts):
        if not p.get("id"):
            raise ValueError(f"prompt at index {idx} missing 'id'")
        if not p.get("prompt"):
            raise ValueError(f"prompt {p.get('id', idx)} missing 'prompt'")
        if not p.get("expected_signals"):
            raise ValueError(f"prompt {p.get('id', idx)} missing 'expected_signals'")
    return data


def main() -> int:
    args = parse_args()
    fixture_path = Path(args.fixture)
    fixture = load_fixture(fixture_path)

    prompts = fixture["prompts"]
    if args.max_prompts and args.max_prompts > 0:
        prompts = prompts[: args.max_prompts]

    if args.dry_run:
        plan = {
            "fixture": fixture.get("name", fixture_path.name),
            "prompt_count": len(prompts),
            "first_prompts": [
                {
                    "id": p["id"],
                    "category": p.get("category"),
                    "preset": args.preset or p.get("preset"),
                    "prompt": p.get("prompt"),
                }
                for p in prompts[:5]
            ],
        }
        print(json.dumps(plan, indent=2))
        return 0

    weights = dict(DEFAULT_WEIGHTS)
    weights.update(fixture.get("weights", {}))

    thresholds = fixture.get("thresholds", {})
    overall_pass_score = float(thresholds.get("overall_pass_score", DEFAULT_OVERALL_PASS))
    pass_rate_min = float(thresholds.get("pass_rate_min", DEFAULT_PASS_RATE))
    metric_mins = dict(DEFAULT_MINIMUMS)
    metric_mins.update(thresholds.get("metric_minimums", {}))

    defaults = fixture.get("defaults", {})
    default_reason_args = defaults.get("reason_args", {}) if isinstance(defaults, dict) else {}

    started_at = datetime.now(tz=timezone.utc)
    results: List[Dict[str, Any]] = []

    for idx, case in enumerate(prompts, start=1):
        if args.verbose:
            print(f"[{idx}/{len(prompts)}] {case['id']}", file=sys.stderr)

        reason_args = dict(default_reason_args)
        reason_args.update(case.get("reason_args", {}))

        run = run_reason(
            binary=args.binary,
            db=args.db,
            prompt=case["prompt"],
            preset=args.preset or case.get("preset") or defaults.get("preset"),
            project=args.project if args.project is not None else case.get("project") or defaults.get("project"),
            model=args.model or case.get("model") or defaults.get("model"),
            embed=args.embed or case.get("embed") or defaults.get("embed"),
            reason_args=reason_args,
            timeout_sec=args.timeout_sec,
        )

        base = {
            "id": case["id"],
            "category": case.get("category", ""),
            "preset": args.preset or case.get("preset") or defaults.get("preset"),
            "prompt": case["prompt"],
            "elapsed_ms": run.elapsed_ms,
            "cmd": run.cmd,
        }

        if run.returncode != 0 or run.payload is None:
            base.update(
                {
                    "passed": False,
                    "error": (run.stderr or "cortex reason failed").strip(),
                    "returncode": run.returncode,
                }
            )
            results.append(base)
            continue

        eval_result = evaluate_case(case, run.payload, metric_mins, weights, overall_pass_score)
        base.update(eval_result)
        results.append(base)

    total = len(results)
    passed = sum(1 for r in results if r.get("passed"))
    errors = sum(1 for r in results if "error" in r)
    failed = total - passed
    pass_rate = (passed / total) if total else 0.0

    metric_averages: Dict[str, float] = {}
    metric_threshold_failures: List[Dict[str, Any]] = []
    scored = [r for r in results if "metric_scores" in r]

    for metric in METRICS:
        vals = [float(r["metric_scores"][metric]) for r in scored]
        avg = statistics.fmean(vals) if vals else 0.0
        metric_averages[metric] = round(avg, 4)
        minv = float(metric_mins.get(metric, DEFAULT_MINIMUMS[metric]))
        if avg < minv:
            metric_threshold_failures.append({"metric": metric, "avg": round(avg, 4), "min": minv})

    overall_scores = [float(r.get("overall_score", 0.0)) for r in scored]
    average_overall_score = statistics.fmean(overall_scores) if overall_scores else 0.0

    suite_passed = pass_rate >= pass_rate_min and average_overall_score >= overall_pass_score and not metric_threshold_failures
    if args.fail_on_errors and errors > 0:
        suite_passed = False

    finished_at = datetime.now(tz=timezone.utc)
    report = {
        "fixture": fixture.get("name", fixture_path.name),
        "fixture_version": fixture.get("version"),
        "started_at": started_at.isoformat(),
        "finished_at": finished_at.isoformat(),
        "summary": {
            "passed": suite_passed,
            "total_prompts": total,
            "passed_prompts": passed,
            "failed_prompts": failed,
            "error_prompts": errors,
            "pass_rate": round(pass_rate, 4),
            "average_overall_score": round(average_overall_score, 4),
            "metric_averages": metric_averages,
            "metric_threshold_failures": metric_threshold_failures,
            "thresholds": {
                "overall_pass_score": overall_pass_score,
                "pass_rate_min": pass_rate_min,
                "metric_minimums": metric_mins,
                "fail_on_errors": bool(args.fail_on_errors),
            },
        },
        "results": results,
    }

    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    stamp = datetime.now(tz=timezone.utc).strftime("%Y%m%d-%H%M%S")
    json_path = Path(args.json_output) if args.json_output else output_dir / f"reason-quality-eval-{stamp}.json"
    md_path = Path(args.markdown_output) if args.markdown_output else output_dir / f"reason-quality-eval-{stamp}.md"

    json_path.write_text(json.dumps(report, indent=2) + "\n")
    md_path.write_text(render_markdown(report, json_path, md_path))

    print(
        json.dumps(
            {
                "suite_passed": suite_passed,
                "total_prompts": total,
                "pass_rate": round(pass_rate, 4),
                "average_overall_score": round(average_overall_score, 4),
                "json_report": str(json_path),
                "markdown_report": str(md_path),
            },
            indent=2,
        )
    )

    if args.print_json:
        print(json.dumps(report, indent=2))

    return 0 if suite_passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
