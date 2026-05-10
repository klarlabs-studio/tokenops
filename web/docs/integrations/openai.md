# OpenAI

Point the OpenAI SDK at the TokenOps proxy by overriding the base URL.
Every request keeps its existing auth header
(`Authorization: Bearer <key>`); the proxy forwards it verbatim to
`https://api.openai.com`.

## Proxy URL

```
http://127.0.0.1:7878/openai/v1
```

## Python

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="http://127.0.0.1:7878/openai/v1"
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:7878/openai/v1")
resp = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello"}],
)
```

## Node

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="http://127.0.0.1:7878/openai/v1"
```

```ts
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://127.0.0.1:7878/openai/v1",
});
```

Streaming and `responses.create` work without further changes.

## Smoke test

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}' \
  http://127.0.0.1:7878/openai/v1/chat/completions
```
