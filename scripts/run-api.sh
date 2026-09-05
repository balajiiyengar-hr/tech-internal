#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "Starting PostgreSQL and Redis..."
docker compose up -d postgres redis

echo "Waiting for database..."
for i in {1..30}; do
  if docker compose exec -T postgres pg_isready -U portal -d tech_internal >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "Waiting for Redis..."
for i in {1..30}; do
  if docker compose exec -T redis redis-cli ping >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "Starting API server on :8080"
export CORS_ORIGINS="${CORS_ORIGINS:-http://127.0.0.1:3100,http://localhost:3100}"
export DEV_LOG_OTP="${DEV_LOG_OTP:-true}"
export PASSWORD_KDF="${PASSWORD_KDF:-sha256}"
go mod tidy
go run ./cmd/server
