#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export UI_PORT="${UI_PORT:-3100}"
export UI_DIR="${UI_DIR:-./frontend}"
export API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:8080/api/v1}"

echo "Starting UI on :${UI_PORT} -> ${API_BASE_URL}"
go run ./cmd/web
