# Event schema

All telemetry rides on a single `Envelope` shape. Concrete payloads
implement the `Payload` interface; the `Type` field discriminates.

## Envelope

| Field            | Type                  | Notes                              |
|------------------|-----------------------|------------------------------------|
| `id`             | string (uuid)         | Stable identifier                  |
| `schema_version` | string                | Semver of the schema               |
| `type`           | EventType             | `prompt` / `workflow` / `optimization` / `coaching` |
| `timestamp`      | RFC3339 + ns          | UTC                                |
| `source`         | string                | `proxy`, `coaching`, …             |
| `payload`        | Payload (typed)       |                                    |
| `attributes`     | map[string]string     | Free-form labels                   |

## Payload types

### PromptEvent

Captures one LLM request/response cycle. Token counts are filled by
the per-provider tokenizer.

Key fields: `prompt_hash`, `provider`, `request_model`,
`response_model`, `input_tokens`, `output_tokens`, `total_tokens`,
`cached_input_tokens`, `context_size`, `max_output_tokens`,
`latency_ns`, `time_to_first_token_ns`, `streaming`, `status`,
`finish_reason`, `error_code`, `cache_hit`, `cost_usd`,
`workflow_id`, `agent_id`, `session_id`, `user_id`.

### WorkflowEvent

Lifecycle marker for a multi-step run. Useful when the upstream
emitter knows the workflow boundaries (e.g. an agent framework).

Fields: `workflow_id`, `parent_workflow_id`, `agent_id`, `state`
(`started` / `completed` / `failed`), `step_count`, `error_code`.

### OptimizationEvent

Emitted per optimizer recommendation.

Fields: `prompt_hash`, `kind` (`prompt_compress` / `semantic_dedupe` /
…), `mode` (`passive` / `interactive` / `replay`), `decision`
(`accepted` / `applied` / `rejected` / `skipped`),
`estimated_savings_tokens`, `estimated_savings_usd`, `quality_score`,
`reason`, `latency_impact_ns`.

### CoachingEvent

Workflow-level finding emitted by the waste detector.

Fields: `workflow_id`, `kind` (`trim_context`, `break_recursion`,
`reuse_cache`, …), `summary`, `details`, `estimated_savings_tokens`,
`replay_metadata`.

## Versioning

`schema_version` follows semver. Field additions bump the minor. Field
renames or type changes bump the major and ship with a migration in
`internal/storage/sqlite/migrate.go`.
