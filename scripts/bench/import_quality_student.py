#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from typing import Any

try:
    from sklearn.feature_extraction.text import TfidfVectorizer
    from sklearn.linear_model import LogisticRegression
    from sklearn.metrics import f1_score, precision_score, recall_score
    from sklearn.model_selection import train_test_split
    from sklearn.pipeline import make_pipeline
except ModuleNotFoundError as exc:  # pragma: no cover
    raise SystemExit(
        "Missing dependency: scikit-learn. "
        "Install it in a venv, e.g. `python3 -m venv /tmp/import-quality-venv && "
        "source /tmp/import-quality-venv/bin/activate && pip install scikit-learn`"
    ) from exc


PATH_POSITIVE_RE = re.compile(r"(?i)(README|MEMORY|DECISION|RULE|PRD|docs/|design|spec|architecture|roadmap)")
PATH_NEGATIVE_RE = re.compile(r"(?i)(/logs?/|/tmp/|/cache/|/node_modules/|/target/|\.log$|\.tmp$|\.out$)")
LOW_SIGNAL_RE = re.compile(r"(?i)\b(?:got it|sounds good|thank you|thanks!|okay|ok|cool|awesome|roger|copy that|noted)\b")
PROTOCOL_NOISE_RE = re.compile(
    r"(?i)\b(?:heartbeat|status: ok|health check|ping|pong|keepalive|session started|session ended|"
    r"connected|disconnected|retrying|ws_token|bearer token|http 200|http 201|trace_id|request_id)\b"
)
CLI_COMMAND_RE = re.compile(r"(?i)\b(?:git|go|cortex|openclaw|npm|python3|pip|ollama|gh)\b")
OPS_KEYWORD_RE = re.compile(r"(?i)\b(?:gateway|restart|status|import|search|test|build|push|pull|deploy|alert|maintenance|db_size_high)\b")
ENDPOINT_RE = re.compile(r"(?i)(?:/[a-z0-9._~!$&'()*+,;=:@%/-]+|[A-Z_]{3,}=)")


def runtime_feature_tokens(row: dict[str, Any]) -> list[str]:
    text = str(row.get("text", "") or "")
    source_file = str(row.get("source_file", "") or "")
    source_section = str(row.get("source_section", "") or "")
    memory_class = str(row.get("memory_class", "") or "").strip().lower()
    content_len = len(text)
    line_count = text.count("\n") + 1 if text else 0

    tokens = []
    if source_section:
        tokens.append("meta_has_source_section")
    if PATH_POSITIVE_RE.search(source_file):
        tokens.append("meta_path_positive")
    if PATH_NEGATIVE_RE.search(source_file):
        tokens.append("meta_path_negative")
    if memory_class:
        safe = re.sub(r"[^a-z0-9]+", "_", memory_class).strip("_")
        if safe:
            tokens.append(f"meta_memory_class_{safe}")
    if content_len < 20:
        tokens.append("meta_len_tiny")
    elif content_len < 120:
        tokens.append("meta_len_short")
    elif content_len <= 4000:
        tokens.append("meta_len_medium")
    else:
        tokens.append("meta_len_long")
    if line_count >= 3:
        tokens.append("meta_multiline")
    if PROTOCOL_NOISE_RE.search(text):
        tokens.append("meta_protocol_noise")
    if LOW_SIGNAL_RE.search(text) and content_len < 140:
        tokens.append("meta_low_signal_ack")
    if CLI_COMMAND_RE.search(text):
        tokens.append("meta_cli_command")
    if OPS_KEYWORD_RE.search(text):
        tokens.append("meta_ops_keyword")
    if ENDPOINT_RE.search(text):
        tokens.append("meta_endpoint_or_config")
    return tokens


def featurize_text(row: dict[str, Any]) -> str:
    tokens = runtime_feature_tokens(row)
    text = str(row.get("text", "") or "")
    if not tokens:
        return text
    return " ".join(tokens) + "\n" + text


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Train/evaluate a tiny import quality keep/drop student model")
    ap.add_argument("--dataset", required=True, help="Input dataset JSONL")
    ap.add_argument("--output", help="Optional JSON report path")
    ap.add_argument("--review-output", help="Optional JSONL review pack path")
    ap.add_argument("--teacher-labels", help="Optional JSONL teacher label overrides")
    ap.add_argument("--export-model", help="Optional JSON path for exported keep/drop model")
    ap.add_argument("--review-limit", type=int, default=50, help="Max uncertain examples in review pack")
    ap.add_argument("--top-features", type=int, default=20, help="How many strongest features to print")
    ap.add_argument("--max-features", type=int, default=10000, help="Max TF-IDF feature count")
    ap.add_argument("--min-df", type=int, default=2, help="Minimum document frequency for features")
    return ap.parse_args()


def load_dataset(path: Path) -> list[dict[str, Any]]:
    rows = []
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        rows.append(json.loads(line))
    return rows


def load_teacher_overrides(path: Path) -> dict[int, dict[str, Any]]:
    overrides: dict[int, dict[str, Any]] = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        memory_id = int(row.get("memory_id", 0))
        if memory_id <= 0:
            continue
        overrides[memory_id] = row
    return overrides


def apply_teacher_overrides(dataset: list[dict[str, Any]], overrides: dict[int, dict[str, Any]]) -> int:
    applied = 0
    for row in dataset:
        memory_id = int(row.get("memory_id", 0))
        override = overrides.get(memory_id)
        if not override:
            continue
        if "teacher_label_keep" in override:
            row["proxy_label_keep"] = int(override["teacher_label_keep"])
        if "teacher_reason" in override:
            row["teacher_reason"] = str(override["teacher_reason"] or "").strip()
        applied += 1
    return applied


def best_threshold(pipe: Any, dataset: list[dict[str, Any]]) -> float:
    texts = [featurize_text(row) for row in dataset]
    labels = [int(row["proxy_label_keep"]) for row in dataset]
    probs = pipe.predict_proba(texts)[:, 1]
    best = (0.5, -1.0)
    for raw in range(20, 91):
        threshold = raw / 100.0
        preds = [1 if p >= threshold else 0 for p in probs]
        score = f1_score(labels, preds)
        if score > best[1]:
            best = (threshold, score)
    return best[0]


def export_model(pipe: Any, dataset: list[dict[str, Any]], path: Path) -> None:
    vectorizer = pipe.named_steps["tfidfvectorizer"]
    classifier = pipe.named_steps["logisticregression"]
    vocab = vectorizer.vocabulary_
    ordered_features = [None] * len(vocab)
    for feature, idx in vocab.items():
        ordered_features[idx] = feature
    threshold = min(best_threshold(pipe, dataset), 0.45)
    payload = {
        "kind": "import_quality_keepdrop_v1",
        "lowercase": True,
        "ngram_range": [1, 2],
        "token_pattern": r"(?u)\b\w\w+\b",
        "features": ordered_features,
        "idf": [round(float(x), 8) for x in vectorizer.idf_],
        "coef": [round(float(x), 8) for x in classifier.coef_[0]],
        "intercept": round(float(classifier.intercept_[0]), 8),
        "threshold": round(float(threshold), 4),
    }
    path.write_text(json.dumps(payload, indent=2) + "\n")


def build_review_pack(dataset: list[dict[str, Any]], probs: list[float], preds: list[int], limit: int) -> list[dict[str, Any]]:
    rows = []
    for idx, row in enumerate(dataset):
        item = {
            "memory_id": int(row.get("memory_id", 0)),
            "text": row.get("text", ""),
            "label": int(row["proxy_label_keep"]),
            "pred": int(preds[idx]),
            "prob_keep": round(float(probs[idx]), 4),
            "uncertainty": round(abs(float(probs[idx]) - 0.5), 4),
            "proxy_positive_signals": row.get("proxy_positive_signals", []),
            "proxy_negative_signals": row.get("proxy_negative_signals", []),
            "source_file": row.get("source_file", ""),
            "source_section": row.get("source_section", ""),
        }
        if int(preds[idx]) != int(row["proxy_label_keep"]):
            item["kind"] = "error"
            rows.append(item)
        else:
            item["kind"] = "uncertain"
            rows.append(item)

    rows.sort(key=lambda item: (item["kind"] != "error", item["uncertainty"]))
    deduped = []
    seen = set()
    for row in rows:
        memory_id = row["memory_id"]
        if memory_id in seen:
            continue
        seen.add(memory_id)
        deduped.append(row)
        if len(deduped) >= limit:
            break
    return deduped


def select_matching_rows(dataset: list[dict[str, Any]], texts: list[str], labels: list[int]) -> list[dict[str, Any]]:
    indexed: dict[tuple[str, int], list[dict[str, Any]]] = {}
    for row in dataset:
        key = (str(row["text"]), int(row["proxy_label_keep"]))
        indexed.setdefault(key, []).append(row)

    out: list[dict[str, Any]] = []
    for text, label in zip(texts, labels):
        key = (text, int(label))
        bucket = indexed.get(key) or []
        if bucket:
            out.append(bucket.pop())
    return out


def main() -> int:
    args = parse_args()
    dataset = load_dataset(Path(args.dataset))
    teacher_rows_applied = 0
    if args.teacher_labels:
        overrides = load_teacher_overrides(Path(args.teacher_labels))
        teacher_rows_applied = apply_teacher_overrides(dataset, overrides)

    texts = [featurize_text(row) for row in dataset]
    labels = [int(row["proxy_label_keep"]) for row in dataset]
    positives = sum(labels)
    negatives = len(labels) - positives
    if positives == 0 or negatives == 0:
        raise SystemExit(
            f"Need both classes for training; got positives={positives} negatives={negatives}"
        )

    pipe = make_pipeline(
        TfidfVectorizer(ngram_range=(1, 2), min_df=args.min_df, max_features=args.max_features),
        LogisticRegression(max_iter=500, class_weight="balanced", solver="liblinear"),
    )
    train_texts, test_texts, train_labels, test_labels = train_test_split(
        texts, labels, test_size=0.2, random_state=42, stratify=labels
    )
    pipe.fit(train_texts, train_labels)
    preds = pipe.predict(test_texts)
    probs = pipe.predict_proba(test_texts)[:, 1]

    # Refit on the full dataset for model export after evaluation.
    pipe.fit(texts, labels)

    vectorizer = pipe.named_steps["tfidfvectorizer"]
    classifier = pipe.named_steps["logisticregression"]
    features = vectorizer.get_feature_names_out()
    weights = classifier.coef_[0]
    ranked = sorted(zip(weights, features))

    report = {
        "samples": len(dataset),
        "positives": positives,
        "negatives": negatives,
        "teacher_rows_applied": teacher_rows_applied,
        "eval_split": "stratified_holdout_80_20",
        "eval_samples": len(test_labels),
        "f1": round(float(f1_score(test_labels, preds)), 4),
        "precision": round(float(precision_score(test_labels, preds)), 4),
        "recall": round(float(recall_score(test_labels, preds)), 4),
        "top_negative_features": [
            {"feature": feat, "weight": round(float(weight), 4)}
            for weight, feat in ranked[: args.top_features]
        ],
        "top_positive_features": [
            {"feature": feat, "weight": round(float(weight), 4)}
            for weight, feat in ranked[-args.top_features :]
        ],
    }

    rendered = json.dumps(report, indent=2)
    print(rendered)
    if args.output:
        Path(args.output).write_text(rendered + "\n")
    if args.review_output:
        heldout_rows = select_matching_rows(dataset, list(test_texts), list(test_labels))
        rows = build_review_pack(heldout_rows, list(probs), list(preds), args.review_limit)
        Path(args.review_output).write_text(
            "\n".join(json.dumps(row, ensure_ascii=True) for row in rows) + ("\n" if rows else "")
        )
    if args.export_model:
        export_model(pipe, dataset, Path(args.export_model))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
