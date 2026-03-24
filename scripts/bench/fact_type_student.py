#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
from collections import Counter
from pathlib import Path
from typing import Any
import random

try:
    from sklearn.feature_extraction.text import TfidfVectorizer
    from sklearn.linear_model import LogisticRegression
    from sklearn.metrics import classification_report, f1_score
    from sklearn.model_selection import train_test_split
    from sklearn.pipeline import make_pipeline
except ModuleNotFoundError as exc:  # pragma: no cover
    raise SystemExit(
        "Missing dependency: scikit-learn. "
        "Install it in a venv, e.g. `python3 -m venv /tmp/fact-type-venv && "
        "source /tmp/fact-type-venv/bin/activate && pip install scikit-learn`"
    ) from exc


DATE_RE = re.compile(r"(?i)\b(?:19|20)\d{2}[-/](?:0?[1-9]|1[0-2])[-/](?:0?[1-9]|[12]\d|3[01])\b")
TIME_RE = re.compile(r"(?i)\b(?:[01]?\d|2[0-3])[:][0-5]\d(?:\s?[ap]m)?\b")
PATH_RE = re.compile(r"(?i)(?:/[\w./-]+|[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)")
COMMIT_RE = re.compile(r"\b[a-f0-9]{7,40}\b")
ENV_RE = re.compile(r"\b[A-Z][A-Z0-9_]{2,}\b")
NUMERIC_RE = re.compile(r"^\s*[-+$]?[0-9][0-9,.:/%+\- ]*\s*$")

EVENT_LEX_RE = re.compile(r"(?i)\b(?:added|built|shipped|fixed|removed|launched|crashed|merged|completed|updated|switched|validated|result|proof)\b")
RULE_LEX_RE = re.compile(r"(?i)\b(?:must|always|should|need to|run |keep |before |constraint|exit|threshold|goal|policy|rule|step)\b")
DECISION_LEX_RE = re.compile(r"(?i)\b(?:decision|decided|choose|chose|approved|confirmed|keep|parked_because|next-step)\b")
CONFIG_LEX_RE = re.compile(r"(?i)\b(?:config|env|port|flag|mode|version|theme|icon|layout|output|setting|parameter|api key)\b")
RELATIONSHIP_LEX_RE = re.compile(r"(?i)\b(?:manager|works on|co-founder|agent on|reports to|uses|manages|partner|owner)\b")
IDENTITY_LEX_RE = re.compile(r"(?i)\b(?:email|phone|dob|birthday|name|account|key restored|credential)\b")
PREFERENCE_LEX_RE = re.compile(r"(?i)\b(?:prefers|likes|dislikes|framing|favorite|wants)\b")
LOCATION_LEX_RE = re.compile(r"(?i)\b(?:path|repo|branch|file|root|folder|directory|lives at|located|in ~/|source_file)\b")
TEMPORAL_LEX_RE = re.compile(r"(?i)\b(?:today|yesterday|tomorrow|am|pm|et|deadline|expires|on feb|on mar|monday|tuesday|wednesday|thursday|friday)\b")


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Train/evaluate a tiny local fact-type classifier")
    ap.add_argument("--dataset", required=True, help="Input dataset JSONL")
    ap.add_argument("--output", help="Optional JSON report path")
    ap.add_argument("--review-output", help="Optional JSONL review pack path")
    ap.add_argument("--export-model", help="Optional JSON path for exported classifier model")
    ap.add_argument("--review-limit", type=int, default=60, help="Max heldout misclassifications to export")
    ap.add_argument("--max-features", type=int, default=16000, help="Max TF-IDF features")
    ap.add_argument("--min-df", type=int, default=2, help="Minimum document frequency")
    ap.add_argument("--top-features", type=int, default=15, help="Top features to print per class")
    ap.add_argument("--target-per-class", type=int, default=64, help="Resample each class toward this count for training")
    ap.add_argument("--random-seed", type=int, default=42, help="Sampling seed")
    return ap.parse_args()


def load_dataset(path: Path) -> list[dict[str, Any]]:
    rows = []
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        rows.append(json.loads(line))
    return rows


def sanitize_token(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "_", value.lower()).strip("_")


def runtime_feature_tokens(row: dict[str, Any]) -> list[str]:
    text = str(row.get("text", "") or "")
    subject = str(row.get("subject", "") or "")
    predicate = str(row.get("predicate", "") or "")
    obj = str(row.get("object", "") or "")
    source_quote = str(row.get("source_quote", "") or "")
    source_file = str(row.get("source_file", "") or "")
    source_section = str(row.get("source_section", "") or "")
    combined = "\n".join(part for part in [text, subject, predicate, obj, source_quote, source_file, source_section] if part)

    tokens = []
    if pred := sanitize_token(predicate):
        tokens.append(f"meta_predicate_{pred}")
    if section := sanitize_token(source_section):
        tokens.append(f"meta_source_section_{section}")
    if source_file:
        tokens.append("meta_has_source_file")
    if source_section:
        tokens.append("meta_has_source_section")
    if DATE_RE.search(combined) or TIME_RE.search(combined) or TEMPORAL_LEX_RE.search(combined):
        tokens.append("meta_temporal_like")
    if PATH_RE.search(combined):
        tokens.append("meta_path_like")
    if COMMIT_RE.search(combined):
        tokens.append("meta_commit_like")
    if ENV_RE.search(combined):
        tokens.append("meta_env_like")
    if NUMERIC_RE.match(obj):
        tokens.append("meta_numeric_object")
    if EVENT_LEX_RE.search(combined):
        tokens.append("meta_event_lex")
    if RULE_LEX_RE.search(combined):
        tokens.append("meta_rule_lex")
    if DECISION_LEX_RE.search(combined):
        tokens.append("meta_decision_lex")
    if CONFIG_LEX_RE.search(combined):
        tokens.append("meta_config_lex")
    if RELATIONSHIP_LEX_RE.search(combined):
        tokens.append("meta_relationship_lex")
    if IDENTITY_LEX_RE.search(combined):
        tokens.append("meta_identity_lex")
    if PREFERENCE_LEX_RE.search(combined):
        tokens.append("meta_preference_lex")
    if LOCATION_LEX_RE.search(combined):
        tokens.append("meta_location_lex")
    if len(obj) <= 20:
        tokens.append("meta_object_short")
    if len(obj) >= 80:
        tokens.append("meta_object_long")
    return tokens


def featurize_text(row: dict[str, Any]) -> str:
    tokens = runtime_feature_tokens(row)
    text = str(row.get("text", "") or "")
    if not tokens:
        return text
    return " ".join(tokens) + "\n" + text


def export_model(pipe: Any, path: Path, threshold: float) -> None:
    vectorizer = pipe.named_steps["tfidfvectorizer"]
    classifier = pipe.named_steps["logisticregression"]
    vocab = vectorizer.vocabulary_
    ordered_features = [None] * len(vocab)
    for feature, idx in vocab.items():
        ordered_features[idx] = feature
    payload = {
        "kind": "fact_type_classifier_v1",
        "lowercase": True,
        "ngram_range": [1, 2],
        "token_pattern": r"(?u)\b\w\w+\b",
        "features": ordered_features,
        "idf": [round(float(x), 8) for x in vectorizer.idf_],
        "classes": [str(x) for x in classifier.classes_],
        "coef": [
            [round(float(weight), 8) for weight in row]
            for row in classifier.coef_
        ],
        "intercept": [round(float(x), 8) for x in classifier.intercept_],
        "threshold": round(float(threshold), 4),
    }
    path.write_text(json.dumps(payload, indent=2) + "\n")


def build_review_pack(
    heldout_rows: list[dict[str, Any]],
    probs: list[list[float]],
    preds: list[str],
    classes: list[str],
    limit: int,
) -> list[dict[str, Any]]:
    rows = []
    class_ix = {name: idx for idx, name in enumerate(classes)}
    for idx, row in enumerate(heldout_rows):
        gold = str(row["label"])
        pred = str(preds[idx])
        probs_row = probs[idx]
        gold_prob = float(probs_row[class_ix[gold]]) if gold in class_ix else 0.0
        pred_prob = float(probs_row[class_ix[pred]]) if pred in class_ix else 0.0
        rows.append(
            {
                "fact_id": int(row.get("fact_id", 0)),
                "label": gold,
                "pred": pred,
                "pred_confidence": round(pred_prob, 4),
                "gold_confidence": round(gold_prob, 4),
                "source": row.get("source", ""),
                "subject": row.get("subject", ""),
                "predicate": row.get("predicate", ""),
                "object": row.get("object", ""),
                "source_quote": row.get("source_quote", ""),
                "text": row.get("text", ""),
            }
        )
    rows.sort(key=lambda row: (row["label"] == row["pred"], -row["pred_confidence"]))
    return rows[:limit]


def rebalance_rows(rows: list[dict[str, Any]], target_per_class: int, seed: int) -> list[dict[str, Any]]:
    if target_per_class <= 0:
        return rows
    rng = random.Random(seed)
    buckets: dict[str, list[dict[str, Any]]] = {}
    for row in rows:
        buckets.setdefault(str(row["label"]), []).append(row)

    balanced: list[dict[str, Any]] = []
    for label, bucket in sorted(buckets.items()):
        if len(bucket) >= target_per_class:
            balanced.extend(rng.sample(bucket, target_per_class))
            continue
        original_len = len(bucket)
        balanced.extend(bucket)
        while len(bucket) < target_per_class:
            bucket.append(rng.choice(bucket))
        balanced.extend(bucket[original_len:])
    rng.shuffle(balanced)
    return balanced


def main() -> int:
    args = parse_args()
    dataset = load_dataset(Path(args.dataset))
    labels = [str(row["label"]) for row in dataset]
    label_counts = Counter(labels)
    low_support = [name for name, count in label_counts.items() if count < 2]
    if low_support:
        raise SystemExit(f"Need at least 2 examples per label for stratified holdout; low-support labels: {low_support}")

    train_rows, test_rows = train_test_split(
        dataset,
        test_size=0.2,
        random_state=42,
        stratify=labels,
    )
    balanced_train_rows = rebalance_rows(list(train_rows), args.target_per_class, args.random_seed)
    train_texts = [featurize_text(row) for row in balanced_train_rows]
    train_labels = [str(row["label"]) for row in balanced_train_rows]
    test_texts = [featurize_text(row) for row in test_rows]
    test_labels = [str(row["label"]) for row in test_rows]

    pipe = make_pipeline(
        TfidfVectorizer(ngram_range=(1, 2), min_df=args.min_df, max_features=args.max_features),
        LogisticRegression(max_iter=800, class_weight="balanced", solver="liblinear", multi_class="ovr"),
    )
    pipe.fit(train_texts, train_labels)

    preds = [str(x) for x in pipe.predict(test_texts)]
    probs = pipe.predict_proba(test_texts)
    classes = [str(x) for x in pipe.named_steps["logisticregression"].classes_]

    # Refit on full dataset for export.
    full_texts = [featurize_text(row) for row in dataset]
    pipe.fit(full_texts, labels)
    classifier = pipe.named_steps["logisticregression"]
    vectorizer = pipe.named_steps["tfidfvectorizer"]
    features = vectorizer.get_feature_names_out()
    top_features: dict[str, list[dict[str, Any]]] = {}
    for class_ix, class_name in enumerate(classifier.classes_):
        weights = classifier.coef_[class_ix]
        ranked = sorted(zip(weights, features), reverse=True)
        top_features[str(class_name)] = [
            {"feature": feat, "weight": round(float(weight), 4)}
            for weight, feat in ranked[: args.top_features]
        ]

    report = classification_report(test_labels, preds, output_dict=True, zero_division=0)
    summary = {
        "samples": len(dataset),
        "train_samples": len(train_rows),
        "balanced_train_samples": len(balanced_train_rows),
        "label_counts": dict(sorted(label_counts.items())),
        "eval_split": "stratified_holdout_80_20",
        "eval_samples": len(test_labels),
        "macro_f1": round(float(f1_score(test_labels, preds, average="macro")), 4),
        "weighted_f1": round(float(f1_score(test_labels, preds, average="weighted")), 4),
        "per_class": {
            label: {
                "precision": round(float(metrics.get("precision", 0.0)), 4),
                "recall": round(float(metrics.get("recall", 0.0)), 4),
                "f1": round(float(metrics.get("f1-score", 0.0)), 4),
                "support": int(metrics.get("support", 0)),
            }
            for label, metrics in report.items()
            if label in label_counts
        },
        "top_features": top_features,
    }

    rendered = json.dumps(summary, indent=2)
    print(rendered)
    if args.output:
        Path(args.output).write_text(rendered + "\n")
    if args.review_output:
        review_rows = build_review_pack(test_rows, probs.tolist(), preds, classes, args.review_limit)
        Path(args.review_output).write_text(
            "\n".join(json.dumps(row, ensure_ascii=True) for row in review_rows) + ("\n" if review_rows else "")
        )
    if args.export_model:
        export_model(pipe, Path(args.export_model), threshold=0.45)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
