#!/usr/bin/env bash
set -euo pipefail
VAULT="${1:-$HOME/Documents/MyVault}"

cortex export obsidian --vault "$VAULT" --clean --validate
