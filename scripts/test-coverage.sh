#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

MIN_COVERAGE="${MIN_COVERAGE:-90}"
REPORT_DIR="$ROOT/reports/coverage"
BASELINE="$REPORT_DIR/latest.txt"
mkdir -p "$REPORT_DIR"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
json="$work/go-test.json"
raw="$work/coverage.out"
gated="$work/coverage-gated.out"

echo "Running Go unit tests and coverage gate..."
set +e
go test -json -timeout=3m -coverprofile="$raw" ./... >"$json"
test_status=$?
set -e

python3 - "$json" "$work/result.txt" <<'PY'
import json, sys
from collections import defaultdict

events, result = sys.argv[1:]
tests = set()
packages = defaultdict(lambda: "unknown")
with open(events) as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        package = event.get("Package")
        action = event.get("Action")
        test = event.get("Test")
        if action == "pass" and test:
            tests.add((package, test))
        if package and not test and action in ("pass", "fail", "skip"):
            packages[package] = action
with open(result, "w") as out:
    out.write(f"tests={len(tests)}\n")
    for package in sorted(packages):
        out.write(f"package={package}\t{packages[package]}\n")
PY

test_count="$(awk -F= '$1=="tests"{print $2}' "$work/result.txt")"
failed_packages="$(awk -F'[\t=]' '$1=="package" && $3=="fail"{print $2}' "$work/result.txt")"
if (( test_status != 0 )); then
  echo "TEST FAILURE: go test ./... failed." >&2
  if [[ -n "$failed_packages" ]]; then
    printf 'Failing packages:\n%s\n' "$failed_packages" >&2
    if [[ -f "$BASELINE" ]]; then
      while IFS= read -r package; do
        if grep -Fq $'package='"$package"$'\tpass' "$BASELINE"; then
          echo "PACKAGE REGRESSION: previously passing package now fails: $package" >&2
        fi
      done <<<"$failed_packages"
    fi
  fi
  go test -timeout=3m ./... || true
  exit 1
fi

awk 'NR == 1 || $1 ~ /^tech-internal\/(internal|pkg)\// || $1 ~ /^tech-internal\/cmd\/server\//' "$raw" >"$gated"
coverage="$(go tool cover -func="$gated" | awk '/^total:/{gsub("%","",$3); print $3}')"
if [[ -z "$coverage" ]]; then
  echo "COVERAGE ERROR: could not calculate gated coverage." >&2
  exit 1
fi

regression=0
if [[ -f "$BASELINE" ]]; then
  old_coverage="$(awk -F= '$1=="coverage"{print $2}' "$BASELINE")"
  old_tests="$(awk -F= '$1=="tests"{print $2}' "$BASELINE")"
  if awk -v new="$coverage" -v old="$old_coverage" 'BEGIN{exit !(new+0 < old+0)}'; then
    echo "COVERAGE REGRESSION: ${old_coverage}% -> ${coverage}%" >&2
    regression=1
  fi
  if (( test_count < old_tests )); then
    echo "TEST COUNT REGRESSION: ${old_tests} -> ${test_count}" >&2
    regression=1
  fi
fi

if awk -v actual="$coverage" -v minimum="$MIN_COVERAGE" 'BEGIN{exit !(actual+0 < minimum+0)}'; then
  echo "COVERAGE BELOW FLOOR: ${coverage}% < ${MIN_COVERAGE}%" >&2
  regression=1
fi
if (( regression != 0 )); then
  exit 1
fi

{
  echo "coverage=$coverage"
  echo "tests=$test_count"
  echo "minimum=$MIN_COVERAGE"
  echo "command=./scripts/test-coverage.sh"
  awk -F= '$1 != "tests"' "$work/result.txt"
} >"$work/latest.txt"
mv "$work/latest.txt" "$BASELINE"
cp "$gated" "$REPORT_DIR/latest.out"

echo "PASS: ${coverage}% gated coverage; ${test_count} passing tests."
echo "Updated baseline: reports/coverage/latest.txt"
