#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
release_checklist.sh — Validate release checklist prerequisites for a tag

Usage:
  scripts/release_checklist.sh --tag vX.Y.Z[-rcN]

Options:
  --tag <tag>                   Release tag (defaults to GITHUB_REF_NAME)
  --repo <owner/repo>           GitHub repo (defaults to GITHUB_REPOSITORY)
  --require-latest-slo-pass     Require latest completed SLO Canary run to be success
  -h, --help                    Show help

Checks:
1) tag format is valid (v<semver>[-rcN])
2) CHANGELOG contains a matching section: ## [<version>]
3) docs/releases/v<version>.md exists
4) go/no-go doc structural guard passes (offline mode)
5) deterministic runtime connectivity smoke passes
6) (optional) latest SLO Canary workflow run status is success
EOF
}

TAG="${GITHUB_REF_NAME:-}"
REPO="${GITHUB_REPOSITORY:-}"
REQUIRE_LATEST_SLO_PASS=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:-}"; shift 2 ;;
    --repo)
      REPO="${2:-}"; shift 2 ;;
    --require-latest-slo-pass)
      REQUIRE_LATEST_SLO_PASS=1; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1 ;;
  esac
done

if [[ -z "$TAG" ]]; then
  echo "ERROR: --tag is required (or set GITHUB_REF_NAME)" >&2
  exit 1
fi

if [[ ! "$TAG" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)(-rc[0-9]+)?$ ]]; then
  echo "ERROR: invalid tag format '$TAG' (expected v<major>.<minor>.<patch>[-rcN])" >&2
  exit 1
fi

VERSION="${TAG#v}"
CHANGELOG="CHANGELOG.md"
RELEASE_NOTES="docs/releases/v${VERSION}.md"

if [[ ! -f "$CHANGELOG" ]]; then
  echo "ERROR: missing $CHANGELOG" >&2
  exit 1
fi

if ! rg -q "^## \[${VERSION//./\.}\]" "$CHANGELOG"; then
  echo "ERROR: $CHANGELOG missing section for version [$VERSION]" >&2
  exit 1
fi

if [[ ! -f "$RELEASE_NOTES" ]]; then
  echo "ERROR: missing release notes file $RELEASE_NOTES" >&2
  exit 1
fi

if ! rg -q "Tag:\s*\`$TAG\`" "$RELEASE_NOTES"; then
  echo "ERROR: $RELEASE_NOTES missing exact tag line 'Tag: `$TAG`'" >&2
  exit 1
fi

scripts/ci_release_guard.sh --offline
scripts/connectivity_smoke.sh

if [[ "$REQUIRE_LATEST_SLO_PASS" -eq 1 ]]; then
  if [[ -z "$REPO" ]]; then
    echo "ERROR: --repo or GITHUB_REPOSITORY required when --require-latest-slo-pass is set" >&2
    exit 1
  fi
  if [[ -z "${GITHUB_TOKEN:-}" ]]; then
    echo "ERROR: GITHUB_TOKEN required for --require-latest-slo-pass" >&2
    exit 1
  fi

  python3 - "$REPO" <<'PY'
import json
import os
import sys
import urllib.request

repo = sys.argv[1]
token = os.environ.get("GITHUB_TOKEN", "")

url = f"https://api.github.com/repos/{repo}/actions/workflows/slo-canary.yml/runs?status=completed&per_page=10"
req = urllib.request.Request(url)
req.add_header("Accept", "application/vnd.github+json")
req.add_header("Authorization", f"Bearer {token}")
req.add_header("User-Agent", "cortex-release-checklist")

with urllib.request.urlopen(req, timeout=20) as resp:
    data = json.loads(resp.read().decode("utf-8"))

runs = data.get("workflow_runs", [])
if not runs:
    raise SystemExit("ERROR: no completed SLO Canary runs found")

latest = runs[0]
conclusion = str(latest.get("conclusion", "")).lower()
run_id = latest.get("id")
if conclusion != "success":
    raise SystemExit(f"ERROR: latest SLO Canary run {run_id} is not successful (conclusion={conclusion})")

print(f"release-checklist: latest SLO Canary run {run_id} is success")
PY
fi

echo "release-checklist: PASS (tag=$TAG, version=$VERSION)"
