# Gemini

Point the `google-genai` SDK at the proxy. Streaming
(`:streamGenerateContent`) and non-streaming both pass through.

## Proxy URL

```
http://127.0.0.1:7878/gemini
```

## Python

```python
from google import genai
from google.genai import types

client = genai.Client(
    api_key="<gemini-api-key>",
    http_options=types.HttpOptions(base_url="http://127.0.0.1:7878/gemini"),
)

resp = client.models.generate_content(
    model="gemini-1.5-pro",
    contents="Hello",
)
```

## Node

```ts
import { GoogleGenAI } from "@google/genai";

const ai = new GoogleGenAI({
  apiKey: "<gemini-api-key>",
  httpOptions: { baseUrl: "http://127.0.0.1:7878/gemini" },
});
```

## Smoke test

```bash
curl -sS -X POST \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"parts":[{"text":"ping"}]}]}' \
  "http://127.0.0.1:7878/gemini/v1beta/models/gemini-1.5-pro:generateContent"
```
