# SDK shims

TokenOps is a transparent proxy. The SDK shim docs explain how to point an
existing client library (Python or Node) at the daemon by overriding its
base URL — no SDK fork, no extra dependency.

| Provider | Default base URL                          | Proxy base URL                            |
|----------|-------------------------------------------|-------------------------------------------|
| OpenAI   | `https://api.openai.com`                  | `http://127.0.0.1:9000/openai/v1`         |
| Anthropic| `https://api.anthropic.com`               | `http://127.0.0.1:9000/anthropic`         |
| Gemini   | `https://generativelanguage.googleapis.com`| `http://127.0.0.1:9000/gemini`           |

Per-SDK setup, attribution headers, and smoke tests:

- [OpenAI](./openai.md) — `OPENAI_BASE_URL` env / `base_url` option
- [Anthropic](./anthropic.md) — `ANTHROPIC_BASE_URL` env / `base_url` option
- [Gemini](./gemini.md) — `http_options.base_url` (Python) / `httpOptions.baseUrl` (Node)

## Auth passthrough

The proxy strips hop-by-hop headers (per RFC 7230) and forwards everything
else, so provider auth headers reach upstream unchanged:

| Provider   | Header(s)                                        |
|------------|--------------------------------------------------|
| OpenAI     | `Authorization: Bearer <key>`                    |
| Anthropic  | `x-api-key`, `anthropic-version`                 |
| Gemini     | `x-goog-api-key` or `Authorization: Bearer <jwt>`|

The proxy never logs auth values; integration tests in
`internal/proxy/sdkshim_test.go` assert the original headers reach the
upstream verbatim.
