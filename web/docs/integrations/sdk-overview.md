# SDK shim overview

TokenOps is a transparent proxy. Every SDK shim is "set the base URL
env var, run as usual." No SDK fork, no patching.

| Provider  | Default base URL                          | Proxy base URL                     |
|-----------|-------------------------------------------|------------------------------------|
| OpenAI    | `https://api.openai.com`                  | `http://127.0.0.1:7878/openai/v1`  |
| Anthropic | `https://api.anthropic.com`               | `http://127.0.0.1:7878/anthropic`  |
| Gemini    | `https://generativelanguage.googleapis.com` | `http://127.0.0.1:7878/gemini`   |

Per-SDK setup:

- [OpenAI](./openai) — `OPENAI_BASE_URL`
- [Anthropic](./anthropic) — `ANTHROPIC_BASE_URL`
- [Gemini](./gemini) — `httpOptions.baseUrl`

## Auth passthrough

The proxy strips hop-by-hop headers (RFC 7230) and forwards everything
else, so provider auth headers reach upstream unchanged:

| Provider   | Header(s)                                        |
|------------|--------------------------------------------------|
| OpenAI     | `Authorization: Bearer <key>`                    |
| Anthropic  | `x-api-key`, `anthropic-version`                 |
| Gemini     | `x-goog-api-key` or `Authorization: Bearer <jwt>`|

The proxy never logs auth values; integration tests in
`internal/proxy/sdkshim_test.go` assert the original headers reach the
upstream verbatim.

## Attribution headers

Tag each request so TokenOps can stitch related calls into workflows
and sessions:

| Header                   | Purpose                       |
|--------------------------|-------------------------------|
| `X-Tokenops-Workflow-Id` | groups multi-step pipelines   |
| `X-Tokenops-Agent-Id`    | identifies the agent emitting |
| `X-Tokenops-Session-Id`  | groups conversation turns     |
| `X-Tokenops-User-Id`     | end-user attribution          |

Attribution headers flow through the SDK shim path unchanged.
