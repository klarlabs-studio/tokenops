# OpenAI SDK shim

Point the OpenAI SDK at the TokenOps proxy by overriding the base URL. Every
request keeps its existing auth header (`Authorization: Bearer <key>`); the
proxy forwards it verbatim to `https://api.openai.com`.

## Proxy URL layout

The proxy mounts the OpenAI surface under `/openai/`. To match the path
prefix the OpenAI SDKs append (`/v1/chat/completions`, `/v1/embeddings`,
…), set the base URL to:

```
http://127.0.0.1:9000/openai/v1
```

If TokenOps was configured with a different listen address or TLS, swap
the host/scheme accordingly. Trust the TokenOps CA before using `https://`
(see `tokenops cert install`).

## Python (`openai` package)

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="http://127.0.0.1:9000/openai/v1"
```

Or in code:

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:9000/openai/v1")
resp = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello"}],
)
```

The async client (`openai.AsyncOpenAI`) accepts the same `base_url` and
also reads `OPENAI_BASE_URL`.

## Node (`openai` package)

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="http://127.0.0.1:9000/openai/v1"
```

Or in code:

```ts
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://127.0.0.1:9000/openai/v1",
});

const resp = await client.chat.completions.create({
  model: "gpt-4o-mini",
  messages: [{ role: "user", content: "Hello" }],
});
```

Streaming and `responses.create` work without further changes.

## Attribution headers

Tag each request so TokenOps can stitch related calls into workflows and
sessions:

| Header                       | Purpose                       |
|------------------------------|-------------------------------|
| `X-Tokenops-Workflow-Id`     | groups multi-step pipelines   |
| `X-Tokenops-Agent-Id`        | identifies the agent emitting |
| `X-Tokenops-Session-Id`      | groups conversation turns     |
| `X-Tokenops-User-Id`         | end-user attribution          |

In Python:

```python
client.with_options(default_headers={
    "X-Tokenops-Workflow-Id": "research-summariser",
    "X-Tokenops-Agent-Id":    "planner",
}).chat.completions.create(...)
```

In Node:

```ts
client.chat.completions.create(req, {
  headers: {
    "X-Tokenops-Workflow-Id": "research-summariser",
    "X-Tokenops-Agent-Id": "planner",
  },
});
```

## Smoke test

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}' \
  http://127.0.0.1:9000/openai/v1/chat/completions
```

A 200 response with a JSON body confirms the shim is working. Inspect
emitted events with `tokenops events tail`.
