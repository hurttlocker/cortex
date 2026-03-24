#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import random
import sqlite3
from collections import Counter
from pathlib import Path
from typing import Any


ALLOWED_TYPES = {
    "kv",
    "relationship",
    "preference",
    "temporal",
    "identity",
    "location",
    "decision",
    "state",
    "config",
    "event",
    "rule",
}


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Build a local fact-type training dataset from cortex.db")
    ap.add_argument("--db", required=True, help="Path to cortex.db")
    ap.add_argument("--output", required=True, help="Output JSONL dataset path")
    ap.add_argument("--report-output", help="Optional JSON report path")
    ap.add_argument("--review-output", help="Optional JSONL review pack path for unlabeled kv facts")
    ap.add_argument("--review-limit", type=int, default=50, help="How many unlabeled kv facts to include in review pack")
    ap.add_argument("--seed-labels", action="append", default=[], help="JSONL files with fact_id -> label overrides")
    ap.add_argument("--seed-examples", action="append", default=[], help="JSONL files with synthetic labeled examples")
    ap.add_argument("--random-seed", type=int, default=42, help="Seed for reproducible review sampling")
    return ap.parse_args()


def normalize_label(label: str) -> str:
    return str(label or "").strip().lower()


def compose_text(row: dict[str, Any]) -> str:
    parts = []
    subject = str(row.get("subject", "") or "").strip()
    predicate = str(row.get("predicate", "") or "").strip()
    obj = str(row.get("object", "") or "").strip()
    source_quote = str(row.get("source_quote", "") or "").strip()
    source_file = str(row.get("source_file", "") or "").strip()
    source_section = str(row.get("source_section", "") or "").strip()

    if subject:
        parts.append(f"subject {subject}")
    if predicate:
        parts.append(f"predicate {predicate}")
    if obj:
        parts.append(f"object {obj}")
    if source_quote and source_quote != obj:
        parts.append(f"quote {source_quote}")
    if source_file:
        parts.append(f"source_file {source_file}")
    if source_section:
        parts.append(f"source_section {source_section}")
    return "\n".join(parts)


def load_seed_labels(paths: list[str]) -> dict[int, dict[str, Any]]:
    overrides: dict[int, dict[str, Any]] = {}
    for raw_path in paths:
        path = Path(raw_path)
        for line in path.read_text().splitlines():
            line = line.strip()
            if not line:
                continue
            row = json.loads(line)
            fact_id = int(row.get("fact_id", 0))
            label = normalize_label(row.get("label", ""))
            if fact_id <= 0 or label not in ALLOWED_TYPES:
                continue
            overrides[fact_id] = row
    return overrides


def load_seed_examples(paths: list[str]) -> list[dict[str, Any]]:
    examples: list[dict[str, Any]] = []
    for raw_path in paths:
        path = Path(raw_path)
        for line in path.read_text().splitlines():
            line = line.strip()
            if not line:
                continue
            row = json.loads(line)
            label = normalize_label(row.get("label", ""))
            if label not in ALLOWED_TYPES:
                continue
            examples.append(
                {
                    "fact_id": int(row.get("fact_id", 0)),
                    "memory_id": 0,
                    "label": label,
                    "subject": "",
                    "predicate": "",
                    "object": "",
                    "source_quote": "",
                    "source_file": "",
                    "source_section": "",
                    "text": str(row.get("text", "") or "").strip(),
                    "source": "seed_example",
                    "teacher_reason": str(row.get("teacher_reason", "") or "").strip(),
                }
            )
    return examples


def load_rows(db_path: Path) -> list[dict[str, Any]]:
    conn = sqlite3.connect(str(db_path))
    conn.row_factory = sqlite3.Row
    try:
        query = """
        SELECT
          f.id AS fact_id,
          f.memory_id,
          COALESCE(f.subject, '') AS subject,
          COALESCE(f.predicate, '') AS predicate,
          COALESCE(f.object, '') AS object,
          COALESCE(f.source_quote, '') AS source_quote,
          COALESCE(f.fact_type, 'kv') AS fact_type,
          COALESCE(m.source_file, '') AS source_file,
          COALESCE(m.source_section, '') AS source_section
        FROM facts f
        LEFT JOIN memories m ON m.id = f.memory_id
        WHERE f.superseded_by IS NULL
        ORDER BY f.id ASC
        """
        return [dict(row) for row in conn.execute(query)]
    finally:
        conn.close()


def build_dataset(
    rows: list[dict[str, Any]],
    seed_labels: dict[int, dict[str, Any]],
    seed_examples: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    dataset: list[dict[str, Any]] = []
    unlabeled_kv: list[dict[str, Any]] = []

    for row in rows:
        fact_type = normalize_label(row["fact_type"])
        fact_id = int(row["fact_id"])
        seed = seed_labels.get(fact_id)

        if fact_type != "kv":
            label = fact_type
            source = "existing_type"
            teacher_reason = ""
        elif seed:
            label = normalize_label(seed.get("label", "kv"))
            source = "seed_label"
            teacher_reason = str(seed.get("teacher_reason", "") or "").strip()
        else:
            unlabeled_kv.append(
                {
                    **row,
                    "text": compose_text(row),
                }
            )
            continue

        if label not in ALLOWED_TYPES:
            continue
        dataset.append(
            {
                "fact_id": fact_id,
                "memory_id": int(row.get("memory_id", 0)),
                "label": label,
                "subject": row["subject"],
                "predicate": row["predicate"],
                "object": row["object"],
                "source_quote": row["source_quote"],
                "source_file": row["source_file"],
                "source_section": row["source_section"],
                "text": compose_text(row),
                "source": source,
                "teacher_reason": teacher_reason,
            }
        )

    dataset.extend(seed_examples)
    return dataset, unlabeled_kv


def main() -> int:
    args = parse_args()
    rows = load_rows(Path(args.db))
    seed_labels = load_seed_labels(args.seed_labels)
    seed_examples = load_seed_examples(args.seed_examples)
    dataset, unlabeled_kv = build_dataset(rows, seed_labels, seed_examples)

    Path(args.output).write_text(
        "\n".join(json.dumps(row, ensure_ascii=True) for row in dataset) + ("\n" if dataset else "")
    )

    if args.review_output:
        random.seed(args.random_seed)
        review_rows = list(unlabeled_kv)
        random.shuffle(review_rows)
        review_rows = review_rows[: args.review_limit]
        Path(args.review_output).write_text(
            "\n".join(json.dumps(row, ensure_ascii=True) for row in review_rows) + ("\n" if review_rows else "")
        )

    type_counts = Counter(row["label"] for row in dataset)
    source_counts = Counter(row["source"] for row in dataset)
    report = {
        "samples": len(dataset),
        "source_counts": dict(source_counts),
        "label_counts": dict(sorted(type_counts.items())),
        "seed_labels_applied": len(seed_labels),
        "seed_examples_loaded": len(seed_examples),
        "unlabeled_kv_available": len(unlabeled_kv),
    }
    rendered = json.dumps(report, indent=2)
    print(rendered)
    if args.report_output:
        Path(args.report_output).write_text(rendered + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
