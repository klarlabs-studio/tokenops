# Claude Code CLI

Claude Code uses the Anthropic SDK under the hood, so the
`ANTHROPIC_BASE_URL` shim is enough to route every call through TokenOps.

## Setup

1. Start the daemon:

   ```bash
   TOKENOPS_STORAGE_ENABLED=true tokenops start
   ```

2. Point Claude Code at the proxy:

   ```bash
   export ANTHROPIC_BASE_URL="http://127.0.0.1:7878/anthropic"
   export ANTHROPIC_API_KEY="sk-ant-..."
   ```

3. Launch Claude Code as usual:

   ```bash
   claude
   ```

## Smoke test

```bash
./scripts/smoketest-claude-code.sh
```

Sends a one-shot Anthropic `messages` request through the proxy and
asserts a fresh PromptEvent landed in `~/.tokenops/events.db`.

## Troubleshooting

- **403 on Claude Code launch** — the proxy stripped a header. Check
  `tokenops` logs for "upstream error"; usually a stale `anthropic-version`.
- **TLS errors** — Claude Code does not ship a custom CA store. Run
  TokenOps without TLS in dev (default) or trust the local CA via
  `tokenops cert install`.
- **No events in store** — confirm `TOKENOPS_STORAGE_ENABLED=true` was
  set before `tokenops start`. Storage is opt-in.
