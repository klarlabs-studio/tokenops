# TokenOps

[![CI](https://github.com/felixgeelhaar/tokenops/actions/workflows/ci.yml/badge.svg)](https://github.com/felixgeelhaar/tokenops/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/felixgeelhaar/tokenops?sort=semver)](https://github.com/felixgeelhaar/tokenops/releases)
[![License](https://img.shields.io/github/license/felixgeelhaar/tokenops)](LICENSE)
[![Go Report](https://goreportcard.com/badge/github.com/felixgeelhaar/tokenops)](https://goreportcard.com/report/github.com/felixgeelhaar/tokenops)

> **Predict rate-limit cutoffs inside your AI agent.** Local MCP server + CLI
> that watches your flat-rate AI subscription window — Claude Max, ChatGPT
> Plus / Pro / Team, GitHub Copilot, Cursor, Mistral Le Chat Pro, Codex Plus
> — and tells the agent, before you hit the cap, to `continue`,
> `slow_down`, `switch_model`, or `wait_for_reset`.

Docs: <https://felixgeelhaar.github.io/tokenops/> · Releases: <https://github.com/felixgeelhaar/tokenops/releases>

## Install

```bash
brew install felixgeelhaar/tap/tokenops
```

Or via Go:

```bash
go install github.com/felixgeelhaar/tokenops/cmd/tokenops@latest
```

Or grab a prebuilt binary from the [releases page](https://github.com/felixgeelhaar/tokenops/releases) (darwin amd64/arm64, linux amd64/arm64, windows amd64).

## 90-second quickstart

```bash
tokenops init --detect                         # sniff installed AI clients, print plan-set commands
tokenops plan set anthropic claude-max-20x     # bind whatever fits (paste from --detect output)
tokenops start                                 # daemon, listens 127.0.0.1:7878 + tokenops.local
```

Wire MCP into your agent:

```json
{
  "mcpServers": {
    "tokenops": { "command": "tokenops", "args": ["serve"] }
  }
}
```

Ask the agent for any of: `tokenops_session_budget`, `tokenops_burn_rate`,
`tokenops_dashboard`, `tokenops_plan_headroom`. Or open the browser dashboard
the agent links you to (`http://tokenops.local:7878/dashboard?token=…`).

## Features

| | |
|---|---|
| 🧮 **13 plan catalog** | Claude Max 5x/20x, Claude Pro, Claude Code (Max + Pro), ChatGPT Plus / Pro / Team, GitHub Copilot Individual / Business, Cursor Pro / Business, Mistral Le Chat Pro, Codex Plus — each with a dated vendor source URL pinned in code |
| 🔌 **Provider-agnostic** | OpenAI, Anthropic, Google Gemini, Mistral through the same proxy and event schema |
| 📊 **Interactive dashboard** | Vue 3 + D3 dashboard at `/dashboard` — cost line, per-model stacked area, tokens-per-bucket, KPI tiles, 15s auto-refresh, provider + model filters that persist across refresh |
| 📍 **mDNS-discoverable** | Daemon advertises `tokenops.local` over zeroconf so the dashboard URL is memorable on every host |
| 🔐 **Dashboard auth** | Shared-secret token, auto-minted on first start, accepted via header / query / cookie. `tokenops dashboard rotate-token` revokes |
| 📡 **Vendor /usage ingestion** | Live per-turn JSONL readers for Claude Code (`~/.claude/projects/`) and Codex CLI (`~/.codex/sessions/`), plus GitHub Copilot OAuth quota, Cursor cookie scrape, Anthropic cookie scraper (only source of the official Claude Max weekly %). Each source has a `tokenops vendor-usage enable <source>` wizard with env-var fallback for secrets |
| 💰 **Cache-aware pricing** | Claude + Codex cache reads bill at ~10% of the new-input rate. For agent-heavy workloads cache reads are >95% of input — the dashboard `Cache hit: XX.X%` tile + cost-aware aggregator make the difference between a naive $94k estimate and the real $10k. Per-provider rate cards ship in code |
| 🧪 **Per-project / per-session attribution** | JSONL pollers stamp `agent_id = "claude-code:<project>"` and `workflow_id = "claude-code:<project>:<session>"` (analogous for Codex). `group=agent` answers "which project burns the most"; coach finds per-session waste |
| 🧠 **Prompt coach** | `tokenops coach prompts` heuristic feedback on your real prompting patterns — length distribution, vague/ack/repeat detection, concrete recommendations. Auto-discovers Claude Code + Codex JSONLs. Prompt text never persisted |
| 🎯 **Honest signal quality** | Every prediction carries `signal_quality.level` (low / medium / high) plus a one-line caveat. Heuristic mode is labelled; proxied mode is labelled |
| 🤖 **MCP-first** | 25 MCP tools agents call directly. Inline SVG sparkline + headroom gauge rendered in markdown so every MCP client shows them today |
| 🧠 **Dynamic-cheapest coaching** | Coaching pipeline picks the lowest blended-rate model per provider at runtime from the pricing table — no hardcoded model names |
| 💾 **Local-first, open source** | SQLite database, no cloud account, no telemetry. Apache 2.0. Demo-data isolation by default so synthetic seeds never contaminate the real signal |

See [docs/architecture-ddd.md](docs/architecture-ddd.md) for the bounded
contexts and layer rules; [docs/plan-cost-model.md](docs/plan-cost-model.md)
for the plan catalog model.

## CLI surface

```
init                              Scaffold config (sqlite + rules on); --detect sniffs installed clients
start                             Run the daemon (proxy + analytics + bus + dashboard)
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
```

Every CLI verb has a matching MCP tool (`tokenops_<name>`).

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
 Spend / forecast / coaching
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

`tokenops demo` writes synthetic `PromptEvent`s tagged `source=demo`. Every
default rollup filters them out so first-run exploration never contaminates
production numbers. Pass `--include-demo` (CLI) or `include_demo: true` (MCP
tool input) to see the synthetic breakdown alongside real traffic.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md),
and [SECURITY.md](SECURITY.md). Plans and tasks live in `.roady/` (see
[roady](https://roady.dev)).

## Changelog

See [CHANGELOG.md](CHANGELOG.md) — latest is [v0.16.0](https://github.com/felixgeelhaar/tokenops/releases/tag/v0.16.0).

## License

Apache License 2.0. See [LICENSE](LICENSE).
