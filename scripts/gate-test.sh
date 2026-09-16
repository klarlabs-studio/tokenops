#!/usr/bin/env bash
# Run the race suite so that a failure names itself.
#
# warden surfaces the TAIL of a step's output, and `go test ./...` ends
# with per-package ok/FAIL lines — so the "--- FAIL: TestX" line that says
# WHICH test failed has already scrolled past. Twice this repo has refused
# a push over internal/proxy and produced no usable evidence either way
# (#263).
#
# So: run quietly, and on failure re-print the failure lines at the END,
# where the tail will show them, plus the path to the whole log.
set -uo pipefail

log="$(mktemp -t tokenops-gate-test)"
go test -race -count=1 -timeout 15m ./... >"$log" 2>&1
code=$?

if [ "$code" -eq 0 ]; then
  rm -f "$log"
  exit 0
fi

echo "--- failing tests ---"
# Cover the shapes go test uses: a failing test, a subtest, a package
# that failed to build, and a panic that killed the run before any
# "--- FAIL" was written.
grep -E '^(--- FAIL|[[:space:]]+--- FAIL|FAIL|panic:|.*\[build failed\])' "$log" | head -40

# The assertion messages sit under their test and explain the failure;
# without them the name alone often is not enough to act on.
echo
echo "--- assertion output ---"
grep -E '^[[:space:]]+[a-z_]+\.go:[0-9]+:' "$log" | head -20

echo
echo "full log: $log"
exit "$code"
