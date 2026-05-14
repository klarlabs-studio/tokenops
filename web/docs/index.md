---
layout: home
hero:
  name: TokenOps
  text: Predict rate-limit cutoffs inside your AI agent
  tagline: Local MCP server + CLI that watches your flat-rate AI subscription window — Claude Max, ChatGPT Plus / Pro / Team, GitHub Copilot, Cursor — and tells the agent, before you hit the cap, to continue, slow down, switch model, or wait for reset.
  actions:
    - theme: brand
      text: 90-second quickstart
      link: /guide/quickstart
    - theme: alt
      text: Star on GitHub
      link: https://github.com/felixgeelhaar/tokenops
features:
  - title: Eleven plans in the catalog
    details: 'Claude Max 5x / 20x, Claude Pro, Claude Code (Max + Pro), ChatGPT Plus / Pro / Team, GitHub Copilot Individual / Business, Cursor Pro / Business. Each with a dated vendor source URL pinned in code.'
  - title: Provider-agnostic by design
    details: 'OpenAI, Anthropic, Google Gemini all flow through the same proxy and event schema. Spend, optimizer, scorecard, MCP tools — every surface treats them as peers, not special cases.'
  - title: Honest signal quality
    details: Every prediction carries `signal_quality.level` (low / medium / high) plus a one-line caveat. Heuristic mode is labelled; proxied mode is labelled; the product never pretends a guess is a guarantee.
  - title: MCP-first with inline charts
    details: '25 MCP tools agents can call directly. `tokenops_session_budget` returns `recommended_action ∈ continue|slow_down|switch_model|wait_for_reset` with an inline SVG headroom gauge; `tokenops_burn_rate` ships a 24h sparkline. Works with Claude Code, Cursor, aider, Codex, and every other MCP host.'
  - title: Auto-detect on first run
    details: '`tokenops init --detect` sniffs Claude Code, Claude Desktop, Cursor, ChatGPT Desktop, and the standard API-key env vars, then prints the exact `tokenops plan set …` commands for what it found. No config archaeology.'
  - title: Interactive Vue + D3 dashboard
    details: 'The daemon serves a local dashboard at `/dashboard` — cost-over-time line, tokens-per-bucket stacked bar, live KPIs, 15s auto-refresh. The `tokenops_dashboard` MCP tool hands your agent a clickable URL to the running instance.'
  - title: Local-first, open source
    details: SQLite database. No cloud account. No telemetry. Apache 2.0. Demo-data isolation by default so synthetic seeds never contaminate the real signal.
---

## What's new in v0.10.x

- **`tokenops.local` via mDNS (v0.10.1)** — The daemon advertises itself over zeroconf on Start, so the dashboard URL becomes `http://tokenops.local:7878/dashboard` instead of a bare loopback address. The `tokenops_dashboard` MCP tool prefers it; falls back to `127.0.0.1` when `.local` resolution isn't available.
- **Vendor /usage ingestion (v0.10.2)** — Two new signal sources upgrade Anthropic confidence beyond the heuristic default. The **Claude Code stats cache reader** parses `~/.claude/stats-cache.json` and emits per-(date, model) deltas (signal_quality → medium). The **Anthropic Admin API poller** calls `/v1/organizations/usage_report/messages` every 5min with an admin key (signal_quality → high). Both wired through `config.vendor_usage.*`; both honest about the Claude Max 5h-window blind spot.
- **Dashboard auth (v0.10.3)** — `/dashboard` + `/api/*` now require a shared-secret token (`/healthz`, `/readyz`, `/version` stay public). Daemon mints + persists the token automatically at `~/.tokenops/dashboard.token`; the MCP tool returns a clickable URL with the token pre-attached so the operator gets a one-click authenticated visit. Browser-style auth mints a session cookie and 303s to a clean URL so the token never lingers in history.
- **Auto-detect on init (v0.10.0)** — `tokenops init --detect` reads your installed AI clients (Claude Code/Desktop, Cursor, ChatGPT Desktop, env-var API keys) and prints the exact plan-set commands. Run it once, paste what fits.
- **Interactive dashboard (v0.10.0)** — A Vue + D3 dashboard ships with the daemon at `/dashboard`. Hourly cost line, tokens-per-bucket stacked bar, KPI tiles, 15s auto-refresh. Driven by the same `/api/spend/*` endpoints the CLI uses.
- **Inline charts in MCP responses (v0.10.0)** — `tokenops_session_budget` leads with a coloured headroom gauge (green / amber / red by overage band); `tokenops_burn_rate` ships a sparkline. Rendered inline in markdown so every MCP client shows them today.
- **Dynamic-cheapest coaching router (v0.10.0)** — The coaching pipeline picks the lowest blended-rate model per provider from the pricing table at runtime. No hardcoded model names; pricing updates flow through automatically.

## Why TokenOps exists

Every major vendor — Anthropic, OpenAI, GitHub, Cursor, Google — publishes *rate-limit windows*, not monthly token caps, for their flat-rate plans. The vendor dashboard tells you you've hit the cap **after** the cap hits. There's no early signal in the agent context where you're actually working.

TokenOps closes that gap for every provider it tracks. One CLI, one MCP server, one event schema. Configure as many plans as you use:

```bash
tokenops plan set anthropic claude-max-20x
tokenops plan set openai gpt-plus
tokenops plan set github copilot-business
tokenops plan set cursor cursor-pro
```

Every plan you bind contributes to a unified headroom view your agent can query mid-conversation.

## Honest about what it sees today

TokenOps reports its own signal quality on every prediction. Four sources, ranked by faithfulness:

- **`mcp_tool_pings` (low)** — Default. Counts MCP invocations as an activity proxy. Useful as a "is the agent talking to me?" signal, not a quota meter.
- **`claude_code_stats_cache` (medium)** — Daemon reads `~/.claude/stats-cache.json` on a tick (config: `vendor_usage.claude_code.enabled: true`). Per-model daily totals; can't resolve the 5h rolling window but gives real attribution. Schema is undocumented — every response carries a caveat.
- **`proxy_traffic` (high)** — Wire your SDK's base URL through the local proxy (`OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `GEMINI_BASE_URL`). Captures every request per-event.
- **`vendor_usage_api` (high)** — Anthropic Admin API poller (config: `vendor_usage.anthropic.{enabled, admin_key}`) reads `/v1/organizations/usage_report/messages` directly from Anthropic. Covers metered API usage only; Claude Max plan window state has no documented endpoint and stays heuristic.

## Who this is for

- Solo founders running coding agents 6+ hours a day across multiple AI subscriptions
- Staff engineers billing client time against AI sessions, juggling Claude + GPT + Copilot stacks
- Anyone who's lost focus to a mid-task rate-limit cutoff on any provider

If you don't recognise the struggling moment, this isn't the product for you yet.

## Looking for early users

If you'd trade a 15-minute call for hands-on help wiring TokenOps to your workflow, [open an issue on GitHub](https://github.com/felixgeelhaar/tokenops/issues/new) or DM `@felixgeelhaar`. Current focus is the first ten real users — across any provider mix.
