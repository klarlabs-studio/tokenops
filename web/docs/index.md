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

## What's new in v0.10.0

- **Auto-detect on init** — `tokenops init --detect` reads your installed AI clients (Claude Code/Desktop, Cursor, ChatGPT Desktop, env-var API keys) and prints the exact plan-set commands. Run it once, paste what fits.
- **Interactive dashboard** — A Vue + D3 dashboard ships with the daemon at `/dashboard`. Hourly cost line, tokens-per-bucket stacked bar, KPI tiles, 15s auto-refresh. Driven by the same `/api/spend/*` endpoints the CLI uses.
- **Inline charts in MCP responses** — `tokenops_session_budget` now leads with a coloured headroom gauge (green / amber / red by overage band); `tokenops_burn_rate` ships a sparkline. Rendered inline in markdown so every MCP client shows them today.
- **Dynamic-cheapest coaching router** — The coaching pipeline now picks the lowest blended-rate model per provider from the pricing table at runtime. No hardcoded model names; pricing updates flow through automatically.

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

The default install observes **MCP tool invocations** as an activity proxy — every response says so. Wire the local proxy and TokenOps sees every request flowing through it (`OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `GEMINI_BASE_URL`). Vendor `/usage` API ingestion is on the roadmap — it lands the highest-quality signal with zero proxy config.

## Who this is for

- Solo founders running coding agents 6+ hours a day across multiple AI subscriptions
- Staff engineers billing client time against AI sessions, juggling Claude + GPT + Copilot stacks
- Anyone who's lost focus to a mid-task rate-limit cutoff on any provider

If you don't recognise the struggling moment, this isn't the product for you yet.

## Looking for early users

If you'd trade a 15-minute call for hands-on help wiring TokenOps to your workflow, [open an issue on GitHub](https://github.com/felixgeelhaar/tokenops/issues/new) or DM `@felixgeelhaar`. Current focus is the first ten real users — across any provider mix.
