# TokenOps

[![CI](https://github.com/klarlabs-studio/tokenops/actions/workflows/ci.yml/badge.svg)](https://github.com/klarlabs-studio/tokenops/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/klarlabs-studio/tokenops?sort=semver)](https://github.com/klarlabs-studio/tokenops/releases)
[![License](https://img.shields.io/github/license/klarlabs-studio/tokenops)](LICENSE)
[![Go Report](https://goreportcard.com/badge/go.klarlabs.de/tokenops)](https://goreportcard.com/report/go.klarlabs.de/tokenops)

> **Predict rate-limit cutoffs inside your AI agent.** Local MCP server + CLI
> that watches your flat-rate AI subscription window — Claude Max, ChatGPT
> Plus / Pro / Team, GitHub Copilot, Cursor, Mistral Le Chat Pro, Codex Plus
> — and tells the agent, before you hit the cap, to `continue`,
> `slow_down`, `switch_model`, or `wait_for_reset`.

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

Then restart your MCP host and ask the agent for any of: `tokenops_session_budget`, `tokenops_burn_rate`,
`tokenops_dashboard`, `tokenops_plan_headroom`. Or open the browser dashboard
the agent links you to (`http://tokenops.local:7878/dashboard?token=…`).

## Features

| | |
|---|---|
| 🧮 **13 plan catalog** | Claude Max 5x/20x, Claude Pro, Claude Code (Max + Pro), ChatGPT Plus / Pro / Team, GitHub Copilot Individual / Business, Cursor Pro / Business, Mistral Le Chat Pro, Codex Plus — each with a dated vendor source URL pinned in code |
| 🔌 **Provider-agnostic** | 17 proxy-metered providers — OpenAI, Anthropic, Gemini, Mistral, Cohere, the OpenAI-compatible fleet (Groq, DeepSeek, xAI, Perplexity, Fireworks, Cerebras, Together, OpenRouter), plus local/self-hosted (Ollama, LM Studio, LiteLLM, Vercel AI Gateway). Bind any with `tokenops provider set <name>` |
| 📊 **Interactive dashboard** | Vue 3 + D3 dashboard at `/dashboard` — cost line, per-model stacked area, tokens-per-bucket, KPI tiles, 15s auto-refresh, provider + model filters that persist across refresh |
| 📍 **mDNS-discoverable** | Daemon advertises `tokenops.local` over zeroconf so the dashboard URL is memorable on every host |
| 🔐 **Dashboard auth** | Shared-secret token, auto-minted on first start, accepted via header / query / cookie. `tokenops dashboard rotate-token` revokes |
| 📡 **Vendor /usage ingestion** | Live per-turn JSONL readers for Claude Code (`~/.claude/projects/`) and Codex CLI (`~/.codex/sessions/`), plus GitHub Copilot OAuth quota, Cursor cookie scrape, Anthropic cookie scraper (only source of the official Claude Max weekly %). Each source has a `tokenops vendor-usage enable <source>` wizard with env-var fallback for secrets |
| 💰 **Cache-aware pricing** | Claude + Codex cache reads bill at ~10% of the new-input rate. For agent-heavy workloads cache reads are >95% of input — the dashboard `Cache hit: XX.X%` tile + cost-aware aggregator make the difference between a naive $94k estimate and the real $10k. Per-provider rate cards ship in code |
| 🧪 **Per-project / per-session attribution** | JSONL pollers stamp `agent_id = "claude-code:<project>"` and `workflow_id = "claude-code:<project>:<session>"` (analogous for Codex). `group=agent` answers "which project burns the most"; coach finds per-session waste |
| 🧠 **Prompt coach** | `tokenops coach prompts` heuristic feedback on your real prompting patterns — length distribution, vague/ack/repeat detection, concrete recommendations. Auto-discovers Claude Code + Codex JSONLs. Prompt text never persisted. Ranked recommendations (v0.18) project tangible savings: turns × tokens × dollars × hours per win |
| 📋 **Reply coach** | `tokenops coach replies` detects output-compression patterns (caveman skill, article density, filler density) per session |
| ⏱️ **Task boundaries** | `tokenops task start "fix X"` / `done` / `list --metrics` — operator-marked task units persisted to `~/.tokenops/tasks.jsonl`. List view rolls up turns / cost / TTFUO / cost-per-turn from the events store within each task window |
| 📐 **8-KPI agent scorecard** | FVT / TEU / SAC (wedge) plus CHR / CGR / RGR / TCS / DAR (agent-workflow), all graded A–F against tuneable thresholds. v0.21.1 honest grading: TEU N/A when optimiser isn't wired; autonomous-loop sentinels filtered from CGR; column→payload attribution sync so SAC reflects reality |
| 🩺 **Agent DX metrics** | `tokenops dx` + `tokenops_agent_dx` — turns, wall-clock, tokens and tool calls per instruction, plus rework / interrupt / escalation / first-try rates, context growth and compactions. Each graded, with the single highest-leverage change named. Derived from transcripts; no proxy needed |
| 🧭 **Context-aware routing** | Rules scope to what a turn *is* (`when_class: mechanical`) and to how tight your plan window is (`when_window_pct_above: 70`) — keep your best model while there's headroom, conserve it only when there isn't. Both abstain rather than guess: an unclassifiable turn or an unmeasured window leaves the model alone |
| 🛡️ **Preferred model ceiling** | `preferred_models` per provider. Cheaper routes apply automatically; a pricier one is refused and surfaced for your answer via MCP, with your preferred model offered as the alternative |
| 🎯 **Honest signal quality** | Every prediction carries `signal_quality.level` (low / medium / high) plus a one-line caveat. Heuristic mode is labelled; proxied mode is labelled |
| ✂️ **Command-output compression** | `tokenops fmt -- <cmd>` shrinks a command's stdout before it hits the agent context — 46 built-in formatters (git, go/pytest/jest/…, npm/pip/uv/…, mvn/gradle/bazel/dotnet/…, docker/kubectl/helm, terraform/pulumi/ansible, aws/gcloud/az, and more) plus user-defined formatters in config (no recompile). Deterministic + critical-line-safe: errors/failures/changed-state never dropped, full output kept in `~/.tokenops/recovery/`. Balanced ~57% / aggressive ~68% stdout reduction. Self-tunes per user via `fmt learn --apply` |
| 🤖 **MCP-first** | 26 MCP tools agents call directly. Inline SVG sparkline + headroom gauge rendered in markdown so every MCP client shows them today |
| 🧠 **Dynamic-cheapest coaching** | Coaching pipeline picks the lowest blended-rate model per provider at runtime from the pricing table — no hardcoded model names |
| 💾 **Local-first, open source** | SQLite database, no cloud account, no telemetry. Apache 2.0. Demo-data isolation by default so synthetic seeds never contaminate the real signal |

See [docs/architecture-ddd.md](docs/architecture-ddd.md) for the bounded
contexts and layer rules; [docs/plan-cost-model.md](docs/plan-cost-model.md)
for the plan catalog model.

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

See [CHANGELOG.md](CHANGELOG.md) — latest is [v0.51.0](https://github.com/klarlabs-studio/tokenops/releases/tag/v0.51.0).

## License

Apache License 2.0. See [LICENSE](LICENSE).
