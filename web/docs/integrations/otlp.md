# OTLP exporter

TokenOps ships envelopes to any OTLP/HTTP/JSON-compatible collector
(OpenTelemetry Collector, Honeycomb, Grafana Agent, …) when
`otel.enabled` is set.

Each envelope becomes one OTLP `LogRecord`. Numeric fields (tokens,
latency, cost) become attributes keyed by the GenAI semantic
conventions (`gen_ai.usage.input_tokens`, `gen_ai.system`, …) plus
TokenOps-specific keys under `tokenops.*`.

## Enable

```yaml
otel:
  enabled: true
  endpoint: http://localhost:4318
  service_name: tokenops
  redact: true        # default: redact secrets before exporting
  headers:            # forwarded on every export request
    x-honeycomb-team: ...
```

Or via env:

```bash
export TOKENOPS_OTEL_ENABLED=true
export TOKENOPS_OTEL_ENDPOINT=http://localhost:4318
```

## Redaction

When `redact: true` (the default), the redaction pipeline runs on
every envelope before encoding so secrets never leave the daemon. The
same pattern + entropy detectors used for the local event store apply.

## Attribute reference

GenAI semantic conventions:

- `gen_ai.system` (openai / anthropic / gcp.gemini)
- `gen_ai.request.model`
- `gen_ai.response.model`
- `gen_ai.usage.input_tokens`
- `gen_ai.usage.output_tokens`
- `gen_ai.usage.total_tokens`

TokenOps-specific:

- `tokenops.event.type`
- `tokenops.prompt.hash`
- `tokenops.cost_usd`
- `tokenops.workflow.id` / `tokenops.agent.id` / `tokenops.session.id`
- `tokenops.cache.hit`
- `tokenops.optimization.{type,mode,decision,estimated_savings_tokens}`

The full key set lives in `pkg/eventschema/otel.go`.
