# Configuration

The daemon reads `config.yaml` (path via `--config`) merged with
`TOKENOPS_*` environment variables; CLI flags win last.

## Operating modes

`mode` decides how TokenOps helps you optimize:

| Mode | What runs |
|---|---|
| `passive` (default) | Collect + analyze on demand. Proxy observes traffic and stamps costs; pollers ingest vendor usage; `spend` / `replay` / `coach` answer when asked. Nothing is altered, nothing fires on its own. |
| `active` | Everything in passive, plus interventions: the proxy **applies `optimizer.routing_rules` to live traffic** (requests for `from_model` are rewritten to `to_model` before reaching the upstream, recorded as applied optimization events), and the daemon runs a **background spend watcher** that evaluates `budgets` every `watch.interval` and warns about budget threshold/forecast breaches and unpriced models. |

Recommended path: run passive, use `tokenops replay` to validate what a
routing rule would have saved on real history, then flip `mode: active`
to enforce it.

Mode, budgets, and routing rules are also editable through the MCP
server — `tokenops_mode`, `tokenops_budget_set`, and
`tokenops_routing_rule_set` write the same `config.yaml` the CLI verbs
manage (validated before every write). The daemon picks the changes up
on its next restart.

Setting `mode: active` via `tokenops_mode` also **ensures a daemon is
running** — active mode's interventions (live routing, spend watcher)
live in the daemon, so activating without one would be a silent no-op.
If no daemon answers on its advertised URL, one is started detached
with the active config (logs land next to `events.db` in
`daemon.log`); if one is already running, the response reminds you to
restart it so it picks up the new mode.

## Reference

```yaml
mode: passive                 # passive | active (see Operating modes)

listen: 127.0.0.1:7878        # bind address
log:
  level: info                 # debug | info | warn | error
  format: text                # text | json
shutdown:
  timeout: 15s                # graceful shutdown grace period

storage:
  enabled: true               # open the local sqlite store
  path: ~/.tokenops/events.db

tls:
  enabled: false              # serve HTTPS with auto-minted cert
  cert_dir: ~/.tokenops/certs
  hostnames: []               # extra SANs

providers:                    # upstream URL overrides
  openai: https://api.openai.com
  anthropic: https://api.anthropic.com
  gemini: https://generativelanguage.googleapis.com

otel:
  enabled: false              # ship envelopes to an OTLP collector
  endpoint: http://localhost:4318
  headers:
    x-honeycomb-team: ...
  service_name: tokenops
  redact: true

pricing:
  path: ~/.tokenops/pricing.yaml  # optional rate overrides (see below)

optimizer:
  routing_min_quality: 0.7    # skip rules below this quality floor
  routing_rules:              # model-routing optimizer (see below)
    - provider: anthropic
      from_model: "claude-fable-5*"
      to_model: claude-opus-4-8
      quality: 0.9
      fallbacks: [claude-sonnet-4-6]

coaching:
  context_limits:             # waste-detector threshold overrides
    - workflow_prefix: "claude-code:"
      max_context_tokens: 500000
      context_growth_limit_tokens: 1000000

budgets:                      # evaluated by the active-mode watcher
  - name: weekly-all
    window: weekly            # daily | weekly | monthly (calendar, UTC)
    limit_usd: 50
    warn_at: 0.75             # optional; fraction of limit_usd
    crit_at: 0.95             # optional
    # workflow_id / agent_id optionally scope the limit

watch:
  interval: 15m               # watcher cadence (default 15m, min 1m)
```

## Model routing rules

`optimizer.routing_rules` feeds the model-routing optimizer used by
`tokenops replay` and the `tokenops_replay` MCP tool. Each rule says
"traffic asking for `from_model` could run on `to_model`"; `quality` is
your confidence (0–1] that the cheaper model preserves task quality.
Replay then reports what each rule would have saved on real history:

```
Model routing opportunities:
  - route claude-fable-5 -> claude-opus-4-8: 124 requests, would save $9.43
```

Replay never resends traffic upstream — routing rules are evaluated
offline against the local event store, so you can validate a rule's
savings before changing any client configuration.

With `mode: active`, the same rules are **enforced on live proxied
traffic**: the proxy rewrites the request's model before forwarding,
logs the intervention, and records an applied optimization event
(visible in the dashboard and `tokenops replay`). The observation keeps
the originally requested model, so you can always audit what clients
asked for versus what was served. Routing never breaks a request — any
parse failure forwards the original body untouched.

## Budgets and the spend watcher

`budgets` define calendar-window (UTC) spend limits. In `mode: active`
the daemon evaluates them every `watch.interval` against actual spend
plus a Holt forecast for the remainder of the window, logging
`threshold_reached` and `forecast_breach` alerts (deduplicated per
window) and publishing `budget.exceeded` domain events. The watcher
also flags models missing from the pricing table. In passive mode
budgets are inert — define them ahead of time and flip the mode when
you want enforcement-grade visibility.

## Waste-detector context limits

The workflow waste detector (`tokenops replay --workflow`, the
`tokenops_workflow_trace` MCP tool, and the dashboard workflow view)
ships built-in thresholds per workflow type: `claude-code:` sessions
flag context above 900k tokens, `codex:` above 250k, everything else
above 32k. `coaching.context_limits` overrides them per workflow-ID
prefix — a matching entry replaces the built-in profile, and fields you
omit keep the detector defaults:

```yaml
coaching:
  context_limits:
    - workflow_prefix: "claude-code:"
      max_context_tokens: 500000          # flag earlier than the 900k default
      context_growth_limit_tokens: 1000000
      max_consecutive_agent_loops: 4
      system_redundancy_min: 3
```

## Pricing overrides

TokenOps ships an embedded list-price catalog (USD per million tokens)
used to cost every request. When a vendor releases a model the catalog
doesn't know yet, `tokenops spend` and the `tokenops_spend_summary` MCP
tool flag it:

```
⚠ no pricing for 1 model(s) — total spend is underestimated:
    anthropic/claude-fable-5[1m] (213 requests)
```

Add a rate without waiting for a TokenOps release — or apply negotiated
rates — via `pricing.path`. The file layers on top of the built-in
catalog; fields you omit inherit from the matching built-in row:

```yaml
# ~/.tokenops/pricing.yaml
currency: USD
rates:
  anthropic:
    "claude-fable-5*":            # trailing * = prefix match, covers
      input_per_million: 10.00    #   suffixed variants like [1m]
      output_per_million: 50.00
      cached_input_per_million: 1.00
```

## Environment variables

| Variable                          | Maps to                       |
|-----------------------------------|-------------------------------|
| `TOKENOPS_LISTEN`                 | `listen`                      |
| `TOKENOPS_LOG_LEVEL`              | `log.level`                   |
| `TOKENOPS_LOG_FORMAT`             | `log.format`                  |
| `TOKENOPS_SHUTDOWN_TIMEOUT`       | `shutdown.timeout`            |
| `TOKENOPS_TLS_ENABLED`            | `tls.enabled`                 |
| `TOKENOPS_TLS_CERT_DIR`           | `tls.cert_dir`                |
| `TOKENOPS_STORAGE_ENABLED`        | `storage.enabled`             |
| `TOKENOPS_STORAGE_PATH`           | `storage.path`                |
| `TOKENOPS_OTEL_ENABLED`           | `otel.enabled`                |
| `TOKENOPS_OTEL_ENDPOINT`          | `otel.endpoint`               |
| `TOKENOPS_OTEL_SERVICE_NAME`      | `otel.service_name`           |
| `TOKENOPS_PROVIDER_OPENAI_URL`    | `providers.openai`            |
| `TOKENOPS_PROVIDER_ANTHROPIC_URL` | `providers.anthropic`         |
| `TOKENOPS_PROVIDER_GEMINI_URL`    | `providers.gemini`            |
| `TOKENOPS_PRICING_PATH`           | `pricing.path`                |
