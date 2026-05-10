#!/usr/bin/env bash
# integ-codex-cli: end-to-end smoke test for the Codex path through the
# TokenOps proxy. Sends a chat.completions request and verifies a
# PromptEvent lands in the local events.db.
set -euo pipefail

: "${OPENAI_API_KEY:?OPENAI_API_KEY must be set (sk-...)}"
TOKENOPS_LISTEN="${TOKENOPS_LISTEN:-127.0.0.1:7878}"
OPENAI_BASE_URL="${OPENAI_BASE_URL:-http://${TOKENOPS_LISTEN}/openai/v1}"
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
  -H "Authorization: Bearer ${OPENAI_API_KEY}" \
  -H "Content-Type: application/json" \
  -H "X-Tokenops-Agent-Id: codex-smoketest" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"reply with the single word: ok"}]}' \
  "${OPENAI_BASE_URL}/chat/completions")

echo "$response" | python3 -c 'import json,sys; r=json.load(sys.stdin); assert "choices" in r, r; print("upstream ok")'

sleep 1
after=$(sqlite3 "$EVENTS_DB" "SELECT COUNT(*) FROM events WHERE type='prompt';")
echo "events after: $after"

if (( after <= before )); then
  echo "FAIL: no new prompt event recorded" >&2
  exit 1
fi
echo "PASS: codex smoke test"
