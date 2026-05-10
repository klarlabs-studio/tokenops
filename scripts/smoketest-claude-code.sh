#!/usr/bin/env bash
# integ-claude-code: end-to-end smoke test for the Claude Code path
# through the TokenOps proxy. Sends a single non-streaming Anthropic
# messages request and verifies a PromptEvent landed in the local
# events.db.
set -euo pipefail

: "${ANTHROPIC_API_KEY:?ANTHROPIC_API_KEY must be set (sk-ant-...)}"
TOKENOPS_LISTEN="${TOKENOPS_LISTEN:-127.0.0.1:7878}"
ANTHROPIC_BASE_URL="${ANTHROPIC_BASE_URL:-http://${TOKENOPS_LISTEN}/anthropic}"
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
  -H "x-api-key: ${ANTHROPIC_API_KEY}" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -H "X-Tokenops-Agent-Id: claude-code-smoketest" \
  -d '{"model":"claude-sonnet-4-6","max_tokens":32,"messages":[{"role":"user","content":"reply with the single word: ok"}]}' \
  "${ANTHROPIC_BASE_URL}/v1/messages")

echo "$response" | python3 -c 'import json,sys; r=json.load(sys.stdin); assert "content" in r, r; print("upstream ok")'

# Wait briefly for the async bus to drain into sqlite.
sleep 1
after=$(sqlite3 "$EVENTS_DB" "SELECT COUNT(*) FROM events WHERE type='prompt';")
echo "events after: $after"

if (( after <= before )); then
  echo "FAIL: no new prompt event recorded" >&2
  exit 1
fi
echo "PASS: claude-code smoke test"
