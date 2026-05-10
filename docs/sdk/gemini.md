# Gemini SDK shim

Point the Google Generative AI SDK (`google-genai`) at the TokenOps proxy
by overriding the base URL. The `x-goog-api-key` header (or
`Authorization: Bearer <token>` for Vertex) is forwarded verbatim to
`https://generativelanguage.googleapis.com`. Streaming
(`:streamGenerateContent`) and non-streaming (`:generateContent`) both
pass through.

## Proxy URL layout

The proxy mounts the Gemini surface under `/gemini/`. The Gemini SDKs
construct paths like `/v1beta/models/<model>:generateContent`, so set the
base URL to:

```
http://127.0.0.1:9000/gemini
```

If TokenOps was configured with a different listen address or TLS, swap
the host/scheme accordingly. Trust the TokenOps CA before using `https://`
(see `tokenops cert install`).

## Python (`google-genai` package)

```python
from google import genai
from google.genai import types

client = genai.Client(
    api_key="<gemini-api-key>",
    http_options=types.HttpOptions(base_url="http://127.0.0.1:9000/gemini"),
)

resp = client.models.generate_content(
    model="gemini-1.5-pro",
    contents="Hello",
)
```

For streaming:

```python
for chunk in client.models.generate_content_stream(
    model="gemini-1.5-pro", contents="Hello"
):
    print(chunk.text, end="")
```

## Node (`@google/genai` package)

```ts
import { GoogleGenAI } from "@google/genai";

const ai = new GoogleGenAI({
  apiKey: "<gemini-api-key>",
  httpOptions: { baseUrl: "http://127.0.0.1:9000/gemini" },
});

const resp = await ai.models.generateContent({
  model: "gemini-1.5-pro",
  contents: "Hello",
});
```

## Attribution headers

| Header                       | Purpose                       |
|------------------------------|-------------------------------|
| `X-Tokenops-Workflow-Id`     | groups multi-step pipelines   |
| `X-Tokenops-Agent-Id`        | identifies the agent emitting |
| `X-Tokenops-Session-Id`      | groups conversation turns     |
| `X-Tokenops-User-Id`         | end-user attribution          |

Pass via `http_options.headers`:

Python:

```python
client = genai.Client(
    api_key="<key>",
    http_options=types.HttpOptions(
        base_url="http://127.0.0.1:9000/gemini",
        headers={
            "X-Tokenops-Workflow-Id": "research-summariser",
            "X-Tokenops-Agent-Id":    "planner",
        },
    ),
)
```

Node:

```ts
const ai = new GoogleGenAI({
  apiKey: "<key>",
  httpOptions: {
    baseUrl: "http://127.0.0.1:9000/gemini",
    headers: {
      "X-Tokenops-Workflow-Id": "research-summariser",
      "X-Tokenops-Agent-Id": "planner",
    },
  },
});
```

## Smoke tests

Non-streaming:

```bash
curl -sS -X POST \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"parts":[{"text":"ping"}]}]}' \
  "http://127.0.0.1:9000/gemini/v1beta/models/gemini-1.5-pro:generateContent"
```

Streaming:

```bash
curl -sS -N -X POST \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"parts":[{"text":"ping"}]}]}' \
  "http://127.0.0.1:9000/gemini/v1beta/models/gemini-1.5-pro:streamGenerateContent?alt=sse"
```
