# Claude Code CLI

Claude Code is Anthropic's coding CLI. Routing it through TokenOps gives
you per-prompt token + spend visibility, response caching, and the
ability to replay sessions through the optimizer pipeline — all without
patching the CLI itself.

## Setup

1. Start the daemon (storage on so events persist):

   ```bash
   TOKENOPS_STORAGE_ENABLED=true tokenops start
   ```

   The daemon binds to `127.0.0.1:7878` by default.

2. Point Claude Code at the proxy:

   ```bash
   export ANTHROPIC_BASE_URL="http://127.0.0.1:7878/anthropic"
   export ANTHROPIC_API_KEY="sk-ant-..."
   ```

3. Launch Claude Code as usual:

   ```bash
   claude
   ```

   Every request now flows through TokenOps. Verify with `tokenops status`
   (the daemon should report `ready`).

## Attribution

Claude Code does not natively set TokenOps workflow / agent headers. To
get richer rollups, wrap the CLI with a tiny launcher that injects
attribution headers via an HTTP-aware reverse proxy or an MITM
helper. The simplest approach is to set the headers via a one-off cURL
during a session boundary — see `docs/sdk/anthropic.md` for the header
list.

## Smoke test

The bundled script verifies the full path end-to-end (daemon → proxy →
Anthropic) and confirms a PromptEvent landed in the local store:

```bash
./scripts/smoketest-claude-code.sh
```

The script:

1. Sends a one-shot `messages` request via the proxy.
2. Asserts the response has `id` + `content` (200 OK).
3. Counts events in `~/.tokenops/events.db` before and after; expects +1.

Set `TOKENOPS_LISTEN`, `ANTHROPIC_BASE_URL`, and `ANTHROPIC_API_KEY`
beforehand. Without an API key the script exits early with a clear
message.

## Troubleshooting

- **403 on Claude Code launch** — the proxy stripped a header. Check
  `tokenops` logs for "upstream error"; usually a stale `anthropic-version`.
- **TLS errors** — Claude Code does not ship a custom CA store. If you
  want HTTPS to the proxy, run `tokenops cert install` to add the local
  CA to your OS trust store, then set `ANTHROPIC_BASE_URL=https://...`.
- **No events in store** — confirm `TOKENOPS_STORAGE_ENABLED=true` was
  set before `tokenops start`. Storage is opt-in.
