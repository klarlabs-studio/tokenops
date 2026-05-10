# Codex CLI

Codex uses the OpenAI SDK under the hood, so the `OPENAI_BASE_URL`
shim is enough.

## Setup

```bash
TOKENOPS_STORAGE_ENABLED=true tokenops start
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="http://127.0.0.1:7878/openai/v1"
codex
```

## Attribution

```bash
export OPENAI_DEFAULT_HEADERS='{"X-Tokenops-Workflow-Id":"codex-session","X-Tokenops-Agent-Id":"codex"}'
```

## Smoke test

```bash
./scripts/smoketest-codex.sh
```
