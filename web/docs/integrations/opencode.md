# opencode

opencode integrates with TokenOps on three planes — pick as many as you want.

## Passive reader (zero wiring)

opencode stores sessions in a local SQLite database. TokenOps reads it
read-only and attributes every assistant turn per project and per session —
no proxy, no keys, no change to how you run opencode.

```bash
tokenops vendor-usage enable opencode      # reads ~/.local/share/opencode/opencode.db
tokenops start                             # restart to pick up the poller
tokenops vendor-usage status               # confirm the source is emitting
```

The reader is multi-provider: whichever model/provider a session used
(anthropic, openai, github-copilot, google, openrouter, or anything else
opencode routes to) is attributed correctly. Use `--root` to point at a
non-default database path.

## MCP tools (agent-side)

opencode is an MCP host, so it can call TokenOps directly:

```jsonc
// opencode.json
{ "mcp": { "tokenops": { "command": "tokenops", "args": ["serve"] } } }
```

The agent can then ask for `tokenops_session_budget`, `tokenops_burn_rate`,
`tokenops_plan_headroom`, and call `tokenops_status` to discover what data
sources are live and how to upgrade signal quality.

## Proxy (ground-truth metering)

Point opencode's provider base URL at the TokenOps proxy to meter every
request/response with exact token counts and live optimization. For an
OpenAI-compatible provider:

```bash
TOKENOPS_STORAGE_ENABLED=true tokenops start
```

Set the provider's `baseURL` in `opencode.json` to
`http://127.0.0.1:7878/<provider>/v1` (e.g. `/openai/v1`, `/openrouter/v1`,
`/groq/v1`). See [Coverage](/integrations/coverage) for the full provider list.
