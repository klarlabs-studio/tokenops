# eventschema

Canonical event schema for TokenOps.

## Sources of truth

| Layer | File |
|---|---|
| Public Go types (consumed by SDKs and integrations) | `*.go` in this package |
| Cross-language Protobuf contract | `proto/v1/events.proto` |
| OpenTelemetry attribute keys | `otel.go` |

The Go types and Protobuf messages are kept in lockstep. Either may be the
starting point for a change as long as the other is updated in the same PR
(see "Versioning" below).

## Event taxonomy

| Event | When emitted | Key fields |
|---|---|---|
| `PromptEvent` | Every LLM request/response observed by the proxy | tokens, latency, provider, model, prompt hash, context size, attribution |
| `WorkflowEvent` | Workflow lifecycle checkpoints (start/progress/complete/fail) | workflow id, agent id, cumulative tokens + cost, step count |
| `OptimizationEvent` | Each optimizer pass | optimization type, mode, estimated savings, decision, quality score |
| `CoachingEvent` | Replay analysis or live waste detection | recommendation kind, summary, savings estimate, efficiency score |

All events share the `Envelope`: id, schema version, type, timestamp, optional
trace/span ids for OTel correlation, source, free-form attributes, and a typed
payload.

## OpenTelemetry compatibility

`otel.go` declares the canonical attribute keys. Where the OpenTelemetry GenAI
semantic conventions exist (`gen_ai.system`, `gen_ai.usage.input_tokens`, ...)
TokenOps reuses them. Everything else lives under the `tokenops.*` namespace.
The OTLP exporter (task `otel-exporter`) translates Envelope + payload fields
into these keys; storage backends use the same vocabulary so traces, metrics,
and analytics share one column dictionary.

## Versioning

`SchemaVersion` (currently `1.0.0`) follows semantic versioning:

- **Patch** — additive doc/clarification, no field changes.
- **Minor** — additive enum members, additive optional fields. Old consumers
  ignore unknown fields and treat unknown enum values as the package-defined
  `*Unknown` sentinel.
- **Major** — breaking changes (renames, removals, type changes). Bump the
  Protobuf package (`v1` → `v2`) and provide a migration window where the
  storage layer accepts both versions.

Changes to `events.proto` and the Go types must be made together; CI enforces
both compile.

## Adding a new event field

1. Add the field to the relevant `*.proto` message with the next free tag and
   to the matching Go struct.
2. If the field maps to an OpenTelemetry attribute, add or reference the key
   in `otel.go`.
3. Update `SchemaVersion` per the rules above.
4. Add a test in `event_test.go` that exercises the field's JSON round-trip.
