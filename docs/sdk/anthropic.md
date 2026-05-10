# Anthropic SDK shim

Point the Anthropic SDK at the TokenOps proxy by overriding the base URL.
The `x-api-key` and `anthropic-version` headers are forwarded verbatim to
`https://api.anthropic.com`. Streaming (`text/event-stream`) is passed
through with per-chunk flush.

## Proxy URL layout

The proxy mounts the Anthropic surface under `/anthropic/`. The Anthropic
SDKs append `/v1/messages` (and other `/v1/...` paths), so set the base URL
to:

```
http://127.0.0.1:9000/anthropic
```

If TokenOps was configured with a different listen address or TLS, swap
the host/scheme accordingly. Trust the TokenOps CA before using `https://`
(see `tokenops cert install`).

## Python (`anthropic` package)

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export ANTHROPIC_BASE_URL="http://127.0.0.1:9000/anthropic"
```

Or in code:

```python
from anthropic import Anthropic

client = Anthropic(base_url="http://127.0.0.1:9000/anthropic")
msg = client.messages.create(
    model="claude-sonnet-4-6",
    max_tokens=512,
    messages=[{"role": "user", "content": "Hello"}],
)
```

Streaming via `client.messages.stream(...)` works without changes.

## Node (`@anthropic-ai/sdk` package)

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export ANTHROPIC_BASE_URL="http://127.0.0.1:9000/anthropic"
```

Or in code:

```ts
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  baseURL: "http://127.0.0.1:9000/anthropic",
});

const stream = await client.messages.stream({
  model: "claude-sonnet-4-6",
  max_tokens: 512,
  messages: [{ role: "user", content: "Hello" }],
});

for await (const event of stream) {
  // ...
}
```

## Attribution headers

| Header                       | Purpose                       |
|------------------------------|-------------------------------|
| `X-Tokenops-Workflow-Id`     | groups multi-step pipelines   |
| `X-Tokenops-Agent-Id`        | identifies the agent emitting |
| `X-Tokenops-Session-Id`      | groups conversation turns     |
| `X-Tokenops-User-Id`         | end-user attribution          |

Anthropic Python:

```python
client.with_options(default_headers={
    "X-Tokenops-Workflow-Id": "code-review",
    "X-Tokenops-Agent-Id":    "claude-code",
}).messages.create(...)
```

Anthropic Node:

```ts
client.messages.create(req, {
  headers: {
    "X-Tokenops-Workflow-Id": "code-review",
    "X-Tokenops-Agent-Id": "claude-code",
  },
});
```

## Streaming smoke test

```bash
curl -sS -N -X POST \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"claude-sonnet-4-6","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"ping"}]}' \
  http://127.0.0.1:9000/anthropic/v1/messages
```

`-N` disables curl buffering; you should see SSE events arrive
incrementally. The proxy sets `X-Accel-Buffering: no` and flushes on every
upstream chunk.

## Claude Code

Claude Code's CLI honours `ANTHROPIC_BASE_URL`. Set it before launch:

```bash
export ANTHROPIC_BASE_URL="http://127.0.0.1:9000/anthropic"
claude
```
