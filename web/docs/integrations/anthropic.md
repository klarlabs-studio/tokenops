# Anthropic

Point the Anthropic SDK at the proxy. Streaming
(`text/event-stream`) is passed through with per-chunk flush.

## Proxy URL

```
http://127.0.0.1:7878/anthropic
```

## Python

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export ANTHROPIC_BASE_URL="http://127.0.0.1:7878/anthropic"
```

```python
from anthropic import Anthropic

client = Anthropic(base_url="http://127.0.0.1:7878/anthropic")
msg = client.messages.create(
    model="claude-sonnet-4-6",
    max_tokens=512,
    messages=[{"role": "user", "content": "Hello"}],
)
```

## Node

```bash
export ANTHROPIC_BASE_URL="http://127.0.0.1:7878/anthropic"
```

```ts
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  baseURL: "http://127.0.0.1:7878/anthropic",
});
```

## Streaming smoke test

```bash
curl -sS -N -X POST \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"claude-sonnet-4-6","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"ping"}]}' \
  http://127.0.0.1:7878/anthropic/v1/messages
```

`-N` disables curl buffering; SSE events arrive incrementally.

## Claude Code

```bash
export ANTHROPIC_BASE_URL="http://127.0.0.1:7878/anthropic"
claude
```
