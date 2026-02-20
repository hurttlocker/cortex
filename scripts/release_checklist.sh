#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
release_checklist.sh — Validate release checklist prerequisites for a tag

Usage:
  scripts/release_checklist.sh --tag vX.Y.Z[-rcN]

Options:
  --tag <tag>          Release tag (defaults to GITHUB_REF_NAME)
  -h, --help           Show help

Checks:
1) tag format is valid (v<semver>[-rcN])
2) CHANGELOG contains a matching section: ## [<version>]
3) docs/releases/v<version>.md exists
4) go/no-go doc structural guard passes (offline mode)
EOF
}

TAG="${GITHUB_REF_NAME:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:-}"; shift 2 ;;
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

echo "release-checklist: PASS (tag=$TAG, version=$VERSION)"
