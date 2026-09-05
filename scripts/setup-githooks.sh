#!/usr/bin/env bash
# Use the repository coverage hook instead of Uber's system hooks (asd-cli / ussh).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

git config core.hooksPath scripts/githooks

echo "Installed repository hooks: scripts/githooks"
echo "  pre-commit -> ./scripts/test-coverage.sh (90% coverage gate)"
echo ""
echo "Uber system hooks (/opt/uber/etc/hooks, asd-cli/ussh) are bypassed for this clone."
echo "Re-run this script after cloning on a new machine."
