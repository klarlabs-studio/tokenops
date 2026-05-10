#!/usr/bin/env bash
# integ-gemini-cli: end-to-end smoke test for the Gemini CLI path through
# the TokenOps proxy.
set -euo pipefail

: "${GEMINI_API_KEY:?GEMINI_API_KEY must be set}"
TOKENOPS_LISTEN="${TOKENOPS_LISTEN:-127.0.0.1:7878}"
GEMINI_BASE_URL="${GEMINI_BASE_URL:-http://${TOKENOPS_LISTEN}/gemini}"
GEMINI_MODEL="${GEMINI_MODEL:-gemini-1.5-pro}"
EVENTS_DB="${EVENTS_DB:-${HOME}/.tokenops/events.db}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 CLI required for event verification" >&2
  exit 2
fi
if [[ ! -f "$EVENTS_DB" ]]; then
  echo "events store not found at $EVENTS_DB; start tokenops with TOKENOPS_STORAGE_ENABLED=true" >&2
  exit 2
fi

before=$(sqlite3 "$EVENTS_DB" "SELECT COUNT(*) FROM events WHERE type='prompt';")
echo "events before: $before"

response=$(curl -fsS -X POST \
  -H "x-goog-api-key: ${GEMINI_API_KEY}" \
  -H "Content-Type: application/json" \
  -H "X-Tokenops-Agent-Id: gemini-cli-smoketest" \
  -d '{"contents":[{"parts":[{"text":"reply with the single word: ok"}]}]}' \
  "${GEMINI_BASE_URL}/v1beta/models/${GEMINI_MODEL}:generateContent")

echo "$response" | python3 -c 'import json,sys; r=json.load(sys.stdin); assert "candidates" in r, r; print("upstream ok")'

sleep 1
after=$(sqlite3 "$EVENTS_DB" "SELECT COUNT(*) FROM events WHERE type='prompt';")
echo "events after: $after"

if (( after <= before )); then
  echo "FAIL: no new prompt event recorded" >&2
  exit 1
fi
echo "PASS: gemini-cli smoke test"
