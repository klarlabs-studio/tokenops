# Quickstart

Five minutes from zero to "every prompt is observed."

## 1. Install

```bash
go install github.com/felixgeelhaar/tokenops/cmd/tokenops@latest
go install github.com/felixgeelhaar/tokenops/cmd/tokenopsd@latest
```

Or grab a prebuilt binary from
[GitHub Releases](https://github.com/felixgeelhaar/tokenops/releases).

## 2. Start the daemon

```bash
TOKENOPS_STORAGE_ENABLED=true tokenops start
```

The daemon binds to `127.0.0.1:7878` by default. Storage is opt-in;
without it, events live only in memory.

## 3. Point an SDK at the proxy

OpenAI:

```bash
export OPENAI_BASE_URL="http://127.0.0.1:7878/openai/v1"
```

Anthropic:

```bash
export ANTHROPIC_BASE_URL="http://127.0.0.1:7878/anthropic"
```

Gemini: pass `httpOptions.baseUrl = "http://127.0.0.1:7878/gemini"`
to `genai.Client()`.

Send a request through any one of them and you'll see a PromptEvent
land in `~/.tokenops/events.db`.

## 4. Inspect what happened

```bash
tokenops spend                      # spend summary + 24h burn
tokenops spend --forecast           # 7-day forecast
tokenops replay <SESSION_ID>        # offline optimizer run
```

For richer attribution, set workflow / agent IDs as headers:

```http
X-Tokenops-Workflow-Id: research-summariser
X-Tokenops-Agent-Id: planner
```

## Next steps

- [SDK shim reference](/integrations/sdk-overview) — Python + Node + curl
- [CLI reference](/guide/cli)
- [Architecture](/architecture/overview) — how the pieces fit
