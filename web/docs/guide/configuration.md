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

### `mode` is about traffic; `coaching.delivery` is about your session

These are two axes, and they are deliberately separate. `mode` decides
whether TokenOps rewrites **traffic** — routing rules on the proxy, the
spend watcher. `coaching.delivery` decides how much of your **session**
the coach may take. Wanting live routing without a coach that interrupts
is a reasonable position, and so is the reverse.

`coaching.delivery` is a ladder graded by interference, each rung adding
a channel to the one below:

| Level | What the coach does |
|---|---|
| `observe` | Records everything, answers when asked — `tokenops coach prompts`, `coach replies`, `dx`, and the MCP coaching tools. The hooks stay installed but say nothing, while still keeping their ledgers: `coach-hook stats` and `read-guard stats` show what you are missing before you let them speak. |
| `advise` (default) | Observe, plus the coach speaks unprompted but never blocks — `coach-hook` nudges as session cost crosses a budget fraction. Advice you can ignore. |
| `intervene` | Advise, plus the coach acts — `read-guard` refuses a redundant re-read before it costs a token. |

The rungs are graded by interference rather than by who started the
exchange, because that is the question you actually have. "Does it speak
without being asked" would put a non-blocking nudge and a refused tool
call on the same rung, and those are not remotely the same imposition.

```bash
tokenops coach delivery              # print the current level
tokenops coach delivery intervene    # let the guard start blocking
tokenops coach delivery advise       # back it out
```

It takes effect immediately. The hooks resolve this on every invocation,
so there is nothing to re-install. `--mode` on `read-guard` still pins a
behaviour against the config when you want one hook to differ.

`advise` is the default because it is what the hooks already did before
this setting existed — upgrading changes nothing until you say so.

#### The coach argues for `intervene`; it never takes it

`read-guard` keeps a ledger of the redundant re-reads it *would* have
blocked. Once that evidence crosses the bar, the coach makes the case
during an ordinary session — with your own numbers and the one command
that acts on them:

```
tokenops: read-guard has watched ~397k tokens of redundant re-reads go by
across 22 sessions — 225 reads it would have refused, and did not.
`tokenops coach delivery intervene` lets it refuse them before they cost
anything.
```

Who pulls the trigger follows `coaching.delivery` itself rather than a
second knob, because it is the same question that ladder already answers:

| Level | What happens with the evidence |
|---|---|
| `observe` | Recorded. Nothing is said. |
| `advise` | The case is made, once per session. The decision stays yours. |
| `intervene` | Already there — the guard is refusing re-reads, so there is nothing to ask for. |

It never promotes itself. Flipping a hook from observing to refusing the
agent's reads is exactly what `intervene` exists to gate, and a tool that
crosses that line on its own has decided the question the gate was there
to ask. A guard that has already blocked something is never argued with
again, whether it got there by `coach delivery intervene` or a pinned
`--mode=active`.

#### `coaching.quiet` — how often the coach may speak

Every finding the coach can raise already latches: a budget tier fires
once per session and then stays quiet, and so does the read-guard case
above. `coaching.quiet` exists for the case a latch cannot see — two
*different* findings landing back to back, which reads as nagging however
reasonable each one was on its own.

The coach also says at most one thing per Stop, and the budget tier goes
first: it is about the session in flight, while the read-guard case will
be just as true next turn.

| Key | Meaning |
|---|---|
| `min_interval` | A floor between two proactive nudges in one session. A nudge held back by the floor is **deferred, not dropped**: its finding stays unlatched and speaks at the next opportunity past the floor. |
| `max_per_session` | A cap on how many proactive nudges one session may carry. Unlike the floor this **drops** — a cap that queues what it refused is not a cap. Zero means no cap. |

Both default to zero, which means the per-finding latches are the whole
policy — exactly what the hooks did before the key existed. It only bites
at `delivery: advise` and `intervene`, where something speaks at all;
neither knob touches anything you ask for, because a direct question is
never an interruption.

`coach-hook stats` counts what the policy held back, by rule:

```
  budget alerts fired: 2
  held back by coaching.quiet: 1
    min_interval    1
```

That line is the point. A rate limit you cannot see working is
indistinguishable from one that does nothing.

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

retention:                    # opt-in; empty keep deletes nothing
  interval: 1h
  keep:                       # by event type
    prompt: 30d
    workflow: 90d
  keep_by_source:             # by reader; overrides the type window
    opencode: forever         # never prune this source
    codex-jsonl: forever
    read-guard: 7d            # may also be shorter than the type window

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
  smart_routing:              # decide per turn, with no rules (see below)
    enabled: false
    window_pct_above: 70      # conserve only once the window is this full
    quality: 0.75             # confidence a mechanical turn survives it
  routing_rules:              # model-routing optimizer (see below)
    - provider: anthropic
      from_model: "claude-fable-5*"
      to_model: claude-opus-4-8
      quality: 0.9
      fallbacks: [claude-sonnet-4-6]
  command_fmt:                # deterministic command-output compression (see below)
    default: balanced         # conservative | balanced | aggressive
    overrides:                # per-command loss level
      kubectl: aggressive
    emit_events: false        # append command_fmt OptimizationEvents to the store
    formatters:               # user-defined formatters (no recompile)
      - command: mytool
        critical: ["(?i)error", "FAILED"]
        drop:
          balanced: ["^DEBUG ", "^TRACE "]
          aggressive: ["^INFO "]

coaching:
  delivery: advise            # observe | advise | intervene
  quiet:                      # rate-limits the *proactive* channel only
    min_interval: 30m         # floor between two nudges in one session
    max_per_session: 3        # cap; 0 defers to the per-finding latches
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
    basis: spend              # spend (real billed cost, default) |
                              # equivalent (API list-price value incl.
                              # plan-covered usage — use on flat plans)
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

## Smart routing (`optimizer.smart_routing`)

A routing rule is a decision you made once, in advance, about a pair of
model names. That is a reasonable thing to be *able* to write and a poor
thing to *have* to write: the rule cannot see the turn it is deciding
about, so it is either too broad to be safe or too narrow to fire, and it
goes stale the moment a model is renamed.

`smart_routing` is the same decision made per turn, from signal that is
actually measured:

| Signal | Where it comes from |
|---|---|
| What kind of work this turn is | the task classifier — a terse instruction over mostly tool traffic reads as mechanical; a long specifying one, or one that rejects the last answer, reads as reasoning |
| How full the plan's rate-limit window is | the vendor's own meter where one exists, the plan-consumption heuristic otherwise |
| What is cheapest right now | the live pricing table, not a model name written into config |

Nobody writes a `from`/`to` pair, so nobody has to maintain one. A
configured rule always wins where one matches; the policy only decides
turns no rule covers.

**It is deliberately narrow, and every narrowing is deliberate:**

- **Downwards only.** The preferred-model ceiling outranks it, so it can
  never raise a bill.
- **Mechanical work only.** Routing a reasoning turn down is the quality
  trade you did not ask for. When the classifier abstains, so does the
  policy.
- **Only while the window is tight.** There is no "conserve always"
  setting. On a flat-rate plan a request costs nothing at the margin, so
  routing down while you have headroom trades quality for a saving that
  does not exist.
- **An unmeasured window is not a full one.** If the meter is not
  reporting, the policy stays put and says so. This meter read 0/200 for
  months on a real machine; acting on that would have degraded quality to
  relieve a shortage that was not happening.

Cheapest is not the same as best, and that is the honest limit of a
policy with no rules in it: if a provider's rate card lists a cheap model
you would never want, the policy will pick it. The gates above are what
make that tolerable — and if you want a specific target, write a rule.

### Where routing bites, and the way round it

Enforcement means rewriting the model in the request, which needs the
proxy: `mode: active` plus a base-URL override. Most operators never wire
that, and on those machines the optimizer holds an opinion nobody can
hear.

So the same policy is also reachable as **advice**, through the MCP tool
`tokenops_routing_advise`. An agent hands it the instruction it is about
to act on and the model it would otherwise use, and gets back `stay` or
`switch` with the reason, the class, and the window reading. It
recommends and never applies — the model stays the caller's choice, and
ultimately yours.

That path needs no proxy and no base-URL override, which makes it the one
routing surface available on every client that speaks MCP. It runs the
same policy the request path runs, deliberately: advice that disagreed
with enforcement would be worse than no advice, because you would be
surprised twice.

## Command-output compression (`command_fmt`)

`optimizer.command_fmt` configures `tokenops fmt` — the deterministic
compressor that shrinks a shell command's stdout **before** it enters an
agent's context, keeping every line the formatter classifies as critical
(errors, failures, changed state) and dropping noise. 46 commands ship
built in (git, go/pytest/jest/vitest/rspec/playwright, npm/pip/uv/…,
mvn/gradle/bazel/dotnet/cmake/…, docker/kubectl/helm, terraform/pulumi/
ansible, aws/gcloud/az, and more).

- `default` — loss level for any command without an override:
  - `conservative` — strip only unambiguous noise (ANSI, blank runs,
    duplicate lines). Nothing semantic dropped.
  - `balanced` (recommended) — also drop command-specific noise
    (progress, banners, up-to-date chatter).
  - `aggressive` — additionally collapse repetitive state (e.g.
    "+142 unchanged files") into a summary.
- `overrides` — per-command loss level (a noisy command can be dialed
  up without loosening the global default).
- `emit_events` — append a `command_fmt` OptimizationEvent per compressed
  run so the dashboard and scorecard count the savings.

Two invariants hold at **every** level, for built-in and user formatters
alike: **determinism** (pure function of input + level) and
**critical-line survival** (a formatter that would drop a critical line
falls back to the raw output instead). The full output is always kept in
`~/.tokenops/recovery/` for retrieval.

### User-defined formatters

`command_fmt.formatters` extends or overrides the catalog for any command
**without recompiling**. Declare the regexes that mark critical lines
(always preserved) and the noise regexes to drop per loss level:

```yaml
optimizer:
  command_fmt:
    formatters:
      - command: mytool
        aliases: [mt]
        critical: ["(?i)error", "FAILED", "^\\s*modified:"]
        drop:
          balanced:   ["^DEBUG ", "^\\s*at "]   # dropped at balanced + aggressive
          aggressive: ["^INFO "]                 # dropped at aggressive only
```

User rules run through the same critical-line guard as built-ins, so an
overly broad drop rule can never remove a line you marked critical — it is
preserved and the formatter falls back safely.

`tokenops fmt learn` mines usage telemetry to suggest which commands need a
formatter and which are over-compressing; `tokenops fmt learn --apply`
writes the safe loss-level tuning back to this config locally. The
`tokenops_fmt_learn` MCP tool exposes the same report to agents. See the
[CLI reference](./cli.md#command-output-compression-fmt).

## Budgets and the spend watcher

`budgets` define calendar-window (UTC) spend limits. In `mode: active`
the daemon evaluates them every `watch.interval` against actual spend
plus a Holt forecast for the remainder of the window, logging
`threshold_reached` and `forecast_breach` alerts (deduplicated per
window) and publishing `budget.exceeded` domain events. The watcher
also flags models missing from the pricing table. In passive mode
budgets are inert — define them ahead of time and flip the mode when
you want enforcement-grade visibility.

On flat-rate plans (Claude Max, ChatGPT Plus) real spend is ~$0 — a
`basis: spend` budget guards nothing. Use `basis: equivalent` to watch
the **API-equivalent value** instead: what the window's usage would
have billed at list prices, including plan-covered traffic. The same
figure appears as `api equivalent` in `tokenops spend` and
`api_equivalent_usd` in the `tokenops_spend_summary` MCP tool.
Equivalent-basis budgets get threshold alerts only (no forecast).

Plan-covered traffic is identified centrally: every event written to
the store for a provider with a plan bound in `plans:` is stamped
`plan_included` at the storage sink, regardless of which poller or
proxy produced it.

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

## Automatic rate-card refresh

A rate card goes stale on its own. A model released after your binary was
built prices at **zero**, so a session that cost real money reports as
free — the exact failure this tool exists to find. So the daemon refreshes
the card itself rather than waiting for someone to remember
`tokenops pricing refresh`.

**This is the one outbound call tokenops makes on its own.** It fetches a
public rate card (LiteLLM's `model_prices_and_context_window.json`) and
sends nothing: no prompt, no file, no identifier, no usage figure. The
privacy claim is about content and no content is involved — but a tool
that starts talking to the network without saying so has spent trust it
cannot buy back, which is why it is documented here and switchable:

```yaml
pricing:
  refresh:
    disabled: true      # off
    interval: 24h       # default 24h, floor 1h
```

Three things about how it behaves:

- **It applies what it fetches.** The spend engine is built once at
  startup, so a refresh hands the new cards to the *running* engine rather
  than leaving them on disk for the next restart. A refresh that writes a
  snapshot the live process never reads is a no-op wearing the clothes of
  an update.
- **It does not write when nothing changed.** Rate cards move on the order
  of weeks; a daily unconditional write would leave a year of
  near-identical snapshots and make `pricing diff` useless. A separate
  marker records that a check happened, so a no-change check still counts
  and a restart does not re-fetch.
- **It fails quiet and soft.** No network, a bad payload, a source outage:
  it logs and keeps the card already in force. Pricing is never the reason
  ingestion stops.

Your `verified: true` rows and your `pricing.path` overrides both outrank
anything fetched, so a refresh cannot regress a rate you hand-checked or
one you negotiated.

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

## mDNS (`mdns`)

The daemon can advertise itself as `tokenops.local` so the dashboard has a
memorable URL. By default it advertises **only when the LAN can actually
reach it** — on the default loopback bind the record would point at
`127.0.0.1`, which no peer can use, while the machine's hostname still went
out on every interface.

```yaml
mdns:
  enabled: true              # force on or off; omit to follow the bind
  instance_name: workstation # replaces the hostname in the advertised name
```

`instance_name` is for operators who want `tokenops.local` without
publishing what their laptop is called.

## Plan limits (`plan_limits`)

Figures only you can supply, for plans whose limit this tool cannot know.
A spend-denominated plan — usage-based Enterprise — is billed at API rates
from the first token, so headroom is measured against the org spend limit
your admins set in the vendor console.

```yaml
plans:
  anthropic: claude-enterprise
plan_limits:
  anthropic:
    spend_limit_usd: 5000
    window: monthly     # monthly (default), weekly or daily
    rate_factor: 0.8    # optional: scale to a negotiated rate
```

These figures are the fallback. When the Claude usage meter is set up
(`tokenops vendor-usage setup claude-usage-meter`), Anthropic reports both
the month's spend and the limit, and headroom uses those instead — what
the vendor bills outranks a recomputation from a rate card, so neither
`spend_limit_usd` nor `rate_factor` applies to them.

Binding such a plan with neither the meter nor `spend_limit_usd` is refused
rather than defaulted: a percentage measured against a number nobody chose
reads exactly as authoritative as a real one.

`rate_factor` exists because enterprise rates are frequently negotiated
off list while TokenOps costs from the public rate card. Without it your
console limit is compared against an overstatement, and the error is
invisible.

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
