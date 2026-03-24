#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import math
import re
import sqlite3
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any


TOKEN_RE = re.compile(r"[A-Za-z0-9_]{2,}")
HEADER_RE = re.compile(r"(?m)^##?\s+.+$")
NUMERIC_ONLY_RE = re.compile(r"^\s*[0-9][0-9\s:./-]*\s*$")
PATH_NEGATIVE_RE = re.compile(
    r"(?i)(/logs?/|/tmp/|/cache/|/node_modules/|/target/|\.log$|\.tmp$|\.out$)"
)
PATH_POSITIVE_RE = re.compile(
    r"(?i)(README|MEMORY|DECISION|RULE|PRD|docs/|design|spec|architecture|roadmap)"
)
LOW_SIGNAL_RE = re.compile(
    r"(?i)\b(?:got it|sounds good|thank you|thanks!|okay|ok|cool|awesome|roger|copy that|noted)\b"
)
PROTOCOL_NOISE_RE = re.compile(
    r"(?i)\b(?:heartbeat|status: ok|health check|ping|pong|keepalive|session started|session ended|"
    r"connected|disconnected|retrying|ws_token|bearer token|http 200|http 201|trace_id|request_id)\b"
)
STACKTRACE_RE = re.compile(r"(?m)^\s*(?:at\s+\S+|File \".+\", line \d+|Traceback \(most recent call last\):)")


@dataclass
class ImportQualityExample:
    text: str
    proxy_label_keep: int
    proxy_score: float
    proxy_uncertainty: float
    proxy_positive_signals: list[str]
    proxy_negative_signals: list[str]
    memory_id: int
    source_file: str
    source_section: str
    project: str
    memory_class: str
    fact_count: int
    access_count: int
    search_access_count: int
    unique_agents: int
    content_len: int
    line_count: int
    digit_ratio: float
    punctuation_ratio: float
    unique_token_ratio: float
    metadata: dict[str, Any]


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Build a proxy-labeled dataset for the import quality gate")
    ap.add_argument("--db", required=True, help="Path to cortex.db")
    ap.add_argument("--output", required=True, help="Output JSONL dataset path")
    ap.add_argument("--review-output", help="Optional JSONL review pack path")
    ap.add_argument("--report-output", help="Optional JSON summary path")
    ap.add_argument("--limit", type=int, default=0, help="Optional max memories to load before sampling")
    ap.add_argument(
        "--balanced-limit",
        type=int,
        default=0,
        help="Optional balanced sample size after proxy labeling (roughly half keep / half drop)",
    )
    ap.add_argument(
        "--review-limit",
        type=int,
        default=100,
        help="How many uncertain/hard examples to include in the review pack",
    )
    ap.add_argument("--seed-keep", action="append", default=[], help="Path to a file or directory of forced-positive examples")
    ap.add_argument("--seed-drop", action="append", default=[], help="Path to a file or directory of forced-negative examples")
    return ap.parse_args()


def digit_ratio(text: str) -> float:
    if not text:
        return 0.0
    digits = sum(ch.isdigit() for ch in text)
    return digits / float(len(text))


def punctuation_ratio(text: str) -> float:
    if not text:
        return 0.0
    punct = sum(not ch.isalnum() and not ch.isspace() for ch in text)
    return punct / float(len(text))


def unique_token_ratio(text: str) -> float:
    tokens = TOKEN_RE.findall(text.lower())
    if not tokens:
        return 0.0
    return len(set(tokens)) / float(len(tokens))


def score_row(row: dict[str, Any]) -> tuple[float, list[str], list[str]]:
    text = (row["content"] or "").strip()
    source_file = row["source_file"] or ""
    source_section = row["source_section"] or ""
    memory_class = (row["memory_class"] or "").strip().lower()
    fact_count = int(row["fact_count"] or 0)
    access_count = int(row["access_count"] or 0)
    search_access_count = int(row["search_access_count"] or 0)
    unique_agents = int(row["unique_agents"] or 0)
    content_len = len(text)
    line_count = text.count("\n") + 1 if text else 0
    d_ratio = digit_ratio(text)
    p_ratio = punctuation_ratio(text)
    u_ratio = unique_token_ratio(text)

    score = 0.0
    positive: list[str] = []
    negative: list[str] = []

    if fact_count >= 3:
        score += 2.0
        positive.append("fact_count>=3")
    elif fact_count > 0:
        score += 1.0
        positive.append("fact_count>0")

    if access_count > 0:
        score += 1.5
        positive.append("access_count>0")
    if search_access_count > 0:
        score += 1.5
        positive.append("search_access_count>0")
    if unique_agents >= 2:
        score += 0.75
        positive.append("unique_agents>=2")

    if memory_class in {"decision", "rule", "identity", "preference", "config"}:
        score += 1.0
        positive.append(f"memory_class={memory_class}")
    elif memory_class in {"scratch"}:
        score -= 1.0
        negative.append("memory_class=scratch")

    if PATH_POSITIVE_RE.search(source_file):
        score += 1.0
        positive.append("path_positive")
    if source_section and len(source_section) >= 6:
        score += 0.25
        positive.append("has_source_section")

    if 80 <= content_len <= 4000:
        score += 0.5
        positive.append("content_len_good")
    if line_count >= 3:
        score += 0.25
        positive.append("line_count>=3")

    if content_len < 20:
        score -= 3.0
        negative.append("content_len<20")
    if NUMERIC_ONLY_RE.match(text):
        score -= 3.0
        negative.append("numeric_only")
    if PATH_NEGATIVE_RE.search(source_file):
        score -= 2.0
        negative.append("path_negative")
    if PROTOCOL_NOISE_RE.search(text):
        score -= 3.0
        negative.append("protocol_noise")
    if STACKTRACE_RE.search(text):
        score -= 2.5
        negative.append("stacktrace_noise")
    if LOW_SIGNAL_RE.search(text) and content_len < 140:
        score -= 1.5
        negative.append("low_signal_ack")
    if fact_count == 0 and access_count == 0 and content_len < 120:
        score -= 1.0
        negative.append("no_facts_no_access_short")
    if d_ratio > 0.35:
        score -= 1.0
        negative.append("high_digit_ratio")
    if p_ratio > 0.22:
        score -= 0.75
        negative.append("high_punctuation_ratio")
    if u_ratio < 0.35 and content_len < 200:
        score -= 0.75
        negative.append("low_unique_token_ratio")

    return score, positive, negative


def load_rows(db_path: Path, limit: int) -> list[dict[str, Any]]:
    conn = sqlite3.connect(str(db_path))
    conn.row_factory = sqlite3.Row
    try:
        query = """
        WITH fact_counts AS (
          SELECT memory_id, COUNT(*) AS fact_count
            FROM facts
           WHERE superseded_by IS NULL
           GROUP BY memory_id
        ),
        access_counts AS (
          SELECT
            f.memory_id AS memory_id,
            COUNT(fa.id) AS access_count,
            SUM(CASE WHEN fa.access_type = 'search' THEN 1 ELSE 0 END) AS search_access_count,
            COUNT(DISTINCT CASE WHEN COALESCE(fa.agent_id, '') != '' THEN fa.agent_id END) AS unique_agents
          FROM facts f
          LEFT JOIN fact_accesses_v1 fa ON fa.fact_id = f.id
          GROUP BY f.memory_id
        )
        SELECT
          m.id,
          m.content,
          COALESCE(m.source_file, '') AS source_file,
          COALESCE(m.source_section, '') AS source_section,
          COALESCE(m.project, '') AS project,
          COALESCE(m.memory_class, '') AS memory_class,
          COALESCE(m.metadata, '') AS metadata,
          COALESCE(fc.fact_count, 0) AS fact_count,
          COALESCE(ac.access_count, 0) AS access_count,
          COALESCE(ac.search_access_count, 0) AS search_access_count,
          COALESCE(ac.unique_agents, 0) AS unique_agents
        FROM memories m
        LEFT JOIN fact_counts fc ON fc.memory_id = m.id
        LEFT JOIN access_counts ac ON ac.memory_id = m.id
        WHERE m.deleted_at IS NULL
        ORDER BY m.id DESC
        """
        if limit > 0:
            query += f" LIMIT {int(limit)}"
        return [dict(row) for row in conn.execute(query)]
    finally:
        conn.close()


def build_examples(rows: list[dict[str, Any]]) -> list[ImportQualityExample]:
    examples: list[ImportQualityExample] = []
    for row in rows:
        text = (row["content"] or "").strip()
        score, positive, negative = score_row(row)
        threshold = 0.75
        metadata_raw = row.get("metadata") or ""
        try:
            metadata = json.loads(metadata_raw) if metadata_raw else {}
        except json.JSONDecodeError:
            metadata = {"_raw": metadata_raw}
        example = ImportQualityExample(
            text=text,
            proxy_label_keep=1 if score >= threshold else 0,
            proxy_score=round(score, 4),
            proxy_uncertainty=round(abs(score - threshold), 4),
            proxy_positive_signals=positive,
            proxy_negative_signals=negative,
            memory_id=int(row["id"]),
            source_file=row["source_file"] or "",
            source_section=row["source_section"] or "",
            project=row["project"] or "",
            memory_class=row["memory_class"] or "",
            fact_count=int(row["fact_count"] or 0),
            access_count=int(row["access_count"] or 0),
            search_access_count=int(row["search_access_count"] or 0),
            unique_agents=int(row["unique_agents"] or 0),
            content_len=len(text),
            line_count=text.count("\n") + 1 if text else 0,
            digit_ratio=round(digit_ratio(text), 4),
            punctuation_ratio=round(punctuation_ratio(text), 4),
            unique_token_ratio=round(unique_token_ratio(text), 4),
            metadata=metadata,
        )
        examples.append(example)
    return examples


def load_seed_examples(paths: list[str], keep: int, start_id: int) -> list[ImportQualityExample]:
    examples: list[ImportQualityExample] = []
    next_id = start_id
    for raw_path in paths:
        path = Path(raw_path)
        candidates = [path]
        if path.is_dir():
            candidates = sorted(p for p in path.iterdir() if p.is_file())
        for candidate in candidates:
            for chunk in split_seed_chunks(candidate):
                text = chunk["content"].strip()
                if not text:
                    continue
                row = {
                    "id": next_id,
                    "content": text,
                    "source_file": str(candidate),
                    "source_section": chunk["section"],
                    "project": "",
                    "memory_class": "rule" if keep else "scratch",
                    "metadata": "{}",
                    "fact_count": 1 if keep else 0,
                    "access_count": 0,
                    "search_access_count": 0,
                    "unique_agents": 0,
                }
                score, positive, negative = score_row(row)
                threshold = 0.75
                examples.append(
                    ImportQualityExample(
                        text=text,
                        proxy_label_keep=keep,
                        proxy_score=round(score, 4),
                        proxy_uncertainty=round(abs(score - threshold), 4),
                        proxy_positive_signals=positive,
                        proxy_negative_signals=negative,
                        memory_id=next_id,
                        source_file=str(candidate),
                        source_section=chunk["section"],
                        project="",
                        memory_class="rule" if keep else "scratch",
                        fact_count=1 if keep else 0,
                        access_count=0,
                        search_access_count=0,
                        unique_agents=0,
                        content_len=len(text),
                        line_count=text.count("\n") + 1 if text else 0,
                        digit_ratio=round(digit_ratio(text), 4),
                        punctuation_ratio=round(punctuation_ratio(text), 4),
                        unique_token_ratio=round(unique_token_ratio(text), 4),
                        metadata={"seed_label": "keep" if keep else "drop"},
                    )
                )
                next_id -= 1
    return examples


def split_seed_chunks(path: Path) -> list[dict[str, str]]:
    text = path.read_text()
    parts = re.split(r"\n\s*\n", text)
    section = path.stem
    chunks = []
    for part in parts:
        part = part.strip()
        if not part:
            continue
        header_match = HEADER_RE.match(part)
        if header_match:
            section = header_match.group(0).lstrip("# ").strip()
            body = part[header_match.end() :].strip()
            if body:
                chunks.append({"section": section, "content": body})
            continue
        chunks.append({"section": section, "content": part})
    return chunks


def spread_sample(rows: list[ImportQualityExample], count: int) -> list[ImportQualityExample]:
    if count <= 0 or len(rows) <= count:
        return rows
    step = len(rows) / float(count)
    out = []
    seen = set()
    for i in range(count):
        idx = min(len(rows) - 1, int(round(i * step)))
        if idx in seen:
            continue
        seen.add(idx)
        out.append(rows[idx])
    return out


def select_examples(examples: list[ImportQualityExample], balanced_limit: int) -> list[ImportQualityExample]:
    if balanced_limit <= 0 or len(examples) <= balanced_limit:
        return examples

    positives = [row for row in examples if row.proxy_label_keep == 1]
    negatives = [row for row in examples if row.proxy_label_keep == 0]
    if not positives or not negatives:
        return examples[:balanced_limit]

    pos_target = balanced_limit // 2
    neg_target = balanced_limit - pos_target
    selected = spread_sample(positives, pos_target) + spread_sample(negatives, neg_target)
    selected.sort(key=lambda row: row.memory_id, reverse=True)
    return selected


def dump_jsonl(rows: list[dict[str, Any]], output_path: Path) -> None:
    output_path.write_text(
        "\n".join(json.dumps(row, ensure_ascii=True) for row in rows) + ("\n" if rows else "")
    )


def build_review_pack(examples: list[ImportQualityExample], limit: int) -> list[dict[str, Any]]:
    sorted_rows = sorted(examples, key=lambda row: row.proxy_uncertainty)
    review = []
    for row in sorted_rows[:limit]:
        review.append(
            {
                "memory_id": row.memory_id,
                "text": row.text,
                "proxy_label_keep": row.proxy_label_keep,
                "proxy_score": row.proxy_score,
                "proxy_positive_signals": row.proxy_positive_signals,
                "proxy_negative_signals": row.proxy_negative_signals,
                "source_file": row.source_file,
                "source_section": row.source_section,
                "project": row.project,
                "memory_class": row.memory_class,
                "fact_count": row.fact_count,
                "access_count": row.access_count,
                "search_access_count": row.search_access_count,
            }
        )
    return review


def build_report(examples: list[ImportQualityExample]) -> dict[str, Any]:
    kept = sum(row.proxy_label_keep for row in examples)
    dropped = len(examples) - kept
    strong_positive = sum(1 for row in examples if row.proxy_score >= 2.0)
    strong_negative = sum(1 for row in examples if row.proxy_score <= -2.0)
    return {
        "samples": len(examples),
        "proxy_kept": kept,
        "proxy_dropped": dropped,
        "strong_positive": strong_positive,
        "strong_negative": strong_negative,
        "top_positive_signals": top_signal_counts(examples, positive=True),
        "top_negative_signals": top_signal_counts(examples, positive=False),
    }


def top_signal_counts(examples: list[ImportQualityExample], positive: bool) -> list[dict[str, Any]]:
    counts: dict[str, int] = {}
    field = "proxy_positive_signals" if positive else "proxy_negative_signals"
    for row in examples:
        for signal in getattr(row, field):
            counts[signal] = counts.get(signal, 0) + 1
    ranked = sorted(counts.items(), key=lambda item: (-item[1], item[0]))
    return [{"signal": signal, "count": count} for signal, count in ranked[:20]]


def main() -> int:
    args = parse_args()
    rows = load_rows(Path(args.db), args.limit)
    examples = build_examples(rows)
    seed_examples = []
    seed_examples.extend(load_seed_examples(args.seed_keep, keep=1, start_id=-1))
    seed_examples.extend(load_seed_examples(args.seed_drop, keep=0, start_id=-100000))
    examples.extend(seed_examples)
    examples = select_examples(examples, args.balanced_limit)
    dump_jsonl([asdict(row) for row in examples], Path(args.output))

    if args.review_output:
        dump_jsonl(build_review_pack(examples, args.review_limit), Path(args.review_output))

    report = build_report(examples)
    rendered = json.dumps(report, indent=2)
    print(rendered)
    if args.report_output:
        Path(args.report_output).write_text(rendered + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
