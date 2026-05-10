# Runbook

Operator-facing checklists and triage steps for the daemon.

## Day one

- [ ] Confirm `tokenops status` returns `health: ok`, `ready: ready`.
- [ ] Storage path under `storage.path` exists and is writable.
- [ ] CA bundle minted at `tls.cert_dir` (only if `tls.enabled`).
- [ ] OTLP collector reachable at `otel.endpoint` (only if
      `otel.enabled`).

## Common ops

- **Restart cleanly** — SIGTERM (Ctrl-C). Graceful shutdown drains
  the event bus within `shutdown.timeout` (default 15s).
- **Inspect a slow request** — `tokenops status --json` shows the
  current bus published / dropped counters; any non-zero `dropped`
  means storage couldn't keep up. Bump `events.QueueCapacity`.
- **Flush the cache** — restart the daemon, or send a request with
  `X-Tokenops-Cache: refresh` to force a re-populate.

## Subsections

- [Health and readiness](./health) — what the probes mean
- [Cache](./cache) — bypass / refresh / invalidation
- [Performance](./performance) — bench-gate, latency budgets, queue
  pressure
