# Architecture overview

TokenOps runs as a single Go daemon on the local machine. The daemon
hosts:

- a reverse proxy on `127.0.0.1:7878` (configurable) that mounts each
  upstream LLM provider (`/openai`, `/anthropic`, `/gemini`);
- an in-process event bus that captures every prompt + response
  observed on the proxy;
- a sqlite event store (opt-in) that persists envelopes locally;
- an optimizer pipeline run in passive / interactive / replay mode;
- a CLI (`tokenops`) with an MCP server (`tokenops serve`) that surface
  the same data to humans and to agents.

```
┌────────────┐        ┌──────────────┐       ┌────────────┐
│  Your SDK  │  HTTP  │ TokenOps     │ HTTPS │ api.openai │
│  (OpenAI,  ├───────▶│ proxy         ├──────▶│ api.anthr  │
│  Anthropic,│        │ • observation │       │ generative │
│  Gemini)   │        │ • cache        │       │ language   │
└────────────┘        │ • optimizer    │       └────────────┘
                      │ • event bus    │
                      └──────┬─────────┘
                             ▼
            ┌────────────┐ ┌──────────────┐ ┌─────────┐
            │ sqlite     │ │ OTLP exporter│ │ MCP /   │
            │ events.db  │ │ (opt-in)     │ │ CLI     │
            └────────────┘ └──────────────┘ └─────────┘
```

## Request lifecycle

1. SDK sends request to `http://127.0.0.1:7878/<provider>/<path>`.
2. Cache middleware (when enabled) checks for a cached response by
   `sha256(provider+method+path+body)`. Hit → respond, emit cache-hit
   event. Miss → continue.
3. Observer middleware reads the body, hashes it, runs the
   tokenizer, builds a `requestObservation`, stashes it in the
   request context.
4. Reverse proxy forwards to the upstream URL with auth headers
   passed through.
5. On response, the proxy meters the body (counts bytes, tokenizes
   output) and emits a `PromptEvent` envelope.
6. The event bus delivers the envelope to every wired sink
   (sqlite, OTLP exporter).
7. Background pipelines (replay, waste detection, coaching) read
   envelopes from sqlite asynchronously.

## Why a local proxy?

Two reasons:

- **Privacy.** Prompts contain secrets, code, customer data. A local
  proxy keeps every byte on the operator's machine unless they
  explicitly enable an exporter.
- **Latency.** Network detours to a SaaS observability layer add
  RTT to every prompt. The proxy benchmarks in CI at sub-1ms
  overhead p99 against a localhost upstream; we treat overhead as a
  feature.

## Where to look in the source

| Concern        | Package                                       |
|----------------|-----------------------------------------------|
| HTTP server    | `internal/proxy`                              |
| Cache          | `internal/proxy/cache`                        |
| Observation    | `internal/proxy/observation.go`               |
| Tokenizers     | `internal/tokenizer`                          |
| Event bus      | `internal/events`                             |
| Storage        | `internal/storage/sqlite`                     |
| Optimizers     | `internal/optimizer/{promptcompress,dedupe,…}`|
| Replay         | `internal/replay`                             |
| Workflow trace | `internal/workflow`                           |
| Waste detector | `internal/waste`                              |
| Spend / pricing| `internal/spend`                              |
| Forecast       | `internal/forecast`                           |
| OTLP exporter  | `internal/otlp`                               |
| MCP server     | `internal/mcp`                                |
