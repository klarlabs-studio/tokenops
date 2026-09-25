# TokenOps

[![CI](https://github.com/klarlabs-studio/tokenops/actions/workflows/ci.yml/badge.svg)](https://github.com/klarlabs-studio/tokenops/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/klarlabs-studio/tokenops?sort=semver)](https://github.com/klarlabs-studio/tokenops/releases)
[![License](https://img.shields.io/github/license/klarlabs-studio/tokenops)](LICENSE)
[![Go Report](https://goreportcard.com/badge/go.klarlabs.de/tokenops)](https://goreportcard.com/report/go.klarlabs.de/tokenops)

> **A local-first adaptive control plane for AI-assisted work.** TokenOps
> understands work, resource pressure, policy, decisions, and observed outcomes
> so it can recommend—or, where an adapter is capable and authorized, apply—
> better use of models and subscription capacity. It keeps the evidence and
> decision history local and explains why each intervention was chosen.

Docs: <https://klarlabs-studio.github.io/tokenops/> · Releases: <https://github.com/klarlabs-studio/tokenops/releases>

## Install

```bash
brew trust klarlabs-studio/tap        # first time only
brew install --cask klarlabs-studio/tap/tokenops
```

Homebrew refuses to load a cask from a third-party tap it has not been told
to trust, so the first install of anything from this tap needs
`brew trust klarlabs-studio/tap` once — per machine, not per tool.

Or via Go:

```bash
go install go.klarlabs.de/tokenops/cmd/tokenops@latest
```

Or grab a prebuilt binary from the [releases page](https://github.com/klarlabs-studio/tokenops/releases) (darwin amd64/arm64, linux amd64/arm64).

## 90-second quickstart

```bash
tokenops init                                  # writes config, registers MCP, installs hooks
tokenops plan set anthropic claude-max-20x     # bind your tier (init tells you if it can't)
tokenops daemon install                        # supervise `tokenops start` so ingestion survives reboot
```

`init` does the wiring. It finds the MCP hosts you actually have (Claude
Code, Claude Desktop), registers `tokenops serve` with each — pinned to
the absolute binary path, so a host can never silently run a stale build —
installs the Claude Code hooks, and prints what still needs you:

```
Wiring tokenops into this machine:
  ✓ MCP: Claude Code       registered — restart Claude Code to load the tools
  ✓ Claude Code hooks      installed coach-hook + read-guard
  · plan binding           detected anthropic but not which tier you pay for
```

Re-run it any time to repair drift. It is idempotent, backs up every file
it touches, and refuses to overwrite a host config it cannot parse. Two
things stay yours: picking your plan tier (the tiers differ 4x in
headroom, so guessing would make every figure confidently wrong) and
pointing a client at the proxy (that reroutes your real traffic).
`--no-wire` writes the config only.

Then restart your MCP host and ask the agent for a compact resource view with
`tokenops_resource_glance`, or use `tokenops_session_budget`,
`tokenops_burn_rate`, `tokenops_dashboard`, and `tokenops_plan_headroom`
individually. Or open the browser dashboard the agent links you to
(`http://tokenops.local:7878/dashboard?token=…`).

## How the control loop works

TokenOps surrounds the harness that owns and performs the work. It observes
executions and available resources, then makes its evidence and uncertainty
visible before recommending or taking an authorized action.

```text
Observe work and resources → Understand progress and constraints
             → Compare options under policy → Recommend or act
             → Verify the result → Learn from the outcome
```

| Loop stage | What TokenOps does |
|---|---|
| Observe | Ingests proxy traffic, local agent transcripts, subscription usage, and task boundaries into a local event store. Sources and signal quality are reported rather than assumed. |
| Understand | Reconstructs sessions into work traces; measures cost, plan-window pressure, context health, rework, and execution state. Estimates carry confidence and caveats. |
| Compare | Uses task class, provider capabilities, live pricing, plan capacity, and prior outcomes to consider alternatives. It can abstain when evidence is stale or incomplete. |
| Decide | Applies configured policy and authority. Decisions retain evidence, alternatives, uncertainty, and rationale so `tokenops decision explain <id>` can answer why. |
| Act and verify | Where an integration permits it, TokenOps can route requests or apply a bounded intervention. It records the result; it does not credit theoretical savings as proven value. |
| Learn and coach | Outcomes inform local, gated beliefs. `tokenops coach`, `tokenops dx`, and `tokenops scorecard` help the user improve their own AI-assisted workflow too. |

Recommendations and explanations are available through CLI, MCP, and the
dashboard. Background status stays concise; evidence and history are available
when requested. Autonomy is policy- and capability-specific, not a global
on/off switch.

For an agent-facing before/after workflow, call `tokenops_prepare_work` with
the task instruction and current model before starting. It returns plan
headroom and a policy-based model recommendation without switching models.
Afterward, call `tokenops_review_work` with the execution's stable
`workflow_id` to get measured token/cost totals, context growth, and any
evidence-based coaching findings. The review returns aggregate metrics, not
prompt content. These intent tools compose existing measurements and advice;
they do not replace the harness that plans or performs the work.

## Capabilities

- **Work and usage:** Claude Code and Codex transcript ingestion, proxy-based
  provider metering, workflow reconstruction, cost-aware pricing, forecasts,
  and subscription headroom. Setup and signal coverage: `tokenops status` and
  `tokenops vendor-usage status`.
- **Resource decisions:** routing advice, preferred-model ceilings, plan-window
  constraints, approval flow, and explainable decisions. Advice is available
  through MCP; automatic routing requires the proxy and the configured policy
  and evidence gates.
- **Verification and learning:** record human or verifier outcomes, explain
  past decisions, run bounded local routing experiments, and compare results
  against baselines. Learning is advisory and gated; it does not rewrite
  runtime behavior on its own.
- **Workflow coaching:** prompt and reply coaching, Agent-DX metrics, scorecards,
  task-level cost and rework, and context/compaction signals derived from
  supported harnesses.
- **Execution support:** deterministic `tokenops fmt -- <cmd>` compression
  preserves critical output and keeps full output recoverable locally. Hooks
  and integrations act only where the client supports the required authority.
- **Local-first surfaces:** Go daemon, SQLite event store, MCP server, protected
  Vue dashboard, and CLI. No cloud account or telemetry is required; the core
  product is Apache 2.0.

See [docs/architecture-ddd.md](docs/architecture-ddd.md) for bounded contexts
and layer rules; [docs/plan-cost-model.md](docs/plan-cost-model.md) for the
subscription-plan model; and the
[integration capability matrix](https://klarlabs-studio.github.io/tokenops/integrations/coverage)
for source coverage and client-specific limitations.

## CLI surface

```
init                              Scaffold config (sqlite + rules on); --detect sniffs installed clients
start                             Run the daemon in the foreground (proxy + analytics + bus + dashboard)
daemon {install|uninstall|status} Supervise `tokenops start` via launchd (macOS) or systemd --user (Linux)
serve                             MCP server over stdio
demo                              Seed 7d of synthetic events
status                            Daemon health + blockers[] / next_actions[]
spend [--forecast]                Spend / burn / 7d forecast
plan {list|set|headroom|catalog}  Subscription plan headroom
provider {list|set|unset}         Upstream LLM provider URLs
vendor-usage {status|backfill}    Inspect / backfill vendor-side pollers
dashboard rotate-token            Mint + persist a fresh dashboard auth token
config show                       Active configuration (redacted)
audit                             Query audit log
events                            Per-kind domain-event counts
rules {analyze|conflicts|...}     Rule intelligence
scorecard                         Wedge KPI scorecard
coverage-debt                     Risk-weighted coverage debt
eval                              Optimizer eval harness + gate
replay <id>                       Replay a session through the optimizer
decision explain <decision-id>   Show the evidence, alternatives, policy, and result of a recorded decision
outcome record <execution-id>    Attach an explicit human outcome to an execution and decision
outcome detect <execution-id>    Record the final verifier result after the last edit in a local session
experiment {start|status|stop}   Manage an opt-in, bounded proxy routing trial
fmt -- <cmd>                      Run <cmd>, compress its output deterministically before it reaches the agent (full output kept in ~/.tokenops/recovery/)
fmt bench --corpus <dir>          Measure formatter savings over captured command outputs
fmt hook [--shell zsh|bash]       Emit env-gated shell wrappers (activate with TOKENOPS_FMT=1)
fmt recover <id>                   Print the full stored output for a run (records the re-access)
fmt learn                         Mine fmt telemetry for next-formatter priorities + over-compression
```

Most CLI verbs have a matching MCP tool (`tokenops_<name>`). `fmt` is
CLI-first (it wraps a shell command); its learning report is exposed to
agents via `tokenops_fmt_learn`.

## Upgrading signal quality

Default install reports **low** confidence (MCP pings only). Two zero-network
upgrades:

```yaml
# ~/.config/tokenops/config.yaml
vendor_usage:
  claude_code:
    enabled: true              # reads ~/.claude/stats-cache.json
    interval: 60s
  anthropic:
    enabled: true              # calls Anthropic Admin API
    admin_key: sk-ant-admin-…  # mint in claude.com console
    interval: 5m
```

`tokenops vendor-usage status` shows whether the pollers are emitting; use
`tokenops vendor-usage backfill --hours 168` to pull a week of history from
Anthropic Admin in one shot after configuring the key.

The Anthropic Admin API only covers metered API usage. Claude Max plan window
state has no documented endpoint and stays heuristic — the cache reader is the
only locally-available Max signal and reports daily granularity with an
explicit caveat.

## Architecture

```
Clients / SDKs / CLIs / MCP hosts
            |
            v
   Local TokenOps daemon (Go)
      /     |       \
 Proxy    MCP      Dashboard
   |     server     /api/*
   v        |        |
 Provider routes     Vue+D3
 (OpenAI/Anth/Gem/Mistral)
            |
            v
    SQLite event store
            |
            v
 Decisions → outcomes → evidence tiers
            |
            v
 Spend / forecast / coaching / learning
```

DDD-organised: contexts under `internal/contexts/<ctx>/<pkg>`, adapters
(`cli`, `mcp`, `proxy`) stay flat. Layering enforced by `internal/archlint`
(`go test ./internal/archlint/...`).

```
cmd/{tokenops,tokenopsd}/         # binaries
internal/
  contexts/                       # bounded contexts (rules, spend, security, ...)
  cli/                            # cobra subcommands
  mcp/                            # MCP tool surface
  proxy/                          # HTTP server + dashboard
  daemon/                         # boot sequence
  storage/sqlite/                 # event store
pkg/eventschema/                  # public envelope + payload types
web/docs/                         # VitePress docs site
.roady/                           # spec-driven planning
```

## Integrations

TokenOps instruments AI usage on three planes; which ones a client supports is
the whole integration story. Full matrix + provider list:
[docs/integrations/coverage](https://klarlabs-studio.github.io/tokenops/integrations/coverage).

| Client | Passive read | MCP | Proxy |
|---|:--:|:--:|:--:|
| Claude Code | ✅ `~/.claude/projects` | ✅ | ✅ `ANTHROPIC_BASE_URL` |
| Codex CLI | ✅ `~/.codex/sessions` | ✅ | ✅ `OPENAI_BASE_URL` |
| opencode | ✅ SQLite store | ✅ | ✅ per-provider baseURL |
| Gemini CLI | ❌ *(no token log)* | ✅ | ✅ base-URL override |
| Desktop apps | ❌ | ✅ *(if MCP host)* | ❌ |

- **Passive read** — reads logs the client already writes; per-turn attribution
  (turn → session → project), zero wiring.
- **MCP** (`tokenops serve`) — the agent calls TokenOps; `tokenops_status`
  reports what's live and the exact command to upgrade signal quality.
- **Proxy** — point the client's base URL at TokenOps for ground-truth token/cost
  accounting. **OpenRouter** (`tokenops provider set openrouter`) is the universal
  fallback for any client with no local reader.

Honest boundaries: Gemini CLI has no local token log (proxy only); AWS Bedrock
needs SigV4 the passthrough proxy can't do; fully-hosted agents (Jules) are out
of reach — TokenOps is local-first with no telemetry.

**Parity across clients does not exist, and the matrix says so.** Desktop and
GitHub clients have no local token log and no base-URL override, so coaching
there is pull-only: no transcripts means no storytelling, no proactive nudges,
no routing. What is left is the MCP surface, which on those clients is the
whole product. The
[capability matrix](https://klarlabs-studio.github.io/tokenops/integrations/coverage#what-reaches-which-client)
lists what reaches which client, feature by feature, gaps included.

## Disabled-subsystem contract

When a subsystem is off, the matching routes return `503` with a structured
`{error, hint}` body instead of `404`. `tokenops status` (and the MCP
`tokenops_status` tool, and `GET /readyz`) surface stable identifiers in
`blockers[]` plus the exact command in `next_actions[]`:

| Blocker | Fix |
|---|---|
| `storage_disabled` | `tokenops init` then restart |
| `rules_disabled` | `tokenops init` then restart |
| `providers_unconfigured` | `tokenops provider set …` |

## Demo data isolation

Two event sources are excluded from every default rollup, because neither is
real LLM traffic you paid for or waited on:

| Source | What it is |
| --- | --- |
| `demo` | Synthetic `PromptEvent`s seeded by `tokenops demo` |
| `mcp-session` | Activity-proxy pings the MCP server records about itself |

Re-admit them one at a time — `--include-source` (CLI, repeatable and
comma-separated) or `include_sources` (MCP tool input):

```bash
tokenops spend --include-source=demo                # synthetic seeds too
tokenops spend --include-source=demo,mcp-session    # and the MCP pings
```

`--include-demo` / `include_demo: true` still work as aliases for
`demo` alone.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md),
and [SECURITY.md](SECURITY.md). Plans and tasks live in `.roady/` (see
[roady](https://roady.dev)).

## Changelog

See [CHANGELOG.md](CHANGELOG.md) — latest is [v0.55.0](https://github.com/klarlabs-studio/tokenops/releases/tag/v0.55.0).

## License

Apache License 2.0. See [LICENSE](LICENSE).
