# TokenOps

**The operational intelligence layer for AI systems.**

Observe. Optimize. Govern. Improve.

TokenOps is an open-source platform that sits between your clients, agents, and
workflows and frontier LLM providers (OpenAI, Anthropic, Google Gemini). It
turns opaque AI consumption into measurable, optimizable operational
infrastructure: real-time token optimization, inference observability, agent
usage analytics, prompt intelligence, forecasting, governance, and AI
efficiency coaching.

> Status: early development. The architecture is being assembled in public.

## Why TokenOps

Token usage is becoming the dominant operational cost of modern software, yet
today's tooling solves only isolated pieces of the problem:

| Existing tools             | What they miss                              |
|----------------------------|---------------------------------------------|
| Prompt optimizers          | No observability, no governance             |
| Provider billing dashboards| No real-time optimization or attribution    |
| Tracing/observability      | No optimization, no coaching                |
| Agent frameworks           | No operational analytics or governance      |

TokenOps combines optimization, observability, forecasting, coaching, and
governance into one integrated operational platform — the **TokenOps**
discipline (think DevOps, FinOps, MLOps, AIOps for inference).

## Architecture (high level)

```
Clients / SDKs / CLIs / Extensions
            |
            v
   Local TokenOps Proxy
            |
            v
   Optimization Engine
            |
            v
  Routing & Analysis Layer
            |
            v
       LLM Providers
            |
            v
   Telemetry Pipeline
            |
            v
 Observability + Coaching
```

## Repository layout

```
tokenops/
  cmd/
    tokenops/         # CLI binary (cobra)
    tokenopsd/        # daemon binary (proxy + analytics)
  internal/
    proxy/            # reverse proxy + provider routing
    optimizer/        # optimization engine
    observability/    # analytics, spend, workflow trace
    coaching/         # replay, waste detection, recommendations
    forecasting/      # forecasting & budget alerts
    storage/          # SQLite + ClickHouse adapters
    events/           # event bus
    redaction/        # secret detection + redaction
    cli/              # CLI command implementations
    config/           # configuration loading
    version/          # build metadata
  pkg/
    eventschema/      # public event schemas
  web/                # Vue 3 dashboard
  docs/               # docs site sources
  deployments/        # docker-compose, helm, etc.
  scripts/            # dev scripts
  .github/workflows/  # CI
```

## Getting started

> The code base is in bootstrap state. The commands below describe the target
> developer experience and are being implemented incrementally.

```bash
# Build everything
make build

# Run the daemon locally (proxy + analytics)
./bin/tokenopsd start

# Point a client SDK at the local proxy
export OPENAI_BASE_URL=http://localhost:7878/v1
export ANTHROPIC_BASE_URL=http://localhost:7878
export GEMINI_BASE_URL=http://localhost:7878

# Replay an expensive session
./bin/tokenops replay <session-id>

# Inspect spend, forecast, burn rate
./bin/tokenops spend
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

Plans and tasks are tracked in `.roady/` (see the [roady](https://roady.dev)
spec-driven planning tool).

## License

Apache License 2.0. See [LICENSE](LICENSE).
