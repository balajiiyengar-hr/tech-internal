#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$ROOT/reports/loadtest}"
API_BASE="${API_BASE:-http://127.0.0.1:8080/api/v1}"
K6="${K6:-k6}"

mkdir -p "$OUT"

echo "==> health check"
curl -fsS "$API_BASE/health"
echo

echo "==> microbenchmarks (Argon2id + JWT)"
(
  cd "$ROOT"
  go test -bench=. -benchmem -count=1 ./internal/auth | tee "$OUT/microbench.txt"
)

echo "==> obtain access token for /apps"
TOKEN="$(
  curl -fsS -X POST "$API_BASE/auth/login/email" \
    -H 'Content-Type: application/json' \
    -d '{"domain":"techhr.com","identifier":"admin@techhr.com","password":"Admin@123"}' \
    | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])'
)"

run_k6() {
  local scenario="$1"
  echo "==> k6 $scenario"
  API_BASE="$API_BASE" ACCESS_TOKEN="$TOKEN" SCENARIO="$scenario" \
    "$K6" run \
      --summary-export "$OUT/${scenario}.summary.json" \
      "$ROOT/scripts/loadtest/k6_auth.js" \
    | tee "$OUT/${scenario}.txt"
}

run_k6 health_1k
run_k6 apps_1k
run_k6 login_closed
run_k6 login_1k
run_k6 login_ramp

python3 - "$OUT" <<'PY'
import json, os, sys
out = sys.argv[1]
print("\n==> summaries written to", out)
for name in ("health_1k", "apps_1k", "login_closed", "login_1k", "login_ramp"):
    path = os.path.join(out, f"{name}.summary.json")
    with open(path) as f:
        s = json.load(f)
    m = s["metrics"]
    dur = m["http_req_duration"]["values"]
    rate = m.get("http_reqs", {}).get("values", {}).get("rate")
    failed = m.get("http_req_failed", {}).get("values", {}).get("rate")
    dropped = m.get("dropped_iterations", {}).get("values", {}).get("count", 0)
    print(f"{name:14} tps={rate:8.1f}  p50={dur['med']:7.2f}ms  p95={dur['p(95)']:7.2f}ms  p99={dur['p(99)']:7.2f}ms  fail={failed:.4%}  dropped={dropped}")
PY

echo "done"
