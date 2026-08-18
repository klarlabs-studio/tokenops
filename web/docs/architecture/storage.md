# Storage and retention

TokenOps stores envelopes in a local SQLite database — `~/.tokenops/events.db`
by default — when `storage.enabled: true`.

## Schema

Envelopes are persisted to a single `events` table with indexed
columns mirroring the most-queried filters: `type`, `provider`,
`model`, `workflow_id`, `agent_id`, `session_id`, and `timestamp_ns`.

The full schema (and incremental migrations) lives in
`internal/storage/sqlite/{schema,migrate}.go`. Migrations apply
in-place at `Open` time; downgrades require an explicit dump + reload.

## Async ingestion

The proxy never blocks on storage. Every emitted envelope is queued
on a buffered channel; a worker goroutine batches them into
transactional `INSERT` statements every 100ms (or on a 64-row
batch). Queue overflow drops envelopes and increments a `dropped`
counter — visible via `tokenops status --json`.

## Retention

`internal/contexts/telemetry/retention` ships a configurable retention
worker that prunes envelopes older than a per-event-type window. It is
**opt-in**: empty `retention.keep` (the default) deletes nothing. Pair
with `tokenops start` (or `tokenops daemon install`) when you set it:

```yaml
retention:
  interval: 1h          # how often the pruner wakes; default 1h
  keep:
    prompt: 30d
    workflow: 90d
    optimization: 30d
    coaching: 365d
```

Windows of `0` skip that type. `audit_log` is never pruned. Day suffix
(`30d`) is accepted alongside Go durations (`720h`).

## Audit log

`internal/audit` writes an append-only log of operator actions
(config changes, optimization accepts, telemetry toggles). The
log lives in the same SQLite database under a separate table so
operators have a tamper-evident record without managing a second
file.
