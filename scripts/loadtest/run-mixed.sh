#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$ROOT/reports/loadtest}"
mkdir -p "$OUT"

cleanup() {
  # Safety net when k6 teardown is interrupted. Purge the V2 person aggregate
  # plus legacy backfill sources using names and identifiers reserved by k6.
  docker compose -f "$ROOT/docker-compose.yml" exec -T postgres \
    psql -U portal -d tech_internal -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
BEGIN;

CREATE TEMP TABLE load_test_persons ON COMMIT DROP AS
SELECT DISTINCT p.id
FROM persons p
LEFT JOIN login_identities i ON i.person_id = p.id
WHERE (
    LOWER(i.identifier) = 'loadmix@techhr.com'
    OR i.identifier LIKE '+1555%'
    OR i.identifier LIKE '+1999%'
    OR (
        LOWER(p.display_name) IN ('load otp user', 'load mix user', 'mix')
        AND EXISTS (
            SELECT 1
            FROM organization_memberships m
            JOIN organizations o ON o.id = m.organization_id
            WHERE m.person_id = p.id AND LOWER(o.slug) = 'techhr.com'
        )
    )
)
AND NOT EXISTS (
    SELECT 1
    FROM login_identities protected
    WHERE protected.person_id = p.id
      AND (
          LOWER(protected.identifier) IN ('admin@techhr.com', 'balaji@home-run.co')
          OR protected.identifier IN ('+919876543210', '9769974346')
      )
);

-- These foreign keys do not cascade when their identity/membership is removed.
DELETE FROM oauth_tokens
WHERE identity_id IN (
        SELECT id FROM login_identities
        WHERE person_id IN (SELECT id FROM load_test_persons)
      )
   OR membership_id IN (
        SELECT id FROM organization_memberships
        WHERE person_id IN (SELECT id FROM load_test_persons)
      )
   OR LOWER(identifier) = 'loadmix@techhr.com'
   OR identifier LIKE '+1555%'
   OR identifier LIKE '+1999%';

DELETE FROM portal_apps
WHERE section = 'Load Mix' OR name LIKE 'mix-%';

-- Prevent a future V2 migration/backfill from recreating stale test people.
DELETE FROM users
WHERE LOWER(identifier) = 'loadmix@techhr.com'
   OR identifier LIKE '+1555%'
   OR identifier LIKE '+1999%'
   OR LOWER(display_name) IN ('load otp user', 'load mix user', 'mix');

-- Cascades to memberships, membership_roles, and login_identities.
DELETE FROM persons WHERE id IN (SELECT id FROM load_test_persons);

COMMIT;
SQL
}
trap cleanup EXIT INT TERM

API_BASE="${API_BASE:-http://127.0.0.1:8080/api/v1}"
RATE="${RATE:-200}"
DURATION="${DURATION:-20s}"
TAG="mixed_${RATE}"

python3 "$ROOT/scripts/loadtest/sample-infra.py" \
  --out "$OUT/${TAG}_infra" \
  --interval "${SAMPLE_INTERVAL:-1}" \
  -- \
  env API_BASE="$API_BASE" RATE="$RATE" DURATION="$DURATION" \
  k6 run \
    --summary-export "$OUT/${TAG}.summary.json" \
    "$ROOT/scripts/loadtest/k6_mixed.js" \
  | tee "$OUT/${TAG}.txt"
