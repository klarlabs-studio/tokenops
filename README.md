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

DDD-organised. Contexts live under `internal/contexts/<ctx>/<pkg>`;
adapters (`cli`, `mcp`, `proxy`) stay flat. Layering rules enforced by
`internal/archlint` (`go test ./internal/archlint/...`).

```
tokenops/
  cmd/
    tokenops/                          # CLI binary (cobra)
    tokenopsd/                         # daemon binary
  internal/
    contexts/
      rules/                           # Rule Intelligence aggregate
      optimization/{optimizer,eval,replay}
      governance/{scorecard,coverdebt,budget}
      workflows/workflow
      coaching/{coaching,efficiency,waste}
      spend/{spend,forecast}
      observability/{analytics,anomaly,observ}
      security/{redaction,audit,dashauth,rbac,tlsmint}
      prompts/{tokenizer,providers,llm}
      telemetry/retention
    infra/
      rulesfs/                         # FS-touching rule corpus loader
    archlint/                          # DDD layering enforcement
    bootstrap/                         # composition root
    config/                            # config loader + Snapshot
    daemon/                            # boot sequence
    domainevents/                      # in-process pub/sub + JSONL log
    events/                            # telemetry envelope bus
    cli/                               # CLI subcommand wiring
    mcp/                               # MCP tool surface
    otlp/                              # OTLP exporter
    proxy/                             # HTTP server + handlers
    storage/sqlite/                    # event store
    version/
  pkg/
    eventschema/                       # public envelope + payload types
  web/dashboard/                       # Vue 3 dashboard
  docs/                                # docs site + architecture-ddd.md
```

See `docs/architecture-ddd.md` for context boundaries and ubiquitous
language.

## Getting started

Three commands take a fresh install to a populated scorecard:

```bash
# 1. Build (or `brew install felixgeelhaar/tap/tokenops`)
make build

# 2. Scaffold an opinionated config (sqlite + rules enabled, idempotent)
./bin/tokenops init

# 3. Seed 7 days of synthetic events so spend/burn/forecast/scorecard return data
./bin/tokenops demo

# 4. (optional) Start the proxy and route real traffic
./bin/tokenopsd start
export OPENAI_BASE_URL=http://localhost:7878/v1
export ANTHROPIC_BASE_URL=http://localhost:7878
export GEMINI_BASE_URL=http://localhost:7878
```

`tokenops init` writes `$XDG_CONFIG_HOME/tokenops/config.yaml` (or
`~/.config/tokenops/config.yaml`) with storage at `$XDG_DATA_HOME/tokenops/events.db`
(or `~/.tokenops/events.db`) and rules rooted at `$PWD`. Re-running is a no-op
unless you pass `--force`.

`tokenops demo` is deterministic — pass `--seed`, `--days`, `--per-day` to tune,
or `--reset` to clear before reseeding. Run `tokenops spend --since 7d` or
`tokenops scorecard` next to see populated output.

### CLI surface

| Command | Purpose |
|---|---|
| `tokenops init` | scaffold the config (sqlite + rules on, idempotent) |
| `tokenops demo` | seed synthetic events for activation |
| `tokenops start` | run the daemon (proxy + analytics + bus) |
| `tokenops serve` | MCP server over stdio |
| `tokenops status` | daemon health + `blockers[]` / `next_actions[]` |
| `tokenops version` | build info |
| `tokenops config show` | active configuration (redacted) |
| `tokenops spend` | spend / burn / forecast |
| `tokenops replay <id>` | replay a session through optimizer |
| `tokenops rules analyze\|conflicts\|compress\|inject\|bench` | rule intelligence |
| `tokenops eval` | optimizer eval harness + gate |
| `tokenops scorecard` | wedge KPI scorecard |
| `tokenops coverage-debt` | risk-weighted coverage debt |
| `tokenops audit` | query audit log |
| `tokenops events` | per-kind domain-event counts |
| `tokenops plan list\|headroom\|catalog` | flat-rate subscription headroom (Claude Max, ChatGPT Plus, Copilot, Cursor) |

### First-run troubleshooting

`tokenops status` (and the MCP `tokenops_status` tool, and `GET /readyz`) return
`blockers[]` with stable identifiers and `next_actions[]` with the exact
command to run. Common cases:

| Blocker | Fix |
|---|---|
| `storage_disabled` | run `tokenops init`, then restart the daemon |
| `rules_disabled` | run `tokenops init`, then restart the daemon |
| `providers_unconfigured` | set at least one `TOKENOPS_PROVIDER_*_URL` or add `providers:` to the config |

When a subsystem is off, the corresponding daemon routes (`/api/spend/*`,
`/api/rules/*`, `/api/audit`) return `503` with a structured `{error, hint}`
body rather than a 404.

### Subscription plans (Claude Max, ChatGPT Plus, etc.)

Flat-rate subscriptions don't bill per token. Configure them so
TokenOps records zero `cost_usd` and tracks plan quota headroom
instead:

```bash
# Bind a provider to a plan (writes to config.yaml; idempotent)
tokenops plan set anthropic claude-max-20x   # or claude-max-5x, claude-pro
tokenops plan set openai gpt-plus            # or gpt-team, gpt-pro

# Inspect bindings
tokenops plan list
tokenops plan headroom                       # live consumption + risk
```

YAML and `TOKENOPS_PLAN_<PROVIDER>` env overrides still work for
declarative deployments, but `tokenops plan set` is the recommended
path on a dev machine.

After setting a plan, reload your MCP server (Claude Code: `/mcp` then
reconnect). The MCP-side session observer counts your activity against
the plan's rate-limit window. Agents can call `tokenops_session_budget`
mid-conversation to find out whether they're about to hit the cap. Then
`tokenops plan headroom` (and the `tokenops_plan_headroom` MCP tool)
return month-to-date consumption and overage risk. See
[docs/plan-cost-model.md](docs/plan-cost-model.md) for the supported
plan catalog and how to add custom plans.

Every CLI command has a matching MCP tool (`tokenops_<name>`).

### Domain event bus

`internal/domainevents.Bus` carries typed cross-context events
(workflow.started/observed/completed, optimization.applied,
rule_corpus.reloaded, budget.exceeded). Daemon runs the bus in async
mode with bounded queue; subscribers include audit recorder, in-memory
counter, debug logger, JSONL persistence. Late subscribers can replay
history via `domainevents.ReplayInto`.

## 5-minute operator golden path

This path is optimized for one goal: prove TokenOps can produce measurable
value in minutes, not days.

### Step 1: Start the daemon

```bash
./bin/tokenopsd start
```

Expected: the process stays running and prints a startup log with a listen
address (default `127.0.0.1:7878`).

### Step 2: Point one SDK request at the proxy

```bash
export OPENAI_BASE_URL=http://127.0.0.1:7878/v1
```

Run one existing request from your app/CLI against the same model you already
use in production.

Expected: request succeeds with no code changes other than base URL override.

### Step 3: Validate attribution and spend visibility

```bash
./bin/tokenops spend
```

Expected: output includes non-zero usage and spend for the recent request.

### Step 4: Replay and inspect optimization headroom

```bash
./bin/tokenops replay <session-id>
```

Expected: side-by-side analysis shows optimization opportunities and projected
token/spend deltas for that session.

### Step 5: Capture your wedge KPI baseline

Track one primary KPI before broad rollout:

- `Token efficiency uplift (%) = (baseline_tokens - optimized_tokens) / baseline_tokens * 100`

Suggested target for initial rollout: 10-20% token reduction on high-volume
workflows while preserving quality gates.

Why this KPI: it directly ties optimization behavior to cost control and gives
an objective pass/fail metric for expansion decisions.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the per-release changes.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

Plans and tasks are tracked in `.roady/` (see the [roady](https://roady.dev)
spec-driven planning tool).

## License

Apache License 2.0. See [LICENSE](LICENSE).
