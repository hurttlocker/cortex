#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
ci_release_guard.sh — CI guard for release docs and issue-status drift

Usage:
  scripts/ci_release_guard.sh [options]

Options:
  --repo <owner/repo>    GitHub repo (default: $GITHUB_REPOSITORY or hurttlocker/cortex)
  --glob <pattern>       Glob for go/no-go docs (default: docs/audits/*go-no-go*.md)
  --offline              Skip live GitHub issue-state checks
  -h, --help             Show this help

What it enforces:
1) Required sections exist in each go/no-go doc:
   - "## Gate Checklist"
   - "## Current Decision"
   - "## Owner Follow-ups"
2) Checklist lines exist under the file (at least one "- [ ]" or "- [x]")
3) Issue status lines match live GitHub issue state:
   - line format: "- [x] #123 closed ..." OR "- [ ] #124 open ..."
   - checked boxes must declare "closed"
   - unchecked boxes must declare "open"
   - declared state must equal live issue state
EOF
}

REPO="${GITHUB_REPOSITORY:-hurttlocker/cortex}"
DOC_GLOB="docs/audits/*go-no-go*.md"
OFFLINE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      REPO="${2:-}"; shift 2 ;;
    --glob)
      DOC_GLOB="${2:-}"; shift 2 ;;
    --offline)
      OFFLINE=1; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1 ;;
  esac
done

# Resolve token for live checks (unless offline)
TOKEN="${GITHUB_TOKEN:-}"
if [[ "$OFFLINE" -eq 0 && -z "$TOKEN" ]]; then
  if command -v gh >/dev/null 2>&1; then
    TOKEN="$(gh auth token 2>/dev/null || true)"
  fi
fi

if [[ "$OFFLINE" -eq 0 && -z "$TOKEN" && "${GITHUB_ACTIONS:-}" == "true" ]]; then
  echo "ERROR: GITHUB_TOKEN is required in CI for live issue-state checks" >&2
  exit 1
fi

python3 - "$REPO" "$DOC_GLOB" "$OFFLINE" "$TOKEN" <<'PY'
import glob
import json
import os
import re
import sys
import urllib.request
import urllib.error

repo, doc_glob, offline_raw, token = sys.argv[1:5]
offline = offline_raw == "1"

paths = sorted(glob.glob(doc_glob))
if not paths:
    print(f"release-guard: no go/no-go docs matched glob '{doc_glob}' (skip)")
    raise SystemExit(0)

required_sections = [
    "## Gate Checklist",
    "## Current Decision",
    "## Owner Follow-ups",
]

issue_line_re = re.compile(r"^\s*-\s*\[(?P<box>[ xX])\]\s*#(?P<num>\d+)\s+(?P<state>open|closed)\b", re.IGNORECASE | re.MULTILINE)
checklist_re = re.compile(r"^\s*-\s*\[[ xX]\]\s+", re.MULTILINE)

errors = []
checked_docs = 0
checked_issue_lines = 0


def gh_issue_state(issue_number: int) -> str:
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}"
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "cortex-ci-release-guard")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            state = str(data.get("state", "")).lower()
            if state not in {"open", "closed"}:
                return ""
            return state
    except urllib.error.HTTPError as e:
        errors.append(f"{url}: HTTP {e.code}")
        return ""
    except Exception as e:
        errors.append(f"{url}: {e}")
        return ""


for path in paths:
    checked_docs += 1
    text = open(path, "r", encoding="utf-8").read()

    for section in required_sections:
        if section not in text:
            errors.append(f"{path}: missing required section '{section}'")

    if not checklist_re.search(text):
        errors.append(f"{path}: missing checklist entries ('- [ ]' / '- [x]')")

    for m in issue_line_re.finditer(text):
        checked_issue_lines += 1
        box = m.group("box").lower()
        issue_num = int(m.group("num"))
        declared_state = m.group("state").lower()

        # Enforce checkbox/state consistency
        if box == "x" and declared_state != "closed":
            errors.append(
                f"{path}: issue #{issue_num} marked checked but declared '{declared_state}' (expected closed)"
            )
        if box == " " and declared_state != "open":
            errors.append(
                f"{path}: issue #{issue_num} marked unchecked but declared '{declared_state}' (expected open)"
            )

        if not offline:
            live_state = gh_issue_state(issue_num)
            if live_state and live_state != declared_state:
                errors.append(
                    f"{path}: issue #{issue_num} declared '{declared_state}' but live GitHub state is '{live_state}'"
                )

if errors:
    print("release-guard: FAILED")
    for err in errors:
        print(f"  - {err}")
    raise SystemExit(1)

mode = "offline" if offline else "live"
print(f"release-guard: PASS ({checked_docs} doc(s), {checked_issue_lines} issue line(s), mode={mode})")
PY
