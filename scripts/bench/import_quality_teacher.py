#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import re
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


SYSTEM_PROMPT = """You label memory chunks for an import-quality keep/drop student model.

Return EXACTLY one character:
- 1 if this memory chunk is worth storing in long-term memory
- 0 if it should be dropped at import time

Keep:
- durable project/user knowledge
- meaningful decisions
- high-signal notes
- chunks likely to help retrieval later

Drop:
- protocol noise
- boilerplate status chatter
- weak acknowledgements
- machine residue
- fragments too low-signal to justify storage
"""


def parse_args() -> argparse.Namespace:
    ap = argparse.ArgumentParser(description="Teacher-label import-quality review rows with Gemini")
    ap.add_argument("--input", required=True, help="Input review-pack JSONL")
    ap.add_argument("--output", required=True, help="Output labeled JSONL")
    ap.add_argument("--model", default="openrouter/google/gemini-2.5-flash", help="Teacher model")
    ap.add_argument("--limit", type=int, default=0, help="Optional max rows")
    return ap.parse_args()


def load_rows(path: Path, limit: int) -> list[dict[str, Any]]:
    rows = []
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        rows.append(json.loads(line))
        if limit > 0 and len(rows) >= limit:
            break
    return rows


def build_payload(model: str, row: dict[str, Any]) -> bytes:
    prompt = json.dumps(
        {
            "memory_id": row.get("memory_id"),
            "text": row.get("text", ""),
            "proxy_label_keep": row.get("label", row.get("proxy_label_keep")),
            "proxy_positive_signals": row.get("proxy_positive_signals", []),
            "proxy_negative_signals": row.get("proxy_negative_signals", []),
            "source_file": row.get("source_file", ""),
            "source_section": row.get("source_section", ""),
        },
        ensure_ascii=True,
        indent=2,
    )
    body = {
        "systemInstruction": {"parts": [{"text": SYSTEM_PROMPT}]},
        "contents": [{"role": "user", "parts": [{"text": prompt}]}],
        "generationConfig": {"temperature": 0.0, "maxOutputTokens": 16},
    }
    return json.dumps(body).encode("utf-8")


def load_openrouter_key() -> str:
    key = os.getenv("OPENROUTER_API_KEY", "").strip()
    if key:
        return key
    cfg = Path.home() / ".cortex" / "config.yaml"
    if not cfg.exists():
        return ""
    for raw_line in cfg.read_text().splitlines():
        line = raw_line.strip()
        if not line.startswith("api_key:"):
            continue
        value = line.split(":", 1)[1].strip()
        if value.startswith(("'", '"')) and value.endswith(("'", '"')) and len(value) >= 2:
            value = value[1:-1]
        return value.strip()
    return ""


def extract_text(payload: dict[str, Any]) -> str:
    candidates = payload.get("candidates") or []
    if candidates:
        parts = candidates[0].get("content", {}).get("parts", [])
        return "".join(part.get("text", "") for part in parts).strip()
    choices = payload.get("choices") or []
    if choices:
        message = choices[0].get("message", {})
        return str(message.get("content", "")).strip()
    return ""


def call_teacher(model: str, row: dict[str, Any]) -> dict[str, Any]:
    payload = build_payload(model, row)

    if model.startswith("openrouter/"):
        api_key = load_openrouter_key()
        if not api_key:
            raise SystemExit("Set OPENROUTER_API_KEY or configure api_key in ~/.cortex/config.yaml")
        actual_model = model[len("openrouter/") :]
        body = {
            "model": actual_model,
            "messages": [
                {"role": "system", "content": SYSTEM_PROMPT},
                {"role": "user", "content": json.loads(payload.decode("utf-8"))["contents"][0]["parts"][0]["text"]},
            ],
            "temperature": 0.0,
            "max_tokens": 16,
        }
        request = urllib.request.Request(
            "https://openrouter.ai/api/v1/chat/completions",
            data=json.dumps(body).encode("utf-8"),
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {api_key}",
                "HTTP-Referer": "https://github.com/hurttlocker/cortex",
                "X-Title": "Cortex Import Quality Teacher",
            },
            method="POST",
        )
    else:
        api_key = os.getenv("GOOGLE_API_KEY") or os.getenv("GEMINI_API_KEY")
        if not api_key:
            raise SystemExit("Set GOOGLE_API_KEY or GEMINI_API_KEY")
        request = urllib.request.Request(
            f"https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={api_key}",
            data=payload,
            headers={"Content-Type": "application/json"},
            method="POST",
        )

    try:
        with urllib.request.urlopen(request, timeout=90) as response:
            response_payload = json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"Teacher HTTP {exc.code}: {body}") from exc

    text = extract_text(response_payload)
    keep_raw = "".join(ch for ch in text if ch in "01")
    if keep_raw not in {"0", "1"}:
        raise RuntimeError(f"Invalid teacher output: {text!r}")
    return {
        "label_keep": int(keep_raw),
        "reason": "",
    }


def label_row(model: str, row: dict[str, Any]) -> dict[str, Any]:
    parsed = call_teacher(model, row)
    return {
        **row,
        "teacher_label_keep": int(parsed.get("label_keep", 0)),
        "teacher_reason": str(parsed.get("reason", "") or "").strip(),
        "teacher_model": model,
    }


def main() -> int:
    args = parse_args()
    rows = load_rows(Path(args.input), args.limit)
    labeled = [label_row(args.model, row) for row in rows]
    output = Path(args.output)
    output.write_text(
        "\n".join(json.dumps(row, ensure_ascii=True) for row in labeled) + ("\n" if labeled else "")
    )
    print(json.dumps({"input_rows": len(rows), "output_rows": len(labeled), "model": args.model}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
