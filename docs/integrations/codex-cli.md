# Codex CLI

OpenAI's Codex CLI uses the OpenAI SDK under the hood, so the
`OPENAI_BASE_URL` shim is enough to route every call through TokenOps.

## Setup

1. Start the daemon:

   ```bash
   TOKENOPS_STORAGE_ENABLED=true tokenops start
   ```

2. Point Codex at the proxy:

   ```bash
   export OPENAI_API_KEY="sk-..."
   export OPENAI_BASE_URL="http://127.0.0.1:7878/openai/v1"
   ```

3. Run Codex:

   ```bash
   codex
   ```

## Attribution

Set workflow / agent headers per session by exporting them before launch:

```bash
export OPENAI_DEFAULT_HEADERS='{"X-Tokenops-Workflow-Id":"codex-session","X-Tokenops-Agent-Id":"codex"}'
```

Codex respects `OPENAI_DEFAULT_HEADERS` (the standard SDK escape hatch).

## Smoke test

```bash
./scripts/smoketest-codex.sh
```

Sends a one-shot `chat.completions` request through the proxy and asserts
a fresh PromptEvent landed in `~/.tokenops/events.db`.

## Troubleshooting

- **401 unauthorized** — `OPENAI_API_KEY` was not exported in the same
  shell that launched Codex.
- **Empty event store** — daemon started without
  `TOKENOPS_STORAGE_ENABLED=true`; restart with the flag.
